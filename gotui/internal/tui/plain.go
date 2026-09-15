package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
)

// PlainOptions drive the non-full-screen prompt path.
//
// This path is used when ACCESSIBLE is set. It reuses the same form builders as
// the TUI, so the two front-ends cannot drift apart in what they ask or how they
// validate it.
type PlainOptions struct {
	ProjectRoot string
	Settings    session.Settings
	Input       io.Reader
	Output      io.Writer
}

// RunPlainPrompts runs the accessible flow as ONE pass of plain prompts.
//
// One form, one read of the reader: huh's accessible Form.Run wraps the reader
// in its own scanner, which buffers ahead, so a second form built over the same
// stdin starts at EOF and returns empty answers for everything.
func RunPlainPrompts(opts PlainOptions) (ActionAnswers, error) {
	in, out := opts.Input, opts.Output
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	settings := opts.Settings
	if settings.BaseURL == "" {
		settings = session.Defaults()
	}
	answers := &PlainAnswers{
		Connect:   ConnectDefaults(settings),
		Timeframe: timeframeAnswers{Timeframe: "24h"},
		Menu:      menuAnswers{Action: ActionOverview},
	}
	form := PlainForm(answers).WithInput(in).WithOutput(out)
	if err := form.Run(); err != nil {
		return ActionAnswers{}, err
	}
	return ActionAnswers{
		Action:    answers.Menu.Action,
		Timeframe: answers.Timeframe.Timeframe,
		BaseURL:   answers.Connect.BaseURL,
	}, nil
}

// ActionRequest is a scripted, non-interactive request.
type ActionRequest struct {
	Action    string
	Timeframe string
	Limit     int
	TestName  string
	JSON      bool
}

// RunAction executes a scripted request and writes its result.
//
// This is the path a cron job, a CI check or a pipe uses, so it never prompts
// and never opens the alternate screen.
func RunAction(ctx context.Context, client *dashboard.Client, req ActionRequest, out io.Writer) error {
	if req.Timeframe == "" {
		req.Timeframe = "24h"
	}
	if err := dashboard.ValidateTimeframe(req.Timeframe); err != nil {
		return err
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	switch req.Action {
	case ActionOverview, "stats":
		stats, err := client.Stats(ctx, req.Timeframe)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, stats)
		}
		fmt.Fprintf(out, "window            %s\n", req.Timeframe)
		fmt.Fprintf(out, "requests          %s\n", Count(stats.TotalRequests))
		fmt.Fprintf(out, "input tokens      %s\n", Count(stats.TotalInputTokens))
		fmt.Fprintf(out, "output tokens     %s\n", Count(stats.TotalOutputTokens))
		fmt.Fprintf(out, "cache read tokens %s\n", Count(stats.TotalCacheReadTokens))
		fmt.Fprintf(out, "cache hits        %s\n", Count(stats.TotalCacheHits))
		fmt.Fprintf(out, "spend             $%s\n", Money(stats.TotalEstimatedCost))
		fmt.Fprintf(out, "cache savings     $%s\n", Money(stats.TotalCacheSavings))
		if len(stats.Models) > 0 {
			fmt.Fprintln(out, "by model")
			for name, totals := range stats.Models {
				fmt.Fprintf(out, "  %-30s $%s  requests %s\n", name, Money(totals.Cost), Count(totals.Requests))
			}
		}
		return nil

	case ActionTraffic:
		rows, err := client.Recent(ctx, req.Limit)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, rows)
		}
		if len(rows) == 0 {
			fmt.Fprintln(out, "No requests recorded yet.")
			return nil
		}
		fmt.Fprintf(out, "%d request(s)\n", len(rows))
		for _, row := range rows {
			cached := "miss"
			if row.WasCached() {
				cached = "HIT"
			}
			fmt.Fprintf(out, "  %s  %-26s %-16s %5s in %5s out %s%s %5s %s $%s\n",
				row.CreatedAt, Truncate(row.ModelUsed, 26), Truncate(row.SourceApp, 16),
				Count(row.InputTokens), Count(row.OutputTokens),
				Count(row.LatencyMS), "ms", Count(row.StatusCode), cached,
				Money(row.EstimatedCost))
		}
		return nil

	case ActionTimeseries:
		buckets, err := client.Timeseries(ctx, req.Timeframe)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, buckets)
		}
		if len(buckets) == 0 {
			fmt.Fprintln(out, "No buckets in this window.")
			return nil
		}
		fmt.Fprintf(out, "%d bucket(s)\n", len(buckets))
		for _, bucket := range buckets {
			fmt.Fprintf(out, "  %-20s requests %7s  spend $%s  cache hits %s\n",
				bucket.Bucket, Count(bucket.Requests), Money(bucket.Cost), Count(bucket.CacheHits))
		}
		return nil

	case ActionCost:
		switch req.TestName {
		case CostByApp:
			rows, err := client.CostByApp(ctx, req.Timeframe)
			if err != nil {
				return err
			}
			if req.JSON {
				return writeJSON(out, rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "No app cost in this window.")
				return nil
			}
			for _, row := range rows {
				fmt.Fprintf(out, "  %-28s requests %7s  spend $%s  avg $%s\n",
					row.App, Count(row.Requests), Money(row.TotalCost), Money(row.AvgCost))
			}
			return nil
		case CostBySession:
			rows, err := client.CostBySession(ctx, req.Limit)
			if err != nil {
				return err
			}
			if req.JSON {
				return writeJSON(out, rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "No session cost recorded.")
				return nil
			}
			for _, row := range rows {
				fmt.Fprintf(out, "  %-24s turns %5s  spend $%s  %s .. %s\n",
					Truncate(row.SessionID, 24), Count(row.Turns), Money(row.ConversationCost),
					row.Started, row.Ended)
			}
			return nil
		default:
			rows, err := client.CostByTag(ctx, req.Timeframe)
			if err != nil {
				return err
			}
			if req.JSON {
				return writeJSON(out, rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "No tagged cost in this window.")
				return nil
			}
			for _, row := range rows {
				fmt.Fprintf(out, "  %-24s requests %7s  spend $%s  avg $%s\n",
					row.Tag, Count(row.Requests), Money(row.TotalCost), Money(row.AvgCost))
			}
			return nil
		}

	case ActionForecast:
		forecast, err := client.CostForecast(ctx)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, forecast)
		}
		fmt.Fprintf(out, "last 7 days       $%s\n", Money(forecast.Last7DaysTotal))
		fmt.Fprintf(out, "daily average     $%s\n", Money(forecast.DailyAvg))
		fmt.Fprintf(out, "monthly projected $%s\n", Money(forecast.MonthlyProjection))
		fmt.Fprintf(out, "active days (7d)  %s\n", Count(forecast.ActiveDays))
		return nil

	case ActionCache:
		stats, err := client.CacheStats(ctx)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, stats)
		}
		fmt.Fprintf(out, "entries        %s\n", Count(stats.Entries))
		fmt.Fprintf(out, "active entries %s\n", Count(stats.ActiveEntries))
		fmt.Fprintf(out, "total hits     %s\n", Count(stats.TotalHits))
		return nil

	case ActionBudget:
		budgets, err := client.BudgetStatus(ctx)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, budgets)
		}
		if len(budgets) == 0 {
			fmt.Fprintln(out, "No budgets configured.")
			return nil
		}
		for _, budget := range budgets {
			scope := budget.Scope
			if budget.ScopeValue != "" {
				scope += ":" + budget.ScopeValue
			}
			state := "ok"
			if budget.UtilizationPct >= 100 {
				state = "OVER"
			} else if budget.UtilizationPct >= 80 {
				state = "near limit"
			}
			fmt.Fprintf(out, "  %-22s %-7s limit $%s spent $%s used %6.2f%%  action=%s state=%s active=%t\n",
				scope, budget.Period, Money(budget.LimitAmount), Money(budget.CurrentSpend),
				budget.UtilizationPct, budget.ActionOnLimit, state, budget.IsActive)
		}
		return nil

	case ActionRouting:
		rules, err := client.RoutingStats(ctx)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, rules)
		}
		if len(rules) == 0 {
			fmt.Fprintln(out, "No routing rules configured.")
			return nil
		}
		for _, rule := range rules {
			fmt.Fprintf(out, "  %-22s -> %-24s requests %7s  spend $%s  avg %sms\n",
				Truncate(rule.RuleName, 22), Truncate(rule.TargetModel, 24),
				Count(rule.TotalRequests), Money(rule.TotalCost), TrimFloat(rule.AvgLatencyMS))
		}
		return nil

	case ActionAB:
		if strings.TrimSpace(req.TestName) == "" {
			tests, err := client.ABTests(ctx)
			if err != nil {
				return err
			}
			if req.JSON {
				return writeJSON(out, tests)
			}
			if len(tests) == 0 {
				fmt.Fprintln(out, "No active A/B tests.")
				return nil
			}
			for _, test := range tests {
				fmt.Fprintf(out, "  %-24s %s vs %s  split %s%%  status=%s\n",
					Truncate(test.TestName, 24), test.ModelA, test.ModelB,
					Count(test.SplitPct), test.Status)
			}
			return nil
		}
		report, err := client.ABReport(ctx, req.TestName)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, report)
		}
		fmt.Fprintf(out, "test %s\n", report.TestName)
		if len(report.Variants) == 0 {
			fmt.Fprintln(out, "  no recorded traffic")
			return nil
		}
		for _, variant := range report.Variants {
			fmt.Fprintf(out, "  %-26s requests %7s  avg %sms  out %s  spend $%s  errors %.2f%%\n",
				Truncate(variant.Variant, 26), Count(variant.TotalRequests),
				TrimFloat(variant.AvgLatencyMS), TrimFloat(variant.AvgOutputTokens),
				Money(variant.TotalCost), variant.ErrorRate)
		}
		return nil

	case ActionUpstreams:
		upstreams, err := client.Upstreams(ctx)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, upstreams)
		}
		if len(upstreams) == 0 {
			fmt.Fprintln(out, "No upstreams configured.")
			return nil
		}
		for _, upstream := range upstreams {
			health := "healthy"
			if !upstream.IsHealthy {
				health = "UNHEALTHY"
			}
			fmt.Fprintf(out, "  %-10s %-42s priority %s  %s  fails %s\n",
				upstream.APIType, Truncate(upstream.BaseURL, 42),
				Count(upstream.Priority), health, Count(upstream.FailCount))
		}
		return nil

	case ActionHealth:
		stats, err := client.Stats(ctx, req.Timeframe)
		if err != nil {
			return err
		}
		if req.JSON {
			return writeJSON(out, map[string]any{"reachable": true, "base_url": client.BaseURL(),
				"requests": stats.TotalRequests, "spend": stats.TotalEstimatedCost})
		}
		fmt.Fprintf(out, "reachable  true\ndashboard  %s\nrequests   %s\nspend      $%s\n",
			client.BaseURL(), Count(stats.TotalRequests), Money(stats.TotalEstimatedCost))
		return nil
	}

	return fmt.Errorf("unknown action %q", req.Action)
}

// writeJSON emits an indented JSON document for scripting.
func writeJSON(out io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	_, err = out.Write(append(encoded, '\n'))
	return err
}

// ParseActionFlags decides which scripted action a flag set requests.
//
// The order matters: the most specific flag wins, so a combination such as
// "--budget --traffic" cannot silently degrade into one of them by accident.
func ParseActionFlags(overview, traffic, timeseries, cost, forecast, cache, budget, routing, ab, upstreams, health bool) (string, bool) {
	switch {
	case health:
		return ActionHealth, true
	case budget:
		return ActionBudget, true
	case upstreams:
		return ActionUpstreams, true
	case routing:
		return ActionRouting, true
	case ab:
		return ActionAB, true
	case cache:
		return ActionCache, true
	case forecast:
		return ActionForecast, true
	case cost:
		return ActionCost, true
	case timeseries:
		return ActionTimeseries, true
	case traffic:
		return ActionTraffic, true
	case overview:
		return ActionOverview, true
	}
	return "", false
}

// ErrNoTerminal is returned when a full-screen UI is asked for without a
// terminal, with the flag set that would work instead.
var ErrNoTerminal = errors.New("no terminal on stdin: pass an action flag such as " +
	"--overview, --traffic, --budget, --cache, --routing, --ab or --upstreams " +
	"(or set ACCESSIBLE for plain prompts)")
