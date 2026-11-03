package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lookup(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := FromLookup(lookup(map[string]string{"TELEGRAM_BOT_TOKEN": " 1:abc "}))
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramToken != "1:abc" || c.RefreshInterval != time.Minute || c.CacheTTL != 4*time.Minute ||
		len(c.Thresholds) != 2 || c.StateDir != "state" || c.ChatID != 0 {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.MessageStatePath() != filepath.Join("state", "telegram-message.json") {
		t.Errorf("state path: %s", c.MessageStatePath())
	}
}

func TestOverrides(t *testing.T) {
	c, err := FromLookup(lookup(map[string]string{
		"TELEGRAM_BOT_TOKEN":      "1:abc",
		"CLAUDE_STATUS_CHAT_ID":   "95040544", // name used by the shell version
		"REFRESH_INTERVAL":        "30s",
		"CACHE_TTL":               "2m",
		"ALERT_THRESHOLDS":        "70, 90 ,99.5",
		"STATE_DIR":               "/var/lib/bot",
		"OPENAI_ADMIN_KEY":        " sk-admin ",
		"LOG_FORMAT":              "json",
		"CLAUDE_CREDENTIALS_FILE": "/c.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ChatID != 95040544 || c.RefreshInterval != 30*time.Second || c.CacheTTL != 2*time.Minute ||
		len(c.Thresholds) != 3 || c.Thresholds[2] != 99.5 || c.StateDir != "/var/lib/bot" ||
		c.OpenAIAdminKey != "sk-admin" || c.LogFormat != "json" || c.CredentialsFile != "/c.json" {
		t.Errorf("unexpected: %+v", c)
	}
}

func TestErrors(t *testing.T) {
	base := map[string]string{"TELEGRAM_BOT_TOKEN": "1:abc"}
	cases := map[string]map[string]string{
		"no token":       {},
		"blank token":    {"TELEGRAM_BOT_TOKEN": "  "},
		"bad chat":       {"CHAT_ID": "abc"},
		"bad interval":   {"REFRESH_INTERVAL": "soon"},
		"zero interval":  {"REFRESH_INTERVAL": "0s"},
		"negative ttl":   {"CACHE_TTL": "-1m"},
		"bad threshold":  {"ALERT_THRESHOLDS": "85,high"},
		"threshold zero": {"ALERT_THRESHOLDS": "0"},
		"threshold 101":  {"ALERT_THRESHOLDS": "101"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			m := map[string]string{}
			if name != "no token" && name != "blank token" {
				for k, v := range base {
					m[k] = v
				}
			}
			for k, v := range extra {
				m[k] = v
			}
			if _, err := FromLookup(lookup(m)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDotenv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	content := `# comment
TELEGRAM_BOT_TOKEN='123:abc'
export OPENAI_ADMIN_KEY="sk-admin"
CHAT_ID=42 # trailing comment
EMPTY=
QUOTED_HASH='a # b'

$(touch /tmp/pwned)=x
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vals, err := ParseDotenvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:abc", "OPENAI_ADMIN_KEY": "sk-admin", "CHAT_ID": "42",
		"EMPTY": "", "QUOTED_HASH": "a # b", "$(touch /tmp/pwned)": "x",
	}
	for k, v := range want {
		if got, ok := vals[k]; !ok || got != v {
			t.Errorf("%q = %q (present %v), want %q", k, got, ok, v)
		}
	}
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		t.Fatal("the dotenv parser must never execute anything")
	}
}

func TestDotenvErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDotenvFile(p); err == nil {
		t.Error("a line without '=' must be rejected")
	}
	if _, err := ParseDotenvFile(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Errorf("missing file: %v", err)
	}
}

func TestLoadPrefersProcessEnvironment(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("TELEGRAM_BOT_TOKEN=from-file\nCHAT_ID=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_ENV", p)
	t.Setenv("TELEGRAM_BOT_TOKEN", "from-env")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramToken != "from-env" || c.ChatID != 1 {
		t.Errorf("env must win, file fills gaps: %+v", c)
	}
}

func TestProviders(t *testing.T) {
	base := map[string]string{"TELEGRAM_BOT_TOKEN": "1:abc"}
	c, _ := FromLookup(lookup(base))
	if !c.Enabled("claude", true) || c.Enabled("claude", false) {
		t.Error("with no list, detection decides")
	}

	base["PROVIDERS"] = "OpenAI, auto"
	c, err := FromLookup(lookup(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled("claude", true) || !c.Enabled("openai", false) {
		t.Errorf("an explicit list overrides detection: %+v", c.Providers)
	}

	base["PROVIDERS"] = "claude,kimi"
	if _, err := FromLookup(lookup(base)); err == nil {
		t.Error("an unknown provider must be rejected")
	}
}
