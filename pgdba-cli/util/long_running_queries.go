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

type LongRunningQueriesModel struct {
	table        table.Model
	allRows      []table.Row
	queryDetails map[string]string // PID string → full query text
	filterText   string
	filterMode   bool
	detailMode   bool
	detailText   string
	initialModel func() tea.Model
	confirmKill  bool
	pidToKill    int
	width        int
	height       int
}

func (m LongRunningQueriesModel) IsInputMode() bool { return m.filterMode }

func CheckLongRunningQueries(initialModel func() tea.Model) tea.Model {
	queries, err := FetchLongRunningQueries(context.Background(), config.Config.DB, 5, 100)
	if err != nil {
		return NewErrorModel(err, "Loading long running queries", initialModel)
	}

	columns := []table.Column{
		{Title: "PID", Width: 8},
		{Title: "User", Width: 15},
		{Title: "Application", Width: 20},
		{Title: "State", Width: 12},
		{Title: "Duration (s)", Width: 14},
		{Title: "Query", Width: 60},
	}

	var rowsData []table.Row
	details := make(map[string]string)

	for _, lrq := range queries {
		pidStr := fmt.Sprintf("%d", lrq.PID)
		details[pidStr] = lrq.Query

		displayQuery := strings.ReplaceAll(lrq.Query, "\n", " ")
		if len(displayQuery) > 80 {
			displayQuery = displayQuery[:80] + "..."
		}

		rowsData = append(rowsData, table.Row{
			pidStr,
			lrq.Username,
			lrq.ApplicationName,
			lrq.State,
			fmt.Sprintf("%.1fs", lrq.DurationSeconds),
			displayQuery,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return LongRunningQueriesModel{
		table:        t,
		allRows:      rowsData,
		queryDetails: details,
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func (m LongRunningQueriesModel) Init() tea.Cmd { return nil }

func (m LongRunningQueriesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := StretchColumn(m.table.Columns(), 5, msg.Width)
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
			if !m.confirmKill {
				row := m.table.SelectedRow()
				if len(row) > 0 {
					if detail, ok := m.queryDetails[row[0]]; ok {
						m.detailText = detail
						m.detailMode = true
					}
				}
			}
			return m, nil
		case "/":
			if !m.confirmKill {
				m.filterMode = true
			}
			return m, nil
		case "q", "esc":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckLongRunningQueries(m.initialModel), nil
		case "k":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			m.pidToKill, _ = strconv.Atoi(selectedRow[0])
			m.confirmKill = true
			return m, nil
		case "y":
			if m.confirmKill {
				if _, err := config.Config.DB.Exec("SELECT pg_terminate_backend($1)", m.pidToKill); err != nil {
					return NewErrorModel(err, fmt.Sprintf("Killing PID %d", m.pidToKill), m.initialModel), nil
				}
				return CheckLongRunningQueries(m.initialModel), nil
			}
		case "n":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m LongRunningQueriesModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Long Running Queries", m.detailText, m.width)
	}
	rules := []ColorRule{
		{Column: 4, Colorize: func(v string) int {
			// Values are formatted as "%.1fs"; strip the trailing 's'.
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "s"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f > 60:
				return 2
			case f > 10:
				return 1
			default:
				return 0
			}
		}},
	}
	s := RenderHeader("Long Running Queries") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmKill {
		s += fmt.Sprintf("\nKill query with PID %d? (y/n)\n", m.pidToKill)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • k kill • / filter • r refresh • q back")
	}
	return s
}
