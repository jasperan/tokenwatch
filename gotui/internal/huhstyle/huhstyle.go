// Package huhstyle adapts charmbracelet/huh forms to the TokenWatch palette:
// one graphite ramp with a single lime readout, the same tokens the web console
// declares in src/tokenwatch/dashboard/index.html (:root).
//
// The hex literals below are the only colours in the Go tree, and this file is
// the only place they are written down. The terminal front-end reads them
// through Colours/Palette rather than scavenging foregrounds back out of huh's
// compiled styles, which expose only a handful of them.
//
// A terminal has no page fill, so the ramp's card steps have no element here: a
// pane is a border and its text, not a filled card. What the ramp contributes is
// the three text roles, two border strengths and the accent. The stronger of the
// two is what draws a pane, because without a filled card the border is the only
// thing that says where the panel begins.
//
// huh's built-in ThemeCatppuccin() is NOT compliant: it paints with Catppuccin
// shades that are not tokens here, and Catppuccin is the default AI theme.
// Never use it; always pass Theme via WithTheme.
package huhstyle

import (
	"image/color"
	"os"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// Approved tokens. Do not add, rename or re-value: every one is either read by
// this package or returned by Colours, and an unused token is a dead one.
const (
	hexBG     = "#141715" // the ink on an accent fill
	hexRaised = "#222723" // the one step above the ground: a quiet control
	hexLine   = "#272a28" // hairline, i.e. rgba(232,237,228,.09) over hexBG
	hexEdge   = "#3a3e3a" // frame,    i.e. rgba(232,237,228,.18) over hexBG
	hexText   = "#e8ede4" // body
	hexMuted  = "#a2ab9e" // meta
	hexDim    = "#7d8679" // table heads, empty states
	hexAccent = "#bce58b" // the readout, and the healthy state
	hexSeries = "#7f8f7b" // the second series of a chart: neutral, not a hue
	hexDanger = "#e08d88" // failures
	hexWarn   = "#d9be86" // approaching a limit
)

var (
	colBG     = lipgloss.Color(hexBG)
	colRaised = lipgloss.Color(hexRaised)
	colLine   = lipgloss.Color(hexLine)
	colEdge   = lipgloss.Color(hexEdge)
	colText   = lipgloss.Color(hexText)
	colMuted  = lipgloss.Color(hexMuted)
	colDim    = lipgloss.Color(hexDim)
	colAccent = lipgloss.Color(hexAccent)
	colSeries = lipgloss.Color(hexSeries)
	colDanger = lipgloss.Color(hexDanger)
	colWarn   = lipgloss.Color(hexWarn)
)

// Colours are the roles the terminal front-end paints with.
//
// It exists so internal/tui and the forms share one definition of each token:
// a colour written in the TUI would instantly diverge from the forms the user is
// looking at.
type Colours struct {
	Raised  color.Color
	Line    color.Color
	Edge    color.Color
	Text    color.Color
	Muted   color.Color
	Dim     color.Color
	Accent  color.Color
	Series  color.Color
	Danger  color.Color
	Warning color.Color
}

// Palette returns the approved colours.
func Palette() Colours {
	return Colours{
		Raised:  colRaised,
		Line:    colLine,
		Edge:    colEdge,
		Text:    colText,
		Muted:   colMuted,
		Dim:     colDim,
		Accent:  colAccent,
		Series:  colSeries,
		Danger:  colDanger,
		Warning: colWarn,
	}
}

// Accessible reports whether the user requested screen-reader mode.
//
// Wire it into every form: huh then swaps the TUI for plain prompts, which is
// the only sane rendering when a screen reader is driving the terminal.
func Accessible() bool { return os.Getenv("ACCESSIBLE") != "" }

// Interactive reports whether stdin is a terminal, i.e. whether a form can
// actually be driven by a human.
//
// Callers must route around a prompt when this is false rather than into it:
// a huh form attached to a pipe or a cron job can never be answered.
//
// This must use term.IsTerminal, NOT an os.ModeCharDevice probe: /dev/null IS a
// character device, so a char-device check reports "interactive" for exactly the
// case it is supposed to reject, and a form would then hang forever on /dev/null.
func Interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// NOTE ON ACCESSIBILITY, measured against huh v2.0.3:
//
// `WithAccessible` is only consulted inside Form.RunWithContext (form.go:676).
// Form.Init/Update/View never read the flag, so an *embedded* form (driven as a
// Bubble Tea component) renders identically with and without it -- screen-reader
// support is real only for a form run standalone via Form.Run().
//
// Pass WithAccessible(huhstyle.Accessible()) anyway (it is the correct intent and
// costs nothing), but never claim accessible support for an embedded form. If a
// flow genuinely must be accessible, run that form standalone on the
// Accessible() path instead of embedding it.

// Styles builds token-compliant form styles.
//
// Design rules applied:
//  1. rounded borders everywhere
//  2. focus is a border *color* change, never a thickness change
//  3. semantic colours only where the content is genuinely semantic
//  4. selection is a ramp step plus the accent, not a second weight
//  5. titles are bold and text-coloured: the accent belongs to the value
//  6. one glyph vocabulary: `>`, `->`, `<-` and the ASCII the forms already
//     print. The only glyph this package cannot own is the ` • ` huh puts
//     between two key hints, which it reads from its own help model
func Styles() *huh.Styles {
	t := huh.ThemeBase(true)

	// Focused field: rounded box, accent border (rule 1 + 2).
	t.Focused.Base = lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent)
	t.Focused.Card = t.Focused.Base

	// Titles are the hierarchy, in the ramp's body colour (rule 5).
	t.Focused.Title = t.Focused.Title.Bold(true).Foreground(colText)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Bold(true).Foreground(colText)
	t.Focused.Description = t.Focused.Description.Foreground(colMuted)
	t.Focused.Directory = t.Focused.Directory.Foreground(colMuted)
	t.Focused.File = t.Focused.File.Foreground(colText)

	// Errors are the one place a semantic colour is mandatory (rule 3).
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(colDanger)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(colDanger)

	// Select: the current row is the accent's ink on an accent fill. The cursor
	// glyph is ASCII, and it is the only marker the list needs (rule 6).
	t.Focused.SelectSelector = t.Focused.SelectSelector.
		Foreground(colBG).
		Background(colAccent)
	// huh renders a select's rows through SelectedOption/UnselectedOption
	// (field_select.go:689,695), not Option, so both have to be set or the list
	// inherits the terminal's own foreground instead of the ramp.
	t.Focused.Option = t.Focused.Option.Foreground(colText)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colAccent)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(colText)
	t.Focused.NextIndicator = t.Focused.NextIndicator.SetString("->").Foreground(colDim)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.SetString("<-").Foreground(colDim)

	// Confirm buttons: the focused choice is the accent fill. A blurred confirm
	// must NOT keep it, or two fields would both read as focused and rule 2
	// (focus is a colour change) would be broken.
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(colBG).
		Background(colAccent)
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(colText).
		Background(colRaised)

	// Inputs.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colAccent)
	t.Focused.TextInput.CursorText = t.Focused.TextInput.CursorText.
		Foreground(colBG).
		Background(colAccent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(colDim)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colMuted)
	t.Focused.TextInput.Text = t.Focused.TextInput.Text.Foreground(colText)

	// Blurred fields keep their structure but lose the accent (rule 2).
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderForeground(colLine)
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.FocusedButton = t.Blurred.BlurredButton
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	// Help footer: keys readable, descriptions muted, ASCII separator.
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(colDim)
	t.Help.ShortKey = t.Help.ShortKey.Foreground(colMuted)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(colDim)
	// huh builds its own help model (group.go:52), so the separator STRING is
	// its " • " and cannot be changed here; only its colour can. Dim keeps it
	// quieter than the keys on either side.
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(colDim)
	t.Help.FullKey = t.Help.FullKey.Foreground(colMuted)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(colDim)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(colLine)

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}

// Theme adapts Styles to huh's Theme interface so it can be passed to
// Form.WithTheme. The isDark argument is ignored: this project ships a single
// dark palette by design.
func Theme(bool) *huh.Styles { return Styles() }
