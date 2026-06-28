package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type SlowQueriesModel struct {
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

func (m SlowQueriesModel) IsInputMode() bool { return m.filterMode }

func IdentifySlowQueries(initialModel func() tea.Model) tea.Model {
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}

	queries, err := FetchSlowQueries(context.Background(), config.Config.DB, threshold, 20)
	if err != nil {
		return NewErrorModel(err, "Loading slow queries", initialModel)
	}

	columns := []table.Column{
		{Title: "Query ID", Width: 12},
		{Title: "Query", Width: 50},
		{Title: "Calls", Width: 8},
		{Title: "Total (ms)", Width: 14},
		{Title: "Mean (ms)", Width: 14},
		{Title: "Stddev (ms)", Width: 14},
		{Title: "Rows", Width: 8},
	}

	var rowsData []table.Row
	details := make(map[string]string)

	for _, sq := range queries {
		qid := fmt.Sprintf("%d", sq.QueryID)
		details[qid] = sq.Query

		displayQuery := strings.ReplaceAll(sq.Query, "\n", " ")
		if len(displayQuery) > 50 {
			displayQuery = displayQuery[:50] + "..."
		}

		rowsData = append(rowsData, table.Row{
			qid,
			displayQuery,
			fmt.Sprintf("%d", sq.Calls),
			fmt.Sprintf("%.2f", sq.TotalExecTimeMS),
			fmt.Sprintf("%.2f", sq.MeanExecTimeMS),
			fmt.Sprintf("%.2f", sq.StddevExecTimeMS),
			fmt.Sprintf("%d", sq.Rows),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return SlowQueriesModel{
		table:        t,
		allRows:      rowsData,
		queryDetails: details,
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func (m SlowQueriesModel) Init() tea.Cmd { return nil }

func (m SlowQueriesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return IdentifySlowQueries(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m SlowQueriesModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Slow Queries", m.detailText, m.width)
	}
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	rules := []ColorRule{
		{Column: 3, Colorize: func(v string) int {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return -1
			}
			switch {
			case f > float64(threshold*2):
				return 2
			case f > float64(threshold):
				return 1
			}
			return -1
		}},
	}
	s := RenderHeader("Slow Queries") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • / filter • r refresh • q back")
	return s
}
