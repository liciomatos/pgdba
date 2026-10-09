package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type ConnectionsModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	usedConns    int
	maxConns     int
	result       ConnectionsResult
	initialModel func() tea.Model
	width        int
	height       int
}

func (m ConnectionsModel) IsInputMode() bool { return m.filterMode }

func CheckConnections(initialModel func() tea.Model) tea.Model {
	result, err := FetchConnections(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading connections overview", initialModel)
	}

	columns := []table.Column{
		{Title: "State", Width: 25},
		{Title: "Count", Width: 10},
	}

	var rowsData []table.Row
	for _, s := range result.States {
		rowsData = append(rowsData, table.Row{s.State, fmt.Sprintf("%d", s.Count)})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ConnectionsModel{
		table:        t,
		allRows:      rowsData,
		usedConns:    result.TotalUsed,
		maxConns:     result.MaxAllowed,
		result:       result,
		initialModel: initialModel,
		height:       30,
	}
}

func (m ConnectionsModel) Init() tea.Cmd { return nil }

func (m ConnectionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
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
		case "/":
			m.filterMode = true
			return m, nil
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

// formatGroups renders "name (count)" pairs, e.g. "api (120), batch (8)".
func formatGroups(groups []ConnectionGroup) string {
	if len(groups) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		parts = append(parts, fmt.Sprintf("%s (%d)", group.Name, group.Count))
	}
	return strings.Join(parts, ", ")
}

// sourceLines summarizes who opens the client connections and the longest idle
// transactions, one line each, cut to the terminal width.
func (m ConnectionsModel) sourceLines() []string {
	lineStyle := lipgloss.NewStyle()
	if m.width > 0 {
		lineStyle = lineStyle.MaxWidth(m.width)
	}
	label := HintStyle.Render
	lines := []string{
		label("  Top applications: ") + formatGroups(m.result.TopApplications),
		label("  Top users:        ") + formatGroups(m.result.TopUsers),
		label("  Top clients:      ") + formatGroups(m.result.TopClients),
	}
	oldest := "-"
	if m.result.OldestTransactionAgeSeconds != nil {
		oldest = formatAge(*m.result.OldestTransactionAgeSeconds)
	}
	idle := "none"
	if len(m.result.IdleInTransaction) > 0 {
		longest := m.result.IdleInTransaction[0]
		idle = SeverityColor(fmt.Sprintf("pid %d (%s, %s) idle for %s", longest.PID, longest.User, longest.Application,
			formatAge(longest.StateAgeSeconds)), int(IdleInTransactionStatus(longest.StateAgeSeconds)))
	}
	lines = append(lines, label("  Oldest transaction: ")+oldest+label("   Longest idle in transaction: ")+idle)
	for _, warning := range m.result.Warnings {
		lines = append(lines, SeverityColor("  ⚠ "+warning, 1))
	}
	for i, line := range lines {
		lines[i] = lineStyle.Render(line)
	}
	return lines
}

func (m ConnectionsModel) View() string {
	s := RenderHeader("Connections Overview") + "\n"
	s += m.table.View()

	pct := 0.0
	if m.maxConns > 0 {
		pct = float64(m.usedConns) / float64(m.maxConns) * 100
	}

	summary := SeverityColor(fmt.Sprintf("%d / %d connections used (%.1f%%)", m.usedConns, m.maxConns, pct),
		int(ConnectionUsageStatus(pct)))
	s += "\n" + summary
	for _, line := range m.sourceLines() {
		s += "\n" + line
	}
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • ? help • q back")
	return s
}
