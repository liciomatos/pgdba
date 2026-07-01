package util

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripAnsi(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// RenderBar returns a fixed-width bar with a percentage label.
// barWidth controls the number of block characters; the full string is
// barWidth + 7 chars wide (bar + space + " xx.x%").
func RenderBar(pct float64, barWidth int) string {
	filled := int(pct / 100.0 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + fmt.Sprintf(" %5.1f%%", pct)
}

// FilterRows returns rows where any cell contains filter (case-insensitive, ANSI-stripped).
func FilterRows(rows []table.Row, filter string) []table.Row {
	if filter == "" {
		return rows
	}
	f := strings.ToLower(filter)
	var out []table.Row
	for _, row := range rows {
		for _, cell := range row {
			if strings.Contains(strings.ToLower(stripAnsi(cell)), f) {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// wrapText breaks s into lines of at most width visible characters at word boundaries.
func wrapText(s string, width int) string {
	var b strings.Builder
	col := 0
	for _, word := range strings.Fields(s) {
		if col > 0 {
			if col+1+len(word) > width {
				b.WriteByte('\n')
				col = 0
			} else {
				b.WriteByte(' ')
				col++
			}
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String()
}

// RenderQueryDetail renders a full-screen detail view for query text.
// text may contain manual newlines for multi-block content (e.g., blocked + blocking).
func RenderQueryDetail(screenName, text string, width int) string {
	w := width - 4
	if w < 40 {
		w = 40
	}
	wrapped := lipgloss.NewStyle().Foreground(ColorWhite).Width(w).Render(wrapText(text, w))
	return RenderHeader(screenName+" — Query Detail") + "\n" +
		wrapped + "\n\n" +
		FooterStyle.Render("esc / enter / q  close")
}

// FilterFooter returns the footer string based on current filter state.
func FilterFooter(filterMode bool, filterText, hints string) string {
	switch {
	case filterMode:
		return FooterStyle.Render(fmt.Sprintf("Filter: %s_  (enter confirm • esc clear)", filterText))
	case filterText != "":
		return FooterStyle.Render(fmt.Sprintf("filter:%q • / edit • esc clear • %s", filterText, hints))
	default:
		return FooterStyle.Render("/ filter • " + hints)
	}
}

var (
	ColorRed    = lipgloss.Color("9")
	ColorYellow = lipgloss.Color("11")
	ColorGreen  = lipgloss.Color("10")
	ColorBlue   = lipgloss.Color("39")
	ColorGray   = lipgloss.Color("240")
	ColorWhite  = lipgloss.Color("255")
)

// FooterStyle is the consistent faint style used for all screen footers.
var FooterStyle = lipgloss.NewStyle().Faint(true)

// HintStyle is used for contextual educational hints — readable but clearly secondary.
var HintStyle = lipgloss.NewStyle().Foreground(ColorGray)

// RenderHeader returns a styled two-line header: logo › screen name + connection info.
func RenderHeader(screenName string) string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("pgdba")
	sep := lipgloss.NewStyle().Foreground(ColorGray).Render(" › ")
	name := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render(screenName)
	conn := lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("%s@%s:%d/%s", config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName),
	)
	return fmt.Sprintf("%s%s%s\n%s\n", logo, sep, name, conn)
}

// SeverityColor colors text by severity level:
// 0=green (ok), 1=yellow (warn), 2=red (critical), 3=gray (muted).
// Level -1 or out of range returns plain text.
func SeverityColor(text string, level int) string {
	colors := []lipgloss.Color{ColorGreen, ColorYellow, ColorRed, ColorGray}
	if level < 0 || level >= len(colors) {
		return text
	}
	return lipgloss.NewStyle().Foreground(colors[level]).Render(text)
}

// TableHeight returns the number of rows the table viewport should show
// given the terminal height. 5 = 2 header lines + 1 blank + 1 blank + 1 footer.
func TableHeight(termHeight int) int {
	h := termHeight - 5
	if h < 5 {
		h = 5
	}
	return h
}

// StretchColumn returns a new column slice where the column at colIdx has its
// width expanded so that the total table fills termWidth.
func StretchColumn(cols []table.Column, colIdx, termWidth int) []table.Column {
	fixed := 0
	for i, c := range cols {
		if i != colIdx {
			fixed += c.Width
		}
	}
	// Bubbles applies Padding(0,1) to every cell: 1 char left + 1 char right = 2 per column.
	w := termWidth - fixed - len(cols)*2
	if w < 20 {
		w = 20
	}
	out := make([]table.Column, len(cols))
	copy(out, cols)
	out[colIdx].Width = w
	return out
}

// InfoTableStyles returns table styles without any row selection highlight.
// Use this for read-only summary tables that are not keyboard-navigable.
func InfoTableStyles() table.Styles {
	s := DefaultTableStyles()
	s.Selected = s.Selected.
		Background(lipgloss.NoColor{}).
		Foreground(lipgloss.NoColor{}).
		Bold(false)
	return s
}

// DefaultTableStyles returns styled table headers and selected-row highlight.
func DefaultTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ColorGray).
		BorderBottom(true).
		Bold(true).
		Foreground(ColorBlue)
	// Dark gray background, no explicit foreground: selection is clear without saturated
	// color competing with green/yellow/red status indicators in other rows. No foreground
	// override lets ColorizeTable inject severity colors with plain SeverityColor (which
	// closes with \x1b[0m) — terminal default fg on default bg remains readable after reset.
	s.Selected = s.Selected.
		Background(lipgloss.Color("237")).
		Bold(false)
	return s
}

// ColorRule specifies which column to colorize and how to derive the severity level.
type ColorRule struct {
	Column   int           // zero-based column index
	Colorize func(string) int // returns 0=green/1=yellow/2=red/3=gray, or -1 for no color
}

// ColorizeTable injects ANSI severity colors into a rendered Bubbles table view.
//
// Bubbles v0.20.0 uses runewidth.Truncate internally. runewidth miscounts ANSI
// escape sequences as visible characters, corrupting them in narrow columns. This
// function keeps plain text in cells and injects colors after the table renders, so
// runewidth never sees the sequences.
//
// For selected rows (starting with ANSI escapes from styles.Selected.Render) all
// consecutive leading escape sequences are measured and their total byte length is
// used as an offset so cell positions stay accurate. SeverityColor (which closes with
// \x1b[0m) is used for all rows; after the reset the terminal falls back to default
// fg/bg which is readable because DefaultTableStyles uses a dark background for
// selection with no explicit foreground override.
//
// Assumes DefaultTableStyles: 2 header lines (title + border separator).
func ColorizeTable(tableView string, cols []table.Column, rules []ColorRule) string {
	if len(rules) == 0 {
		return tableView
	}

	colorizers := make(map[int]func(string) int, len(rules))
	for _, rule := range rules {
		colorizers[rule.Column] = rule.Colorize
	}

	// Compute the byte offset of the value-start for each column.
	// Bubbles Cell style is Padding(0,1): 1 space left, Width chars, 1 space right.
	colValueStart := make([]int, len(cols))
	pos := 0
	for i, col := range cols {
		colValueStart[i] = pos + 1 // skip left-padding space
		pos += col.Width + 2
	}

	lines := strings.Split(tableView, "\n")
	const headerLines = 2 // title row + border row (BorderBottom in DefaultTableStyles)

	for lineIdx := headerLines; lineIdx < len(lines); lineIdx++ {
		line := lines[lineIdx]
		if len(line) == 0 {
			continue
		}

		// Consume all consecutive leading ANSI sequences (\x1b[...m) to find where
		// actual cell content starts. lipgloss may emit background and foreground as
		// separate sequences (e.g. \x1b[48;5;237m\x1b[1m), so we scan past all of them.
		ansiPrefix := 0
		for ansiPrefix < len(line) && line[ansiPrefix] == '\x1b' &&
			ansiPrefix+1 < len(line) && line[ansiPrefix+1] == '[' {
			rel := strings.IndexByte(line[ansiPrefix:], 'm')
			if rel < 0 {
				break
			}
			ansiPrefix += rel + 1
		}

		// Process columns right-to-left: injecting at a higher byte offset does not
		// shift the byte positions of columns to the left.
		for colIdx := len(cols) - 1; colIdx >= 0; colIdx-- {
			colorizer, ok := colorizers[colIdx]
			if !ok {
				continue
			}

			start := colValueStart[colIdx] + ansiPrefix
			if start >= len(line) {
				continue
			}
			end := start + cols[colIdx].Width
			if end > len(line) {
				end = len(line)
			}

			cellText := strings.TrimRight(line[start:end], " ")
			if cellText == "" {
				continue
			}

			level := colorizer(cellText)
			if level < 0 {
				continue
			}

			colored := SeverityColor(cellText, level)
			lines[lineIdx] = line[:start] + colored + line[start+len(cellText):]
			line = lines[lineIdx]
		}
	}

	return strings.Join(lines, "\n")
}
