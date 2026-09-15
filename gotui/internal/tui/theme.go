package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jasperan/tokenwatch/gotui/internal/huhstyle"
)

// minPaneWidth is the narrowest a panel's CONTENT area is allowed to render at.
//
// A terminal narrower than this is common in a split pane, and rendering below it
// produced borders narrower than their own title. Clamping is what keeps a narrow
// window from panicking on a negative repeat count.
const minPaneWidth = 20

// paneChrome is the columns a pane spends on its own frame: a 2-column border
// plus 2 columns of padding on each side.
//
// It exists because lipgloss Width(w) sets the block's TOTAL width, frame
// included, so its content area is only w - paneChrome. Measured, not assumed:
// lipgloss.NewStyle().Border(RoundedBorder()).Padding(1, 2).Width(w) renders
// exactly w columns, with w - 6 available for text.
const paneChrome = 6

// minPaneTotal is the narrowest a pane's TOTAL width may be, so its content area
// never drops below minPaneWidth.
const minPaneTotal = minPaneWidth + paneChrome

// innerWidth converts the total columns a pane occupies into the content width
// available inside its frame.
//
// The floor here is a DESIGN minimum: columns are sized against it so a bar or a
// table still has room to be laid out. It is deliberately not used to size the
// pager, because a pager clamped wider than its terminal overflows it.
func innerWidth(total int) int {
	inner := total - paneChrome
	if inner < minPaneWidth {
		inner = minPaneWidth
	}
	return inner
}

// viewportWidth is the pager's width: the terminal minus the pane's frame, and
// never wider than the terminal itself.
func viewportWidth(total int) int {
	width := total - paneChrome
	if width < 1 {
		width = 1
	}
	return width
}

// palette derives every colour from the theme, never declaring a literal here.
//
// Reading the compiled styles back out of internal/huhstyle keeps one definition
// of each token: a colour written in this file would instantly diverge from the
// forms the user is looking at, and would fail
// scripts/tui-shot/check_palette.py.
//
// Only nine tokens are reachable through huh.Styles(), which is what the canonical
// huhstyle exposes. There is no accessor for the warm success/warning shades, so
// this front-end never invents one: emphasis is carried by these tokens and the
// exact meaning is always spelled out in the label text next to the value.
func palette() (primary, text, subtext, muted, danger, success lipgloss.Style) {
	styles := huhstyle.Styles()
	return lipgloss.NewStyle().Foreground(styles.Focused.Title.GetForeground()),
		lipgloss.NewStyle().Foreground(styles.Focused.Option.GetForeground()),
		lipgloss.NewStyle().Foreground(styles.Focused.Description.GetForeground()),
		lipgloss.NewStyle().Foreground(styles.Focused.TextInput.Placeholder.GetForeground()),
		lipgloss.NewStyle().Foreground(styles.Focused.ErrorMessage.GetForeground()),
		lipgloss.NewStyle().Foreground(styles.Focused.SelectedOption.GetForeground())
}

// Pane renders a titled panel: rounded border, generous padding, primary title.
//
// Below minPaneTotal the frame is dropped entirely and the content is wrapped
// instead. A border needs 6 columns of chrome, so at that width a framed pane
// would either leave no room for text or overflow the terminal and wrap -- the
// text is what matters, so the decoration goes.
func Pane(title, body string, width int) string {
	primary, text, _, _, _, _ := palette()

	if width < minPaneTotal {
		if width < 1 {
			width = 1
		}
		heading := ""
		if title != "" {
			heading = primary.Bold(true).Render(title) + "\n"
		}
		return lipgloss.NewStyle().Width(width).Render(heading + text.Render(body))
	}

	heading := ""
	if title != "" {
		heading = primary.Bold(true).Render(title) + "\n"
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(width).
		Render(heading + text.Render(body))
}

// PaneError renders a failure panel. A failed API call is a normal state in this
// front-end, not a crash: the user gets the cause and a way onward.
func PaneError(title string, err error, width int) string {
	_, _, _, _, danger, _ := palette()
	message := "unknown error"
	if err != nil {
		message = err.Error()
	}
	return Pane(title, danger.Render(message), width)
}

// Fog renders low-emphasis text.
func Fog(body string) string {
	_, _, subtext, _, _, _ := palette()
	return subtext.Render(body)
}

// Subtle renders metadata text.
func Subtle(body string) string {
	_, _, _, muted, _, _ := palette()
	return muted.Render(body)
}

// Good renders a healthy/within-budget value.
func Good(body string) string {
	_, _, _, _, _, success := palette()
	return success.Render(body)
}

// Bad renders a failing/over-budget value.
func Bad(body string) string {
	_, _, _, _, danger, _ := palette()
	return danger.Render(body)
}

// Stat renders one "label: value" line.
func Stat(label, value string) string {
	_, text, subtext, _, _, _ := palette()
	return subtext.Render(label+": ") + text.Render(value)
}

// StatRow renders label/value pairs across one or more lines, so a set of
// headline numbers stays readable when the terminal is narrow.
func StatRow(pairs [][2]string, width int) string {
	if len(pairs) == 0 {
		return ""
	}
	var out strings.Builder
	for i, pair := range pairs {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(Stat(pair[0], pair[1]))
	}
	return out.String()
}

// Bar renders a proportional bar of the given inner width.
//
// Bars are the point of this front-end: spend and traffic are ratios, and a
// ratio is far easier to read as a length than as a number.
func Bar(fraction float64, width int) string {
	if width < 1 {
		width = 1
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	if fraction > 0 && filled == 0 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	_, _, _, _, _, success := palette()
	return success.Render(strings.Repeat("=", filled)) +
		Subtle(strings.Repeat(".", width-filled))
}

// Money renders a cost with four decimal places, the unit the Python CLI uses.
func Money(value float64) string { return strconv.FormatFloat(value, 'f', 4, 64) }

// Count renders a count without a spurious decimal point.
//
// Aggregate endpoints are served straight from Oracle, where SUM()/COUNT() are
// NUMBER columns encoded as JSON numbers; they are decoded as float64 so both
// "120" and "120.0" parse, so the integer form is restored for display here.
func Count(value float64) string { return strconv.FormatFloat(value, 'f', 0, 64) }

// TrimFloat renders a measurement with up to two decimals, dropping trailing zeros.
func TrimFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// Header is the persistent identity bar, showing the proxy address because that
// is the address an operator points their apps at.
//
// It degrades in two steps rather than overflowing: first the proxy address is
// dropped, then the title itself is truncated. An identity bar that wraps pushes
// the whole layout down by a line.
func Header(baseURL, proxyURL string, width int) string {
	primary, _, subtext, _, _, _ := palette()
	if width < 1 {
		width = 1
	}
	title := primary.Bold(true).Render("TokenWatch") + " " + subtext.Render("Go front-end")
	if lipgloss.Width(title) > width {
		return Truncate(title, width)
	}

	right := subtext.Render("proxy " + proxyURL)
	// The gap is at least 2 columns, so both halves need to fit with room spare.
	if lipgloss.Width(title)+lipgloss.Width(right)+2 > width {
		return title
	}
	_ = baseURL
	gap := width - lipgloss.Width(title) - lipgloss.Width(right) - 1
	return title + strings.Repeat(" ", gap) + right
}

// Footer is the key-hint bar.
func Footer(hint string, width int) string {
	_, _, subtext, _, _, _ := palette()
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(hint) > width {
		hint = Truncate(hint, width)
	}
	return subtext.Render(hint)
}

// Pad right-pads to n columns, ignoring any styling already in s.
func Pad(s string, n int) string {
	width := lipgloss.Width(s)
	if width >= n {
		return s
	}
	return s + strings.Repeat(" ", n-width)
}

// Truncate shortens s to n columns, tail included.
//
// It goes through x/ansi rather than counting runes, because a cell may already
// carry a colour: counting runes would measure the escape sequence itself and
// cut the text mid-escape, which renders as a fragment like "[38;2;~" instead of
// a truncated value.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "~")
}

// barColumn sizes the bar column of a table so the whole row fits the pane's
// content width.
//
// The other columns and the two-space gap between every pair are subtracted. A
// row even one column too wide wraps, which pushes the bar onto its own line and
// makes the panel unreadable -- so this is measured, not guessed.
func barColumn(total int, fixed []int) int {
	used := 0
	for _, width := range fixed {
		used += width
	}
	// len(fixed) gaps: one between each pair including the one before the bar.
	used += 2 * len(fixed)
	width := innerWidth(total) - used
	if width < 4 {
		width = 4
	}
	return width
}

// barColumnFlex sizes each of several bar columns that share one table, so the
// row still fits.
//
// fixedWidths are the non-bar columns, gaps the number of two-space separators
// in the whole row, and bars how many bar columns are sharing what is left.
func barColumnFlex(total int, fixedWidths []int, gaps, bars int) int {
	used := 0
	for _, width := range fixedWidths {
		used += width
	}
	used += 2 * gaps
	width := (innerWidth(total) - used) / bars
	if width < 4 {
		width = 4
	}
	return width
}

// Table renders aligned columns. Every cell is padded to its column width so a
// narrow terminal truncates rather than wrapping mid-row.
//
// available is the content width the table must fit inside. The requested widths
// are scaled down to meet it, because a row even one column too wide wraps at its
// spaces -- which splits a value from its own label and is far harder to read
// than a shortened name.
func Table(headers []string, rows [][]string, widths []int, available int) string {
	_, text, _, muted, _, _ := palette()
	widths = fitWidths(available, widths)

	var head strings.Builder
	for i, header := range headers {
		head.WriteString(Pad(Truncate(header, widths[i]), widths[i]))
		if i < len(headers)-1 {
			head.WriteString("  ")
		}
	}

	var out strings.Builder
	out.WriteString(muted.Render(head.String()))
	for _, row := range rows {
		out.WriteString("\n")
		for i := range headers {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			out.WriteString(text.Render(Pad(Truncate(cell, widths[i]), widths[i])))
			if i < len(headers)-1 {
				out.WriteString("  ")
			}
		}
	}
	return out.String()
}

// minColumnWidth is the narrowest a column may be squeezed to. Below it a numeric
// column shows nothing useful, so a terminal that narrow is better served by the
// viewport clipping the table than by every column being unreadable.
const minColumnWidth = 3

// fitWidths scales widths down so the whole row, gaps included, fits available.
//
// Requested widths are honoured exactly whenever they already fit, so a wide
// terminal is unaffected.
func fitWidths(available int, widths []int) []int {
	if len(widths) == 0 {
		return widths
	}
	gaps := 2 * (len(widths) - 1)
	total := gaps
	for _, width := range widths {
		total += width
	}
	if total <= available {
		return widths
	}

	// Floor the budget so a very narrow terminal still gets a stable layout; the
	// viewport clips the remainder rather than wrapping.
	budget := available - gaps
	if minimum := len(widths) * minColumnWidth; budget < minimum {
		budget = minimum
	}

	scaled := make([]int, len(widths))
	remaining := budget
	for i, width := range widths {
		if i == len(widths)-1 {
			// The last column absorbs the rounding remainder.
			scaled[i] = remaining
			break
		}
		share := width * budget / total
		if share < minColumnWidth {
			share = minColumnWidth
		}
		if share > remaining {
			share = remaining
		}
		scaled[i] = share
		remaining -= share
	}
	if scaled[len(scaled)-1] < minColumnWidth {
		scaled[len(scaled)-1] = minColumnWidth
	}
	return scaled
}
