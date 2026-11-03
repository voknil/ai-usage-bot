package alerts

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var th = []float64{85, 95}

func target(pct float64) []Target {
	return []Target{{Key: "claude_5h", Label: "Claude 5h", Percent: pct, ResetsAt: time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC)}}
}

func TestCrossingThresholdsNotifiesOnce(t *testing.T) {
	var s Store
	if plans := s.Evaluate(target(50), th); len(plans) != 0 {
		t.Fatalf("below thresholds: %+v", plans)
	}
	plans := s.Evaluate(target(86), th)
	if len(plans) != 1 || len(plans[0].Notify) != 1 || plans[0].Notify[0].Threshold != 85 {
		t.Fatalf("85%% crossing: %+v", plans)
	}
	s.Sent("claude_5h", 85, 101)

	if plans := s.Evaluate(target(88), th); len(plans) != 0 {
		t.Fatalf("85%% must not repeat: %+v", plans)
	}
	plans = s.Evaluate(target(96), th)
	if len(plans) != 1 || plans[0].Notify[0].Threshold != 95 {
		t.Fatalf("95%% crossing: %+v", plans)
	}
	s.Sent("claude_5h", 95, 102)
}

func TestJumpingPastBothSendsBoth(t *testing.T) {
	var s Store
	plans := s.Evaluate(target(97), th)
	if len(plans) != 1 || len(plans[0].Notify) != 2 {
		t.Fatalf("got %+v", plans)
	}
}

func TestResetDeletesAlertsAndRearms(t *testing.T) {
	var s Store
	s.Evaluate(target(96), th)
	s.Sent("claude_5h", 85, 101)
	s.Sent("claude_5h", 95, 102)

	plans := s.Evaluate(target(3), th)
	if len(plans) != 1 || !reflect.DeepEqual(plans[0].Delete, []int64{101, 102}) {
		t.Fatalf("reset must delete both alerts: %+v", plans)
	}
	if st := s.Windows["claude_5h"]; len(st.Notified) != 0 || len(st.MessageIDs) != 0 {
		t.Errorf("state not cleared: %+v", st)
	}
	// Usage climbing again re-arms the alert.
	plans = s.Evaluate(target(90), th)
	if len(plans) != 1 || plans[0].Notify[0].Threshold != 85 {
		t.Fatalf("must re-arm after reset: %+v", plans)
	}
}

func TestSmallDipIsNotAReset(t *testing.T) {
	var s Store
	s.Evaluate(target(90), th)
	s.Sent("claude_5h", 85, 5)
	if plans := s.Evaluate(target(87), th); len(plans) != 0 {
		t.Fatalf("a 3-point dip must not reset: %+v", plans)
	}
}

func TestWindowsAreIndependent(t *testing.T) {
	var s Store
	plans := s.Evaluate([]Target{
		{Key: "a", Label: "A", Percent: 90},
		{Key: "b", Label: "B", Percent: 10},
	}, th)
	if len(plans) != 1 || plans[0].Key != "a" {
		t.Fatalf("got %+v", plans)
	}
}

// The state file written by the shell version must load unchanged.
func TestLegacyStateCompatible(t *testing.T) {
	legacy := `{"windows":{"claude_5h":{"resets_at":"2026-11-03T08:30:00Z","last_pct":96.0,"notified":[85,95],"message_ids":{"85":11,"95":12}}}}`
	var s Store
	if err := json.Unmarshal([]byte(legacy), &s); err != nil {
		t.Fatal(err)
	}
	plans := s.Evaluate(target(96), th)
	if len(plans) != 0 {
		t.Fatalf("already-notified thresholds must stay quiet: %+v", plans)
	}
	plans = s.Evaluate(target(2), th)
	if len(plans) != 1 || !reflect.DeepEqual(plans[0].Delete, []int64{11, 12}) {
		t.Fatalf("legacy message IDs must be deleted on reset: %+v", plans)
	}
	out, err := json.Marshal(&s)
	if err != nil || len(out) == 0 {
		t.Fatalf("marshal: %v", err)
	}
}
