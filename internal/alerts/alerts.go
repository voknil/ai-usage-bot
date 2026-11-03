// Package alerts decides when to send threshold warnings and when to retract
// them. It is pure bookkeeping: callers do the Telegram calls and report back
// the IDs of the messages they sent.
package alerts

import (
	"sort"
	"strconv"
	"time"
)

// resetDrop is how far usage must fall between two readings to count as a
// window reset. The reset timestamp itself is not used: some sources report a
// rolling value that changes on every poll.
const resetDrop = 5

// Target is one tracked window.
type Target struct {
	Key      string
	Label    string
	Percent  float64
	ResetsAt time.Time
}

// WindowState is what is remembered per window. The JSON layout matches the
// shell version's state file.
type WindowState struct {
	ResetsAt   string           `json:"resets_at"`
	LastPct    float64          `json:"last_pct"`
	Notified   []float64        `json:"notified"`
	MessageIDs map[string]int64 `json:"message_ids"`
}

// Store is the persisted alert state.
type Store struct {
	Windows map[string]*WindowState `json:"windows"`
}

// Notice asks the caller to send one alert message.
type Notice struct {
	Key       string
	Label     string
	Threshold float64
	Percent   float64
	ResetsAt  time.Time
}

// Plan is the work for one window.
type Plan struct {
	Key string
	// Delete lists alert messages to remove because the window reset.
	Delete []int64
	Notify []Notice
}

// Evaluate updates the store with the latest readings and returns what to do.
// Windows with no reading are left alone.
func (s *Store) Evaluate(targets []Target, thresholds []float64) []Plan {
	if s.Windows == nil {
		s.Windows = map[string]*WindowState{}
	}
	var plans []Plan
	for _, t := range targets {
		st := s.Windows[t.Key]
		if st == nil {
			st = &WindowState{LastPct: -1}
			s.Windows[t.Key] = st
		}
		if st.MessageIDs == nil {
			st.MessageIDs = map[string]int64{}
		}
		p := Plan{Key: t.Key}
		if st.LastPct >= 0 && t.Percent < st.LastPct-resetDrop {
			for _, id := range st.MessageIDs {
				p.Delete = append(p.Delete, id)
			}
			sort.Slice(p.Delete, func(i, j int) bool { return p.Delete[i] < p.Delete[j] })
			st.Notified = nil
			st.MessageIDs = map[string]int64{}
		}
		for _, th := range thresholds {
			if t.Percent >= th && !contains(st.Notified, th) {
				p.Notify = append(p.Notify, Notice{
					Key: t.Key, Label: t.Label, Threshold: th, Percent: t.Percent, ResetsAt: t.ResetsAt,
				})
			}
		}
		st.LastPct = t.Percent
		if !t.ResetsAt.IsZero() {
			st.ResetsAt = t.ResetsAt.UTC().Format(time.RFC3339)
		}
		if len(p.Delete) > 0 || len(p.Notify) > 0 {
			plans = append(plans, p)
		}
	}
	return plans
}

// Sent records that the alert for threshold went out as messageID.
func (s *Store) Sent(key string, threshold float64, messageID int64) {
	st := s.Windows[key]
	if st == nil {
		return
	}
	if st.MessageIDs == nil {
		st.MessageIDs = map[string]int64{}
	}
	st.MessageIDs[strconv.FormatFloat(threshold, 'f', -1, 64)] = messageID
	if !contains(st.Notified, threshold) {
		st.Notified = append(st.Notified, threshold)
		sort.Float64s(st.Notified)
	}
}

func contains(xs []float64, x float64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
