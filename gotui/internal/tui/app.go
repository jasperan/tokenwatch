// Package tui is the Go front-end's terminal UI: an operator console for
// TokenWatch built on charm.land/bubbletea/v2, reading the same dashboard JSON
// API (src/tokenwatch/dashboard_app.py) that the bundled web console uses.
//
// It reimplements no accounting, routing, caching or budgeting: every number is
// computed by the Python service, so a Go user and a browser user see identical
// figures.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
)

// requestTimeout bounds every network call so a hung service shows an error
// state instead of freezing the UI.
const requestTimeout = 60 * time.Second

// Screen identifies the active view.
type Screen int

// Screens.
const (
	// ScreenConnect collects where the dashboard is.
	ScreenConnect Screen = iota
	// ScreenMenu is the operator's list of panels.
	ScreenMenu
	// ScreenPanel shows one loaded, read-only panel. It installs no form, so
	// every key can drive the pager.
	ScreenPanel
	// ScreenTimeframe, ScreenLimit, ScreenCost and ScreenAB collect a single
	// choice before opening a panel.
	ScreenTimeframe
	ScreenLimit
	ScreenCost
	ScreenAB
)

// Panel identifies which dataset the read-only view shows.
type Panel int

// Panels.
const (
	PanelNone Panel = iota
	PanelOverview
	PanelTraffic
	PanelTimeseries
	PanelCostTag
	PanelCostApp
	PanelCostSession
	PanelForecast
	PanelCache
	PanelBudget
	PanelRouting
	PanelAB
	PanelUpstreams
)

// panelTitles names each panel for its heading.
var panelTitles = map[Panel]string{
	PanelOverview:    "Overview",
	PanelTraffic:     "Recent traffic",
	PanelTimeseries:  "Rate over time",
	PanelCostTag:     "Cost by feature tag",
	PanelCostApp:     "Cost by source app",
	PanelCostSession: "Cost by session",
	PanelForecast:    "Forecast",
	PanelCache:       "Semantic cache",
	PanelBudget:      "Budgets",
	PanelRouting:     "Routing rules",
	PanelAB:          "A/B tests",
	PanelUpstreams:   "Upstreams",
}

// Options configure a TUI run.
type Options struct {
	ProjectRoot string
	Settings    session.Settings
	// Interval turns on a periodic refresh. Zero means no auto-refresh, which is
	// the default and what tests use: a timer command blocks until it fires, so a
	// suite that drains commands would otherwise wait out the whole interval.
	Interval time.Duration
}

// Model is the root bubbletea model.
type Model struct {
	width  int
	height int
	opts   Options

	screen Screen
	panel  Panel
	client *dashboard.Client
	form   *huh.Form
	view   viewport.Model

	connect   connectAnswers
	menu      menuAnswers
	timeframe timeframeAnswers
	limit     limitAnswers
	cost      costAnswers
	ab        abAnswers

	stats        *dashboard.UsageStats
	recent       []dashboard.RequestRow
	buckets      []dashboard.TimeseriesBucket
	costTags     []dashboard.CostByTag
	costApps     []dashboard.CostByApp
	costSessions []dashboard.CostBySession
	forecast     *dashboard.CostForecast
	cache        *dashboard.CacheStats
	budgets      []dashboard.BudgetStatus
	routing      []dashboard.RoutingStat
	abTests      []dashboard.ABTest
	abReport     *dashboard.ABReport
	upstreams    []dashboard.Upstream

	// server is non-nil when this front-end started TokenWatch itself, so it can
	// stop it again on exit.
	server *session.Server
	// password lives in memory only. It is never written to disk and never passed
	// as a command-line argument.
	password string

	busy    string
	failure error
	notice  string
}

// New builds the root model and shows the connection form.
func New(opts Options) *Model {
	if opts.Settings.BaseURL == "" {
		opts.Settings = session.Defaults()
	}
	m := &Model{
		opts:      opts,
		width:     100,
		height:    30,
		client:    dashboard.NewClient(opts.Settings.BaseURL),
		connect:   ConnectDefaults(opts.Settings),
		timeframe: timeframeAnswers{Timeframe: "24h"},
		limit:     limitAnswers{Limit: "20"},
		screen:    ScreenConnect,
	}
	m.view = viewport.New(viewport.WithWidth(viewportWidth(m.width)), viewport.WithHeight(m.bodyHeight()))
	m.form = ConnectForm(&m.connect, session.PasswordFromEnv() != "")
	m.resize()
	return m
}

// setForm installs a freshly built form, sizes it, and returns its first
// command. Every screen transition with input goes through here so no form can
// ever be left at huh's default zero width (which renders as blank lines).
func (m *Model) setForm(form *huh.Form) tea.Cmd {
	m.form = form
	m.resize()
	return m.form.Init()
}

// resize applies the current window size to the form and the pager.
func (m *Model) resize() {
	if m.form != nil {
		// A form is given the full width minus its own frame; WithWidth is the
		// TOTAL width, and huh renders blank lines at width 0.
		width := m.width - 4
		if width < minPaneWidth {
			width = minPaneWidth
		}
		m.form = m.form.WithWidth(width)
	}
	// The pager is inside a pane, so it gets the pane's CONTENT width, not the
	// terminal's. Getting this wrong makes every row wrap, and clamping it up to
	// a design minimum is what made the pager wider than a very narrow terminal.
	m.view.SetWidth(viewportWidth(m.width))
	m.view.SetHeight(m.bodyHeight())
}

// bodyHeight is the rows available to panel content after the header, the
// status lines and the footer.
func (m *Model) bodyHeight() int {
	height := m.height - 6
	if height < 1 {
		height = 1
	}
	return height
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.form.Init()}
	if tick := m.tickCmd(); tick != nil {
		cmds = append(cmds, tick)
	}
	return tea.Batch(cmds...)
}

// Close stops any service this front-end started.
func (m *Model) Close() {
	if m.server != nil {
		m.server.Stop()
		m.server = nil
	}
}

// tickCmd schedules the next auto-refresh, or returns nil when refresh is off.
func (m *Model) tickCmd() tea.Cmd {
	if m.opts.Interval <= 0 {
		return nil
	}
	return tea.Tick(m.opts.Interval, func(time.Time) tea.Msg { return refreshMsg{} })
}

// refreshMsg triggers a reload of the active panel.
type refreshMsg struct{}

// --- async results -----------------------------------------------------------------

type loadedMsg struct {
	panel     Panel
	stats     *dashboard.UsageStats
	recent    []dashboard.RequestRow
	buckets   []dashboard.TimeseriesBucket
	costTags  []dashboard.CostByTag
	costApps  []dashboard.CostByApp
	sessions  []dashboard.CostBySession
	forecast  *dashboard.CostForecast
	cache     *dashboard.CacheStats
	budgets   []dashboard.BudgetStatus
	routing   []dashboard.RoutingStat
	abTests   []dashboard.ABTest
	abReport  *dashboard.ABReport
	upstreams []dashboard.Upstream
	err       error
}

// --- commands ----------------------------------------------------------------------

// loadCmd fetches exactly the dataset the given panel needs. Each panel issues
// one request, so a slow endpoint cannot delay an unrelated screen.
func loadCmd(client *dashboard.Client, panel Panel, timeframe string, limit int, testName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		out := loadedMsg{panel: panel}
		switch panel {
		case PanelOverview:
			out.stats, out.err = client.Stats(ctx, timeframe)
		case PanelTraffic:
			out.recent, out.err = client.Recent(ctx, limit)
		case PanelTimeseries:
			out.buckets, out.err = client.Timeseries(ctx, timeframe)
		case PanelCostTag:
			out.costTags, out.err = client.CostByTag(ctx, timeframe)
		case PanelCostApp:
			out.costApps, out.err = client.CostByApp(ctx, timeframe)
		case PanelCostSession:
			out.sessions, out.err = client.CostBySession(ctx, limit)
		case PanelForecast:
			out.forecast, out.err = client.CostForecast(ctx)
		case PanelCache:
			out.cache, out.err = client.CacheStats(ctx)
		case PanelBudget:
			out.budgets, out.err = client.BudgetStatus(ctx)
		case PanelRouting:
			out.routing, out.err = client.RoutingStats(ctx)
		case PanelAB:
			if testName == "" {
				out.abTests, out.err = client.ABTests(ctx)
			} else {
				out.abReport, out.err = client.ABReport(ctx, testName)
			}
		case PanelUpstreams:
			out.upstreams, out.err = client.Upstreams(ctx)
		}
		return out
	}
}

// --- update ------------------------------------------------------------------------

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshPanel()
		return m, nil

	case refreshMsg:
		if m.screen == ScreenPanel {
			m.busy = "Refreshing"
			return m, tea.Batch(m.loadCurrent(), m.tickCmd())
		}
		return m, m.tickCmd()

	case loadedMsg:
		return m.applyLoaded(msg)

	case tea.KeyPressMsg:
		// Only key PRESSES are handled. bubbletea v2 also delivers key releases,
		// and acting on both would fire every binding twice.
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
		m.notice = ""
		// A read-only panel has no form, so keys belong to the pager, except for
		// the two that leave or refresh it.
		if m.screen == ScreenPanel {
			switch msg.String() {
			case "q", "esc", "backspace":
				return m, m.toMenu()
			case "r":
				m.busy = "Refreshing"
				return m, m.loadCurrent()
			// bubbles' default pager keymap has no home/end bindings, so the two
			// keys the footer advertises are wired here. A hint that does not work
			// is worse than no hint.
			case "g", "home":
				m.view.GotoTop()
				return m, nil
			case "G", "end":
				m.view.GotoBottom()
				return m, nil
			}
			var cmd tea.Cmd
			m.view, cmd = m.view.Update(msg)
			return m, cmd
		}
	}

	if m.form == nil {
		return m, nil
	}

	// Escape must be intercepted before the form sees it.
	//
	// huh's default keymap binds Quit to ctrl+c ONLY (huh/v2 keymap.go:109), and
	// the sole path that sets StateAborted is key.Matches(msg, keymap.Quit)
	// (form.go:564). ctrl+c is intercepted at the top of Update, so the form never
	// receives Quit either: delegating esc left the form installed, where it
	// swallowed every later keystroke while the footer advertised "esc aborts".
	//
	// Only a key PRESS may abort, never the matching release (see the double-fire
	// rule at the top of this function).
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "esc" {
		m.form = nil
		m.failure = nil
		m.notice = "Cancelled; no changes were made."
		return m, m.abortForm()
	}

	updated, cmd := m.form.Update(msg)
	if form, ok := updated.(*huh.Form); ok {
		m.form = form
	}
	if m.form.State != huh.StateNormal {
		return m, m.advance()
	}
	return m, cmd
}

// applyLoaded stores the fetched dataset and opens the panel.
func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	m.failure = explain(msg.err)

	if msg.err == nil {
		switch msg.panel {
		case PanelOverview:
			m.stats = msg.stats
		case PanelTraffic:
			m.recent = msg.recent
		case PanelTimeseries:
			m.buckets = msg.buckets
		case PanelCostTag:
			m.costTags = msg.costTags
		case PanelCostApp:
			m.costApps = msg.costApps
		case PanelCostSession:
			m.costSessions = msg.sessions
		case PanelForecast:
			m.forecast = msg.forecast
		case PanelCache:
			m.cache = msg.cache
		case PanelBudget:
			m.budgets = msg.budgets
		case PanelRouting:
			m.routing = msg.routing
		case PanelAB:
			if msg.abReport != nil {
				m.abReport = msg.abReport
			} else {
				m.abTests = msg.abTests
				// No tests means no choice to offer: say so instead of building a
				// Select with zero options, which huh renders as an empty field
				// whose Enter silently completes with an empty value.
				if len(m.abTests) == 0 {
					m.panel = PanelAB
					m.screen = ScreenPanel
					m.notice = "No active A/B tests. Create one with: tokenwatch ab create"
					return m, m.refreshPanelCmd()
				}
				m.ab = abAnswers{}
				m.screen = ScreenAB
				return m, m.setForm(ABForm(&m.ab, m.abTests))
			}
		case PanelUpstreams:
			m.upstreams = msg.upstreams
		}
		m.panel = msg.panel
		m.screen = ScreenPanel
		return m, m.refreshPanelCmd()
	}

	// An error keeps the user on a screen they can act from rather than dropping
	// them into an empty panel.
	m.screen = ScreenPanel
	m.panel = msg.panel
	return m, m.refreshPanelCmd()
}

// refreshPanelCmd repaints the pager from the current model state.
func (m *Model) refreshPanelCmd() tea.Cmd {
	m.refreshPanel()
	return m.tickCmd()
}

// tableWidth is the content width a table inside a panel may occupy.
//
// It is the pane's CONTENT width, not the pager's: a table is drawn inside the
// pane's frame and padding, so a row sized to the pager would wrap inside it.
func (m *Model) tableWidth() int { return innerWidth(m.panelWidth()) }

// panelWidth is the total width a panel may occupy.
//
// It is the pager's width, NOT the terminal's: panel content is drawn inside the
// pager, so sizing a pane to the full terminal would push its right border past
// the pager's edge and clip it away.
func (m *Model) panelWidth() int { return viewportWidth(m.width) }

// refreshPanel re-renders the pager content for the active panel.
func (m *Model) refreshPanel() {
	if m.screen != ScreenPanel {
		return
	}
	m.view.SetContent(m.renderPanel())
	m.view.GotoTop()
}

// abortForm applies the outcome of abandoning a form, and is the single
// definition shared by escape and by a huh-reported abort.
//
// The menu quits, because there is nothing behind it. Every other screen returns
// to the menu, which installs the menu's own form.
//
// Note the connection form returns to the menu rather than quitting: that is
// what this code has always done, and it is what the footer contract implies, so
// escape gives the user a way out of the first screen without ending the
// session.
func (m *Model) abortForm() tea.Cmd {
	if m.screen == ScreenMenu {
		return tea.Quit
	}
	return m.toMenu()
}

// advance reacts to a finished form. The screen decides what the answers mean.
func (m *Model) advance() tea.Cmd {
	state := m.form.State
	m.form = nil
	m.failure = nil

	if state == huh.StateAborted {
		// Unreachable by keystroke: huh only aborts on its Quit key, which is
		// ctrl+c alone, and ctrl+c quits at the top of Update before any form sees
		// it. Escape is handled explicitly there instead, and routes here so both
		// paths share one definition of what aborting means.
		return m.abortForm()
	}

	switch m.screen {
	case ScreenConnect:
		return m.finishConnect()
	case ScreenMenu:
		return m.finishMenu()
	case ScreenTimeframe:
		if err := dashboard.ValidateTimeframe(m.timeframe.Timeframe); err != nil {
			m.failure = err
			return m.toMenu()
		}
		m.notice = "Window set to " + m.timeframe.Timeframe
		return m.toMenu()
	case ScreenLimit:
		m.limit.Limit = fmt.Sprintf("%d", ParseLimit(m.limit.Limit))
		// The limit exists to open the traffic panel, so it must do that rather
		// than returning to the menu and making the user pick again.
		return m.openPanel(PanelTraffic)
	case ScreenCost:
		return m.openCostPanel()
	case ScreenAB:
		return m.openABPanel()
	}
	return nil
}

// finishConnect applies the connection answers and moves to the menu.
func (m *Model) finishConnect() tea.Cmd {
	// The field validators let a blank answer stand (see ValidateDefaulted), so
	// the requirement is enforced here, once.
	if err := ValidateConnect(m.connect); err != nil {
		m.failure = err
		m.notice = "The connection was not changed."
		return m.toMenu()
	}
	settings := m.connect.toSettings()

	// A blank password field means "use whatever is already in the environment",
	// which is how a scripted user avoids typing it at all.
	m.password = m.connect.OraclePassword
	if m.password == "" {
		m.password = session.PasswordFromEnv()
	}

	m.opts.Settings = settings
	m.client = dashboard.NewClient(settings.BaseURL)

	// Only non-secret settings are persisted, and a save failure is not fatal.
	if err := session.Save(settings); err != nil {
		m.notice = "Could not save settings: " + err.Error()
	}

	if settings.LaunchServer {
		m.busy = "Starting TokenWatch (dashboard port " + fmt.Sprint(settings.DashboardPort) + ")"
		server, err := session.LaunchServer(context.Background(), m.opts.ProjectRoot,
			settings.DashboardPort, settings.ProxyPort,
			session.ServerEnv(settings.OracleUser, m.password, settings.OracleDSN))
		if err != nil {
			m.busy = ""
			m.failure = err
			return m.toMenu()
		}
		m.server = server
		if err := session.WaitForPort(context.Background(), "127.0.0.1", settings.DashboardPort, 45*time.Second); err != nil {
			m.failure = err
		} else {
			m.notice = "TokenWatch started on port " + fmt.Sprint(settings.DashboardPort)
		}
		m.busy = ""
	}

	return m.toMenu()
}

// finishMenu routes the top-level menu selection.
func (m *Model) finishMenu() tea.Cmd {
	switch m.menu.Action {
	case ActionOverview:
		return m.openPanel(PanelOverview)
	case ActionTraffic:
		m.screen = ScreenLimit
		return m.setForm(LimitForm(&m.limit))
	case ActionTimeseries:
		return m.openPanel(PanelTimeseries)
	case ActionCost:
		m.cost = costAnswers{}
		m.screen = ScreenCost
		return m.setForm(CostForm(&m.cost))
	case ActionForecast:
		return m.openPanel(PanelForecast)
	case ActionCache:
		return m.openPanel(PanelCache)
	case ActionBudget:
		return m.openPanel(PanelBudget)
	case ActionRouting:
		return m.openPanel(PanelRouting)
	case ActionAB:
		m.busy = "Loading A/B tests"
		m.screen = ScreenPanel
		m.panel = PanelAB
		return loadCmd(m.client, PanelAB, m.timeframe.Timeframe, ParseLimit(m.limit.Limit), "")
	case ActionUpstreams:
		return m.openPanel(PanelUpstreams)
	case ActionTimeframe:
		m.timeframe = timeframeAnswers{Timeframe: m.currentTimeframe()}
		m.screen = ScreenTimeframe
		return m.setForm(TimeframeForm(&m.timeframe))
	case ActionReconnect:
		m.connect = ConnectDefaults(m.persistedSettings())
		m.screen = ScreenConnect
		return m.setForm(ConnectForm(&m.connect, session.PasswordFromEnv() != ""))
	default:
		return tea.Quit
	}
}

// openPanel loads a panel that takes no extra input.
func (m *Model) openPanel(panel Panel) tea.Cmd {
	m.busy = "Loading " + strings.ToLower(panelTitles[panel])
	m.screen = ScreenPanel
	m.panel = panel
	return loadCmd(m.client, panel, m.currentTimeframe(), ParseLimit(m.limit.Limit), "")
}

// openCostPanel loads whichever breakdown was chosen.
func (m *Model) openCostPanel() tea.Cmd {
	switch m.cost.Kind {
	case CostByApp:
		return m.openPanel(PanelCostApp)
	case CostBySession:
		return m.openPanel(PanelCostSession)
	default:
		return m.openPanel(PanelCostTag)
	}
}

// openABPanel loads the report for the chosen test.
func (m *Model) openABPanel() tea.Cmd {
	name := strings.TrimSpace(m.ab.TestName)
	if name == "" {
		m.notice = "No test selected."
		return m.toMenu()
	}
	m.busy = "Loading the report for " + name
	m.screen = ScreenPanel
	m.panel = PanelAB
	return loadCmd(m.client, PanelAB, m.currentTimeframe(), ParseLimit(m.limit.Limit), name)
}

// loadCurrent reloads the active panel.
func (m *Model) loadCurrent() tea.Cmd {
	testName := ""
	if m.panel == PanelAB && m.abReport != nil {
		testName = m.abReport.TestName
	}
	return loadCmd(m.client, m.panel, m.currentTimeframe(), ParseLimit(m.limit.Limit), testName)
}

// currentTimeframe never returns an empty window: the service silently treats an
// unknown timeframe as 24h, so an empty value here would misreport the window.
func (m *Model) currentTimeframe() string {
	if err := dashboard.ValidateTimeframe(m.timeframe.Timeframe); err != nil {
		return "24h"
	}
	return m.timeframe.Timeframe
}

func (m *Model) toMenu() tea.Cmd {
	m.screen = ScreenMenu
	m.menu = menuAnswers{}
	return m.setForm(MenuForm(&m.menu, m.currentTimeframe(), m.client.BaseURL()))
}

func (m *Model) persistedSettings() session.Settings {
	settings := m.opts.Settings
	if settings.BaseURL == "" {
		return session.Defaults()
	}
	return settings
}

// explain turns a transport failure into something actionable. A stopped
// service is the common case here, not an exceptional one.
func explain(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dashboard.ErrUnreachable) {
		return errors.New(err.Error() + " - start TokenWatch (tokenwatch start), then choose Reconnect")
	}
	return err
}

// --- view --------------------------------------------------------------------------

// View implements tea.Model.
func (m *Model) View() tea.View {
	var body strings.Builder

	body.WriteString(Header(m.client.BaseURL(), m.currentProxyURL(), m.width))
	body.WriteString("\n")

	if m.busy != "" {
		body.WriteString("\n")
		body.WriteString(Pane("Working", m.busy, m.width))
	}
	if m.failure != nil {
		body.WriteString("\n")
		body.WriteString(PaneError("Problem", m.failure, m.width))
	}
	if m.notice != "" {
		body.WriteString("\n")
		body.WriteString(Pane("Done", m.notice, m.width))
	}

	if m.screen == ScreenPanel {
		body.WriteString("\n")
		// The pager's own rendering is drawn, never a re-render of the content
		// string: setting content and drawing something else is how a panel ends
		// up unscrollable and clipped.
		body.WriteString(m.view.View())
	} else if m.form != nil {
		body.WriteString("\n")
		body.WriteString(m.form.View())
	}

	body.WriteString("\n")
	body.WriteString(Footer(m.hint(), m.width))

	view := tea.NewView(body.String())
	view.AltScreen = true
	return view
}

// currentProxyURL prefers the live setting, falling back to the saved default.
func (m *Model) currentProxyURL() string {
	if m.opts.Settings.ProxyURL != "" {
		return m.opts.Settings.ProxyURL
	}
	return session.Defaults().ProxyURL
}

func (m *Model) hint() string {
	switch m.screen {
	case ScreenConnect:
		return "tab next - shift+tab back - enter submit - ctrl+c quit"
	case ScreenPanel:
		return "up/down scroll - pgup/pgdn page - g/G top or bottom - r refresh - q back to the menu - ctrl+c quit"
	case ScreenMenu:
		return "arrows move - / filters - enter selects - esc quits from the menu - ctrl+c quits"
	default:
		return "tab next - enter submit - esc aborts - ctrl+c quit"
	}
}

// renderPanel renders the active panel as pager content.
func (m *Model) renderPanel() string {
	title := panelTitles[m.panel]
	if title == "" {
		title = "Panel"
	}
	var out strings.Builder

	switch m.panel {
	case PanelOverview:
		out.WriteString(m.viewOverview())
	case PanelTraffic:
		out.WriteString(m.viewTraffic())
	case PanelTimeseries:
		out.WriteString(m.viewTimeseries())
	case PanelCostTag, PanelCostApp, PanelCostSession:
		out.WriteString(m.viewCost())
	case PanelForecast:
		out.WriteString(m.viewForecast())
	case PanelCache:
		out.WriteString(m.viewCache())
	case PanelBudget:
		out.WriteString(m.viewBudget())
	case PanelRouting:
		out.WriteString(m.viewRouting())
	case PanelAB:
		out.WriteString(m.viewAB())
	case PanelUpstreams:
		out.WriteString(m.viewUpstreams())
	}
	return out.String()
}

// empty renders a panel that simply has no rows yet.
func empty(what string) string {
	return Fog("No " + what + " yet.")
}

// viewOverview shows the headline numbers and the per-model split.
func (m *Model) viewOverview() string {
	if m.stats == nil {
		return empty("usage")
	}
	stats := m.stats

	headline := StatRow([][2]string{
		{"window", m.currentTimeframe()},
		{"requests", Count(stats.TotalRequests)},
		{"spend", "$" + Money(stats.TotalEstimatedCost)},
		{"input tokens", Count(stats.TotalInputTokens)},
		{"output tokens", Count(stats.TotalOutputTokens)},
		{"cache reads", Count(stats.TotalCacheReadTokens)},
		{"cache hits", Count(stats.TotalCacheHits)},
		{"cache savings", "$" + Money(stats.TotalCacheSavings)},
	}, m.panelWidth())

	var out strings.Builder
	out.WriteString(Pane("Spend and traffic", headline, m.panelWidth()))

	// Model split, bars scaled against the top spender.
	out.WriteString("\n\n")
	models := make([]string, 0, len(stats.Models))
	maxCost := 0.0
	for name, totals := range stats.Models {
		models = append(models, name)
		if totals.Cost > maxCost {
			maxCost = totals.Cost
		}
	}
	// Sorted by cost descending: the point of the panel is where the money goes.
	sort.Slice(models, func(i, j int) bool {
		if stats.Models[models[i]].Cost != stats.Models[models[j]].Cost {
			return stats.Models[models[i]].Cost > stats.Models[models[j]].Cost
		}
		return models[i] < models[j]
	})

	if len(models) == 0 {
		out.WriteString(Pane("By model", empty("model usage"), m.panelWidth()))
		return out.String()
	}

	barWidth := barColumn(m.panelWidth(), []int{30, 7, 9, 9, 12})
	rows := make([][]string, 0, len(models))
	for _, name := range models {
		totals := stats.Models[name]
		share := 0.0
		if maxCost > 0 {
			share = totals.Cost / maxCost
		}
		rows = append(rows, []string{
			Truncate(name, 30),
			Count(totals.Requests),
			Count(totals.InputTokens),
			Count(totals.OutputTokens),
			"$" + Money(totals.Cost),
			Bar(share, barWidth),
		})
	}
	table := Table(
		[]string{"model", "reqs", "in", "out", "spend", "share"},
		rows,
		[]int{30, 7, 9, 9, 12, barWidth},
		m.tableWidth(),
	)
	out.WriteString(Pane("By model", table, m.panelWidth()))
	return out.String()
}

// viewTraffic lists the most recent requests.
func (m *Model) viewTraffic() string {
	if len(m.recent) == 0 {
		return Pane("Recent traffic", empty("requests"), m.panelWidth())
	}
	rows := make([][]string, 0, len(m.recent))
	for _, row := range m.recent {
		cached := "miss"
		if row.WasCached() {
			cached = "HIT"
		}
		status := Count(row.StatusCode)
		if row.StatusCode >= 400 {
			status = Bad(status)
		}
		rows = append(rows, []string{
			row.CreatedAt,
			Truncate(row.ModelUsed, 26),
			Truncate(row.SourceApp, 14),
			Count(row.InputTokens),
			Count(row.OutputTokens),
			Count(row.LatencyMS) + "ms",
			status,
			cached,
			"$" + Money(row.EstimatedCost),
		})
	}
	// The feature tag is deliberately not a column: with it the row needs 136
	// columns and the spend column is clipped away on a normal terminal, and the
	// cost-by-tag panel already answers that question properly.
	table := Table(
		[]string{"when", "model used", "app", "in", "out", "latency", "code", "cache", "spend"},
		rows,
		[]int{19, 26, 14, 6, 6, 8, 4, 5, 9},
		m.tableWidth(),
	)
	summary := Fog(fmt.Sprintf("%d request(s) shown", len(m.recent)))
	return Pane("Recent traffic", summary+"\n\n"+table, m.panelWidth())
}

// viewTimeseries draws spend and request volume per bucket.
//
// This is the panel a text table cannot replace: a trend is a shape, and the
// bars are what make a spike visible.
func (m *Model) viewTimeseries() string {
	if len(m.buckets) == 0 {
		return Pane("Rate over time", empty("buckets"), m.panelWidth())
	}
	maxCost := 0.0
	maxRequests := 0.0
	for _, bucket := range m.buckets {
		if bucket.Cost > maxCost {
			maxCost = bucket.Cost
		}
		if bucket.Requests > maxRequests {
			maxRequests = bucket.Requests
		}
	}

	// Two bar columns share the remaining space: 5 gaps across 6 columns.
	barWidth := barColumnFlex(m.panelWidth(), []int{17, 7, 12, 6}, 5, 2)
	rows := make([][]string, 0, len(m.buckets))
	for _, bucket := range m.buckets {
		costShare, reqShare := 0.0, 0.0
		if maxCost > 0 {
			costShare = bucket.Cost / maxCost
		}
		if maxRequests > 0 {
			reqShare = bucket.Requests / maxRequests
		}
		rows = append(rows, []string{
			Truncate(bucket.Bucket, 17),
			Count(bucket.Requests),
			Bar(reqShare, barWidth),
			Bar(costShare, barWidth),
			"$" + Money(bucket.Cost),
			Count(bucket.CacheHits),
		})
	}
	table := Table(
		[]string{"bucket", "reqs", "requests", "spend", "cost", "hits"},
		rows,
		[]int{17, 7, barWidth, barWidth, 12, 6},
		m.tableWidth(),
	)
	summary := Fog(fmt.Sprintf("%d bucket(s) - bars are scaled to the busiest bucket", len(m.buckets)))
	return Pane("Rate over time ("+m.currentTimeframe()+")", summary+"\n\n"+table, m.panelWidth())
}

// viewCost renders whichever cost breakdown is loaded.
func (m *Model) viewCost() string {
	switch m.panel {
	case PanelCostApp:
		if len(m.costApps) == 0 {
			return Pane("Cost by source app", empty("app cost"), m.panelWidth())
		}
		maxCost := 0.0
		for _, row := range m.costApps {
			if row.TotalCost > maxCost {
				maxCost = row.TotalCost
			}
		}
		barWidth := barColumn(m.panelWidth(), []int{28, 7, 12, 12})
		rows := make([][]string, 0, len(m.costApps))
		for _, row := range m.costApps {
			rows = append(rows, []string{
				Truncate(row.App, 28),
				Count(row.Requests),
				"$" + Money(row.TotalCost),
				"$" + Money(row.AvgCost),
				Bar(share(row.TotalCost, maxCost), barWidth),
			})
		}
		table := Table([]string{"app", "reqs", "spend", "avg", "share"},
			rows, []int{28, 7, 12, 12, barWidth},
			m.tableWidth())
		return Pane("Cost by source app", table, m.panelWidth())

	case PanelCostSession:
		if len(m.costSessions) == 0 {
			return Pane("Cost by session", empty("sessions"), m.panelWidth())
		}
		maxCost := 0.0
		for _, row := range m.costSessions {
			if row.ConversationCost > maxCost {
				maxCost = row.ConversationCost
			}
		}
		barWidth := barColumn(m.panelWidth(), []int{24, 6, 12, 19, 19})
		rows := make([][]string, 0, len(m.costSessions))
		for _, row := range m.costSessions {
			rows = append(rows, []string{
				Truncate(row.SessionID, 24),
				Count(row.Turns),
				"$" + Money(row.ConversationCost),
				Truncate(row.Started, 19),
				Truncate(row.Ended, 19),
				Bar(share(row.ConversationCost, maxCost), barWidth),
			})
		}
		table := Table([]string{"session", "turns", "spend", "started", "ended", "share"},
			rows, []int{24, 6, 12, 19, 19, barWidth},
			m.tableWidth())
		return Pane("Cost by session", table, m.panelWidth())

	default:
		if len(m.costTags) == 0 {
			return Pane("Cost by feature tag", empty("tagged cost"), m.panelWidth())
		}
		maxCost := 0.0
		for _, row := range m.costTags {
			if row.TotalCost > maxCost {
				maxCost = row.TotalCost
			}
		}
		barWidth := barColumn(m.panelWidth(), []int{24, 7, 12, 12, 9, 9})
		rows := make([][]string, 0, len(m.costTags))
		for _, row := range m.costTags {
			rows = append(rows, []string{
				Truncate(row.Tag, 24),
				Count(row.Requests),
				"$" + Money(row.TotalCost),
				"$" + Money(row.AvgCost),
				Count(row.InputTokens),
				Count(row.OutputTokens),
				Bar(share(row.TotalCost, maxCost), barWidth),
			})
		}
		table := Table([]string{"tag", "reqs", "spend", "avg", "in", "out", "share"},
			rows, []int{24, 7, 12, 12, 9, 9, barWidth},
			m.tableWidth())
		return Pane("Cost by feature tag", table, m.panelWidth())
	}
}

// share guards every bar against a zero denominator.
func share(value, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return value / max
}

// viewForecast shows the projection the service computed.
func (m *Model) viewForecast() string {
	if m.forecast == nil {
		return Pane("Forecast", empty("forecast"), m.panelWidth())
	}
	body := StatRow([][2]string{
		{"last 7 days", "$" + Money(m.forecast.Last7DaysTotal)},
		{"daily average", "$" + Money(m.forecast.DailyAvg)},
		{"monthly projection", "$" + Money(m.forecast.MonthlyProjection)},
		{"active days (7d)", Count(m.forecast.ActiveDays)},
	}, m.panelWidth())
	return Pane("Forecast", body+"\n\n"+Fog("Projection = 7-day average x 30, computed by db.cost_forecast."), m.panelWidth())
}

// viewCache shows cache occupancy and the hit share of all requests.
func (m *Model) viewCache() string {
	if m.cache == nil {
		return Pane("Semantic cache", empty("cache data"), m.panelWidth())
	}
	// The API reports entries and hits but no denominator, so the share shown is
	// hits against hits-plus-entries. It is labelled that way rather than being
	// presented as a true hit rate, which the service does not compute.
	ofEntries := share(m.cache.TotalHits, m.cache.TotalHits+m.cache.Entries)
	barWidth := innerWidth(m.panelWidth()) - 24
	if barWidth < 4 {
		barWidth = 4
	}
	body := StatRow([][2]string{
		{"entries", Count(m.cache.Entries)},
		{"active entries", Count(m.cache.ActiveEntries)},
		{"total hits", Count(m.cache.TotalHits)},
	}, m.panelWidth())
	body += "\n\n" + Stat("hits vs hits+entries",
		fmt.Sprintf("%.1f%%", ofEntries*100)+"  "+Bar(ofEntries, barWidth))
	return Pane("Semantic cache", body, m.panelWidth())
}

// viewBudget shows each budget's utilisation.
func (m *Model) viewBudget() string {
	if len(m.budgets) == 0 {
		return Pane("Budgets", empty("budgets"), m.panelWidth())
	}
	barWidth := barColumn(m.panelWidth(), []int{22, 7, 7, 12, 12, 8, 10, 4})
	rows := make([][]string, 0, len(m.budgets))
	for _, budget := range m.budgets {
		scope := budget.Scope
		if budget.ScopeValue != "" {
			scope += ":" + budget.ScopeValue
		}
		fraction := budget.UtilizationPct / 100
		status := "ok"
		label := fmt.Sprintf("%.1f%%", budget.UtilizationPct)
		if budget.UtilizationPct >= 100 {
			status = "over"
			label = Bad(label)
		} else if budget.UtilizationPct >= 80 {
			status = "near limit"
			label = Fog(label)
		}
		active := "yes"
		if !budget.IsActive {
			active = "no"
		}
		rows = append(rows, []string{
			Truncate(scope, 22),
			budget.Period,
			Truncate(budget.ActionOnLimit, 7),
			"$" + Money(budget.LimitAmount),
			"$" + Money(budget.CurrentSpend),
			label,
			status,
			active,
			Bar(fraction, barWidth),
		})
	}
	table := Table(
		[]string{"scope", "period", "action", "limit", "spent", "used", "state", "on", "utilisation"},
		rows,
		[]int{22, 7, 7, 12, 12, 8, 10, 4, barWidth},
		m.tableWidth(),
	)
	return Pane("Budgets", table+"\n\n"+
		Fog("state is derived from utilisation_pct: over is 100%+, near limit is 80-99%."), m.panelWidth())
}

// viewRouting shows per-rule traffic.
func (m *Model) viewRouting() string {
	if len(m.routing) == 0 {
		return Pane("Routing rules", empty("rules"), m.panelWidth())
	}
	maxRequests := 0.0
	for _, row := range m.routing {
		if row.TotalRequests > maxRequests {
			maxRequests = row.TotalRequests
		}
	}
	barWidth := barColumn(m.panelWidth(), []int{22, 24, 7, 12, 12})
	rows := make([][]string, 0, len(m.routing))
	for _, rule := range m.routing {
		rows = append(rows, []string{
			Truncate(rule.RuleName, 22),
			Truncate(rule.TargetModel, 24),
			Count(rule.TotalRequests),
			"$" + Money(rule.TotalCost),
			TrimFloat(rule.AvgLatencyMS) + "ms",
			Bar(share(rule.TotalRequests, maxRequests), barWidth),
		})
	}
	table := Table([]string{"rule", "target model", "reqs", "spend", "avg latency", "share"},
		rows, []int{22, 24, 7, 12, 12, barWidth},
		m.tableWidth())
	return Pane("Routing rules", table, m.panelWidth())
}

// viewAB shows either the test list or one test's variant comparison.
func (m *Model) viewAB() string {
	if m.abReport != nil {
		report := m.abReport
		if len(report.Variants) == 0 {
			return Pane("A/B test "+report.TestName, empty("recorded traffic for this test"), m.panelWidth())
		}
		minError, maxError := report.Variants[0].ErrorRate, report.Variants[0].ErrorRate
		for _, v := range report.Variants {
			if v.ErrorRate < minError {
				minError = v.ErrorRate
			}
			if v.ErrorRate > maxError {
				maxError = v.ErrorRate
			}
		}
		barWidth := barColumn(m.panelWidth(), []int{26, 7, 12, 9, 12, 8})
		rows := make([][]string, 0, len(report.Variants))
		for _, variant := range report.Variants {
			errorText := fmt.Sprintf("%.2f%%", variant.ErrorRate)
			if variant.ErrorRate == maxError && maxError > minError {
				errorText = Bad(errorText)
			}
			rows = append(rows, []string{
				Truncate(variant.Variant, 26),
				Count(variant.TotalRequests),
				TrimFloat(variant.AvgLatencyMS) + "ms",
				TrimFloat(variant.AvgOutputTokens),
				"$" + Money(variant.TotalCost),
				errorText,
				Bar(share(variant.TotalRequests, maxRequestsOf(report.Variants)), barWidth),
			})
		}
		table := Table([]string{"variant", "reqs", "avg latency", "avg out", "spend", "errors", "share"},
			rows, []int{26, 7, 12, 9, 12, 8, barWidth},
			m.tableWidth())
		return Pane("A/B test "+report.TestName, table, m.panelWidth())
	}

	if len(m.abTests) == 0 {
		return Pane("A/B tests", empty("active A/B tests"), m.panelWidth())
	}
	rows := make([][]string, 0, len(m.abTests))
	for _, test := range m.abTests {
		rows = append(rows, []string{
			Truncate(test.TestName, 24),
			Truncate(test.ModelA, 24),
			Truncate(test.ModelB, 24),
			Count(test.SplitPct),
			test.Status,
		})
	}
	table := Table([]string{"test", "model a", "model b", "split", "status"},
		rows, []int{24, 24, 24, 6, 10},
		m.tableWidth())
	return Pane("Active A/B tests", table, m.panelWidth())
}

// maxRequestsOf finds the busiest variant, for bar scaling.
func maxRequestsOf(variants []dashboard.ABVariant) float64 {
	max := 0.0
	for _, variant := range variants {
		if variant.TotalRequests > max {
			max = variant.TotalRequests
		}
	}
	return max
}

// viewUpstreams shows endpoint health.
func (m *Model) viewUpstreams() string {
	if len(m.upstreams) == 0 {
		return Pane("Upstreams", empty("upstreams"), m.panelWidth())
	}
	rows := make([][]string, 0, len(m.upstreams))
	for _, upstream := range m.upstreams {
		health := Good("healthy")
		if !upstream.IsHealthy {
			health = Bad("unhealthy")
		}
		rows = append(rows, []string{
			upstream.APIType,
			Truncate(upstream.BaseURL, 42),
			Count(upstream.Priority),
			health,
			Count(upstream.FailCount),
		})
	}
	table := Table([]string{"api", "base url", "priority", "health", "fails"},
		rows, []int{10, 42, 8, 10, 6},
		m.tableWidth())
	return Pane("Upstreams", table, m.panelWidth())
}

// AccessibleNotice explains the mode switch a screen-reader user gets.
const AccessibleNotice = "ACCESSIBLE is set: using plain prompts instead of the full-screen UI."
