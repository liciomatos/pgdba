package util

import (
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
	query := `
        SELECT
            pid,
            usename,
            application_name,
            state,
            ROUND(EXTRACT(EPOCH FROM (now() - query_start))::numeric, 1) AS duration_seconds,
            COALESCE(query, '') AS query
        FROM pg_stat_activity
        WHERE state != 'idle'
          AND query_start IS NOT NULL
          AND now() - query_start > interval '5 seconds'
        ORDER BY duration_seconds DESC;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading long running queries", initialModel)
	}
	defer rows.Close()

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

	for rows.Next() {
		var pid int
		var usename, applicationName, state string
		var durationSeconds float64
		var q string

		if err := rows.Scan(&pid, &usename, &applicationName, &state, &durationSeconds, &q); err != nil {
			return NewErrorModel(err, "Scanning long running queries row", initialModel)
		}

		pidStr := fmt.Sprintf("%d", pid)
		details[pidStr] = q

		q = strings.ReplaceAll(q, "\n", " ")
		if len(q) > 80 {
			q = q[:80] + "..."
		}

		durStr := fmt.Sprintf("%.1fs", durationSeconds)
		switch {
		case durationSeconds > 60:
			durStr = SeverityColor(durStr, 2)
		case durationSeconds > 10:
			durStr = SeverityColor(durStr, 1)
		default:
			durStr = SeverityColor(durStr, 0)
		}

		rowsData = append(rowsData, table.Row{
			pidStr,
			usename,
			applicationName,
			state,
			durStr,
			q,
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
	s := RenderHeader("Long Running Queries") + "\n"
	s += m.table.View()
	if m.confirmKill {
		s += fmt.Sprintf("\nKill query with PID %d? (y/n)\n", m.pidToKill)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • k kill • / filter • r refresh • q back")
	}
	return s
}
