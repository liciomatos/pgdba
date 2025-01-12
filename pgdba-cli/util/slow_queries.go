package util

import (
	"fmt"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type SlowQueriesModel struct {
	table        table.Model
	initialModel func() tea.Model
}

func IdentifySlowQueries(initialModel func() tea.Model) SlowQueriesModel {
	query := `
        SELECT
            queryid,
            query,
            calls,
            total_exec_time,
            mean_exec_time,
            stddev_exec_time,
            rows
        FROM
            pg_stat_statements
        ORDER BY
            mean_exec_time DESC
        LIMIT 10;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		fmt.Printf("Error executing query: %v\n", err)
		return SlowQueriesModel{}
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Query ID", Width: 10},
		{Title: "Query", Width: 50},
		{Title: "Calls", Width: 10},
		{Title: "Total Exec Time (ms)", Width: 20},
		{Title: "Mean Exec Time (ms)", Width: 20},
		{Title: "Stddev Exec Time (ms)", Width: 20},
		{Title: "Rows", Width: 10},
	}

	var rowsData []table.Row
	for rows.Next() {
		var queryID int64
		var query string
		var calls int
		var totalExecTime, meanExecTime, stddevExecTime float64
		var rowsReturned int

		err := rows.Scan(&queryID, &query, &calls, &totalExecTime, &meanExecTime, &stddevExecTime, &rowsReturned)
		if err != nil {
			fmt.Printf("Error scanning row: %v\n", err)
			return SlowQueriesModel{}
		}

		// Replace newline characters with spaces to prevent wrapping
		query = strings.ReplaceAll(query, "\n", " ")

		// Truncate the query to a fixed length to prevent wrapping
		maxQueryLength := 50
		if len(query) > maxQueryLength {
			query = query[:maxQueryLength] + "..."
		}

		rowsData = append(rowsData, table.Row{
			fmt.Sprintf("%d", queryID),
			query,
			fmt.Sprintf("%d", calls),
			fmt.Sprintf("%.2f", totalExecTime),
			fmt.Sprintf("%.2f", meanExecTime),
			fmt.Sprintf("%.2f", stddevExecTime),
			fmt.Sprintf("%d", rowsReturned),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
	)

	return SlowQueriesModel{table: t, initialModel: initialModel}
}

func (m SlowQueriesModel) Init() tea.Cmd {
	return nil
}

func (m SlowQueriesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m SlowQueriesModel) View() string {
	s := fmt.Sprintf("PostgreSQL Version: %s\n", config.Config.Version)
	s += fmt.Sprintf("Connected to: %s@%s:%d/%s\n\n", config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName)
	s += m.table.View()
	return s
}
