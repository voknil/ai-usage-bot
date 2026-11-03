// Package openai reads ChatGPT/Codex subscription usage (through the CodexBar
// CLI) and today's pay-as-you-go API spend (through the organization costs API).
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/voknil/ai-usage-bot/internal/usage"
)

const costsAPI = "https://api.openai.com/v1/organization/costs"

// Client fetches the OpenAI section.
type Client struct {
	// CodexBarBin is the codexbar executable; empty means look it up on PATH.
	CodexBarBin string
	// AdminKey enables billing; it must be an organization admin key with the
	// api.usage.read scope.
	AdminKey string
	HTTP     *http.Client
	Endpoint string
	Run      func(ctx context.Context, bin string, args ...string) ([]byte, error)
	Now      func() time.Time
}

// New returns a Client with production defaults.
func New(codexBarBin, adminKey string) *Client {
	return &Client{
		CodexBarBin: codexBarBin,
		AdminKey:    adminKey,
		HTTP:        &http.Client{Timeout: 10 * time.Second},
		Endpoint:    costsAPI,
		Run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).Output() //nolint:gosec // bin is operator configuration
		},
		Now: time.Now,
	}
}

// Configured reports whether anything can be fetched at all.
func (c *Client) Configured() bool {
	return c.AdminKey != "" || c.codexBin() != ""
}

func (c *Client) codexBin() string {
	if c.CodexBarBin != "" {
		return c.CodexBarBin
	}
	p, err := exec.LookPath("codexbar")
	if err != nil {
		return ""
	}
	return p
}

// Fetch collects both halves independently. It returns an error only when
// nothing could be read, so a broken billing key does not hide Codex usage.
func (c *Client) Fetch(ctx context.Context) (*usage.OpenAI, error) {
	out := &usage.OpenAI{CapturedAt: c.Now()}
	var errs []error

	if bin := c.codexBin(); bin != "" {
		codex, err := c.fetchCodex(ctx, bin)
		if err != nil {
			out.CodexErr = true
			errs = append(errs, err)
		} else {
			out.Codex = codex
		}
	}
	if c.AdminKey != "" {
		b, err := c.fetchBilling(ctx)
		if err != nil {
			out.BillingErr = true
			errs = append(errs, err)
		} else {
			out.Billing = b
		}
	}
	if out.Codex == nil && out.Billing == nil {
		if len(errs) == 0 {
			return nil, errors.New("openai: nothing configured")
		}
		return nil, fmt.Errorf("openai: %w", errors.Join(errs...))
	}
	return out, nil
}

// ---- Codex via CodexBar ----

type codexWindow struct {
	UsedPercent *float64 `json:"usedPercent"`
	ResetsAt    string   `json:"resetsAt"`
}

type codexEntry struct {
	Usage struct {
		AccountEmail string       `json:"accountEmail"`
		Primary      *codexWindow `json:"primary"`
		Secondary    *codexWindow `json:"secondary"`
		ResetCredits struct {
			AvailableCount int `json:"availableCount"`
		} `json:"codexResetCredits"`
	} `json:"usage"`
}

func (w *codexWindow) window(key, label string) *usage.Window {
	if w == nil || w.UsedPercent == nil {
		return nil
	}
	out := &usage.Window{Key: key, Label: label, Percent: *w.UsedPercent}
	if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		out.ResetsAt = t
	}
	return out
}

// ParseCodex reads `codexbar usage --provider codex --format json`. CodexBar
// reports two independent rolling windows: primary (5 hours) and secondary
// (weekly).
func ParseCodex(data []byte) (*usage.Codex, error) {
	var entries []codexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode codexbar output: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("codexbar returned no entries")
	}
	e := entries[0]
	c := &usage.Codex{
		Account:    e.Usage.AccountEmail,
		Session:    e.Usage.Primary.window("codex_5h", "Codex 5h"),
		Week:       e.Usage.Secondary.window("codex_week", "Codex week"),
		FreeResets: e.Usage.ResetCredits.AvailableCount,
	}
	if c.Session == nil && c.Week == nil {
		return nil, errors.New("codexbar reported no usage windows")
	}
	return c, nil
}

func (c *Client) fetchCodex(ctx context.Context, bin string) (*usage.Codex, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.Run(ctx, bin, "usage", "--provider", "codex", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("codexbar: %w", err)
	}
	return ParseCodex(out)
}

// ---- Billing ----

type costsResponse struct {
	Data []struct {
		Results []struct {
			Amount struct {
				Value float64 `json:"value"`
			} `json:"amount"`
			LineItem string `json:"line_item"`
		} `json:"results"`
	} `json:"data"`
}

// ParseCosts sums a costs API response by model.
func ParseCosts(data []byte) (*usage.Billing, error) {
	var r costsResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode costs response: %w", err)
	}
	var total float64
	byModel := map[string]float64{}
	for _, bucket := range r.Data {
		for _, res := range bucket.Results {
			total += res.Amount.Value
			model, _, _ := strings.Cut(res.LineItem, ",")
			model = strings.TrimSpace(model)
			if model == "" {
				model = "other"
			}
			byModel[model] += res.Amount.Value
		}
	}
	b := &usage.Billing{TodayUSD: total}
	for m, v := range byModel {
		b.ByModel = append(b.ByModel, usage.ModelCost{Model: m, USD: v})
	}
	sort.Slice(b.ByModel, func(i, j int) bool {
		if b.ByModel[i].USD != b.ByModel[j].USD {
			return b.ByModel[i].USD > b.ByModel[j].USD
		}
		return b.ByModel[i].Model < b.ByModel[j].Model
	})
	return b, nil
}

func (c *Client) fetchBilling(ctx context.Context) (*usage.Billing, error) {
	now := c.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	q := url.Values{}
	q.Set("start_time", strconv.FormatInt(start.Unix(), 10))
	q.Set("limit", "31")
	q.Set("group_by", "line_item")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.AdminKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("costs api: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("costs api: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("costs api: HTTP %d", resp.StatusCode)
	}
	return ParseCosts(body)
}
