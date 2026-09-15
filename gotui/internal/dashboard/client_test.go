package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capturedEndpoint is one entry of testdata/endpoints.json: a real response
// recorded from the actual FastAPI app.
type capturedEndpoint struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

// loadCaptured reads the recorded payloads. The file is the real output of
// src/tokenwatch/dashboard_app.py with a fake Oracle-shaped Database, so these
// tests fail if db.py changes the JSON the TUI depends on.
func loadCaptured(t *testing.T) map[string]capturedEndpoint {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "endpoints.json"))
	if err != nil {
		t.Fatalf("read captured endpoints: %v", err)
	}
	var out map[string]capturedEndpoint
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse captured endpoints: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("captured endpoints file is empty")
	}
	return out
}

// newServer serves each captured body at its own path, so every client method
// is exercised against real service output rather than a hand-built stub.
func newServer(t *testing.T, captured map[string]capturedEndpoint) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		entry, ok := captured[key]
		if !ok {
			// /api/ab/report/{name} and the two limit/top params vary, so fall
			// back to a prefix match on the path alone.
			for candidate, value := range captured {
				if strings.HasPrefix(candidate, r.URL.Path+"?") || candidate == r.URL.Path {
					entry, ok = value, true
					break
				}
			}
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"no captured fixture for ` + key + `"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(entry.StatusCode)
		_, _ = w.Write(entry.Body)
	}))
}

// TestEveryMethodDecodesRealServiceOutput is the central shape guard: each
// method must decode the payload the real service produced.
func TestEveryMethodDecodesRealServiceOutput(t *testing.T) {
	captured := loadCaptured(t)
	server := newServer(t, captured)
	defer server.Close()
	client := NewClient(server.URL)
	ctx := context.Background()

	t.Run("Stats", func(t *testing.T) {
		stats, err := client.Stats(ctx, "24h")
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if stats.TotalRequests != 2 || stats.TotalEstimatedCost != 1.25 {
			t.Errorf("totals = %v/%v, want 2/1.25", stats.TotalRequests, stats.TotalEstimatedCost)
		}
		if stats.TotalCacheSavings != 0.4 {
			t.Errorf("cache savings = %v, want 0.4", stats.TotalCacheSavings)
		}
		// models is keyed by model_used and is a plain dict, not a model.
		model, ok := stats.Models["claude-sonnet-4-5"]
		if !ok {
			t.Fatalf("models = %v, want the claude-sonnet-4-5 entry", stats.Models)
		}
		if model.Requests != 2 || model.Cost != 1.25 {
			t.Errorf("model totals = %v/%v, want 2/1.25", model.Requests, model.Cost)
		}
	})

	t.Run("Recent", func(t *testing.T) {
		rows, err := client.Recent(ctx, 1)
		if err != nil {
			t.Fatalf("Recent: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		row := rows[0]
		if row.RequestID != "req-1" || row.ModelUsed != "claude-haiku-4-5" {
			t.Errorf("row = %+v, want req-1/claude-haiku-4-5", row)
		}
		if row.LatencyMS != 830 || row.StatusCode != 200 {
			t.Errorf("latency/status = %v/%v, want 830/200", row.LatencyMS, row.StatusCode)
		}
		if !row.WasCached() {
			t.Error("WasCached() = false, want true for cache_hit=1")
		}
		if row.CreatedAt != "2026-03-28T10:00:00" {
			t.Errorf("created_at = %q", row.CreatedAt)
		}
		// ab_test_id is null in the capture: JSON null must decode cleanly into
		// the numeric field rather than erroring.
		if row.ABTestID != 0 {
			t.Errorf("ABTestID = %v, want 0 for a null column", row.ABTestID)
		}
		if row.RoutingRuleID != 3 {
			t.Errorf("RoutingRuleID = %v, want 3", row.RoutingRuleID)
		}
	})

	t.Run("Timeseries", func(t *testing.T) {
		buckets, err := client.Timeseries(ctx, "24h")
		if err != nil {
			t.Fatalf("Timeseries: %v", err)
		}
		if len(buckets) != 1 || buckets[0].Cost != 1.25 || buckets[0].Requests != 2 {
			t.Errorf("buckets = %+v", buckets)
		}
	})

	t.Run("CostByTag", func(t *testing.T) {
		rows, err := client.CostByTag(ctx, "24h")
		if err != nil {
			t.Fatalf("CostByTag: %v", err)
		}
		if len(rows) != 1 || rows[0].Tag != "chat" || rows[0].AvgCost != 0.625 {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("CostByApp", func(t *testing.T) {
		rows, err := client.CostByApp(ctx, "24h")
		if err != nil {
			t.Fatalf("CostByApp: %v", err)
		}
		if len(rows) != 1 || rows[0].App != "claude-code" || rows[0].TotalCost != 1.25 {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("CostBySession", func(t *testing.T) {
		rows, err := client.CostBySession(ctx, 2)
		if err != nil {
			t.Fatalf("CostBySession: %v", err)
		}
		if len(rows) != 1 || rows[0].SessionID != "sess-1" || rows[0].Turns != 3 {
			t.Errorf("rows = %+v", rows)
		}
		if rows[0].Started != "2026-03-28T10:00:00" || rows[0].Ended != "2026-03-28T10:30:00" {
			t.Errorf("window = %q..%q", rows[0].Started, rows[0].Ended)
		}
	})

	t.Run("CostForecast", func(t *testing.T) {
		forecast, err := client.CostForecast(ctx)
		if err != nil {
			t.Fatalf("CostForecast: %v", err)
		}
		if forecast.MonthlyProjection != 15.0 || forecast.ActiveDays != 7 {
			t.Errorf("forecast = %+v", forecast)
		}
	})

	t.Run("RoutingStats", func(t *testing.T) {
		rows, err := client.RoutingStats(ctx)
		if err != nil {
			t.Fatalf("RoutingStats: %v", err)
		}
		if len(rows) != 1 || rows[0].RuleName != "small-prompts" || rows[0].AvgLatencyMS != 412.5 {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("CacheStats", func(t *testing.T) {
		stats, err := client.CacheStats(ctx)
		if err != nil {
			t.Fatalf("CacheStats: %v", err)
		}
		if stats.Entries != 4 || stats.TotalHits != 2 || stats.ActiveEntries != 4 {
			t.Errorf("stats = %+v", stats)
		}
	})

	t.Run("BudgetStatus", func(t *testing.T) {
		rows, err := client.BudgetStatus(ctx)
		if err != nil {
			t.Fatalf("BudgetStatus: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		row := rows[0]
		if row.Scope != "global" || row.LimitAmount != 10.0 || row.UtilizationPct != 12.5 {
			t.Errorf("row = %+v", row)
		}
		if !row.IsActive {
			t.Error("IsActive = false, want true (is_active is a real bool from the model)")
		}
		if row.ID == nil || *row.ID != 1 {
			t.Errorf("ID = %v, want 1", row.ID)
		}
	})

	t.Run("ABTests", func(t *testing.T) {
		tests, err := client.ABTests(ctx)
		if err != nil {
			t.Fatalf("ABTests: %v", err)
		}
		if len(tests) != 1 || tests[0].TestName != "latency" || tests[0].SplitPct != 50 {
			t.Errorf("tests = %+v", tests)
		}
	})

	t.Run("ABReport", func(t *testing.T) {
		report, err := client.ABReport(ctx, "latency")
		if err != nil {
			t.Fatalf("ABReport: %v", err)
		}
		if report.TestName != "latency" || len(report.Variants) != 1 {
			t.Fatalf("report = %+v", report)
		}
		variant := report.Variants[0]
		if variant.Variant != "claude-haiku-4-5" || variant.AvgLatencyMS != 400.0 {
			t.Errorf("variant = %+v", variant)
		}
		if variant.AvgOutputTokens != 90.5 {
			t.Errorf("avg output tokens = %v, want 90.5", variant.AvgOutputTokens)
		}
	})

	t.Run("Upstreams", func(t *testing.T) {
		upstreams, err := client.Upstreams(ctx)
		if err != nil {
			t.Fatalf("Upstreams: %v", err)
		}
		if len(upstreams) != 1 || upstreams[0].BaseURL != "https://api.anthropic.com" {
			t.Errorf("upstreams = %+v", upstreams)
		}
		if !upstreams[0].IsHealthy {
			t.Error("IsHealthy = false, want true")
		}
	})
}

// TestUnreachableIsDistinguishable keeps "the service is not running" a
// different error from "the service answered with a failure", because the UI
// tells the user to start TokenWatch in one case and shows the cause in the
// other.
func TestUnreachableIsDistinguishable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed := server.URL
	server.Close()

	_, err := NewClient(closed).Stats(context.Background(), "24h")
	if err == nil {
		t.Fatal("Stats to a closed server returned no error")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want it to wrap ErrUnreachable", err)
	}
}

// TestHTTPErrorSurfacesDetail pins FastAPI's {"detail": ...} being reported
// instead of a bare status code.
func TestHTTPErrorSurfacesDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"ORA-12541: cannot connect to Oracle"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL).Stats(context.Background(), "24h")
	if err == nil {
		t.Fatal("Stats returned no error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "ORA-12541") {
		t.Errorf("err = %v, want the service's own detail message", err)
	}
}

// TestValidateBaseURL rejects typos in the connection form rather than at
// request time.
func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"http://127.0.0.1:8878", false},
		{"https://tokenwatch.internal", false},
		{"127.0.0.1:8878", true}, // no scheme
		{"ftp://127.0.0.1", true},
		{"", true},
		{"http://", true}, // no host
	}
	for _, c := range cases {
		err := ValidateBaseURL(c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateBaseURL(%q) error = %v, wantErr %v", c.raw, err, c.wantErr)
		}
	}
}

// TestValidateTimeframe only allows the five values db._timeframe_where knows;
// anything else would silently be treated as 24h by the service.
func TestValidateTimeframe(t *testing.T) {
	for _, ok := range Timeframes {
		if err := ValidateTimeframe(ok); err != nil {
			t.Errorf("ValidateTimeframe(%q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateTimeframe("90d"); err == nil {
		t.Error("ValidateTimeframe(\"90d\") = nil, want an error")
	}
}

// TestRecentClampsLimit keeps an out-of-range limit from being rejected by the
// service's ge=1/le=500 constraint.
func TestRecentClampsLimit(t *testing.T) {
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := NewClient(server.URL)

	if _, err := client.Recent(context.Background(), 0); err != nil {
		t.Fatalf("Recent(0): %v", err)
	}
	if gotLimit != "50" {
		t.Errorf("limit = %q, want the 50 default", gotLimit)
	}
	if _, err := client.Recent(context.Background(), 5000); err != nil {
		t.Fatalf("Recent(5000): %v", err)
	}
	if gotLimit != "500" {
		t.Errorf("limit = %q, want it clamped to 500", gotLimit)
	}
}

// TestABReportNeedsAName refuses a blank test name rather than requesting
// /api/ab/report/ and getting a confusing 404.
func TestABReportNeedsAName(t *testing.T) {
	if _, err := NewClient("http://127.0.0.1:1").ABReport(context.Background(), "  "); err == nil {
		t.Fatal("ABReport with a blank name returned no error")
	}
}
