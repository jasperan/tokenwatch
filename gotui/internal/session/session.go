// Package session holds the non-secret connection settings for the Go
// front-end and the one mechanism used to hand Oracle credentials to the Python
// service: environment variables.
//
// Why environment and never argv: a command line is world-readable through
// /proc/<pid>/cmdline and is recorded in shell history. TokenWatch's own
// config.py reads TOKENWATCH_ORACLE_* from the environment, so this is the
// codebase's supported channel rather than an invention.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Credential and port environment variables, matched to config.py.
const (
	EnvOracleDSN      = "TOKENWATCH_ORACLE_DSN"
	EnvOracleUser     = "TOKENWATCH_ORACLE_USER"
	EnvOraclePassword = "TOKENWATCH_ORACLE_PASSWORD"

	// DefaultProxyPort and DefaultDashboardPort mirror config.py's
	// TOKENWATCH_PROXY_PORT / TOKENWATCH_DASHBOARD_PORT defaults.
	DefaultProxyPort     = 8877
	DefaultDashboardPort = 8878
)

// Settings are the persisted, NON-SECRET connection settings. There is
// deliberately no password field: see the package comment.
type Settings struct {
	// BaseURL is the dashboard root the Go front-end reads from.
	BaseURL string `json:"base_url"`
	// ProxyURL is shown for reference: TokenWatch's whole purpose is that apps
	// point at the proxy, and an operator wants that address on screen.
	ProxyURL      string `json:"proxy_url"`
	LaunchServer  bool   `json:"launch_server"`
	DashboardPort int    `json:"dashboard_port"`
	ProxyPort     int    `json:"proxy_port"`
	OracleDSN     string `json:"oracle_dsn"`
	OracleUser    string `json:"oracle_user"`
}

// Defaults returns the settings a first run should start from.
func Defaults() Settings {
	return Settings{
		BaseURL:       "http://127.0.0.1:8878",
		ProxyURL:      "http://127.0.0.1:8877",
		LaunchServer:  false,
		DashboardPort: DefaultDashboardPort,
		ProxyPort:     DefaultProxyPort,
		OracleDSN:     "localhost:1521/FREEPDB1",
		OracleUser:    "",
	}
}

// ConfigPath is where Settings live. The file is written 0600 and holds no
// secret, so a leaked copy reveals only URLs.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "tokenwatch", "gotui.json"), nil
}

// Load reads Settings, falling back to Defaults when there is no file yet.
func Load() (Settings, error) {
	path, err := ConfigPath()
	if err != nil {
		return Defaults(), err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return Defaults(), fmt.Errorf("read %s: %w", path, err)
	}
	settings := Defaults()
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Defaults(), fmt.Errorf("parse %s: %w", path, err)
	}
	return settings, nil
}

// Save writes Settings with 0600 permissions. It refuses to persist anything
// that looks like a credential, so a future edit cannot silently start writing
// a secret to disk.
func Save(settings Settings) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	blob, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if strings.Contains(strings.ToLower(string(blob)), "password") {
		return errors.New("refusing to persist a settings object containing a credential field")
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// PasswordFromEnv returns the Oracle password from the environment, if any, so
// a user who exports TOKENWATCH_ORACLE_PASSWORD never has to type it.
func PasswordFromEnv() string { return os.Getenv(EnvOraclePassword) }

// ValidatePort rejects a port huh could have accepted as text.
func ValidatePort(raw string) error {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("use a number between 1 and 65535")
	}
	if value < 1 || value > 65535 {
		return errors.New("use a number between 1 and 65535")
	}
	return nil
}

// ParsePort converts a validated port string, falling back to fallback.
func ParsePort(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 || value > 65535 {
		return fallback
	}
	return value
}

// ServerEnv builds the environment for the spawned Python service.
//
// Credentials go into the child's environment only: never an argument, never on
// disk, never logged.
func ServerEnv(oracleUser, oraclePassword, oracleDSN string) []string {
	env := os.Environ()
	if oracleUser != "" {
		env = append(env, EnvOracleUser+"="+oracleUser)
	}
	if oraclePassword != "" {
		env = append(env, EnvOraclePassword+"="+oraclePassword)
	}
	if oracleDSN != "" {
		env = append(env, EnvOracleDSN+"="+oracleDSN)
	}
	return env
}

// Server is a spawned TokenWatch process.
type Server struct {
	cmd    *exec.Cmd
	stderr strings.Builder
}

// Stderr returns whatever the service logged, for error reporting.
func (s *Server) Stderr() string {
	if s == nil {
		return ""
	}
	return s.stderr.String()
}

// Stop terminates the spawned service and everything it spawned.
//
// Killing only the direct child is not enough: the CLI is reached through
// `uv run tokenwatch ...`, and uvicorn is started inside that process. A
// surviving descendant would keep the pipes open and keep holding the Oracle
// pool, so the whole process group is signalled.
func (s *Server) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = killProcessGroup(s.cmd)
	_, _ = s.cmd.Process.Wait()
}

// ServerArgs is the exact argv used to start the service.
//
// Exported so a test can assert that no credential can ever appear here:
// /proc/<pid>/cmdline is world-readable, so a secret in argv would leak to
// every user on the box regardless of file permissions.
func ServerArgs(dashboardPort, proxyPort int) []string {
	return []string{
		"uv", "run", "tokenwatch", "start",
		"--host", "127.0.0.1",
		"--dashboard-port", strconv.Itoa(dashboardPort),
		"--proxy-port", strconv.Itoa(proxyPort),
	}
}

// LaunchServer starts the repo's own CLI as the service, using the interface
// the repo already documents (`tokenwatch start`) rather than reimplementing it.
func LaunchServer(ctx context.Context, projectRoot string, dashboardPort, proxyPort int, env []string) (*Server, error) {
	argv := ServerArgs(dashboardPort, proxyPort)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = projectRoot
	cmd.Env = env

	// Put the child in its own process group so Stop can take down uvicorn and
	// any worker it started, not just the `uv` wrapper.
	setProcessGroup(cmd)

	server := &Server{cmd: cmd}
	cmd.Stdout = discard{}
	cmd.Stderr = &server.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tokenwatch: %w", err)
	}
	return server, nil
}

// discard swallows the service's stdout; the TUI owns the screen.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// WaitForPort polls until the service accepts TCP connections, so the UI shows
// a deterministic "starting" state instead of a connection error.
func WaitForPort(ctx context.Context, host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	address := net.JoinHostPort(host, strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service did not accept connections on %s within %s", address, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
