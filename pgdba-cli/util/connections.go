package util

import (
	"fmt"
	"strconv"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type ConnectionsModel struct {
	table        table.Model
	usedConns    int
	maxConns     int
	initialModel func() tea.Model
}

func CheckConnections(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT COALESCE(state, 'background'), count(*)
        FROM pg_stat_activity
        GROUP BY state
        ORDER BY count(*) DESC;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading connections overview", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "State", Width: 25},
		{Title: "Count", Width: 10},
	}

	var rowsData []table.Row
	usedConns := 0
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return NewErrorModel(err, "Scanning connections row", initialModel)
		}
		usedConns += count
		rowsData = append(rowsData, table.Row{state, fmt.Sprintf("%d", count)})
	}

	var maxConnsStr string
	if err := config.Config.DB.QueryRow("SHOW max_connections;").Scan(&maxConnsStr); err != nil {
		return NewErrorModel(err, "Reading max_connections", initialModel)
	}
	maxConns, _ := strconv.Atoi(maxConnsStr)

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithStyles(DefaultTableStyles()),
	)

	return ConnectionsModel{table: t, usedConns: usedConns, maxConns: maxConns, initialModel: initialModel}
}

func (m ConnectionsModel) Init() tea.Cmd { return nil }

func (m ConnectionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckConnections(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m ConnectionsModel) View() string {
	s := RenderHeader("Connections Overview") + "\n"
	s += m.table.View()

	pct := 0.0
	if m.maxConns > 0 {
		pct = float64(m.usedConns) / float64(m.maxConns) * 100
	}

	var level int
	switch {
	case pct >= 90:
		level = 2
	case pct >= 70:
		level = 1
	default:
		level = 0
	}

	summary := SeverityColor(fmt.Sprintf("%d / %d connections used (%.1f%%)", m.usedConns, m.maxConns, pct), level)
	s += "\n" + summary
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
