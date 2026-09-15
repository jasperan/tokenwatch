package session

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// isolateConfig points the config directory at a temp dir so a test never touches
// the user's real settings.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// TestSettingsRoundTrip keeps a reconnect from losing the user's configuration.
func TestSettingsRoundTrip(t *testing.T) {
	isolateConfig(t)

	want := Settings{
		BaseURL:       "http://127.0.0.1:8878",
		ProxyURL:      "http://127.0.0.1:8877",
		LaunchServer:  true,
		DashboardPort: 9000,
		ProxyPort:     9001,
		OracleDSN:     "localhost:1521/FREEPDB1",
		OracleUser:    "tokenwatch",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// TestSavedSettingsArePrivateAndSecretFree asserts both halves of the promise:
// the file is 0600, and it cannot contain a credential.
func TestSavedSettingsArePrivateAndSecretFree(t *testing.T) {
	isolateConfig(t)
	t.Setenv(EnvOraclePassword, "hunter2-must-not-be-stored")

	settings := Defaults()
	settings.OracleUser = "tokenwatch"
	if err := Save(settings); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Error("the password from the environment was written to disk")
	}
	// The struct must have no field that could carry one.
	if strings.Contains(strings.ToLower(string(raw)), "password") {
		t.Errorf("the settings file mentions a credential field:\n%s", raw)
	}
}

// TestSaveRefusesACredentialShapedValue is the guard against a future edit
// silently starting to persist a secret.
func TestSaveRefusesACredentialShapedValue(t *testing.T) {
	isolateConfig(t)

	settings := Defaults()
	settings.OracleUser = "password=oops"
	if err := Save(settings); err == nil {
		t.Fatal("Save accepted a credential-shaped value")
	}
}

// TestLoadFallsBackToDefaultsOnAMissingFile keeps a first run working.
func TestLoadFallsBackToDefaultsOnAMissingFile(t *testing.T) {
	isolateConfig(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != Defaults() {
		t.Errorf("Load = %+v, want the defaults", got)
	}
}

// TestServerArgsCarryNoCredential is the security property that matters most:
// /proc/<pid>/cmdline is world-readable, so a secret in argv leaks to every user
// on the box regardless of file permissions.
func TestServerArgsCarryNoCredential(t *testing.T) {
	argv := ServerArgs(8878, 8877)

	joined := strings.Join(argv, " ")
	for _, forbidden := range []string{"password", "PASSWORD", "secret", "token=", "TOKEN", "oracle"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("argv mentions %q: %v", forbidden, argv)
		}
	}
	// And it must be the repo's own documented entry point.
	if argv[0] != "uv" || argv[1] != "run" || argv[2] != "tokenwatch" || argv[3] != "start" {
		t.Errorf("argv = %v, want `uv run tokenwatch start ...`", argv)
	}
	if !strings.Contains(joined, strconv.Itoa(8878)) || !strings.Contains(joined, strconv.Itoa(8877)) {
		t.Errorf("argv = %v, want both ports", argv)
	}
}

// TestServerEnvPassesCredentialsOnlyThroughTheEnvironment is the other half: the
// child still needs them.
func TestServerEnvPassesCredentialsOnlyThroughTheEnvironment(t *testing.T) {
	env := ServerEnv("tokenwatch", "top-secret", "localhost:1521/FREEPDB1")
	joined := strings.Join(env, "\n")

	for _, want := range []string{
		EnvOracleUser + "=tokenwatch",
		EnvOraclePassword + "=top-secret",
		EnvOracleDSN + "=localhost:1521/FREEPDB1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("environment is missing %q", want)
		}
	}
}

// TestServerEnvOmitsEmptyValues keeps an unset credential from being exported as
// an empty string, which the service would treat as a real (wrong) value.
func TestServerEnvOmitsEmptyValues(t *testing.T) {
	env := ServerEnv("", "", "")
	for _, entry := range env {
		if strings.HasPrefix(entry, EnvOraclePassword+"=") ||
			strings.HasPrefix(entry, EnvOracleUser+"=") ||
			strings.HasPrefix(entry, EnvOracleDSN+"=") {
			t.Errorf("an empty credential was exported: %q", entry)
		}
	}
}

// TestPasswordOnlyComesFromTheEnvironment pins the single supported channel.
func TestPasswordOnlyComesFromTheEnvironment(t *testing.T) {
	t.Setenv(EnvOraclePassword, "")
	if got := PasswordFromEnv(); got != "" {
		t.Errorf("PasswordFromEnv() = %q, want empty", got)
	}
	t.Setenv(EnvOraclePassword, "from-env")
	if got := PasswordFromEnv(); got != "from-env" {
		t.Errorf("PasswordFromEnv() = %q, want the environment value", got)
	}
}

// TestValidatePort covers the values huh could hand over as text.
func TestValidatePort(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"1", false},
		{"8878", false},
		{"65535", false},
		{"0", true},
		{"65536", true},
		{"-1", true},
		{"", true},
		{"eighty", true},
		{" 8878 ", false},
	}
	for _, c := range cases {
		if err := ValidatePort(c.raw); (err != nil) != c.wantErr {
			t.Errorf("ValidatePort(%q) error = %v, wantErr %v", c.raw, err, c.wantErr)
		}
	}
}

// TestParsePortFallsBack keeps a blank answer resolving rather than producing 0,
// which is what the accessible path relies on.
func TestParsePortFallsBack(t *testing.T) {
	if got := ParsePort("", 8878); got != 8878 {
		t.Errorf("ParsePort(blank) = %d, want the fallback", got)
	}
	if got := ParsePort("oops", 8878); got != 8878 {
		t.Errorf("ParsePort(oops) = %d, want the fallback", got)
	}
	if got := ParsePort("9000", 8878); got != 9000 {
		t.Errorf("ParsePort(9000) = %d, want 9000", got)
	}
}

// TestWaitForPortSucceedsOnAListeningSocket is what turns a cold start into a
// deterministic "starting" state instead of a connection error.
func TestWaitForPortSucceedsOnAListeningSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	if err := WaitForPort(context.Background(), "127.0.0.1", port, 5*time.Second); err != nil {
		t.Fatalf("WaitForPort: %v", err)
	}
}

// TestWaitForPortTimesOut keeps a service that never starts from hanging the UI.
func TestWaitForPortTimesOut(t *testing.T) {
	// Bind and close to get a port nothing is listening on.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	start := time.Now()
	err = WaitForPort(context.Background(), "127.0.0.1", port, 750*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPort returned nil for a closed port")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("WaitForPort took %s, want it to honour the timeout", elapsed)
	}
}

// TestWaitForPortHonoursCancellation keeps a cancelled context from polling on.
func TestWaitForPortHonoursCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForPort(ctx, "127.0.0.1", port, 10*time.Second); err == nil {
		t.Fatal("WaitForPort ignored a cancelled context")
	}
}

// TestStopIsSafeWithNothingRunning keeps Close() from panicking on a model that
// never spawned a service.
func TestStopIsSafeWithNothingRunning(t *testing.T) {
	var server *Server
	server.Stop() // must not panic

	empty := &Server{}
	empty.Stop() // must not panic
	if empty.Stderr() != "" {
		t.Error("Stderr on an unused Server returned text")
	}
	var nilServer *Server
	if nilServer.Stderr() != "" {
		t.Error("Stderr on a nil Server returned text")
	}
}

// TestKillProcessGroupWithoutAProcessIsSafe covers the branch the process-group
// helper takes when Start never succeeded.
func TestKillProcessGroupWithoutAProcessIsSafe(t *testing.T) {
	// A command that was never started has a nil Process.
	if err := killProcessGroup(exec.Command("true")); err != nil {
		t.Errorf("killProcessGroup on a command with no process = %v, want nil", err)
	}
}

// TestConfigPathIsUnderTheConfigDir documents where settings live, so a reviewer
// can find them without reading the code.
func TestConfigPathIsUnderTheConfigDir(t *testing.T) {
	isolateConfig(t)
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if filepath.Base(path) != "gotui.json" {
		t.Errorf("path = %q, want a gotui.json file", path)
	}
	if !strings.Contains(path, "tokenwatch") {
		t.Errorf("path = %q, want it namespaced under tokenwatch", path)
	}
}

// TestSavedFileIsValidJSON keeps the file readable by anything else.
func TestSavedFileIsValidJSON(t *testing.T) {
	isolateConfig(t)
	if err := Save(Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := ConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var decoded Settings
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("the saved file is not valid JSON: %v", err)
	}
}
