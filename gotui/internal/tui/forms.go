package tui

import (
	"errors"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
	"github.com/jasperan/tokenwatch/gotui/internal/huhstyle"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
)

// Menu actions. Each is a stable identifier rather than a label, so a label can
// be reworded without silently changing which panel opens.
const (
	ActionOverview   = "overview"
	ActionTraffic    = "traffic"
	ActionTimeseries = "timeseries"
	ActionCost       = "cost"
	ActionForecast   = "forecast"
	ActionCache      = "cache"
	ActionBudget     = "budget"
	ActionRouting    = "routing"
	ActionAB         = "ab"
	ActionUpstreams  = "upstreams"
	ActionTimeframe  = "timeframe"
	ActionReconnect  = "reconnect"
	ActionQuit       = "quit"

	// ActionHealth is the scripted reachability check: it reports whether the
	// dashboard is up without opening any panel.
	ActionHealth = "health"
)

// ActionAnswers is what the accessible path resolved: which panel to print, for
// which window, against which dashboard. It carries no credentials.
type ActionAnswers struct {
	Action    string
	Timeframe string
	BaseURL   string
}

// Cost breakdown kinds, matching the three cost endpoints.
const (
	CostByTag     = "tag"
	CostByApp     = "app"
	CostBySession = "session"
)

// connectAnswers collects the dashboard location and how to reach Oracle.
type connectAnswers struct {
	BaseURL        string
	ProxyURL       string
	DashboardPort  string
	ProxyPort      string
	Launch         bool
	OracleUser     string
	OracleDSN      string
	OraclePassword string
}

// toSettings converts answers into persisted, non-secret settings.
//
// A blank field means "keep the default": the accessible prompts never print a
// pre-filled value, so a blank answer has to resolve rather than fail here.
func (a connectAnswers) toSettings() session.Settings {
	defaults := session.Defaults()
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if base == "" {
		base = defaults.BaseURL
	}
	proxy := strings.TrimRight(strings.TrimSpace(a.ProxyURL), "/")
	if proxy == "" {
		proxy = defaults.ProxyURL
	}
	return session.Settings{
		BaseURL:       base,
		ProxyURL:      proxy,
		LaunchServer:  a.Launch,
		DashboardPort: session.ParsePort(a.DashboardPort, session.DefaultDashboardPort),
		ProxyPort:     session.ParsePort(a.ProxyPort, session.DefaultProxyPort),
		OracleDSN:     strings.TrimSpace(a.OracleDSN),
		OracleUser:    strings.TrimSpace(a.OracleUser),
	}
}

// ValidateConnect checks the answers AFTER the form has closed.
//
// This is where a requirement lives once its field validator has been relaxed:
// the field validators must let a blank answer through so a screen-reader user is
// not trapped, and the requirement is then enforced once, here.
func ValidateConnect(answers connectAnswers) error {
	if err := dashboard.ValidateBaseURL(answers.toSettings().BaseURL); err != nil {
		return err
	}
	if err := session.ValidatePort(strconv.Itoa(answers.toSettings().DashboardPort)); err != nil {
		return err
	}
	return session.ValidatePort(strconv.Itoa(answers.toSettings().ProxyPort))
}

// ConnectDefaults seeds the connection form from saved settings.
func ConnectDefaults(settings session.Settings) connectAnswers {
	if settings.BaseURL == "" {
		settings = session.Defaults()
	}
	return connectAnswers{
		BaseURL:       settings.BaseURL,
		ProxyURL:      settings.ProxyURL,
		DashboardPort: strconv.Itoa(settings.DashboardPort),
		ProxyPort:     strconv.Itoa(settings.ProxyPort),
		Launch:        settings.LaunchServer,
		OracleUser:    settings.OracleUser,
		OracleDSN:     settings.OracleDSN,
	}
}

// menuAnswers holds the chosen menu action.
type menuAnswers struct{ Action string }

// timeframeAnswers holds the chosen timeframe.
type timeframeAnswers struct{ Timeframe string }

// limitAnswers holds the requested row count.
type limitAnswers struct{ Limit string }

// costAnswers holds the chosen cost breakdown.
type costAnswers struct{ Kind string }

// abAnswers holds the chosen A/B test.
type abAnswers struct{ TestName string }

// themed applies the canonical theme and accessible mode to a form.
//
// huh.ThemeFunc is required: huh v2's Theme interface declares a Theme method,
// which *huh.Styles does not have, so the bare *Styles cannot be passed to
// WithTheme. ThemeFunc's underlying type matches huhstyle.Theme's signature, so
// converting it satisfies the interface.
func themed(form *huh.Form) *huh.Form {
	return form.WithTheme(huh.ThemeFunc(huhstyle.Theme)).WithAccessible(huhstyle.Accessible())
}

// ValidateDefaulted lets a blank answer stand.
//
// It exists because of how huh's accessible prompts behave: the validator runs on
// the RAW line and the field's default is substituted only afterwards, and the
// prompt never prints that default. A pre-filled field whose validator rejects ""
// therefore re-prompts on every bare Enter, so a screen-reader user is stuck on a
// value they cannot see or accept. Every validator on a pre-filled field is
// wrapped in this, and the real requirement is enforced once, after the form.
func ValidateDefaulted(inner func(string) error) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return inner(s)
	}
}

// connectionFields are the dashboard location fields.
func connectionFields(answers *connectAnswers) []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Dashboard URL").
			Description("the TokenWatch dashboard API, default 127.0.0.1:8878").
			Value(&answers.BaseURL).
			Validate(ValidateDefaulted(dashboard.ValidateBaseURL)),
		huh.NewInput().
			Title("Proxy URL").
			Description("the address your apps send traffic to").
			Value(&answers.ProxyURL),
	}
}

// portFields are the two ports and the launch decision.
func portFields(answers *connectAnswers) []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Dashboard port").
			Value(&answers.DashboardPort).
			Validate(ValidateDefaulted(session.ValidatePort)),
		huh.NewInput().
			Title("Proxy port").
			Value(&answers.ProxyPort).
			Validate(ValidateDefaulted(session.ValidatePort)),
		huh.NewConfirm().
			Title("Start TokenWatch for me?").
			Description("runs the repo's own CLI: uv run tokenwatch start").
			Affirmative("Start it").
			Negative("It is already running").
			Value(&answers.Launch),
	}
}

// credentialFields are the Oracle values the spawned service reads from its
// environment.
func credentialFields(answers *connectAnswers, passwordInEnv bool) []huh.Field {
	passwordHint := "read from " + session.EnvOraclePassword
	if !passwordInEnv {
		passwordHint = "leave blank to use " + session.EnvOraclePassword
	}
	return []huh.Field{
		huh.NewInput().
			Title("Oracle user").
			Description("passed to the service as " + session.EnvOracleUser).
			Value(&answers.OracleUser),
		huh.NewInput().
			Title("Oracle DSN").
			Description("passed to the service as " + session.EnvOracleDSN).
			Value(&answers.OracleDSN),
		huh.NewInput().
			Title("Oracle password").
			Description("passed to the service as " + session.EnvOraclePassword + "; never written to disk").
			EchoMode(huh.EchoModePassword).
			Value(&answers.OraclePassword).
			Placeholder(passwordHint),
	}
}

// connectGroups builds the connection form's groups.
func connectGroups(answers *connectAnswers, passwordInEnv bool) []*huh.Group {
	return []*huh.Group{
		huh.NewGroup(connectionFields(answers)...).Title("Connection"),
		huh.NewGroup(portFields(answers)...).Title("Ports"),
		huh.NewGroup(credentialFields(answers, passwordInEnv)...).
			Title("Credentials (only needed to start the service)"),
	}
}

// connectFields flattens the connection form's fields, so the accessible tests
// can drive the real fields one at a time. huh's accessible prompts each build
// their own buffered scanner, so only the first prompt of a multi-field form can
// be fed real input; per-field driving is the only way to exercise the rest.
func connectFields(answers *connectAnswers, passwordInEnv bool) []huh.Field {
	fields := connectionFields(answers)
	fields = append(fields, portFields(answers)...)
	fields = append(fields, credentialFields(answers, passwordInEnv)...)
	return fields
}

// ConnectForm asks where the dashboard is and how to reach Oracle.
func ConnectForm(answers *connectAnswers, passwordInEnv bool) *huh.Form {
	return themed(huh.NewForm(connectGroups(answers, passwordInEnv)...))
}

// MenuForm presents the operator's panels.
//
// The timeframe is part of the label, not a separate screen, so a user always
// knows which window the numbers describe.
func MenuForm(answers *menuAnswers, timeframe, baseURL string) *huh.Form {
	return themed(huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What do you want to see?  (" + timeframe + " window)").
				Description(baseURL).
				Options(MenuOptions()...).
				Value(&answers.Action),
		),
	))
}

// MenuOptions is the menu's option list, exposed so a test can assert that the
// seeded action is one of them: huh silently selects the FIRST option when a
// seeded value is absent, which would open the wrong panel.
func MenuOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("Overview - spend, requests, cache savings", ActionOverview),
		huh.NewOption("Traffic - most recent requests", ActionTraffic),
		huh.NewOption("Rate over time - spend and requests per bucket", ActionTimeseries),
		huh.NewOption("Cost breakdown - by tag, app or session", ActionCost),
		huh.NewOption("Forecast - projected monthly spend", ActionForecast),
		huh.NewOption("Cache - semantic cache entries and hits", ActionCache),
		huh.NewOption("Budgets - limits and utilisation", ActionBudget),
		huh.NewOption("Routing - per-rule traffic", ActionRouting),
		huh.NewOption("A/B tests - variants side by side", ActionAB),
		huh.NewOption("Upstreams - endpoint health", ActionUpstreams),
		huh.NewOption("Change window", ActionTimeframe),
		huh.NewOption("Reconnect", ActionReconnect),
		huh.NewOption("Quit", ActionQuit),
	}
}

// TimeframeOptions is the five windows the service accepts.
func TimeframeOptions() []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(dashboard.Timeframes))
	for _, tf := range dashboard.Timeframes {
		options = append(options, huh.NewOption(tf, tf))
	}
	return options
}

// TimeframeForm picks one of the five windows the service accepts.
func TimeframeForm(answers *timeframeAnswers) *huh.Form {
	return themed(huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Window").
				Description("matches db._timeframe_where; the service only accepts these").
				Options(TimeframeOptions()...).
				Value(&answers.Timeframe),
		),
	))
}

// limitFields are the row-count fields, exposed for per-field driving.
func limitFields(answers *limitAnswers) []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("How many recent requests?").
			Description("1-500; the service enforces the same bound").
			Value(&answers.Limit).
			Validate(ValidateDefaulted(ValidateLimit)),
	}
}

// LimitForm asks how many recent requests to show.
func LimitForm(answers *limitAnswers) *huh.Form {
	return themed(huh.NewForm(huh.NewGroup(limitFields(answers)...)))
}

// CostOptions is which cost breakdown can be shown.
func CostOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("Feature tag", CostByTag),
		huh.NewOption("Source app", CostByApp),
		huh.NewOption("Session (top conversation cost)", CostBySession),
	}
}

// CostForm picks which cost breakdown to show.
func CostForm(answers *costAnswers) *huh.Form {
	return themed(huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Cost by").
				Options(CostOptions()...).
				Value(&answers.Kind),
		),
	))
}

// ABOptions builds the test list to choose from, so a test can assert the seed
// is among them.
func ABOptions(tests []dashboard.ABTest) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(tests))
	for _, test := range tests {
		label := test.TestName
		if test.ModelA != "" || test.ModelB != "" {
			label += "  (" + test.ModelA + " vs " + test.ModelB + ")"
		}
		options = append(options, huh.NewOption(label, test.TestName))
	}
	return options
}

// ABForm picks one of the A/B tests the service reports as active.
//
// The options come from the loaded list, so the selection can never name a test
// that does not exist. This matters because huh silently falls back to the FIRST
// option when a seeded value is absent, which would report the wrong test.
func ABForm(answers *abAnswers, tests []dashboard.ABTest) *huh.Form {
	return themed(huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which A/B test?").
				Options(ABOptions(tests)...).
				Value(&answers.TestName),
		),
	))
}

// ValidateLimit accepts only 1-500, the bound the service's own query enforces.
func ValidateLimit(raw string) error {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("use a whole number")
	}
	if value < 1 || value > 500 {
		return errors.New("use a number between 1 and 500")
	}
	return nil
}

// ParseLimit converts a validated limit, falling back to the service default.
func ParseLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 || value > 500 {
		return 50
	}
	return value
}

// PlainAnswers is every answer the accessible path collects.
type PlainAnswers struct {
	Connect   connectAnswers
	Menu      menuAnswers
	Timeframe timeframeAnswers
}

// plainGroupFields are the accessible form's fields in order.
func plainGroupFields(answers *PlainAnswers) []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Dashboard URL").
			Value(&answers.Connect.BaseURL).
			Validate(ValidateDefaulted(dashboard.ValidateBaseURL)),
		huh.NewSelect[string]().
			Title("Window").
			Options(TimeframeOptions()...).
			Value(&answers.Timeframe.Timeframe),
		huh.NewSelect[string]().
			Title("What do you want to see?").
			Options(MenuOptions()...).
			Value(&answers.Menu.Action),
	}
}

// plainGroups builds the accessible flow as ONE form.
func plainGroups(answers *PlainAnswers) []*huh.Group {
	return []*huh.Group{huh.NewGroup(plainGroupFields(answers)...)}
}

// plainFields flattens the accessible form's fields, for per-field driving.
func plainFields(answers *PlainAnswers) []huh.Field { return plainGroupFields(answers) }

// PlainForm is the accessible flow as ONE pass of plain prompts.
//
// One form, one read of the reader: huh's accessible Form.Run wraps the reader
// in its own scanner, which buffers ahead, so a second form built over the same
// stdin starts at EOF and returns empty answers for everything.
func PlainForm(answers *PlainAnswers) *huh.Form {
	return themed(huh.NewForm(plainGroups(answers)...))
}
