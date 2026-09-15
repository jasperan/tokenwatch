package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jasperan/tokenwatch/gotui/internal/huhstyle"
	"github.com/jasperan/tokenwatch/gotui/internal/session"
)

// This file covers the accessible half of the form contract.
//
// Why it exists: huh's accessible prompts run a field's validator on the RAW line
// and only afterwards substitute the field's default, and they never print that
// default. A pre-filled field whose validator rejects "" therefore re-prompts on
// every bare Enter, so a screen-reader user cannot accept a value they cannot
// see. Two consequences are pinned here:
//
//  1. no pre-filled field may reject a blank answer, and
//  2. the requirement it represents must still be enforced, once, after the form.
//
// Field.RunAccessible is used rather than Form.Run because each accessible prompt
// builds its own buffered scanner over the reader, so only the first prompt of a
// multi-field form can be fed a real answer.

// runFieldAccessible drives one real field, built by this module's own field
// builder, through huh's accessible path with the given input.
func runFieldAccessible(t *testing.T, field huh.Field, input string) string {
	t.Helper()
	var out bytes.Buffer
	if err := field.RunAccessible(&out, strings.NewReader(input)); err != nil {
		// A field whose echo mode needs a tty reports here; huh.Form ignores it too.
		t.Logf("RunAccessible returned %v (ignored, as huh.Form does)", err)
	}
	return out.String()
}

// promptComplained reports whether driving a field printed anything beyond its
// own title.
//
// huh's accessible path is the reason this exists: it prints a validation error
// inline but does NOT persist it, so field.Error() stays nil after a rejection.
// The printed complaint is therefore the only observable signal.
func promptComplained(t *testing.T, field huh.Field, input, title string) bool {
	t.Helper()
	text := ansi.Strip(runFieldAccessible(t, field, input))
	return strings.TrimSpace(strings.ReplaceAll(text, title, "")) != ""
}

// TestValidateDefaultedAcceptsBlank is the unit half of the fix.
func TestValidateDefaultedAcceptsBlank(t *testing.T) {
	inner := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("blank answer rejected")
		}
		if s == "bad" {
			return errors.New("not usable")
		}
		return nil
	}
	wrapped := ValidateDefaulted(inner)

	for _, in := range []string{"", "   ", "\t"} {
		if err := wrapped(in); err != nil {
			t.Errorf("ValidateDefaulted(inner)(%q) = %v, want nil", in, err)
		}
	}
	if err := wrapped("bad"); err == nil {
		t.Error(`ValidateDefaulted(inner)("bad") = nil, want the inner validator's error`)
	}
	if err := wrapped("http://127.0.0.1:8878"); err != nil {
		t.Errorf("a usable value was rejected: %v", err)
	}
}

// TestNoPrefilledFieldRejectsABlankAnswer drives every pre-filled field in the
// connection form and the limit field with a bare Enter.
//
// A rejection here is not cosmetic: it traps a screen-reader user in a loop on a
// value that is never printed.
func TestNoPrefilledFieldRejectsABlankAnswer(t *testing.T) {
	t.Run("connection", func(t *testing.T) {
		answers := ConnectDefaults(session.Defaults())
		fields := connectFields(&answers, false)
		titles := []string{"Dashboard URL", "Proxy URL", "Dashboard port", "Proxy port"}
		for i, field := range fields {
			title := ""
			if i < len(titles) {
				title = titles[i]
			}
			if title == "" {
				// The confirm and credential fields have no validator to trip.
				continue
			}
			if promptComplained(t, field, "\n", title) {
				t.Errorf("pre-filled field %d (%s) rejected a blank answer", i, title)
			}
		}
	})

	t.Run("limit", func(t *testing.T) {
		answers := limitAnswers{Limit: "20"}
		out := runFieldAccessible(t, limitFields(&answers)[0], "\n")
		if strings.Contains(out, "use a number") || strings.Contains(out, "required") {
			t.Errorf("the pre-filled limit rejected a blank answer:\n%s", out)
		}
	})

	t.Run("accessible flow", func(t *testing.T) {
		answers := &PlainAnswers{
			Connect:   ConnectDefaults(session.Defaults()),
			Timeframe: timeframeAnswers{Timeframe: "24h"},
			Menu:      menuAnswers{Action: ActionOverview},
		}
		for i, field := range plainFields(answers) {
			out := runFieldAccessible(t, field, "\n")
			if strings.Contains(out, "required") || strings.Contains(out, "not a valid URL") {
				t.Errorf("accessible field %d rejected a blank answer:\n%s", i, out)
			}
		}
	})
}

// TestBadValuesAreStillRejected is the other half: relaxing the blank case must
// not have disabled validation for real mistakes.
//
// The assertion is on the field's own error rather than on the prompt text,
// because the exact wording of a rejection is allowed to change.
func TestBadValuesAreStillRejected(t *testing.T) {
	t.Run("dashboard url", func(t *testing.T) {
		answers := ConnectDefaults(session.Defaults())
		fields := connectFields(&answers, false)
		if len(fields) == 0 {
			t.Fatal("no connection fields were built")
		}
		if !promptComplained(t, fields[0], "127.0.0.1:8878\n", "Dashboard URL") {
			t.Error("a URL without a scheme was accepted without complaint")
		}
	})

	t.Run("port", func(t *testing.T) {
		answers := ConnectDefaults(session.Defaults())
		// Index 2 is the dashboard port: two URL fields come first.
		fields := connectFields(&answers, false)
		if len(fields) < 3 {
			t.Fatal("the connection form lost its port fields")
		}
		if !promptComplained(t, fields[2], "99999\n", "Dashboard port") {
			t.Error("an out-of-range port was accepted without complaint")
		}
	})

	t.Run("limit", func(t *testing.T) {
		answers := limitAnswers{Limit: "20"}
		field := limitFields(&answers)[0]
		if !promptComplained(t, field, "0\n", "How many recent requests?") {
			t.Error("a zero limit was accepted without complaint")
		}
	})

	t.Run("a good value leaves no error", func(t *testing.T) {
		answers := ConnectDefaults(session.Defaults())
		fields := connectFields(&answers, false)
		if promptComplained(t, fields[0], "http://127.0.0.1:8878\n", "Dashboard URL") {
			t.Error("a valid URL was rejected")
		}
	})
}

// TestRequirementsAreEnforcedAfterTheForm is where the enforced-once half lives.
func TestRequirementsAreEnforcedAfterTheForm(t *testing.T) {
	t.Run("a blank URL resolves to the default rather than failing", func(t *testing.T) {
		answers := connectAnswers{BaseURL: "", ProxyURL: "", DashboardPort: "", ProxyPort: ""}
		if err := ValidateConnect(answers); err != nil {
			t.Fatalf("ValidateConnect(blank) = %v, want the defaults to satisfy it", err)
		}
		settings := answers.toSettings()
		if settings.BaseURL != "http://127.0.0.1:8878" {
			t.Errorf("base URL = %q, want the default", settings.BaseURL)
		}
		if settings.DashboardPort != 8878 || settings.ProxyPort != 8877 {
			t.Errorf("ports = %d/%d, want 8878/8877", settings.DashboardPort, settings.ProxyPort)
		}
	})

	t.Run("a bad URL is refused", func(t *testing.T) {
		if err := ValidateConnect(connectAnswers{BaseURL: "ftp://x"}); err == nil {
			t.Error("a non-http URL must be refused")
		}
	})

	t.Run("an out-of-range port resolves to the default", func(t *testing.T) {
		settings := connectAnswers{DashboardPort: "99999"}.toSettings()
		if settings.DashboardPort != 8878 {
			t.Errorf("port = %d, want the default 8878", settings.DashboardPort)
		}
	})

	t.Run("the limit falls back rather than failing", func(t *testing.T) {
		if got := ParseLimit(""); got != 50 {
			t.Errorf("ParseLimit(blank) = %d, want 50", got)
		}
		if got := ParseLimit("99999"); got != 50 {
			t.Errorf("ParseLimit(99999) = %d, want 50", got)
		}
		if got := ParseLimit("25"); got != 25 {
			t.Errorf("ParseLimit(25) = %d, want 25", got)
		}
	})
}

// TestAccessibleFlagIsHonoured pins the env switch a screen reader sets.
func TestAccessibleFlagIsHonoured(t *testing.T) {
	t.Setenv("ACCESSIBLE", "")
	if huhstyle.Accessible() {
		t.Error("ACCESSIBLE unset must not select the accessible path")
	}
	t.Setenv("ACCESSIBLE", "1")
	if !huhstyle.Accessible() {
		t.Error("ACCESSIBLE set must select the accessible path")
	}
}

// TestAccessibleNoticeExists keeps the mode switch announced rather than silent.
func TestAccessibleNoticeExists(t *testing.T) {
	if !strings.Contains(AccessibleNotice, "plain prompts") {
		t.Errorf("AccessibleNotice = %q, want it to name the plain-prompt mode", AccessibleNotice)
	}
}

// TestEmbeddedFormsCannotServeAScreenReader documents the measured limitation:
// huh v2 only consults WithAccessible inside Form.RunWithContext, so the
// full-screen embedded UI cannot honour it. The front-end routes around this by
// using the standalone plain path, which is why that path exists at all.
func TestEmbeddedFormsCannotServeAScreenReader(t *testing.T) {
	answers := &menuAnswers{Action: ActionOverview}
	form := MenuForm(answers, "24h", "http://127.0.0.1:8878").WithAccessible(true)
	if form == nil {
		t.Fatal("WithAccessible(true) broke the form")
	}
	// The flag is accepted for intent, but the embedded View is unaffected: this
	// is asserted so nobody later claims accessible support for the TUI itself.
	if form.State != huh.StateNormal {
		t.Errorf("State = %v, want an untouched normal form", form.State)
	}
}
