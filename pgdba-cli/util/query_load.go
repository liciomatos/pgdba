package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type QueryLoadModel struct {
	table        table.Model
	allRows      []table.Row
	queryDetails map[string]string // queryID → full query text
	filterText   string
	filterMode   bool
	detailMode   bool
	detailText   string
	initialModel func() tea.Model
	width        int
	height       int
}

func (m QueryLoadModel) IsInputMode() bool { return m.filterMode }

// CheckQueryLoad loads the top 20 queries by total execution time from
// pg_stat_statements, computes each query's share of total instance load,
// and includes buffer and temp disk usage as resource proxies.
func CheckQueryLoad(initialModel func() tea.Model) tea.Model {
	queries, err := FetchQueryLoad(context.Background(), config.Config.DB, 20)
	if err != nil {
		return NewErrorModel(err, "Loading query load (pg_stat_statements required)", initialModel)
	}

	// Load bar is the last column so its multi-byte block chars (█ ░) don't
	// shift the byte positions that ColorizeTable uses for earlier columns.
	columns := []table.Column{
		{Title: "Query ID", Width: 12},
		{Title: "Query", Width: 36},
		{Title: "Calls", Width: 7},
		{Title: "Total (ms)", Width: 12},
		{Title: "Avg (ms)", Width: 9},
		{Title: "Buffer (MB)", Width: 11},
		{Title: "Temp (MB)", Width: 9},
		{Title: "Load", Width: 20},
	}

	var rowsData []table.Row
	details := make(map[string]string)

	for _, ql := range queries {
		details[ql.QueryID] = ql.Query

		displayQuery := strings.ReplaceAll(ql.Query, "\n", " ")
		if len(displayQuery) > 36 {
			displayQuery = displayQuery[:36] + "..."
		}

		rowsData = append(rowsData, table.Row{
			ql.QueryID,
			displayQuery,
			fmt.Sprintf("%d", ql.Calls),
			fmt.Sprintf("%.0f", ql.TotalMS),
			fmt.Sprintf("%.2f", ql.MeanMS),
			fmt.Sprintf("%.1f", ql.BufferMB),
			fmt.Sprintf("%.1f", ql.TempMB),
			RenderBar(ql.LoadPct, 12),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return QueryLoadModel{
		table:        t,
		allRows:      rowsData,
		queryDetails: details,
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func (m QueryLoadModel) Init() tea.Cmd { return nil }

func (m QueryLoadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := StretchColumn(m.table.Columns(), 1, msg.Width)
		m.table.SetColumns(cols)
		m.table.SetHeight(TableHeight(msg.Height))
		return m, nil
	case tea.KeyMsg:
		if m.detailMode {
			switch msg.String() {
			case "q", "esc", "enter":
				m.detailMode = false
			}
			return m, nil
		}
		if m.filterMode {
			switch msg.Type {
			case tea.KeyEsc:
				m.filterMode = false
				m.filterText = ""
				m.table.SetRows(m.allRows)
			case tea.KeyBackspace:
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.table.SetRows(FilterRows(m.allRows, m.filterText))
				}
			case tea.KeyRunes:
				m.filterText += msg.String()
				m.table.SetRows(FilterRows(m.allRows, m.filterText))
			case tea.KeyEnter:
				m.filterMode = false
			}
			return m, nil
		}
		switch msg.String() {
		case "enter":
			row := m.table.SelectedRow()
			if len(row) > 0 {
				if detail, ok := m.queryDetails[row[0]]; ok {
					m.detailText = detail
					m.detailMode = true
				}
			}
			return m, nil
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckQueryLoad(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m QueryLoadModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Query Load", m.detailText, m.width)
	}

	// Color Total (ms) column (col 3): red if > 60s, yellow if > 5s.
	// No rule for the Load bar (col 7) — its block chars are multi-byte UTF-8
	// and the bar itself is already visually informative.
	rules := []ColorRule{
		{Column: 3, Colorize: func(v string) int {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return -1
			}
			switch {
			case f > 60000:
				return 2
			case f > 5000:
				return 1
			}
			return -1
		}},
	}

	dot := func(level int, label string) string {
		return SeverityColor("■", level) + FooterStyle.Render(" "+label)
	}
	legend := "  " + dot(2, "Total > 60s") + "   " + dot(1, "Total > 5s") + "   " + FooterStyle.Render("Load bar = % of total instance exec time")

	s := RenderHeader("Query Load") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + legend
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • / filter • r refresh • q back")
	return s
}
