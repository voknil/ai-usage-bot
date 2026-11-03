package render

import (
	"strings"
	"testing"
	"time"

	"github.com/voknil/ai-usage-bot/internal/alerts"
	"github.com/voknil/ai-usage-bot/internal/usage"
)

var (
	msk = time.FixedZone("MSK", 3*3600)
	now = time.Date(2026, 11, 3, 9, 25, 0, 0, msk) // Tue
)

func at(h, m int, day int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, msk) }

func full() Snapshot {
	return Snapshot{
		Claude: &usage.Claude{
			CapturedAt: at(9, 20, 3),
			Session:    &usage.Window{Label: "Current session", Percent: 19, ResetsAt: at(11, 30, 3)},
			Week:       &usage.Window{Label: "All models", Percent: 34, ResetsAt: at(8, 0, 6)},
			Others:     []usage.Window{{Label: "Fable", Percent: 0, ResetsAt: at(8, 0, 6)}},
		},
		OpenAI: &usage.OpenAI{
			Codex: &usage.Codex{
				Session:    &usage.Window{Percent: 0, ResetsAt: at(13, 0, 3)},
				Week:       &usage.Window{Percent: 16, ResetsAt: at(11, 0, 4)},
				FreeResets: 2,
			},
			Billing: &usage.Billing{TodayUSD: 2.19, ByModel: []usage.ModelCost{{Model: "gpt-a", USD: 2.15}, {Model: "gpt-b", USD: 0.03}}},
		},
		Services: []ServiceView{
			{Name: "Anthropic", Svc: &usage.Service{Indicator: "none", Summary: "All Systems Operational"}},
			{Name: "OpenAI", Svc: &usage.Service{Indicator: "none", Summary: "All Systems Operational"}},
		},
	}
}

func TestStatusGolden(t *testing.T) {
	want := `5h: 19% used · rst 11:30
Week: 34% used · rst Fri 08:00

Current session
[###---------------] 19% used
Resets: 11:30 (in 2h 5m)

Weekly limits
All models
[######------------] 34% used
Resets: 08:00 (in 70h 35m)

Fable
[------------------] 0% used
Resets: 08:00 (in 70h 35m)

🤖 OpenAI
Codex 5h: 0% used · rst 13:00
Codex week: 16% used · rst Wed 11:00
  ↳ free rate-limit resets available: 2
API billing today: $2.19 (gpt-a $2.15, gpt-b $0.03)

🌐 Anthropic: ✅ All Systems Operational
🌐 OpenAI: ✅ All Systems Operational
✅ Updated: 09:20 (5m ago)`
	if got := Status(full(), now, msk); got != want {
		t.Errorf("status mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestPreviewLinesComeFirst(t *testing.T) {
	lines := strings.SplitN(Status(full(), now, msk), "\n", 3)
	if lines[0] != "5h: 19% used · rst 11:30" || !strings.HasPrefix(lines[1], "Week: ") {
		t.Errorf("preview lines wrong: %q", lines[:2])
	}
}

func TestNoClaudeData(t *testing.T) {
	got := Status(Snapshot{}, now, msk)
	want := "5h: ? used · rst ?\nWeek: ? used · rst ?\n\n⏳ Status: waiting for Claude Code data\n✅ Updated: never"
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestMissingWindows(t *testing.T) {
	s := Snapshot{Claude: &usage.Claude{CapturedAt: at(9, 20, 3)}}
	got := Status(s, now, msk)
	for _, must := range []string{"Current session: unknown", "Weekly limits\nunknown"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in\n%s", must, got)
		}
	}
}

func TestStaleFooter(t *testing.T) {
	s := full()
	s.Claude.CapturedAt = at(8, 40, 3)
	if got := Status(s, now, msk); !strings.HasSuffix(got, "✅ Updated: 08:40 (45m ago) stale") {
		t.Errorf("footer wrong:\n%s", got)
	}
}

func TestServiceProblemsListed(t *testing.T) {
	s := Snapshot{Services: []ServiceView{
		{Name: "Anthropic", Svc: &usage.Service{
			Indicator: "major", Summary: "Partial System Outage",
			Components: []usage.Component{{Name: "API", Status: "operational"}, {Name: "Chat", Status: "partial_outage"}, {Name: "Login", Status: "under_maintenance"}},
			Incidents: []usage.Incident{
				{Name: "Old", Status: "resolved"}, {Name: "One", Status: "investigating"}, {Name: "Two", Status: "identified"},
				{Name: "Three", Status: "monitoring"}, {Name: "Four", Status: "investigating"}, {Name: "Pm", Status: "postmortem"},
			},
		}},
		{Name: "OpenAI"},
	}}
	got := Status(s, now, msk)
	for _, must := range []string{
		"🌐 Anthropic: 🟠 Partial System Outage",
		"  🟠 Chat: partial outage", "  🔧 Login: under maintenance",
		"  ⚠️ Incident: One", "  ⚠️ Incident: Three",
		"🌐 OpenAI: ❔ status temporarily unavailable",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in\n%s", must, got)
		}
	}
	for _, mustNot := range []string{"API:", "Incident: Old", "Incident: Four", "Incident: Pm"} {
		if strings.Contains(got, mustNot) {
			t.Errorf("unexpected %q in\n%s", mustNot, got)
		}
	}
}

func TestOpenAIErrorsAndEmpty(t *testing.T) {
	got := Status(Snapshot{OpenAI: &usage.OpenAI{CodexErr: true, BillingErr: true}}, now, msk)
	if !strings.Contains(got, "Codex (subscription): read error") || !strings.Contains(got, "API billing: read error") {
		t.Errorf("errors not shown:\n%s", got)
	}
	if got := Status(Snapshot{OpenAI: &usage.OpenAI{}}, now, msk); strings.Contains(got, "OpenAI") {
		t.Errorf("an empty OpenAI section must be omitted:\n%s", got)
	}
}

func TestProgressBarBounds(t *testing.T) {
	for p, want := range map[float64]string{
		-5: "[------------------]", 0: "[------------------]",
		50: "[#########---------]", 100: "[##################]", 250: "[##################]",
	} {
		if got := progressBar(p); got != want {
			t.Errorf("progressBar(%v) = %s, want %s", p, got, want)
		}
	}
}

func TestResetFormatting(t *testing.T) {
	r := renderer{now: now, loc: msk}
	if got := r.resetShort(time.Time{}); got != "?" {
		t.Errorf("zero reset = %q", got)
	}
	if got := r.resetLong(now.Add(-time.Hour)); got != "09:25 (in 0m)" && !strings.Contains(got, "(in 0m)") {
		t.Errorf("past reset must clamp to 0m, got %q", got)
	}
	// Same instant in another zone renders in the configured one.
	if got := r.resetShort(time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC)); got != "11:30" {
		t.Errorf("zone conversion: %q", got)
	}
}

func TestAlertText(t *testing.T) {
	n := alerts.Notice{Label: "Claude 5h", Threshold: 85, ResetsAt: at(11, 30, 3)}
	if got := Alert(n, msk); !strings.HasPrefix(got, "⚠️ Claude 5h: 85% used, resets: ") {
		t.Errorf("alert = %q", got)
	}
	if got := Alert(alerts.Notice{Label: "X", Threshold: 92.5}, msk); got != "⚠️ X: 92.5% used" {
		t.Errorf("alert without reset = %q", got)
	}
}
