package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is where TokenWatch's dashboard listens by default
// (config.py: DASHBOARD_PORT = TOKENWATCH_DASHBOARD_PORT or 8878).
const DefaultBaseURL = "http://127.0.0.1:8878"

// ErrUnreachable reports that the dashboard could not be contacted at all. It
// is distinct from an HTTP error status so the UI can say "start TokenWatch"
// instead of showing a transport stack trace: a stopped service is the common
// case here, not an exceptional one.
var ErrUnreachable = errors.New("TokenWatch dashboard unreachable")

// Client is a read-only client for the TokenWatch dashboard API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a client for baseURL. A trailing slash is tolerated.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// BaseURL returns the configured service root.
func (c *Client) BaseURL() string { return c.baseURL }

// ValidateBaseURL rejects anything that is not an absolute http(s) URL with a
// host, so a typo is caught in the form rather than at request time.
func ValidateBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("a dashboard URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("use an http:// or https:// URL")
	}
	if u.Host == "" {
		return errors.New("the URL needs a host, for example 127.0.0.1:8878")
	}
	return nil
}

// get performs a GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, query string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+query, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GET %s: HTTP %d: %s", query, resp.StatusCode, detail(payload))
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode %s body: %w", query, err)
	}
	return nil
}

// detail pulls FastAPI's {"detail": ...} message, falling back to a truncated
// body so an unexpected error page is still diagnosable.
func detail(payload []byte) string {
	var envelope struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.Detail != nil {
		if s, ok := envelope.Detail.(string); ok {
			return s
		}
		if encoded, err := json.Marshal(envelope.Detail); err == nil {
			return string(encoded)
		}
	}
	trimmed := strings.TrimSpace(string(payload))
	if len(trimmed) > 300 {
		trimmed = trimmed[:300] + "..."
	}
	if trimmed == "" {
		return "(empty response)"
	}
	return trimmed
}

// Timeframes are the values every timeframe-taking endpoint accepts. They match
// db._timeframe_where exactly; anything else is rejected by the service.
var Timeframes = []string{"1h", "24h", "7d", "30d", "all"}

// ValidateTimeframe rejects a timeframe the service would refuse.
func ValidateTimeframe(value string) error {
	for _, allowed := range Timeframes {
		if value == allowed {
			return nil
		}
	}
	return fmt.Errorf("use one of %s", strings.Join(Timeframes, ", "))
}

// Stats calls GET /api/stats.
func (c *Client) Stats(ctx context.Context, timeframe string) (*UsageStats, error) {
	var out UsageStats
	if err := c.get(ctx, "/api/stats?timeframe="+url.QueryEscape(timeframe), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Recent calls GET /api/recent.
func (c *Client) Recent(ctx context.Context, limit int) ([]RequestRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	var out []RequestRow
	if err := c.get(ctx, fmt.Sprintf("/api/recent?limit=%d", limit), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Timeseries calls GET /api/timeseries.
func (c *Client) Timeseries(ctx context.Context, timeframe string) ([]TimeseriesBucket, error) {
	var out []TimeseriesBucket
	if err := c.get(ctx, "/api/timeseries?timeframe="+url.QueryEscape(timeframe), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CostByTag calls GET /api/cost/by-tag.
func (c *Client) CostByTag(ctx context.Context, timeframe string) ([]CostByTag, error) {
	var out []CostByTag
	if err := c.get(ctx, "/api/cost/by-tag?timeframe="+url.QueryEscape(timeframe), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CostByApp calls GET /api/cost/by-app.
func (c *Client) CostByApp(ctx context.Context, timeframe string) ([]CostByApp, error) {
	var out []CostByApp
	if err := c.get(ctx, "/api/cost/by-app?timeframe="+url.QueryEscape(timeframe), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CostBySession calls GET /api/cost/by-session.
func (c *Client) CostBySession(ctx context.Context, top int) ([]CostBySession, error) {
	if top <= 0 {
		top = 20
	}
	if top > 100 {
		top = 100
	}
	var out []CostBySession
	if err := c.get(ctx, fmt.Sprintf("/api/cost/by-session?top=%d", top), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CostForecast calls GET /api/cost/forecast.
func (c *Client) CostForecast(ctx context.Context) (*CostForecast, error) {
	var out CostForecast
	if err := c.get(ctx, "/api/cost/forecast", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CacheStats calls GET /api/cache/stats.
func (c *Client) CacheStats(ctx context.Context) (*CacheStats, error) {
	var out CacheStats
	if err := c.get(ctx, "/api/cache/stats", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BudgetStatus calls GET /api/budget/status.
func (c *Client) BudgetStatus(ctx context.Context) ([]BudgetStatus, error) {
	var out []BudgetStatus
	if err := c.get(ctx, "/api/budget/status", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RoutingStats calls GET /api/routing/stats.
func (c *Client) RoutingStats(ctx context.Context) ([]RoutingStat, error) {
	var out []RoutingStat
	if err := c.get(ctx, "/api/routing/stats", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ABTests calls GET /api/ab/list.
func (c *Client) ABTests(ctx context.Context) ([]ABTest, error) {
	var out []ABTest
	if err := c.get(ctx, "/api/ab/list", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ABReport calls GET /api/ab/report/{test_name}.
func (c *Client) ABReport(ctx context.Context, testName string) (*ABReport, error) {
	if strings.TrimSpace(testName) == "" {
		return nil, errors.New("a test name is required")
	}
	var out ABReport
	if err := c.get(ctx, "/api/ab/report/"+url.PathEscape(testName), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Upstreams calls GET /api/upstreams.
func (c *Client) Upstreams(ctx context.Context) ([]Upstream, error) {
	var out []Upstream
	if err := c.get(ctx, "/api/upstreams", &out); err != nil {
		return nil, err
	}
	return out, nil
}
