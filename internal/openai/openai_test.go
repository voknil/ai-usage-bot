package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const codexSample = `[{"provider":"codex","usage":{"accountEmail":"me@example.com",
"primary":{"usedPercent":12,"resetsAt":"2026-11-03T10:00:12Z","windowMinutes":300},
"secondary":{"usedPercent":41.5,"resetsAt":"2026-11-04T08:00:14Z","windowMinutes":10080},
"codexResetCredits":{"availableCount":2}},"credits":{"remaining":5}}]`

func TestParseCodex(t *testing.T) {
	c, err := ParseCodex([]byte(codexSample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Session.Percent != 12 || c.Week.Percent != 41.5 || c.FreeResets != 2 || c.Account != "me@example.com" {
		t.Errorf("unexpected: %+v session=%+v week=%+v", c, c.Session, c.Week)
	}
	if c.Session.ResetsAt.IsZero() {
		t.Error("reset time not parsed")
	}
	for _, bad := range []string{`[]`, `{}`, `[{"usage":{}}]`} {
		if _, err := ParseCodex([]byte(bad)); err == nil {
			t.Errorf("ParseCodex(%s) must fail", bad)
		}
	}
}

func TestParseCostsSortsByModel(t *testing.T) {
	b, err := ParseCosts([]byte(`{"data":[{"results":[
		{"amount":{"value":0.03},"line_item":"gpt-b, input"},
		{"amount":{"value":2.0},"line_item":"gpt-a, output"},
		{"amount":{"value":0.15},"line_item":"gpt-a, input"},
		{"amount":{"value":0.01},"line_item":""}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if b.TodayUSD < 2.18 || b.TodayUSD > 2.20 {
		t.Errorf("total = %v", b.TodayUSD)
	}
	if len(b.ByModel) != 3 || b.ByModel[0].Model != "gpt-a" || b.ByModel[2].Model != "other" {
		t.Errorf("by model = %+v", b.ByModel)
	}
}

func TestFetchIndependentHalves(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[{"results":[{"amount":{"value":1.5},"line_item":"gpt-a, x"}]}]}`))
	}))
	defer srv.Close()

	c := New("/fake/codexbar", "sk-admin")
	c.Endpoint = srv.URL
	c.Now = func() time.Time { return time.Date(2026, 11, 3, 15, 4, 5, 0, time.UTC) }
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte(codexSample), nil }
	o, err := c.Fetch(context.Background())
	if err != nil || o.Codex == nil || o.Billing == nil {
		t.Fatalf("both halves: %+v %v", o, err)
	}
	if gotAuth != "Bearer sk-admin" {
		t.Errorf("auth = %q", gotAuth)
	}
	if want := "start_time=1793664000"; !strings.Contains(gotQuery, want) {
		t.Errorf("query %q lacks %q (UTC midnight)", gotQuery, want)
	}

	// Codex broken, billing fine: still a result, with the error flagged.
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("garbage"), nil }
	o, err = c.Fetch(context.Background())
	if err != nil || o.Codex != nil || !o.CodexErr || o.Billing == nil {
		t.Fatalf("partial: %+v %v", o, err)
	}

	// Billing rejected, Codex fine.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer bad.Close()
	c.Endpoint = bad.URL
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte(codexSample), nil }
	o, err = c.Fetch(context.Background())
	if err != nil || o.Codex == nil || !o.BillingErr {
		t.Fatalf("billing failure: %+v %v", o, err)
	}

	// Everything broken is an error.
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, context.DeadlineExceeded }
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("expected an error when nothing could be read")
	}
}

func TestNotConfigured(t *testing.T) {
	c := New("", "")
	c.CodexBarBin = ""
	t.Setenv("PATH", t.TempDir())
	if c.Configured() {
		t.Error("no codexbar and no key must report not configured")
	}
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Error("fetch without configuration must fail")
	}
}
