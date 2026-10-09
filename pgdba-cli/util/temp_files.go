package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type TempFilesModel struct {
	table         table.Model
	usage         []TempFileUsage
	topQueries    []TempQuery
	topQueriesErr error // pg_stat_statements missing or unreadable
	initialModel  func() tea.Model
	height        int
	width         int
}

// tempTopQueryCount is how many statements the Top temp writers section lists.
const tempTopQueryCount = 5

// formatStatsReset renders a stats_reset timestamp, "never reset" when nil.
func formatStatsReset(reset *time.Time) string {
	if reset == nil {
		return "never reset"
	}
	return reset.Format("2006-01-02 15:04")
}

func CheckTempFiles(initialModel func() tea.Model) tea.Model {
	usage, err := FetchTempFileUsage(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading temp file usage", initialModel)
	}

	topQueries, topQueriesErr := FetchTopTempQueries(context.Background(), config.Config.DB, tempTopQueryCount)

	columns := []table.Column{
		{Title: "Database", Width: 25},
		{Title: "Temp Files", Width: 12},
		{Title: "Temp Size", Width: 15},
		{Title: "Avg / File", Width: 12},
		{Title: "Stats Reset", Width: 18},
	}

	var rowsData []table.Row
	for _, u := range usage {
		average := "-"
		if u.AvgBytesPerFile != nil {
			average = formatBytes(int64(*u.AvgBytesPerFile))
		}
		rowsData = append(rowsData, table.Row{
			u.Database,
			fmt.Sprintf("%d", u.TempFiles),
			u.TempPretty,
			average,
			formatStatsReset(u.StatsReset),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return TempFilesModel{
		table:         t,
		usage:         usage,
		topQueries:    topQueries,
		topQueriesErr: topQueriesErr,
		initialModel:  initialModel,
		height:        30,
		width:         120,
	}
}

func (m TempFilesModel) Init() tea.Cmd { return nil }

func (m TempFilesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		cols := StretchColumn(m.table.Columns(), 0, msg.Width)
		m.table.SetColumns(cols)
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckTempFiles(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m TempFilesModel) View() string {
	hint := "  Temp files are created when a query needs more memory than work_mem for sorts, hashes, or materializations — persistent growth here is a sign work_mem may be too low."
	s := RenderHeader("Temp File Usage") + "\n"
	s += HintStyle.Render(hint) + "\n\n"

	if len(m.usage) == 0 {
		s += FooterStyle.Render("No database activity recorded.") + "\n"
	} else {
		rules := []ColorRule{
			{Column: 1, Colorize: func(v string) int {
				if v == "0" {
					return -1
				}
				return 1
			}},
		}
		s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	}

	s += "\n" + m.topQueriesSection()
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • ? help • q back")
	return s
}

// topQueriesSection lists the statements that wrote the most temp data, one line each
// (cut to the terminal width so the frame height stays predictable).
func (m TempFilesModel) topQueriesSection() string {
	lineStyle := lipgloss.NewStyle()
	if m.width > 0 {
		lineStyle = lineStyle.MaxWidth(m.width)
	}
	s := HintStyle.Render("  Top temp writers (pg_stat_statements):") + "\n"
	switch {
	case m.topQueriesErr != nil:
		return s + lineStyle.Render("    "+m.topQueriesErr.Error()) + "\n"
	case len(m.topQueries) == 0:
		return s + HintStyle.Render("    no statement has written temp files") + "\n"
	}
	for _, query := range m.topQueries {
		line := fmt.Sprintf("    %9s  %6d calls  %s", formatBytes(query.TempBytesWritten), query.Calls,
			strings.Join(strings.Fields(query.Query), " "))
		s += lineStyle.Render(line) + "\n"
	}
	return s
}
