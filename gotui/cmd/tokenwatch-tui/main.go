// Command tokenwatch-tui is an additional way to run TokenWatch: an operator
// console in the charm v2 + huh stack that reads the same dashboard JSON API
// (src/tokenwatch/dashboard_app.py) the bundled web console uses.
//
// It reimplements no accounting, routing, caching or budgeting. Spend, traffic,
// cache and budget figures all come from the Python service, so a Go user and a
// browser user see identical numbers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"github.com/jasperan/tokenwatch/gotui/internal/dashboard"
	"github.com/jasperan/tokenwatch/gotui/internal/huhstyle"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
	"github.com/jasperan/tokenwatch/gotui/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tokenwatch-tui: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		baseURL       = flag.String("base-url", "", "dashboard URL (default from saved settings or 127.0.0.1:8878)")
		dashboardPort = flag.Int("dashboard-port", 0, "dashboard port for --start-service")
		proxyPort     = flag.Int("proxy-port", 0, "proxy port for --start-service")
		startSvc      = flag.Bool("start-service", false, "start the repo's own service (uv run tokenwatch start) before connecting")
		oracleUser    = flag.String("oracle-user", "", "Oracle user for the started service (env: "+session.EnvOracleUser+")")
		oracleDSN     = flag.String("oracle-dsn", "", "Oracle DSN for the started service (env: "+session.EnvOracleDSN+")")
		projectRoot   = flag.String("project-root", "", "tokenwatch checkout used to start the service (default: this binary's repo)")
		refresh       = flag.Duration("refresh", 0, "auto-refresh interval for the full-screen UI (0 disables it)")

		overview   = flag.Bool("overview", false, "print spend and traffic totals, then exit")
		traffic    = flag.Bool("traffic", false, "print recent requests, then exit")
		timeseries = flag.Bool("timeseries", false, "print spend and requests per bucket, then exit")
		cost       = flag.Bool("cost", false, "print cost attribution, then exit")
		forecast   = flag.Bool("forecast", false, "print the monthly projection, then exit")
		cache      = flag.Bool("cache", false, "print semantic cache statistics, then exit")
		budget     = flag.Bool("budget", false, "print budget limits and utilisation, then exit")
		routing    = flag.Bool("routing", false, "print per-rule routing statistics, then exit")
		ab         = flag.Bool("ab", false, "list active A/B tests, or one test's report with --test, then exit")
		upstreams  = flag.Bool("upstreams", false, "print upstream health, then exit")
		health     = flag.Bool("health", false, "print a one-line reachability summary, then exit")

		timeframe = flag.String("timeframe", "24h", "window for timeframe-aware actions: 1h, 24h, 7d, 30d or all")
		limit     = flag.Int("limit", 20, "rows for --traffic and --cost=session")
		testName  = flag.String("test", "", "A/B test name for --ab")
		jsonFlag  = flag.Bool("json", false, "emit machine-readable JSON for scripted actions")
		noInput   = flag.Bool("no-input", false, "never prompt; fail instead if input is required")
	)
	flag.Parse()

	settings, err := session.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "note: "+err.Error())
	}
	if *baseURL != "" {
		if err := dashboard.ValidateBaseURL(*baseURL); err != nil {
			return fmt.Errorf("--base-url: %w", err)
		}
		settings.BaseURL = strings.TrimRight(strings.TrimSpace(*baseURL), "/")
	}
	if *dashboardPort != 0 {
		if err := session.ValidatePort(fmt.Sprint(*dashboardPort)); err != nil {
			return fmt.Errorf("--dashboard-port: %w", err)
		}
		settings.DashboardPort = *dashboardPort
	}
	if *proxyPort != 0 {
		if err := session.ValidatePort(fmt.Sprint(*proxyPort)); err != nil {
			return fmt.Errorf("--proxy-port: %w", err)
		}
		settings.ProxyPort = *proxyPort
	}
	if *oracleUser != "" {
		settings.OracleUser = *oracleUser
	}
	if *oracleDSN != "" {
		settings.OracleDSN = *oracleDSN
	}
	if *startSvc {
		settings.LaunchServer = true
	}

	root := *projectRoot
	if root == "" {
		root = defaultProjectRoot()
	}

	// --- scripted path -----------------------------------------------------------
	// This runs before any prompt is considered, so a pipeline never blocks on a
	// question. It is also the path a cron job uses to watch spend.
	action, isAction := tui.ParseActionFlags(*overview, *traffic, *timeseries, *cost,
		*forecast, *cache, *budget, *routing, *ab, *upstreams, *health)
	if isAction {
		client := dashboard.NewClient(settings.BaseURL)
		var server *session.Server
		if settings.LaunchServer {
			server, err = startService(settings, root, os.Stdout)
			if err != nil {
				return err
			}
			defer server.Stop()
		}
		return tui.RunAction(context.Background(), client, tui.ActionRequest{
			Action:    action,
			Timeframe: *timeframe,
			Limit:     *limit,
			TestName:  *testName,
			JSON:      *jsonFlag,
		}, os.Stdout)
	}

	// --- screen-reader / piped path ---------------------------------------------
	// huh's accessible rendering only exists in its standalone Run path, so the
	// embedded full-screen UI is skipped entirely here.
	if huhstyle.Accessible() {
		fmt.Fprintln(os.Stdout, tui.AccessibleNotice)
		return runPlain(settings, *noInput)
	}
	if !huhstyle.Interactive() || *noInput {
		return tui.ErrNoTerminal
	}

	// --- full-screen path --------------------------------------------------------
	model := tui.New(tui.Options{ProjectRoot: root, Settings: settings, Interval: *refresh})
	defer model.Close()

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

// startService launches the repo's own CLI when the connection asked for it.
func startService(settings session.Settings, root string, out interface{ Write([]byte) (int, error) }) (*session.Server, error) {
	fmt.Fprintf(out, "Starting TokenWatch (dashboard port %d, proxy port %d)...\n",
		settings.DashboardPort, settings.ProxyPort)
	server, err := session.LaunchServer(context.Background(), root,
		settings.DashboardPort, settings.ProxyPort,
		session.ServerEnv(settings.OracleUser, session.PasswordFromEnv(), settings.OracleDSN))
	if err != nil {
		return nil, err
	}
	if err := session.WaitForPort(context.Background(), "127.0.0.1", settings.DashboardPort, 45*time.Second); err != nil {
		server.Stop()
		return nil, fmt.Errorf("%w (service log: %s)", err, strings.TrimSpace(server.Stderr()))
	}
	return server, nil
}

// runPlain drives the accessible path: one pass of plain prompts, then the
// chosen panel printed as text.
func runPlain(settings session.Settings, noInput bool) error {
	if noInput {
		return errors.New("-no-input cannot be combined with ACCESSIBLE plain prompts")
	}
	answers, err := tui.RunPlainPrompts(tui.PlainOptions{
		Settings: settings,
		Input:    os.Stdin,
		Output:   os.Stdout,
	})
	if err != nil {
		return err
	}
	if answers.BaseURL != "" {
		settings.BaseURL = strings.TrimRight(answers.BaseURL, "/")
	}
	if err := session.Save(settings); err != nil {
		fmt.Fprintln(os.Stderr, "note: "+err.Error())
	}
	return tui.RunAction(context.Background(), dashboard.NewClient(settings.BaseURL), tui.ActionRequest{
		Action:    answers.Action,
		Timeframe: answers.Timeframe,
		Limit:     20,
	}, os.Stdout)
}

// defaultProjectRoot finds the checkout so the service can be started from it.
// The binary may be built anywhere, so this walks up from the executable and
// falls back to the working directory.
func defaultProjectRoot() string {
	if wd, err := os.Getwd(); err == nil && looksLikeTokenWatch(wd) {
		return wd
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		for i := 0; i < 6; i++ {
			if looksLikeTokenWatch(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	wd, _ := os.Getwd()
	return wd
}

// looksLikeTokenWatch reports whether dir is the checkout by looking for the two
// things starting the service needs.
func looksLikeTokenWatch(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "tokenwatch", "cli.py")); err != nil {
		return false
	}
	return true
}
