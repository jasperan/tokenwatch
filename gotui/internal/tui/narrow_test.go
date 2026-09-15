package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// allPanels is every read-only panel, so width sweeps cannot miss one.
var allPanels = []Panel{
	PanelOverview, PanelTraffic, PanelTimeseries, PanelCostTag, PanelCostApp,
	PanelCostSession, PanelForecast, PanelCache, PanelBudget, PanelRouting,
	PanelAB, PanelUpstreams,
}

// renderAt drives a real resize, loads the panel, and returns the rendered lines.
func renderAt(t *testing.T, panel Panel, width, height int) []string {
	t.Helper()
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(*Model)
	m = drain(m, m.openPanel(panel))
	if panel == PanelAB {
		m.ab.TestName = "latency"
		m = drain(m, m.openABPanel())
	}
	return strings.Split(m.View().Content, "\n")
}

// TestNoPanelPanicsAtAnyWidth sweeps 0..120 columns.
//
// A negative content width used to panic on a negative repeat count, so the
// degenerate widths are the point of this test rather than an afterthought. It
// also covers the form screens, which size themselves separately from the pager.
func TestNoPanelPanicsAtAnyWidth(t *testing.T) {
	for width := 0; width <= 120; width++ {
		for _, panel := range allPanels {
			lines := renderAt(t, panel, width, 24)
			if len(lines) == 0 {
				t.Fatalf("width %d panel %v rendered nothing", width, panel)
			}
		}
		for _, screen := range []Screen{ScreenConnect, ScreenMenu, ScreenTimeframe, ScreenLimit, ScreenCost, ScreenAB} {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = updated.(*Model)
			m.screen = screen
			m.form = MenuForm(&m.menu, "24h", m.client.BaseURL())
			if got := m.View().Content; got == "" {
				t.Fatalf("width %d screen %v rendered nothing", width, screen)
			}
		}
	}
}

// TestRenderedLinesNeverExceedTheTerminal is the guard for the wrapping bug: a
// table row that is one column too wide wraps inside its pane, which pushes the
// bar onto its own line and makes the whole panel unreadable.
func TestRenderedLinesNeverExceedTheTerminal(t *testing.T) {
	for width := 1; width <= 120; width++ {
		for _, panel := range allPanels {
			for _, line := range renderAt(t, panel, width, 24) {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("width %d panel %v: a line is %d columns wide (%q)",
						width, panel, got, ansi.Strip(line))
				}
			}
		}
	}
}

// TestPaneDropsItsFrameWhenTooNarrow documents the degradation: below the width
// a border needs, the frame is dropped so the text still fits the terminal.
func TestPaneDropsItsFrameWhenTooNarrow(t *testing.T) {
	for _, width := range []int{1, 5, 10, 19, minPaneTotal - 1} {
		rendered := Pane("T", "body text", width)
		if got := widestLine(rendered); got > width {
			t.Errorf("Pane(width %d) rendered %d columns, want no more than %d", width, got, width)
		}
		if strings.ContainsAny(rendered, "╭╰│") {
			t.Errorf("Pane(width %d) still drew a frame", width)
		}
	}
	// At and above the minimum the frame is present and exactly the requested
	// total width, frame included.
	for _, width := range []int{minPaneTotal, 40, 114} {
		rendered := Pane("T", "body", width)
		if got := widestLine(rendered); got != width {
			t.Errorf("Pane(width %d) rendered %d columns, want exactly %d", width, got, width)
		}
	}
}

// TestTableTruncatesRatherThanWrapping pins the column behaviour: a long cell is
// cut to its column width, so a row stays one line.
func TestTableTruncatesRatherThanWrapping(t *testing.T) {
	table := Table(
		[]string{"a", "b"},
		[][]string{{strings.Repeat("x", 100), strings.Repeat("y", 100)}},
		[]int{10, 10},
		22,
	)
	lines := strings.Split(table, "\n")
	if len(lines) != 2 {
		t.Fatalf("table rendered %d lines, want 2 (one header, one row)", len(lines))
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got > 22 {
			t.Errorf("a table line is %d columns wide, want at most 22", got)
		}
	}
}

// TestStyledCellsTruncateWithoutBreakingTheEscape is the regression guard for a
// goal-styled cell being cut mid-sequence, which rendered as a fragment like
// "[38;2;~" instead of the value.
func TestStyledCellsTruncateWithoutBreakingTheEscape(t *testing.T) {
	styled := Bad("125.00% over budget and then some")
	cut := Truncate(styled, 8)
	if strings.Contains(ansi.Strip(cut), "[38;2;") {
		t.Errorf("truncation left an escape fragment: %q", ansi.Strip(cut))
	}
	if got := ansi.StringWidth(cut); got > 8 {
		t.Errorf("truncated width = %d, want at most 8", got)
	}
	if !strings.HasPrefix(ansi.Strip(cut), "125.00%") {
		t.Errorf("truncated text = %q, want it to start with the value", ansi.Strip(cut))
	}
}

// TestBarColumnFillsTheRowExactly is what keeps every table on one line: the bar
// takes whatever the other columns and their gaps leave.
func TestBarColumnFillsTheRowExactly(t *testing.T) {
	cases := []struct {
		total int
		fixed []int
	}{
		{120, []int{30, 7, 9, 9, 12}},
		{120, []int{19, 26, 16, 14, 7, 7, 9, 5, 5, 10}},
		{120, []int{22, 7, 7, 12, 12, 8, 10, 4}},
		{80, []int{30, 7, 9, 9, 12}},
		{100, []int{24, 7, 12, 12, 9, 9}},
	}
	for _, c := range cases {
		bar := barColumn(c.total, c.fixed)
		used := bar + 2*len(c.fixed)
		for _, width := range c.fixed {
			used += width
		}
		// The bar is floored at 4, so a very narrow terminal may exceed; above
		// that the row must land exactly on the pane's content width.
		if bar > 4 && used != innerWidth(c.total) {
			t.Errorf("total %d fixed %v: row sums to %d, want %d", c.total, c.fixed, used, innerWidth(c.total))
		}
	}
}

// TestTrafficColumnsFitInsideThePane is the concrete case that was broken: the
// widest table in the front-end must still fit a normal terminal.
func TestTrafficColumnsFitInsideThePane(t *testing.T) {
	widths := []int{19, 26, 14, 6, 6, 8, 4, 5, 9}
	total := 2 * (len(widths) - 1)
	for _, width := range widths {
		total += width
	}
	if total > innerWidth(120) {
		t.Errorf("the traffic table needs %d columns, more than the %d available at 120", total, innerWidth(120))
	}
}

// widestLine returns the widest rendered line in columns, ignoring styling.
func widestLine(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if width := ansi.StringWidth(line); width > max {
			max = width
		}
	}
	return max
}

// TestPanelContentFitsThePager is the regression guard for a pane sized to the
// terminal while it is drawn inside the narrower pager.
//
// lipgloss Width(w) is the block's TOTAL width, so a pane rendered at the
// terminal width overflows the pager by the frame's chrome and wraps -- which is
// exactly how a model's row ended up split from its own bar chart.
func TestPanelContentFitsThePager(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 120, 160} {
		m := newTestModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = updated.(*Model)
		m = drain(m, m.openPanel(PanelOverview))

		pager := viewportWidth(m.width)
		if got := m.panelWidth(); got != pager {
			t.Errorf("width %d: panelWidth() = %d, want the pager width %d", width, got, pager)
		}
		for i, line := range strings.Split(m.renderPanel(), "\n") {
			if got := ansi.StringWidth(line); got > pager {
				t.Errorf("width %d: panel line %d is %d columns, more than the pager's %d: %q",
					width, i, got, pager, ansi.Strip(line))
			}
		}
	}
}

// TestBarsAreSizedForThePaneNotTheTerminal pins the arithmetic directly, since
// this is the mistake that produced the wrapped bar.
func TestBarsAreSizedForThePaneNotTheTerminal(t *testing.T) {
	const width = 120
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = updated.(*Model)

	paneBar := barColumn(m.panelWidth(), []int{30, 7, 9, 9, 12})
	terminalBar := barColumn(width, []int{30, 7, 9, 9, 12})
	if paneBar == terminalBar {
		t.Fatalf("the two widths agreed (%d); the test cannot detect the regression", paneBar)
	}
	row := 0
	for _, w := range []int{30, 7, 9, 9, 12} {
		row += w
	}
	row += paneBar + 2*5
	if row > innerWidth(m.panelWidth()) {
		t.Errorf("the row is %d columns but the pane content is %d", row, innerWidth(m.panelWidth()))
	}
}
