package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const cliSample = `You are currently using your subscription to power your Claude Code usage

Current session: 19% used · resets Nov 3 at 11:30am (Europe/Moscow)
Current week (all models): 34% used · resets Nov 6 at 8am (Europe/Moscow)
Current week (Fable): 0% used · resets Nov 6 at 8am (Europe/Moscow)

What's contributing to your limits usage?
`

func TestParseCLI(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	u, err := ParseCLI(cliSample, now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Session == nil || u.Session.Percent != 19 {
		t.Fatalf("session = %+v", u.Session)
	}
	if want := time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC); !u.Session.ResetsAt.Equal(want) {
		t.Errorf("session reset = %v, want %v", u.Session.ResetsAt, want)
	}
	if u.Week == nil || u.Week.Percent != 34 {
		t.Fatalf("week = %+v", u.Week)
	}
	if want := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC); !u.Week.ResetsAt.Equal(want) {
		t.Errorf("week reset = %v, want %v", u.Week.ResetsAt, want)
	}
	if len(u.Others) != 1 || u.Others[0].Label != "Fable" || u.Others[0].Percent != 0 {
		t.Errorf("others = %+v", u.Others)
	}
}

func TestParseCLIYearRollover(t *testing.T) {
	now := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	u, err := ParseCLI("Current week (all models): 10% used · resets Jan 2 at 8am (UTC)\n", now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Week.ResetsAt.Year() != 2027 {
		t.Errorf("reset year = %d, want 2027", u.Week.ResetsAt.Year())
	}
}

func TestParseCLIMinutesAndGarbage(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	u, err := ParseCLI("Current session: 5.5% used · resets Nov 3 at 6:45pm (UTC)\n", now)
	if err != nil || u.Session.Percent != 5.5 || u.Session.ResetsAt.Hour() != 18 || u.Session.ResetsAt.Minute() != 45 {
		t.Fatalf("got %+v, %v", u, err)
	}
	if _, err := ParseCLI("nothing useful here", now); err == nil {
		t.Error("garbage must be rejected")
	}
	u, err = ParseCLI("Current session: 7% used\n", now)
	if err != nil || !u.Session.ResetsAt.IsZero() {
		t.Errorf("a missing reset must leave the time zero: %+v %v", u, err)
	}
}

func TestParseAPI(t *testing.T) {
	body := `{"five_hour":{"utilization":12.5,"resets_at":"2026-11-03T08:30:00Z"},
	"seven_day":{"utilization":40,"resets_at":"2026-11-06T05:00:00Z"},
	"seven_day_opus":{"utilization":3,"resets_at":null},"seven_day_sonnet":null}`
	u, err := ParseAPI([]byte(body), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if u.Session.Percent != 12.5 || u.Week.Percent != 40 || len(u.Others) != 1 || u.Others[0].Label != "Opus" {
		t.Errorf("unexpected: %+v", u)
	}
	if _, err := ParseAPI([]byte(`{}`), time.Now()); err == nil {
		t.Error("an empty response must be rejected")
	}
	if _, err := ParseAPI([]byte(`nope`), time.Now()); err == nil {
		t.Error("invalid JSON must be rejected")
	}
}

func credsFile(t *testing.T, token string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "creds.json")
	if err := os.WriteFile(p, []byte(`{"claudeAiOauth":{"accessToken":"`+token+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetchPrefersAPIAndFallsBackToCLI(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":50,"resets_at":"2026-11-03T08:30:00Z"}}`))
	}))
	defer srv.Close()

	c := New("", credsFile(t, "tok"))
	c.Endpoint = srv.URL
	c.Run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("CLI must not run when the API works")
		return nil, nil
	}
	u, err := c.Fetch(context.Background())
	if err != nil || u.Source != "oauth_api" || u.Session.Percent != 50 || gotAuth != "Bearer tok" {
		t.Fatalf("api path: %+v %v auth=%q", u, err, gotAuth)
	}

	// Without credentials the CLI is used.
	c = New("/fake/claude", filepath.Join(t.TempDir(), "missing.json"))
	c.Run = func(_ context.Context, bin string, args ...string) ([]byte, error) {
		if bin != "/fake/claude" || len(args) != 1 || args[0] != "/usage" {
			t.Errorf("unexpected command %s %v", bin, args)
		}
		return []byte(cliSample), nil
	}
	u, err = c.Fetch(context.Background())
	if err != nil || u.Source != "cli" || u.Week.Percent != 34 {
		t.Fatalf("cli path: %+v %v", u, err)
	}
}

func TestFetchBothFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("/fake/claude", credsFile(t, "tok"))
	c.Endpoint = srv.URL
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("garbage"), nil }
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("expected an error when every source fails")
	}
}

func TestAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if New("", filepath.Join(t.TempDir(), "none.json")).Available() {
		t.Error("nothing installed must report unavailable")
	}
	if !New("/x/claude", "").Available() {
		t.Error("a configured binary counts as available")
	}
	if !New("", credsFile(t, "tok")).Available() {
		t.Error("an existing credentials file counts as available")
	}
}
