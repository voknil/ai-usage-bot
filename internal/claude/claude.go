// Package claude reads Claude Code subscription usage, from the OAuth usage
// API when credentials are available and from `claude /usage` otherwise.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/voknil/ai-usage-bot/internal/usage"
)

const usageAPI = "https://api.anthropic.com/api/oauth/usage"

// Client fetches Claude usage.
type Client struct {
	// Bin is the claude executable; empty means look it up on PATH.
	Bin             string
	CredentialsFile string
	HTTP            *http.Client
	// Endpoint overrides the usage API URL (tests).
	Endpoint string
	// Run executes the CLI and returns its output (tests inject a fake).
	Run func(ctx context.Context, bin string, args ...string) ([]byte, error)
	Now func() time.Time
}

// New returns a Client with production defaults.
func New(bin, credentialsFile string) *Client {
	return &Client{
		Bin:             bin,
		CredentialsFile: credentialsFile,
		HTTP:            &http.Client{Timeout: 8 * time.Second},
		Endpoint:        usageAPI,
		Run:             runCommand,
		Now:             time.Now,
	}
}

// Fetch returns current usage: the API first, the CLI as fallback. The error
// joins both failures when neither works.
func (c *Client) Fetch(ctx context.Context) (*usage.Claude, error) {
	u, apiErr := c.FetchAPI(ctx)
	if apiErr == nil {
		return u, nil
	}
	u, cliErr := c.FetchCLI(ctx)
	if cliErr == nil {
		return u, nil
	}
	return nil, fmt.Errorf("claude usage unavailable: %w", errors.Join(apiErr, cliErr))
}

// ---- OAuth API ----

type apiLimit struct {
	Utilization *float64 `json:"utilization"`
	Used        *float64 `json:"used_percentage"`
	ResetsAt    string   `json:"resets_at"`
}

func (l *apiLimit) window(key, label string) *usage.Window {
	if l == nil {
		return nil
	}
	pct := l.Utilization
	if pct == nil {
		pct = l.Used
	}
	if pct == nil {
		return nil
	}
	w := &usage.Window{Key: key, Label: label, Percent: *pct}
	if t, err := time.Parse(time.RFC3339, l.ResetsAt); err == nil {
		w.ResetsAt = t
	}
	return w
}

type apiResponse struct {
	FiveHour       *apiLimit `json:"five_hour"`
	SevenDay       *apiLimit `json:"seven_day"`
	SevenDaySonnet *apiLimit `json:"seven_day_sonnet"`
	SevenDayOpus   *apiLimit `json:"seven_day_opus"`
}

// ParseAPI converts the usage API response.
func ParseAPI(body []byte, now time.Time) (*usage.Claude, error) {
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}
	u := &usage.Claude{CapturedAt: now, Source: "oauth_api"}
	u.Session = r.FiveHour.window("five_hour", "Current session")
	u.Week = r.SevenDay.window("seven_day", "All models")
	if w := r.SevenDaySonnet.window("sonnet", "Sonnet"); w != nil {
		u.Others = append(u.Others, *w)
	}
	if w := r.SevenDayOpus.window("opus", "Opus"); w != nil {
		u.Others = append(u.Others, *w)
	}
	if u.Session == nil && u.Week == nil {
		return nil, errors.New("usage response has no limits")
	}
	return u, nil
}

type credentials struct {
	ClaudeAIOAuth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// FetchAPI asks the OAuth usage endpoint. It needs the access token Claude
// Code keeps in its credentials file (absent on macOS installs that use the
// keychain, in which case the CLI path is used).
func (c *Client) FetchAPI(ctx context.Context) (*usage.Claude, error) {
	data, err := os.ReadFile(c.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	var cred credentials
	if err := json.Unmarshal(data, &cred); err != nil || cred.ClaudeAIOAuth.AccessToken == "" {
		return nil, errors.New("credentials: no OAuth access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.ClaudeAIOAuth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage api: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("usage api: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage api: HTTP %d", resp.StatusCode)
	}
	return ParseAPI(body, c.Now())
}

// ---- CLI ----

var (
	reWindow = regexp.MustCompile(`(?im)^\s*(Current session|Current week \(([^)]+)\)):\s*([0-9]+(?:\.[0-9]+)?)%\s+used(?:.*?resets\s+(.+?))?\s*$`)
	reReset  = regexp.MustCompile(`^([A-Z][a-z]{2})\s+(\d{1,2})\s+at\s+(\d{1,2}(?::\d{2})?)\s*([ap]m)\s+\(([^)]+)\)`)
)

// ParseCLI extracts the limits from `claude /usage` output.
func ParseCLI(text string, now time.Time) (*usage.Claude, error) {
	u := &usage.Claude{CapturedAt: now, Source: "cli"}
	for _, m := range reWindow.FindAllStringSubmatch(text, -1) {
		pct, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			continue
		}
		w := usage.Window{Percent: pct}
		if m[4] != "" {
			w.ResetsAt = parseCLIReset(m[4], now)
		}
		switch {
		case strings.EqualFold(m[1], "Current session"):
			w.Key, w.Label = "five_hour", "Current session"
			u.Session = &w
		case strings.EqualFold(m[2], "all models"):
			w.Key, w.Label = "seven_day", "All models"
			u.Week = &w
		default:
			w.Key, w.Label = strings.ToLower(m[2]), m[2]
			u.Others = append(u.Others, w)
		}
	}
	if u.Session == nil && u.Week == nil && len(u.Others) == 0 {
		return nil, errors.New("could not parse claude /usage output")
	}
	return u, nil
}

// parseCLIReset reads "Nov 6 at 8am (Europe/Moscow)". The output carries no
// year; it is taken from now, rolling forward when the date would otherwise be
// far in the past (a December capture showing a January reset).
func parseCLIReset(s string, now time.Time) time.Time {
	m := reReset.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}
	}
	loc, err := time.LoadLocation(m[5])
	if err != nil {
		loc = time.UTC
	}
	clock := m[3]
	layout := "2006 Jan 2 3pm"
	if strings.Contains(clock, ":") {
		layout = "2006 Jan 2 3:04pm"
	}
	parse := func(year int) (time.Time, bool) {
		t, err := time.ParseInLocation(layout, fmt.Sprintf("%d %s %s %s%s", year, m[1], m[2], clock, m[4]), loc)
		return t, err == nil
	}
	t, ok := parse(now.In(loc).Year())
	if !ok {
		return time.Time{}
	}
	if t.Before(now.Add(-24 * time.Hour * 31)) {
		if next, ok := parse(t.Year() + 1); ok {
			t = next
		}
	}
	return t.UTC()
}

// FetchCLI runs `claude /usage`.
func (c *Client) FetchCLI(ctx context.Context) (*usage.Claude, error) {
	bin := c.Bin
	if bin == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			return nil, errors.New("claude executable not found")
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.Run(ctx, bin, "/usage")
	if err != nil {
		return nil, fmt.Errorf("claude /usage: %w", err)
	}
	return ParseCLI(string(out), c.Now())
}

func runCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, bin, args...).Output() //nolint:gosec // bin is operator configuration
}
