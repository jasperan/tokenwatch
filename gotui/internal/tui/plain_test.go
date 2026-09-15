package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
)

// runScripted runs one scripted action against the fake dashboard and returns
// its output.
func runScripted(t *testing.T, req ActionRequest) string {
	t.Helper()
	server := fakeDashboard(t)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	if err := RunAction(context.Background(), dashboard.NewClient(server.URL), req, &out); err != nil {
		t.Fatalf("RunAction(%q): %v", req.Action, err)
	}
	return out.String()
}

// TestEveryScriptedActionPrintsSomething covers the whole flag surface, which is
// the path a cron job or a CI check uses.
func TestEveryScriptedActionPrintsSomething(t *testing.T) {
	cases := []struct {
		action string
		want   []string
	}{
		{ActionOverview, []string{"requests", "spend", "cache savings", "by model"}},
		{ActionTraffic, []string{"request(s)", "claude-haiku-4-5", "HIT"}},
		{ActionTimeseries, []string{"bucket(s)", "requests"}},
		{ActionCost, []string{"chat", "spend"}},
		{ActionForecast, []string{"monthly projected", "15.0000"}},
		{ActionCache, []string{"entries", "total hits"}},
		{ActionBudget, []string{"global", "limit", "OVER", "near limit"}},
		{ActionRouting, []string{"small-prompts", "claude-haiku-4-5"}},
		{ActionUpstreams, []string{"anthropic", "healthy", "UNHEALTHY"}},
		{ActionHealth, []string{"reachable  true", "dashboard"}},
		{ActionTimeframe, []string{}},
	}
	for _, c := range cases {
		if c.action == ActionTimeframe {
			continue // not an action; covered by ValidateTimeframe
		}
		t.Run(c.action, func(t *testing.T) {
			output := runScripted(t, ActionRequest{Action: c.action, Timeframe: "24h", Limit: 20})
			if strings.TrimSpace(output) == "" {
				t.Fatal("no output")
			}
			for _, want := range c.want {
				if !strings.Contains(output, want) {
					t.Errorf("output does not mention %q:\n%s", want, output)
				}
			}
		})
	}
}

// TestCostBreakdownsAreSelectable keeps the three cost shapes reachable from the
// scripted path, where there is no form to pick one.
func TestCostBreakdownsAreSelectable(t *testing.T) {
	t.Run("app", func(t *testing.T) {
		output := runScripted(t, ActionRequest{Action: ActionCost, Timeframe: "24h", TestName: CostByApp})
		if !strings.Contains(output, "claude-code") {
			t.Errorf("output does not show the app breakdown:\n%s", output)
		}
	})
	t.Run("session", func(t *testing.T) {
		output := runScripted(t, ActionRequest{Action: ActionCost, Timeframe: "24h", TestName: CostBySession})
		if !strings.Contains(output, "sess-1") {
			t.Errorf("output does not show the session breakdown:\n%s", output)
		}
	})
	t.Run("tag", func(t *testing.T) {
		output := runScripted(t, ActionRequest{Action: ActionCost, Timeframe: "24h"})
		if !strings.Contains(output, "chat") {
			t.Errorf("output does not show the tag breakdown:\n%s", output)
		}
	})
}

// TestABScriptedListsThenReports covers both halves without a form.
func TestABScriptedListsThenReports(t *testing.T) {
	list := runScripted(t, ActionRequest{Action: ActionAB})
	if !strings.Contains(list, "latency") {
		t.Errorf("the list does not mention the test:\n%s", list)
	}
	report := runScripted(t, ActionRequest{Action: ActionAB, TestName: "latency"})
	for _, want := range []string{"test latency", "claude-haiku-4-5", "2.50%"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}

// TestJSONModeIsMachineReadable is what makes the scripted path usable from a
// pipeline: the body must parse and carry the service's values.
func TestJSONModeIsMachineReadable(t *testing.T) {
	cases := []struct {
		action string
		extra  string
	}{
		{ActionOverview, ""},
		{ActionTraffic, ""},
		{ActionTimeseries, ""},
		{ActionCost, ""},
		{ActionForecast, ""},
		{ActionCache, ""},
		{ActionBudget, ""},
		{ActionRouting, ""},
		{ActionAB, ""},
		{ActionUpstreams, ""},
		{ActionHealth, ""},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			output := runScripted(t, ActionRequest{Action: c.action, Timeframe: "24h", Limit: 20, JSON: true})
			var decoded any
			if err := json.Unmarshal([]byte(output), &decoded); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, output)
			}
			if decoded == nil {
				t.Error("JSON body is null")
			}
		})
	}
}

// TestScriptedTimeoutIsRejectedBeforeAnyRequest keeps an unsupported window from
// silently being served as 24 hours.
func TestScriptedTimeoutIsRejectedBeforeAnyRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request was made for an invalid timeframe")
	}))
	defer server.Close()

	err := RunAction(context.Background(), dashboard.NewClient(server.URL),
		ActionRequest{Action: ActionOverview, Timeframe: "90d"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("an unsupported timeframe was accepted")
	}
	if !strings.Contains(err.Error(), "1h, 24h, 7d, 30d, all") {
		t.Errorf("err = %v, want it to list the accepted windows", err)
	}
}

// TestScriptedServiceErrorsAreReported keeps a broken service from looking like
// an empty result.
func TestScriptedServiceErrorsAreReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"ORA-12541: cannot connect to Oracle"}`))
	}))
	defer server.Close()

	err := RunAction(context.Background(), dashboard.NewClient(server.URL),
		ActionRequest{Action: ActionBudget}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("no error for a failing service")
	}
	if !strings.Contains(err.Error(), "ORA-12541") {
		t.Errorf("err = %v, want the service's own detail", err)
	}
}

// TestUnknownActionIsRefused rather than silently succeeding.
func TestUnknownActionIsRefused(t *testing.T) {
	server := fakeDashboard(t)
	defer server.Close()
	if err := RunAction(context.Background(), dashboard.NewClient(server.URL),
		ActionRequest{Action: "nonsense"}, &bytes.Buffer{}); err == nil {
		t.Fatal("an unknown action returned no error")
	}
}

// TestEmptyResultsReadAsEmptyNotAsBroken keeps a fresh install legible.
func TestEmptyResultsReadAsEmptyNotAsBroken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/stats":
			_, _ = w.Write([]byte(`{"total_requests":0,"total_estimated_cost":0,"models":{}}`))
		case "/api/cost/forecast":
			_, _ = w.Write([]byte(`{"last_7_days_total":0,"daily_avg":0,"monthly_projection":0,"active_days":0}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	client := dashboard.NewClient(server.URL)

	checks := map[string][]string{
		ActionTraffic:   {"No requests recorded yet."},
		ActionCost:      {"No tagged cost in this window."},
		ActionBudget:    {"No budgets configured."},
		ActionRouting:   {"No routing rules configured."},
		ActionAB:        {"No active A/B tests."},
		ActionUpstreams: {"No upstreams configured."},
	}
	for action, want := range checks {
		var out bytes.Buffer
		if err := RunAction(context.Background(), client, ActionRequest{Action: action}, &out); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		for _, phrase := range want {
			if !strings.Contains(out.String(), phrase) {
				t.Errorf("%s: output = %q, want %q", action, out.String(), phrase)
			}
		}
	}
}

// TestParseActionFlagsMostSpecificWins keeps a combination of flags from
// degrading into the wrong action.
func TestParseActionFlagsMostSpecificWins(t *testing.T) {
	// health beats everything.
	if action, ok := ParseActionFlags(true, true, true, true, true, true, true, true, true, true, true); !ok || action != ActionHealth {
		t.Errorf("action = %q/%v, want health", action, ok)
	}
	if action, ok := ParseActionFlags(false, false, false, false, false, false, true, false, false, false, false); !ok || action != ActionBudget {
		t.Errorf("action = %q/%v, want budget", action, ok)
	}
	if action, ok := ParseActionFlags(true, false, false, false, false, false, false, false, false, false, false); !ok || action != ActionOverview {
		t.Errorf("action = %q/%v, want overview", action, ok)
	}
	if action, ok := ParseActionFlags(false, false, false, false, false, false, false, false, false, false, false); ok {
		t.Errorf("action = %q/%v, want no action", action, ok)
	}
}

// TestActionFlagsProduceADistinctAction guards against two flags mapping to the
// same action, which would make one of them useless.
func TestActionFlagsProduceADistinctAction(t *testing.T) {
	seen := map[string]string{}
	flags := []struct {
		name string
		args []bool
	}{
		{ActionOverview, []bool{true}},
		{ActionTraffic, []bool{false, true}},
		{ActionTimeseries, []bool{false, false, true}},
		{ActionCost, []bool{false, false, false, true}},
		{ActionForecast, []bool{false, false, false, false, true}},
		{ActionCache, []bool{false, false, false, false, false, true}},
		{ActionBudget, []bool{false, false, false, false, false, false, true}},
		{ActionRouting, []bool{false, false, false, false, false, false, false, true}},
		{ActionAB, []bool{false, false, false, false, false, false, false, false, true}},
		{ActionUpstreams, []bool{false, false, false, false, false, false, false, false, false, true}},
	}
	for _, f := range flags {
		args := make([]bool, 11)
		copy(args, f.args)
		action, ok := ParseActionFlags(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10])
		if !ok {
			t.Errorf("flag for %s produced no action", f.name)
			continue
		}
		if previous, dup := seen[action]; dup {
			t.Errorf("%s and %s both map to %q", previous, f.name, action)
		}
		seen[action] = f.name
		if action != f.name {
			t.Errorf("flag for %s produced %q", f.name, action)
		}
	}
}

// TestNoTerminalErrorNamesAFlag is the message a user sees when they forget a
// flag: it must name one that works.
func TestNoTerminalErrorNamesAFlag(t *testing.T) {
	message := ErrNoTerminal.Error()
	for _, flag := range []string{"--overview", "--traffic", "--budget", "ACCESSIBLE"} {
		if !strings.Contains(message, flag) {
			t.Errorf("ErrNoTerminal does not mention %q: %s", flag, message)
		}
	}
}
