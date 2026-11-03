// Package usage defines the data every provider produces.
package usage

import "time"

// Window is one rate-limit window: how much of it is used and when it resets.
type Window struct {
	// Key is a stable identifier ("five_hour", "seven_day", "opus", …).
	Key string
	// Label is the human name ("Current session", "All models", "Opus").
	Label string
	// Percent is the share used, 0–100.
	Percent float64
	// ResetsAt is zero when the provider did not say.
	ResetsAt time.Time
}

// Claude is a snapshot of Claude Code subscription limits.
type Claude struct {
	CapturedAt time.Time
	// Source says where the numbers came from ("oauth_api", "cli").
	Source string
	// Session is the rolling 5-hour window, Week the all-models weekly one.
	Session *Window
	Week    *Window
	// Others are further weekly limits (per model, extra usage).
	Others []Window
}

// Codex is a snapshot of ChatGPT/Codex subscription limits.
type Codex struct {
	Account string
	Session *Window
	Week    *Window
	// FreeResets is the number of free rate-limit resets available.
	FreeResets int
}

// Billing is today's pay-as-you-go OpenAI API spend.
type Billing struct {
	TodayUSD float64
	// ByModel is sorted by descending cost.
	ByModel []ModelCost
}

// ModelCost is spend attributed to one model.
type ModelCost struct {
	Model string
	USD   float64
}

// OpenAI groups what the OpenAI section can show. Each part is independent;
// an Err is shown instead of the part when its fetch failed.
type OpenAI struct {
	CapturedAt time.Time
	Codex      *Codex
	CodexErr   bool
	Billing    *Billing
	BillingErr bool
}

// Service is a public status-page summary.
type Service struct {
	Name       string
	CapturedAt time.Time
	Indicator  string // none | minor | major | critical
	Summary    string
	Components []Component
	Incidents  []Incident
}

// Component is one part of a service and its status.
type Component struct {
	Name   string
	Status string // operational | degraded_performance | partial_outage | major_outage | under_maintenance
}

// Incident is an open incident on a status page.
type Incident struct {
	Name   string
	Status string
}
