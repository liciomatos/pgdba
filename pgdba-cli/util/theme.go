package util

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

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

// SeverityColor colors text by severity level: 0=green, 1=yellow, 2=red.
func SeverityColor(text string, level int) string {
	colors := []lipgloss.Color{ColorGreen, ColorYellow, ColorRed}
	if level < 0 || level > 2 {
		level = 0
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
	// ~1 char padding per column separator, small extra buffer
	w := termWidth - fixed - len(cols) - 2
	if w < 20 {
		w = 20
	}
	out := make([]table.Column, len(cols))
	copy(out, cols)
	out[colIdx].Width = w
	return out
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
	s.Selected = s.Selected.
		Foreground(ColorWhite).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}
