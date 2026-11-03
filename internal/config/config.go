// Package config loads the bot's settings from the environment and an
// optional dotenv file. The file is parsed, never executed.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the runtime settings.
type Config struct {
	TelegramToken string
	// ChatID, when non-zero, pins the bot to one chat. Without it the first
	// private chat that sends /start becomes the owner.
	ChatID int64

	StateDir        string
	RefreshInterval time.Duration
	// CacheTTL is how long a fetched source is considered fresh.
	CacheTTL   time.Duration
	Thresholds []float64

	ClaudeBin       string
	CredentialsFile string
	CodexBarBin     string
	OpenAIAdminKey  string

	LogLevel  string
	LogFormat string
}

// Load reads settings. Process environment wins over the dotenv file, whose
// path comes from BOT_ENV (default ".env" in the working directory; missing
// is fine).
func Load() (*Config, error) {
	file := os.Getenv("BOT_ENV")
	if file == "" {
		file = os.Getenv("CLAUDE_STATUS_BOT_ENV") // name used by the shell version
	}
	if file == "" {
		file = ".env"
	}
	vals, err := ParseDotenvFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return FromLookup(func(k string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return vals[k]
	})
}

// FromLookup builds a Config from any key lookup; it exists so tests need no
// process environment.
func FromLookup(get func(string) string) (*Config, error) {
	c := &Config{
		RefreshInterval: time.Minute,
		CacheTTL:        4 * time.Minute,
		Thresholds:      []float64{85, 95},
		LogLevel:        "info",
		LogFormat:       "text",
	}
	c.TelegramToken = strings.TrimSpace(get("TELEGRAM_BOT_TOKEN"))
	if c.TelegramToken == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN is not set")
	}

	for _, k := range []string{"CHAT_ID", "CLAUDE_STATUS_CHAT_ID"} {
		if v := strings.TrimSpace(get(k)); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			c.ChatID = id
			break
		}
	}

	c.StateDir = firstNonEmpty(get("STATE_DIR"), "state")

	var err error
	if v := strings.TrimSpace(get("REFRESH_INTERVAL")); v != "" {
		if c.RefreshInterval, err = parsePositive("REFRESH_INTERVAL", v); err != nil {
			return nil, err
		}
	}
	if v := strings.TrimSpace(get("CACHE_TTL")); v != "" {
		if c.CacheTTL, err = parsePositive("CACHE_TTL", v); err != nil {
			return nil, err
		}
	}
	if v := strings.TrimSpace(get("ALERT_THRESHOLDS")); v != "" {
		if c.Thresholds, err = parseThresholds(v); err != nil {
			return nil, err
		}
	}

	c.ClaudeBin = get("CLAUDE_BIN")
	c.CredentialsFile = firstNonEmpty(get("CLAUDE_CREDENTIALS_FILE"), defaultCredentials())
	c.CodexBarBin = get("CODEXBAR_BIN")
	c.OpenAIAdminKey = strings.TrimSpace(get("OPENAI_ADMIN_KEY"))
	c.LogLevel = firstNonEmpty(get("LOG_LEVEL"), c.LogLevel)
	c.LogFormat = firstNonEmpty(get("LOG_FORMAT"), c.LogFormat)
	return c, nil
}

// MessageStatePath etc. name the files under StateDir. The formats match the
// shell version, so an existing installation keeps its message and alerts.
func (c *Config) MessageStatePath() string { return filepath.Join(c.StateDir, "telegram-message.json") }
func (c *Config) AlertStatePath() string   { return filepath.Join(c.StateDir, "usage-alerts.json") }
func (c *Config) OffsetPath() string       { return filepath.Join(c.StateDir, "telegram-updates.offset") }

func parsePositive(name, v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return d, nil
}

func parseThresholds(v string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f, err := strconv.ParseFloat(part, 64)
		if err != nil || f <= 0 || f > 100 {
			return nil, fmt.Errorf("ALERT_THRESHOLDS: %q is not a percentage in (0,100]", part)
		}
		out = append(out, f)
	}
	return out, nil
}

func defaultCredentials() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ParseDotenvFile reads KEY=VALUE lines. It understands comments, an optional
// "export " prefix and single- or double-quoted values; nothing is expanded or
// executed.
func ParseDotenvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator configuration (BOT_ENV)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		out[key] = unquote(strings.TrimSpace(val))
	}
	return out, sc.Err()
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
	}
	// Unquoted: strip a trailing comment.
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
