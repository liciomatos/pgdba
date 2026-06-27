package util

import (
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type IndexUsageModel struct {
	table        table.Model
	initialModel func() tea.Model
}

func CheckIndexUsage(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            schemaname,
            relname AS tablename,
            indexrelname AS indexname,
            idx_scan,
            idx_tup_read,
            idx_tup_fetch,
            pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
        FROM pg_stat_user_indexes
        ORDER BY idx_scan ASC
        LIMIT 20;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading index usage", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 25},
		{Title: "Index", Width: 35},
		{Title: "Scans", Width: 10},
		{Title: "Tup Read", Width: 12},
		{Title: "Tup Fetch", Width: 12},
		{Title: "Size", Width: 10},
	}

	var rowsData []table.Row
	for rows.Next() {
		var schemaname, tablename, indexname, indexSize string
		var idxScan, idxTupRead, idxTupFetch int64

		if err := rows.Scan(&schemaname, &tablename, &indexname, &idxScan, &idxTupRead, &idxTupFetch, &indexSize); err != nil {
			return NewErrorModel(err, "Scanning index usage row", initialModel)
		}

		scanStr := fmt.Sprintf("%d", idxScan)
		if idxScan == 0 {
			scanStr = SeverityColor(scanStr, 2)
		}

		rowsData = append(rowsData, table.Row{
			schemaname,
			tablename,
			indexname,
			scanStr,
			fmt.Sprintf("%d", idxTupRead),
			fmt.Sprintf("%d", idxTupFetch),
			indexSize,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithStyles(DefaultTableStyles()),
	)

	return IndexUsageModel{table: t, initialModel: initialModel}
}

func (m IndexUsageModel) Init() tea.Cmd { return nil }

func (m IndexUsageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckIndexUsage(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m IndexUsageModel) View() string {
	s := RenderHeader("Index Usage") + "\n"
	s += m.table.View()
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
