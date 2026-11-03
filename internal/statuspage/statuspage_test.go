package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sample = `{"status":{"indicator":"minor","description":"Minor Service Outage"},
"components":[{"name":"API","status":"operational"},{"name":"Chat","status":"degraded_performance"}],
"incidents":[{"name":"Elevated errors","status":"investigating"}]}`

func TestParse(t *testing.T) {
	s, err := Parse("Anthropic", []byte(sample), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if s.Indicator != "minor" || len(s.Components) != 2 || s.Components[1].Status != "degraded_performance" ||
		len(s.Incidents) != 1 {
		t.Errorf("unexpected: %+v", s)
	}
	for _, bad := range []string{`{}`, `nope`, `{"status":{}}`} {
		if _, err := Parse("x", []byte(bad), time.Now()); err == nil {
			t.Errorf("Parse(%s) must fail", bad)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			http.Error(w, "x", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	s, err := New("X", srv.URL+"/ok").Fetch(context.Background())
	if err != nil || s.Name != "X" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := New("X", srv.URL+"/down").Fetch(context.Background()); err == nil {
		t.Fatal("a non-200 response must be an error")
	}
}
