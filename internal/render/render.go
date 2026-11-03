// Package render turns usage data into the text of the Telegram status message.
//
// The first two lines are written for chat previews:
//
//	5h: 13% used · rst 18:20
//	Week: 53% used · rst Fri 08:00
package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/voknil/ai-usage-bot/internal/alerts"
	"github.com/voknil/ai-usage-bot/internal/usage"
)

// staleAfter marks data older than this as stale in the footer.
const staleAfter = 20 * time.Minute

// maxIncidents bounds how many open incidents are listed per service.
const maxIncidents = 3

// ServiceView is one public status page; Svc is nil when it could not be read.
type ServiceView struct {
	Name string
	Svc  *usage.Service
}

// Snapshot is everything the message shows.
type Snapshot struct {
	Claude   *usage.Claude
	OpenAI   *usage.OpenAI
	Services []ServiceView
}

// Status renders the status message. now and loc fix the clock and time zone.
func Status(s Snapshot, now time.Time, loc *time.Location) string {
	r := renderer{now: now.In(loc), loc: loc}
	return r.status(s)
}

// Alert renders a threshold warning.
func Alert(n alerts.Notice, loc *time.Location) string {
	r := renderer{now: time.Now().In(loc), loc: loc}
	text := fmt.Sprintf("⚠️ %s: %s%% used", n.Label, trimFloat(n.Threshold))
	if !n.ResetsAt.IsZero() {
		text += ", resets: " + r.resetShort(n.ResetsAt)
	}
	return text
}

type renderer struct {
	now time.Time
	loc *time.Location
}

func (r renderer) status(s Snapshot) string {
	var lines []string
	c := s.Claude

	var session, week *usage.Window
	if c != nil {
		session, week = c.Session, c.Week
	}
	lines = append(lines, r.topLine("5h", session), r.topLine("Week", week))

	if c == nil {
		lines = append(lines, "", "⏳ Status: waiting for Claude Code data")
	} else {
		lines = append(lines, "")
		if session != nil {
			lines = append(lines, r.block(*session)...)
		} else {
			lines = append(lines, "Current session: unknown")
		}
		lines = append(lines, "", "Weekly limits")
		if week != nil {
			lines = append(lines, r.block(*week)...)
		}
		for _, w := range c.Others {
			lines = append(lines, "")
			lines = append(lines, r.block(w)...)
		}
		if week == nil && len(c.Others) == 0 {
			lines = append(lines, "unknown")
		}
	}

	if s.OpenAI != nil {
		if section := r.openAI(s.OpenAI); len(section) > 0 {
			lines = append(lines, "")
			lines = append(lines, section...)
		}
	}

	if len(s.Services) > 0 {
		lines = append(lines, "")
		for _, sv := range s.Services {
			lines = append(lines, r.service(sv)...)
		}
	}

	lines = append(lines, r.footer(c))
	return strings.Join(lines, "\n")
}

func (r renderer) topLine(label string, w *usage.Window) string {
	if w == nil {
		return fmt.Sprintf("%s: ? used · rst ?", label)
	}
	return fmt.Sprintf("%s: %.0f%% used · rst %s", label, w.Percent, r.resetShort(w.ResetsAt))
}

func (r renderer) block(w usage.Window) []string {
	out := []string{w.Label, fmt.Sprintf("%s %.0f%% used", progressBar(w.Percent), w.Percent)}
	if !w.ResetsAt.IsZero() {
		out = append(out, "Resets: "+r.resetLong(w.ResetsAt))
	}
	return out
}

func (r renderer) openAI(o *usage.OpenAI) []string {
	var lines []string
	if c := o.Codex; c != nil {
		if c.Session != nil {
			lines = append(lines, fmt.Sprintf("Codex 5h: %.0f%% used · rst %s", c.Session.Percent, r.resetShort(c.Session.ResetsAt)))
		}
		if c.Week != nil {
			lines = append(lines, fmt.Sprintf("Codex week: %.0f%% used · rst %s", c.Week.Percent, r.resetShort(c.Week.ResetsAt)))
		}
		if c.FreeResets > 0 {
			lines = append(lines, fmt.Sprintf("  ↳ free rate-limit resets available: %d", c.FreeResets))
		}
	} else if o.CodexErr {
		lines = append(lines, "Codex (subscription): read error")
	}
	if b := o.Billing; b != nil {
		text := fmt.Sprintf("API billing today: $%.2f", b.TodayUSD)
		var top []string
		for i, m := range b.ByModel {
			if i == 3 {
				break
			}
			top = append(top, fmt.Sprintf("%s $%.2f", m.Model, m.USD))
		}
		if len(top) > 0 {
			text += " (" + strings.Join(top, ", ") + ")"
		}
		lines = append(lines, text)
	} else if o.BillingErr {
		lines = append(lines, "API billing: read error")
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{"🤖 OpenAI"}, lines...)
}

var indicatorIcons = map[string]string{"none": "✅", "minor": "⚠️", "major": "🟠", "critical": "🔴"}

var componentIcons = map[string]string{
	"degraded_performance": "⚠️",
	"partial_outage":       "🟠",
	"major_outage":         "🔴",
	"under_maintenance":    "🔧",
}

// service renders one status page: a headline, plus the concrete components and
// incidents only when something is wrong.
func (r renderer) service(sv ServiceView) []string {
	if sv.Svc == nil {
		return []string{fmt.Sprintf("🌐 %s: ❔ status temporarily unavailable", sv.Name)}
	}
	icon, ok := indicatorIcons[strings.ToLower(sv.Svc.Indicator)]
	if !ok {
		icon = "❔"
	}
	summary := sv.Svc.Summary
	if summary == "" {
		summary = "Unknown"
	}
	lines := []string{fmt.Sprintf("🌐 %s: %s %s", sv.Name, icon, summary)}
	for _, c := range sv.Svc.Components {
		if icon, bad := componentIcons[c.Status]; bad {
			lines = append(lines, fmt.Sprintf("  %s %s: %s", icon, c.Name, strings.ReplaceAll(c.Status, "_", " ")))
		}
	}
	shown := 0
	for _, i := range sv.Svc.Incidents {
		if i.Status == "resolved" || i.Status == "postmortem" {
			continue
		}
		if shown == maxIncidents {
			break
		}
		name := i.Name
		if name == "" {
			name = "unknown"
		}
		lines = append(lines, "  ⚠️ Incident: "+name)
		shown++
	}
	return lines
}

func (r renderer) footer(c *usage.Claude) string {
	if c == nil {
		return "✅ Updated: never"
	}
	if c.CapturedAt.IsZero() {
		return "✅ Updated: unknown"
	}
	age := int(r.now.Sub(c.CapturedAt).Minutes())
	if age < 0 {
		age = 0
	}
	stale := ""
	if r.now.Sub(c.CapturedAt) > staleAfter {
		stale = " stale"
	}
	return fmt.Sprintf("✅ Updated: %s (%dm ago)%s", c.CapturedAt.In(r.loc).Format("15:04"), age, stale)
}

// resetShort is "18:20" for today and "Fri 08:00" otherwise.
func (r renderer) resetShort(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	t = t.In(r.loc)
	y1, m1, d1 := t.Date()
	y2, m2, d2 := r.now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// resetLong is "18:20 (in 1h 5m)".
func (r renderer) resetLong(t time.Time) string {
	mins := int(t.Sub(r.now).Minutes())
	if mins < 0 {
		mins = 0
	}
	h, m := mins/60, mins%60
	local := t.In(r.loc).Format("15:04")
	if h > 0 {
		return fmt.Sprintf("%s (in %dh %dm)", local, h, m)
	}
	return fmt.Sprintf("%s (in %dm)", local, m)
}

const barWidth = 18

func progressBar(p float64) string {
	p = math.Max(0, math.Min(100, p))
	filled := int(math.RoundToEven(barWidth * p / 100))
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled) + "]"
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}
