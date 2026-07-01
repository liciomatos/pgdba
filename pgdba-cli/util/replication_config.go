package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type ReplicationConfigModel struct {
	table        table.Model
	allRows      []table.Row
	counts       ReplicationCounts
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	height       int
	width        int
}

func (m ReplicationConfigModel) IsInputMode() bool { return m.filterMode }

func CheckReplicationConfig(initialModel func() tea.Model) tea.Model {
	params, counts, err := FetchReplicationConfig(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading replication config", initialModel)
	}

	columns := []table.Column{
		{Title: "Parameter", Width: 35},
		{Title: "Setting", Width: 20},
		{Title: "Unit", Width: 6},
		{Title: "Context", Width: 12},
		{Title: "Hint", Width: 55},
	}

	var rowsData []table.Row
	for _, p := range params {
		hint := p.Hint
		if hint == "" {
			hint = p.ShortDesc
		}
		rowsData = append(rowsData, table.Row{
			p.Name,
			p.Setting,
			p.Unit,
			p.Context,
			hint,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ReplicationConfigModel{
		table:        t,
		allRows:      rowsData,
		counts:       counts,
		initialModel: initialModel,
		height:       30,
		width:        120,
	}
}

func (m ReplicationConfigModel) Init() tea.Cmd { return nil }

func (m ReplicationConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.table.SetHeight(TableHeight(msg.Height) - 4) // -2 counts summary, -1 hint, -1 blank
		cols := StretchColumn(m.table.Columns(), 4, msg.Width)
		m.table.SetColumns(cols)
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
			return CheckReplicationConfig(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m ReplicationConfigModel) View() string {
	hint := "  Logical replication streams row-level changes (INSERT/UPDATE/DELETE) to subscribers using wal_level=logical and a decoder plugin (pgoutput, wal2json)."
	s := RenderHeader("Replication Config") + "\n"
	s += HintStyle.Render(hint) + "\n\n"

	// Live counts summary line
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render
	s += fmt.Sprintf("  %s %s   %s %s   %s %s\n\n",
		renderLabel("Active senders:"),
		lipgloss.NewStyle().Foreground(ColorWhite).Render(fmt.Sprintf("%d", m.counts.ActiveSenders)),
		renderLabel("Total slots:"),
		lipgloss.NewStyle().Foreground(ColorWhite).Render(fmt.Sprintf("%d", m.counts.TotalSlots)),
		renderLabel("Active slots:"),
		lipgloss.NewStyle().Foreground(ColorWhite).Render(fmt.Sprintf("%d", m.counts.ActiveSlots)))

	// Color the Hint column based on hint level stored implicitly.
	// We rebuild it from the allRows data by comparing with hint text.
	// Since hint level is not stored in rows, we colorize based on content heuristics.
	// Rows with "risk" or "required" or "disabled" get warning coloring.
	rules := []ColorRule{
		{Column: 4, Colorize: func(v string) int {
			switch {
			case v == "":
				return -1
			case containsAny(v, "risk", "disabled", "corruption", "loss"):
				return 2
			case containsAny(v, "required", "may cause", "will be", "not available"):
				return 1
			case containsAny(v, "supports", "OK", "streaming"):
				return 0
			default:
				return 3 // gray for neutral descriptions
			}
		}},
	}

	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}