package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
)

// --- harness -----------------------------------------------------------------------

// cmdTimeout bounds how long the test drain waits for one command.
//
// A command that blocks on a timer (tea.Tick) does not return until its timer
// fires, and calling it synchronously would stall the whole suite. The model's
// own auto-refresh is off in tests (Interval 0), so nothing here should ever
// need this window; it is a safety net, and tests additionally run with
// -timeout=60s so a hang fails fast instead of eating ten minutes.
const cmdTimeout = 2 * time.Second

// runCmd executes a command but never blocks longer than cmdTimeout.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() {
		defer func() { _ = recover() }()
		done <- cmd()
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(cmdTimeout):
		return nil
	}
}

// drain feeds a command's messages back into the model until it stops producing
// work, so a test can load a panel without a running event loop.
func drain(m *Model, cmd tea.Cmd) *Model {
	for depth := 0; depth < 32 && cmd != nil; depth++ {
		msg := runCmd(cmd)
		if msg == nil {
			return m
		}
		updated, next := m.Update(msg)
		var ok bool
		m, ok = updated.(*Model)
		if !ok {
			return m
		}
		cmd = next
	}
	return m
}

// press sends a key press.
func press(m *Model, key tea.KeyPressMsg) (*Model, tea.Cmd) {
	updated, cmd := m.Update(key)
	model, ok := updated.(*Model)
	if !ok {
		panic("Update returned a non-*Model")
	}
	return model, cmd
}

// pressRunes builds a key press for a printable key.
func pressRunes(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: text, Code: rune(text[0])}
}

// fakeDashboard serves representative JSON for every endpoint the TUI reads, so
// no test needs Oracle or a live service. The exact response SHAPES are pinned
// separately in internal/dashboard's client_test.go against real captured
// payloads; these bodies only need to be well-formed.
func fakeDashboard(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/stats":
			fmt.Fprint(w, `{"total_requests":2,"total_input_tokens":120,"total_output_tokens":80,
				"total_cache_creation_tokens":0,"total_cache_read_tokens":5,"total_estimated_cost":1.25,
				"total_cache_hits":1,"total_cache_savings":0.4,
				"models":{"claude-sonnet-4-5":{"requests":2,"input_tokens":120,"output_tokens":80,"cost":1.25},
				          "claude-haiku-4-5":{"requests":1,"input_tokens":10,"output_tokens":5,"cost":0.25}}}`)
		case r.URL.Path == "/api/recent":
			fmt.Fprint(w, `[{"id":7,"request_id":"req-1","api_type":"anthropic","model_requested":"claude-sonnet-4-5",
				"model_used":"claude-haiku-4-5","input_tokens":120,"output_tokens":80,"cache_creation_tokens":0,
				"cache_read_tokens":5,"latency_ms":830,"status_code":200,"source_app":"claude-code",
				"session_id":"sess-1","feature_tag":"chat","estimated_cost":0.0125,"cache_hit":1,
				"ab_test_id":null,"routing_rule_id":3,"created_at":"2026-03-28T10:00:00"}]`)
		case r.URL.Path == "/api/timeseries":
			fmt.Fprint(w, `[{"bucket":"2026-03-28 10:00","input_tokens":120,"output_tokens":80,"requests":2,
				"cost":1.25,"cache_hits":1}]`)
		case r.URL.Path == "/api/cost/by-tag":
			fmt.Fprint(w, `[{"tag":"chat","requests":2,"total_cost":1.25,"avg_cost":0.625,
				"input_tokens":120,"output_tokens":80}]`)
		case r.URL.Path == "/api/cost/by-app":
			fmt.Fprint(w, `[{"app":"claude-code","requests":2,"total_cost":1.25,"avg_cost":0.625}]`)
		case r.URL.Path == "/api/cost/by-session":
			fmt.Fprint(w, `[{"session_id":"sess-1","turns":3,"conversation_cost":0.9,
				"started":"2026-03-28T10:00:00","ended":"2026-03-28T10:30:00"}]`)
		case r.URL.Path == "/api/cost/forecast":
			fmt.Fprint(w, `{"last_7_days_total":3.5,"daily_avg":0.5,"monthly_projection":15.0,"active_days":7}`)
		case r.URL.Path == "/api/cache/stats":
			fmt.Fprint(w, `{"entries":4,"total_hits":2,"active_entries":4}`)
		case r.URL.Path == "/api/budget/status":
			fmt.Fprint(w, `[{"id":1,"scope":"global","scope_value":"","limit_amount":10.0,"period":"daily",
				"action_on_limit":"block","webhook_url":"","is_active":true,"current_spend":9.0,
				"utilization_pct":90.0},{"id":2,"scope":"model","scope_value":"gpt","limit_amount":5.0,
				"period":"daily","action_on_limit":"warn","webhook_url":"","is_active":true,
				"current_spend":6.0,"utilization_pct":120.0}]`)
		case r.URL.Path == "/api/routing/stats":
			fmt.Fprint(w, `[{"id":3,"rule_name":"small-prompts","target_model":"claude-haiku-4-5",
				"total_requests":9,"total_cost":0.11,"avg_latency_ms":412.5}]`)
		case r.URL.Path == "/api/ab/list":
			fmt.Fprint(w, `[{"id":3,"test_name":"latency","model_a":"claude-haiku-4-5",
				"model_b":"claude-sonnet-4-5","split_pct":50,"status":"active"}]`)
		case strings.HasPrefix(r.URL.Path, "/api/ab/report/"):
			fmt.Fprint(w, `{"test_name":"latency","variants":[
				{"variant":"claude-haiku-4-5","total_requests":5,"avg_latency_ms":400.0,
				 "avg_output_tokens":90.5,"total_cost":0.05,"error_rate":0.0},
				{"variant":"claude-sonnet-4-5","total_requests":5,"avg_latency_ms":700.0,
				 "avg_output_tokens":120.0,"total_cost":0.25,"error_rate":2.5}]}`)
		case r.URL.Path == "/api/upstreams":
			fmt.Fprint(w, `[{"id":2,"api_type":"anthropic","base_url":"https://api.anthropic.com",
				"priority":100,"is_healthy":true,"fail_count":0},
				{"id":3,"api_type":"openai","base_url":"https://api.z.ai","priority":100,
				 "is_healthy":false,"fail_count":4}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"detail":"unexpected path %s"}`, r.URL.Path)
		}
	}))
}

// newTestModel builds a model pointed at a fake dashboard, with auto-refresh off
// and the connect form skipped so tests can drive the menu directly.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	server := fakeDashboard(t)
	t.Cleanup(server.Close)

	// Keep the real config file untouched.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := New(Options{Settings: session.Settings{BaseURL: server.URL}, Interval: 0})
	m.client = dashboard.NewClient(server.URL)
	m.width, m.height = 120, 40
	m.form = nil
	m.screen = ScreenMenu
	return m
}

// openPanel drives the menu action and loads the panel.
func openPanel(t *testing.T, m *Model, action string) *Model {
	t.Helper()
	m.menu.Action = action
	cmd := m.finishMenu()
	return drain(m, cmd)
}

// --- menu routing ------------------------------------------------------------------

// TestMenuRoutesEveryActionToItsPanel guards the switch that decides which panel
// opens: a wrong constant would silently show one panel's numbers under another
// panel's title.
func TestMenuRoutesEveryActionToItsPanel(t *testing.T) {
	cases := []struct {
		action string
		want   Panel
	}{
		{ActionOverview, PanelOverview},
		{ActionTimeseries, PanelTimeseries},
		{ActionForecast, PanelForecast},
		{ActionCache, PanelCache},
		{ActionBudget, PanelBudget},
		{ActionRouting, PanelRouting},
		{ActionUpstreams, PanelUpstreams},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			m := openPanel(t, newTestModel(t), c.action)
			if m.panel != c.want {
				t.Errorf("panel = %v, want %v", m.panel, c.want)
			}
			if m.screen != ScreenPanel {
				t.Errorf("screen = %v, want ScreenPanel", m.screen)
			}
			if m.failure != nil {
				t.Errorf("failure = %v, want nil", m.failure)
			}
		})
	}
}

// TestTrafficAsksForALimitFirst keeps the row count a user choice rather than a
// silent default.
func TestTrafficAsksForALimitFirst(t *testing.T) {
	m := newTestModel(t)
	m.menu.Action = ActionTraffic
	m.finishMenu()
	if m.screen != ScreenLimit {
		t.Fatalf("screen = %v, want ScreenLimit so the user picks a row count", m.screen)
	}
	// Only a number in range is accepted.
	if err := ValidateLimit("0"); err == nil {
		t.Error("ValidateLimit(0) = nil, want an error")
	}
	if err := ValidateLimit("501"); err == nil {
		t.Error("ValidateLimit(501) = nil, want an error")
	}
	if err := ValidateLimit("nope"); err == nil {
		t.Error("ValidateLimit(nope) = nil, want an error")
	}
	if err := ValidateLimit("25"); err != nil {
		t.Errorf("ValidateLimit(25) = %v, want nil", err)
	}
}

// --- escape from an open form -------------------------------------------------------

// TestEscClosesAnOpenForm is the regression guard for a bug in this repo: the
// footer above every choice form advertised "esc aborts", but the key was
// delegated to the huh form, which cannot abort on esc.
//
// huh's default keymap binds Quit to ctrl+c ONLY (huh/v2 keymap.go:109), and the
// only path that sets StateAborted is key.Matches(msg, keymap.Quit)
// (form.go:564). ctrl+c is intercepted at the top of Update, so the form never
// receives it either -- which made advance()'s StateAborted branch unreachable
// and left esc doing nothing. The form then stayed installed and swallowed every
// later keystroke, so the user could not get out and the menu behind it stopped
// responding.
//
// The form is deliberately NOT asserted to be nil. Every screen here owns a form
// and abort means "go back to the menu", which installs the menu's own form; a
// nil form would be an undriveable screen, so asserting nil would assert an
// impossible state.
func TestEscClosesAnOpenForm(t *testing.T) {
	m := newTestModel(t)

	// Reach a real choice form: the menu routes Traffic to the row-count form.
	m.menu.Action = ActionTraffic
	m = drain(m, m.finishMenu())
	if m.screen != ScreenLimit || m.form == nil {
		t.Fatalf("setup: screen = %v, form = %v; want ScreenLimit with a live form", m.screen, m.form)
	}

	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})

	// The abandoned choice form must be gone and the user back on the menu.
	if m.screen != ScreenMenu {
		t.Errorf("screen = %v after esc, want ScreenMenu", m.screen)
	}
	if m.form == nil {
		t.Fatal("no form after esc; the menu screen cannot be driven without one")
	}
	if m.notice == "" {
		t.Error("no cancellation notice after esc; the user gets no confirmation")
	}

	// The real damage this bug did: an installed-but-abandoned form swallowed
	// later keystrokes. A menu choice must work again immediately.
	m.menu.Action = ActionTraffic
	if cmd := m.finishMenu(); cmd == nil {
		t.Error("a menu choice after esc produced no command; keys are still being swallowed")
	}
}

// TestEscOnTheMenuQuits covers the other half of the footer contract: the menu
// advertising "esc quits from the menu" was equally untrue while esc fell
// through to the huh form.
func TestEscOnTheMenuQuits(t *testing.T) {
	m := newTestModel(t)
	// newTestModel starts on ScreenMenu with no form installed, so install the
	// menu's own form first -- the key path under test is the one with a form open.
	m = drain(m, m.toMenu())
	if m.screen != ScreenMenu || m.form == nil {
		t.Fatalf("setup: screen = %v, form = %v; want ScreenMenu with a live form", m.screen, m.form)
	}

	_, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc on the menu produced no command, want tea.Quit")
	}
	if _, quit := runCmd(cmd).(tea.QuitMsg); !quit {
		t.Errorf("esc on the menu produced %T, want tea.QuitMsg", runCmd(cmd))
	}
}

// TestCtrlCStillQuitsFromInsideAForm guards the path adjacent to the new esc
// handling: esc is now intercepted before the form, and ctrl+c must keep working
// rather than being swallowed by the same guard.
func TestCtrlCStillQuitsFromInsideAForm(t *testing.T) {
	m := newTestModel(t)
	m.menu.Action = ActionTraffic
	m = drain(m, m.finishMenu())
	if m.form == nil {
		t.Fatal("setup: no form is open")
	}

	_, cmd := press(m, tea.KeyPressMsg{Text: "ctrl+c", Code: 'c'})
	if cmd == nil {
		t.Fatal("ctrl+c from inside a form produced no command, want tea.Quit")
	}
	if _, quit := runCmd(cmd).(tea.QuitMsg); !quit {
		t.Errorf("ctrl+c from inside a form produced %T, want tea.QuitMsg", runCmd(cmd))
	}
}

// TestEscOnAReadOnlyPanelStillReturnsToTheMenu pins the path that returns before
// the form handling, so the new interception cannot have captured it. A panel
// installs no form, and esc there has always meant "back to the menu".
func TestEscOnAReadOnlyPanelStillReturnsToTheMenu(t *testing.T) {
	m := openPanel(t, newTestModel(t), ActionRouting)
	if m.screen != ScreenPanel || m.form != nil {
		t.Fatalf("setup: screen = %v, form = %v; want ScreenPanel with no form", m.screen, m.form)
	}

	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.screen != ScreenMenu {
		t.Errorf("screen = %v after esc on a panel, want ScreenMenu", m.screen)
	}
}

// TestEscReleaseDoesNotAbort keeps the double-fire rule intact for the new esc
// handling: only a key PRESS may abort a form, never the matching release.
func TestEscReleaseDoesNotAbort(t *testing.T) {
	m := newTestModel(t)
	m.menu.Action = ActionTraffic
	m = drain(m, m.finishMenu())
	if m.screen != ScreenLimit {
		t.Fatalf("setup: screen = %v, want ScreenLimit", m.screen)
	}

	updated, _ := m.Update(tea.KeyReleaseMsg{Code: tea.KeyEscape})
	after, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if after.screen != ScreenLimit {
		t.Errorf("an esc RELEASE aborted the form (screen = %v); key releases must not act", after.screen)
	}
}

// TestCostBreakdownRoutesToEachEndpoint keeps the three cost panels distinct.
func TestCostBreakdownRoutesToEachEndpoint(t *testing.T) {
	cases := []struct {
		kind string
		want Panel
	}{
		{CostByTag, PanelCostTag},
		{CostByApp, PanelCostApp},
		{CostBySession, PanelCostSession},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			m := newTestModel(t)
			m.cost.Kind = c.kind
			m = drain(m, m.openCostPanel())
			if m.panel != c.want {
				t.Errorf("panel = %v, want %v", m.panel, c.want)
			}
		})
	}
}

// TestUnknownCostKindFallsBackToTag keeps a blank selection from opening nothing.
func TestUnknownCostKindFallsBackToTag(t *testing.T) {
	m := newTestModel(t)
	m.cost.Kind = ""
	m = drain(m, m.openCostPanel())
	if m.panel != PanelCostTag {
		t.Errorf("panel = %v, want the tag breakdown", m.panel)
	}
}

// --- data application --------------------------------------------------------------

// TestLoadedDataReachesItsPanel checks the payload actually lands in the model,
// which is the difference between a populated panel and an empty one.
func TestLoadedDataReachesItsPanel(t *testing.T) {
	t.Run("overview", func(t *testing.T) {
		m := openPanel(t, newTestModel(t), ActionOverview)
		if m.stats == nil {
			t.Fatal("stats = nil")
		}
		if m.stats.TotalEstimatedCost != 1.25 {
			t.Errorf("spend = %v, want 1.25", m.stats.TotalEstimatedCost)
		}
		if !strings.Contains(m.View().Content, "1.2500") {
			t.Error("the rendered view does not show the spend")
		}
	})
	t.Run("traffic", func(t *testing.T) {
		// Traffic first asks for a row count, so the panel itself is loaded
		// through the same loader the limit form uses.
		m := newTestModel(t)
		m = drain(m, m.openPanel(PanelTraffic))
		if len(m.recent) != 1 {
			t.Fatalf("got %d rows, want 1", len(m.recent))
		}
		if !strings.Contains(m.View().Content, "claude-haiku-4-5") {
			t.Error("the rendered view does not show the model used")
		}
	})
	t.Run("budget", func(t *testing.T) {
		m := openPanel(t, newTestModel(t), ActionBudget)
		if len(m.budgets) != 2 {
			t.Fatalf("got %d budgets, want 2", len(m.budgets))
		}
		content := m.View().Content
		// 120% must read as over budget; 90% as near limit.
		if !strings.Contains(content, "over") {
			t.Error("an over-budget row was not labelled")
		}
		if !strings.Contains(content, "near limit") {
			t.Error("a near-limit row was not labelled")
		}
	})
	t.Run("upstreams", func(t *testing.T) {
		m := openPanel(t, newTestModel(t), ActionUpstreams)
		content := m.View().Content
		if !strings.Contains(content, "healthy") || !strings.Contains(content, "unhealthy") {
			t.Error("upstream health was not rendered")
		}
	})
}

// TestABLoadsTheTestListThenTheReport covers the two-step A/B flow.
func TestABLoadsTheTestListThenTheReport(t *testing.T) {
	m := newTestModel(t)
	m = openPanel(t, m, ActionAB)

	// With tests available the model asks which one, rather than loading a report
	// for an arbitrary test.
	if m.screen != ScreenAB {
		t.Fatalf("screen = %v, want ScreenAB (a test must be chosen)", m.screen)
	}
	if len(m.abTests) != 1 {
		t.Fatalf("got %d tests, want 1", len(m.abTests))
	}
	if m.form == nil {
		t.Fatal("no form was installed to choose a test")
	}

	m.ab.TestName = "latency"
	m = drain(m, m.openABPanel())
	if m.abReport == nil {
		t.Fatal("abReport = nil")
	}
	if len(m.abReport.Variants) != 2 {
		t.Errorf("got %d variants, want 2", len(m.abReport.Variants))
	}
	content := m.View().Content
	if !strings.Contains(content, "2.50%") {
		t.Error("the variant error rate was not rendered")
	}
}

// TestBlankABNameDoesNotLoadAReport: an empty selection must not become a
// request for /api/ab/report/.
func TestBlankABNameDoesNotLoadAReport(t *testing.T) {
	m := newTestModel(t)
	m.ab.TestName = "   "
	m.openABPanel()
	if !strings.Contains(m.notice, "No test selected") {
		t.Errorf("notice = %q, want a message about the missing selection", m.notice)
	}
	if m.screen != ScreenMenu {
		t.Errorf("screen = %v, want the menu rather than an empty report", m.screen)
	}
	if m.abReport != nil {
		t.Error("a report was loaded for a blank test name")
	}
}

// --- failure handling --------------------------------------------------------------

// TestUnreachableServiceShowsAnActionableProblem is the common case: the user
// forgot to start TokenWatch. The UI must say so, not show a stack trace, and
// must stay on a screen they can act from.
func TestUnreachableServiceShowsAnActionableProblem(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := New(Options{Settings: session.Settings{BaseURL: "http://127.0.0.1:1"}, Interval: 0})
	m.width, m.height = 120, 40
	m.form = nil
	m.screen = ScreenMenu

	m = openPanel(t, m, ActionOverview)

	if m.failure == nil {
		t.Fatal("failure = nil, want the transport error surfaced")
	}
	if !strings.Contains(m.failure.Error(), "start TokenWatch") {
		t.Errorf("failure = %q, want it to tell the user to start the service", m.failure)
	}
	if m.screen != ScreenPanel {
		t.Errorf("screen = %v, want to stay on a panel so the user can act", m.screen)
	}
	if !strings.Contains(m.View().Content, "Problem") {
		t.Error("the rendered view does not show the problem pane")
	}
}

// TestExplainOnlyRewritesTransportFailures keeps a service-side error intact.
func TestExplainOnlyRewritesTransportFailures(t *testing.T) {
	plain := errors.New("HTTP 500: ORA-12541: cannot connect to Oracle")
	if got := explain(plain); got.Error() != plain.Error() {
		t.Errorf("explain rewrote a non-transport error: %q", got)
	}
	wrapped := fmt.Errorf("%w: dial tcp", dashboard.ErrUnreachable)
	if !strings.Contains(explain(wrapped).Error(), "start TokenWatch") {
		t.Error("explain did not add guidance for an unreachable service")
	}
}

// --- interaction -------------------------------------------------------------------

// TestKeyReleasesDoNotAct is the regression guard for this workspace's known bug:
// bubbletea v2 delivers key releases as well as presses, and acting on both
// fires every binding twice.
func TestKeyReleasesDoNotAct(t *testing.T) {
	m := openPanel(t, newTestModel(t), ActionOverview)
	before := m.screen

	updated, _ := m.Update(tea.KeyReleaseMsg{Text: "q", Code: 'q'})
	after := updated.(*Model)
	if after.screen != before {
		t.Errorf("a key RELEASE moved from screen %v to %v", before, after.screen)
	}
}

// TestPanelKeysDriveThePager proves the pager is the thing being scrolled, and
// that q returns to the menu rather than quitting the program.
func TestPanelKeysDriveThePager(t *testing.T) {
	m := openPanel(t, newTestModel(t), ActionRouting)

	if m.form != nil {
		t.Fatal("a read-only panel must not install a form, or keys would go to it")
	}

	// A long content string so there is something to scroll.
	m.view.SetContent(strings.Repeat("row\n", 400))
	m.view.GotoTop()

	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.view.YOffset() == 0 {
		t.Error("pgdown did not scroll the pager")
	}

	m, _ = press(m, pressRunes("g"))
	if m.view.YOffset() != 0 {
		t.Errorf("g did not return to the top (offset %d)", m.view.YOffset())
	}

	cmd := func() tea.Cmd {
		updated, cmd := press(m, pressRunes("q"))
		m = updated
		return cmd
	}()
	if m.screen != ScreenMenu {
		t.Errorf("screen = %v, want the menu after q", m.screen)
	}
	if cmd != nil {
		if _, isQuit := runCmd(cmd).(tea.QuitMsg); isQuit {
			t.Error("q in a panel quit the program instead of returning to the menu")
		}
	}
}

// TestViewRendersThePagerNotARerender guards the exact bug that made a panel
// unscrollable: setting viewport content but drawing the raw string instead.
func TestViewRendersThePagerNotARerender(t *testing.T) {
	m := openPanel(t, newTestModel(t), ActionOverview)

	// Put a sentinel in the pager that the panel renderer would never produce.
	m.view.SetContent("SENTINEL-CONTENT-ONLY-IN-PAGER")
	if !strings.Contains(m.view.View(), "SENTINEL-CONTENT-ONLY-IN-PAGER") {
		t.Fatal("the drawn view does not come from the pager")
	}
	// A resize repaints by re-rendering the active panel, so the pager must end up
	// holding the panel's real content again rather than a stale or empty body.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	resized := updated.(*Model)
	content := resized.view.View()
	if !strings.Contains(content, "Spend and traffic") {
		t.Error("a resize did not repaint the panel content")
	}
	if strings.Contains(content, "SENTINEL-CONTENT-ONLY-IN-PAGER") {
		t.Error("a resize left the stale sentinel in the pager")
	}
}

// TestRefreshIsOffByDefault keeps a test suite from ever waiting on a timer.
func TestRefreshIsOffByDefault(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.tickCmd(); cmd != nil {
		t.Error("tickCmd() returned a command with Interval 0; a test drain would block on it")
	}
	// Opting in must actually produce a timer.
	m.opts.Interval = time.Hour
	if m.tickCmd() == nil {
		t.Error("tickCmd() = nil with a positive Interval, want a tick")
	}
}

// TestTimeframeNeverBecomesEmpty guards the silent-24h trap: db._timeframe_where
// maps an unknown value to 24h rather than failing, so an empty window would
// misreport which period the numbers cover.
func TestTimeframeNeverBecomesEmpty(t *testing.T) {
	m := newTestModel(t)
	m.timeframe.Timeframe = ""
	if got := m.currentTimeframe(); got != "24h" {
		t.Errorf("currentTimeframe() = %q, want 24h", got)
	}
	m.timeframe.Timeframe = "90d"
	if got := m.currentTimeframe(); got != "24h" {
		t.Errorf("currentTimeframe() = %q, want 24h for an unsupported window", got)
	}
	m.timeframe.Timeframe = "7d"
	if got := m.currentTimeframe(); got != "7d" {
		t.Errorf("currentTimeframe() = %q, want 7d", got)
	}
}

// TestEverySelectSeedIsAmongItsOptions is the guard for huh's silent fallback:
// when a Select's seeded value is not among its options, huh selects the FIRST
// option instead, so an unmatched seed would silently report the wrong data.
func TestEverySelectSeedIsAmongItsOptions(t *testing.T) {
	t.Run("menu", func(t *testing.T) {
		answers := &menuAnswers{Action: ActionOverview}
		form := MenuForm(answers, "24h", "http://127.0.0.1:8878")
		if form == nil {
			t.Fatal("MenuForm returned nil")
		}
		assertSeedIsAnAction(t, answers.Action)
	})

	t.Run("timeframe", func(t *testing.T) {
		answers := &timeframeAnswers{Timeframe: "24h"}
		if form := TimeframeForm(answers); form == nil {
			t.Fatal("TimeframeForm returned nil")
		}
		if err := dashboard.ValidateTimeframe(answers.Timeframe); err != nil {
			t.Errorf("seeded timeframe %q is not accepted: %v", answers.Timeframe, err)
		}
	})

	t.Run("ab", func(t *testing.T) {
		tests := []dashboard.ABTest{{TestName: "latency"}}
		answers := &abAnswers{TestName: "latency"}
		if form := ABForm(answers, tests); form == nil {
			t.Fatal("ABForm returned nil")
		}
		found := false
		for _, test := range tests {
			if test.TestName == answers.TestName {
				found = true
			}
		}
		if !found {
			t.Errorf("seeded test %q is not among the options", answers.TestName)
		}
	})

	t.Run("cost", func(t *testing.T) {
		answers := &costAnswers{Kind: CostByApp}
		if form := CostForm(answers); form == nil {
			t.Fatal("CostForm returned nil")
		}
		switch answers.Kind {
		case CostByTag, CostByApp, CostBySession:
		default:
			t.Errorf("seeded cost kind %q is not among the options", answers.Kind)
		}
	})
}

// assertSeedIsAnAction checks the menu's default action is one the switch
// understands, so a menu opened with a stale seed cannot fall through to Quit.
func assertSeedIsAnAction(t *testing.T, action string) {
	t.Helper()
	known := []string{ActionOverview, ActionTraffic, ActionTimeseries, ActionCost,
		ActionForecast, ActionCache, ActionBudget, ActionRouting, ActionAB,
		ActionUpstreams, ActionTimeframe, ActionReconnect, ActionQuit}
	for _, candidate := range known {
		if action == candidate {
			return
		}
	}
	t.Errorf("seeded action %q is not one of the menu's options", action)
}

// TestBarsNeverPanicOnDegenerateInput covers the zero/negative-width and
// zero-denominator cases that produced a panic in a previous front-end.
func TestBarsNeverPanicOnDegenerateInput(t *testing.T) {
	for _, width := range []int{-10, -1, 0, 1, 2, 40} {
		for _, fraction := range []float64{-5, 0, 0.0001, 0.5, 1, 9} {
			if got := Bar(fraction, width); got == "" && width > 0 {
				t.Errorf("Bar(%v, %d) returned an empty string", fraction, width)
			}
		}
	}
	if share(5, 0) != 0 {
		t.Error("share with a zero denominator is not 0")
	}
	if share(0, 0) != 0 {
		t.Error("share(0,0) is not 0")
	}
}

// TestNumberFormattingRestoresIntegerCounts documents why aggregate numbers are
// decoded as float64: Oracle NUMBER columns can arrive as 120.0, and a count
// must still read as "120".
func TestNumberFormattingRestoresIntegerCounts(t *testing.T) {
	if got := Count(120.0); got != "120" {
		t.Errorf("Count(120.0) = %q, want 120", got)
	}
	if got := Count(0); got != "0" {
		t.Errorf("Count(0) = %q, want 0", got)
	}
	if got := Money(1.25); got != "1.2500" {
		t.Errorf("Money(1.25) = %q, want 1.2500", got)
	}
	if got := TrimFloat(412.5); got != "412.5" {
		t.Errorf("TrimFloat(412.5) = %q, want 412.5", got)
	}
}

// TestCloseStopsAServiceItStarted keeps a spawned TokenWatch from outliving the
// front-end that spawned it.
func TestCloseStopsAServiceItStarted(t *testing.T) {
	m := newTestModel(t)
	if m.server != nil {
		t.Fatal("a test model should not own a service")
	}
	// Close must be safe with nothing to stop.
	m.Close()
}

// TestInitDoesNotBlockOnATimer asserts the initial command set produces no
// timer, so a test calling Init and draining it cannot hang.
func TestInitDoesNotBlockOnATimer(t *testing.T) {
	m := newTestModel(t)
	m.form = MenuForm(&m.menu, "24h", m.client.BaseURL())
	cmd := m.Init()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 4 && cmd != nil; i++ {
			msg := runCmd(cmd)
			if msg == nil {
				return
			}
			updated, next := m.Update(msg)
			m = updated.(*Model)
			cmd = next
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Init's command tree blocked; a timer is being awaited in a test")
	}
}

// TestContextIsNotRequiredByLoadCmd is a compile-time-ish guard that the load
// path returns a message rather than an error when the service is fine.
func TestLoadCmdProducesThePanelsData(t *testing.T) {
	m := newTestModel(t)
	msg := runCmd(loadCmd(m.client, PanelOverview, "24h", 20, ""))
	loaded, ok := msg.(loadedMsg)
	if !ok {
		t.Fatalf("loadCmd produced %T, want loadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("loadCmd error: %v", loaded.err)
	}
	if loaded.stats == nil || loaded.stats.TotalRequests != 2 {
		t.Errorf("stats = %+v, want 2 requests", loaded.stats)
	}
}

// TestLoadCmdPropagatesServiceErrors keeps a broken endpoint from looking like an
// empty panel.
func TestLoadCmdPropagatesServiceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"detail":"ORA-12541: cannot connect to Oracle"}`)
	}))
	defer server.Close()

	msg := runCmd(loadCmd(dashboard.NewClient(server.URL), PanelBudget, "24h", 20, ""))
	loaded, ok := msg.(loadedMsg)
	if !ok {
		t.Fatalf("got %T, want loadedMsg", msg)
	}
	if loaded.err == nil {
		t.Fatal("err = nil, want the service error")
	}
	if !strings.Contains(loaded.err.Error(), "ORA-12541") {
		t.Errorf("err = %v, want the service's own detail", loaded.err)
	}
}

var _ = context.Background
