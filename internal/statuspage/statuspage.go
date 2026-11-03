// Package statuspage reads public Atlassian Statuspage summaries, which both
// Anthropic and OpenAI publish.
package statuspage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/voknil/ai-usage-bot/internal/usage"
)

// Well-known summaries.
const (
	AnthropicURL = "https://status.anthropic.com/api/v2/summary.json"
	OpenAIURL    = "https://status.openai.com/api/v2/summary.json"
)

// Client fetches one status page.
type Client struct {
	Name string
	URL  string
	HTTP *http.Client
	Now  func() time.Time
}

// New returns a Client for the given page.
func New(name, url string) *Client {
	return &Client{Name: name, URL: url, HTTP: &http.Client{Timeout: 8 * time.Second}, Now: time.Now}
}

type summary struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
	Incidents []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"incidents"`
}

// Parse converts a summary.json body.
func Parse(name string, body []byte, now time.Time) (*usage.Service, error) {
	var s summary
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	if s.Status.Indicator == "" {
		return nil, errors.New("summary has no status indicator")
	}
	svc := &usage.Service{
		Name:       name,
		CapturedAt: now,
		Indicator:  s.Status.Indicator,
		Summary:    s.Status.Description,
	}
	for _, c := range s.Components {
		svc.Components = append(svc.Components, usage.Component{Name: c.Name, Status: c.Status})
	}
	for _, i := range s.Incidents {
		svc.Incidents = append(svc.Incidents, usage.Incident{Name: i.Name, Status: i.Status})
	}
	return svc, nil
}

// Fetch downloads and parses the summary.
func (c *Client) Fetch(ctx context.Context) (*usage.Service, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s status: %w", c.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s status: HTTP %d", c.Name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%s status: %w", c.Name, err)
	}
	return Parse(c.Name, body, c.Now())
}
