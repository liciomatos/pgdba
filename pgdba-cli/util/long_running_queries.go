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
	initialModel func() tea.Model
	confirmKill  bool
	pidToKill    int
}

func CheckLongRunningQueries(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            pid,
            usename,
            application_name,
            state,
            ROUND(EXTRACT(EPOCH FROM (now() - query_start))::numeric, 1) AS duration_seconds,
            left(query, 80) AS query
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
	for rows.Next() {
		var pid int
		var usename, applicationName, state string
		var durationSeconds float64
		var query string

		if err := rows.Scan(&pid, &usename, &applicationName, &state, &durationSeconds, &query); err != nil {
			return NewErrorModel(err, "Scanning long running queries row", initialModel)
		}

		query = strings.ReplaceAll(query, "\n", " ")

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
			fmt.Sprintf("%d", pid),
			usename,
			applicationName,
			state,
			durStr,
			query,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithStyles(DefaultTableStyles()),
	)

	return LongRunningQueriesModel{table: t, initialModel: initialModel}
}

func (m LongRunningQueriesModel) Init() tea.Cmd { return nil }

func (m LongRunningQueriesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
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
	s := RenderHeader("Long Running Queries") + "\n"
	s += m.table.View()
	if m.confirmKill {
		s += fmt.Sprintf("\nKill query with PID %d? (y/n)\n", m.pidToKill)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • k kill selected • r refresh • q back")
	}
	return s
}
