package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

// RecoveryConflictsModel shows a standby's replay state and, per database, the queries
// canceled because WAL replay needed something they held.
type RecoveryConflictsModel struct {
	table        table.Model
	allRows      []table.Row
	conflicts    RecoveryConflicts
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m RecoveryConflictsModel) IsInputMode() bool { return m.filterMode }

var recoveryConflictValueColumns = []int{1, 2, 3, 4, 5, 6, 7, 8}

// statusColumnRule colors a Status column: green ok, yellow warning, red critical.
func statusColumnRule(column int) ColorRule {
	return ColorRule{Column: column, Colorize: func(v string) int {
		switch strings.TrimSpace(v) {
		case "critical":
			return 2
		case "warning":
			return 1
		case "ok":
			return 0
		}
		return -1
	}}
}

func CheckRecoveryConflicts(initialModel func() tea.Model) tea.Model {
	conflicts, err := FetchRecoveryConflicts(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading recovery conflicts", initialModel)
	}

	columns := []table.Column{
		{Title: "Database", Width: 20},
		{Title: "Status", Width: 8},
		{Title: "Total", Width: 8},
		{Title: "Snapshot", Width: 8},
		{Title: "Lock", Width: 8},
		{Title: "Bufferpin", Width: 9},
		{Title: "Deadlock", Width: 8},
		{Title: "Tablespace", Width: 10},
		{Title: "Logical Slot", Width: 12},
	}
	var rowsData []table.Row
	for _, d := range conflicts.Databases {
		logicalSlot := "-"
		if d.ActiveLogicalSlot != nil {
			logicalSlot = formatCount(float64(*d.ActiveLogicalSlot))
		}
		rowsData = append(rowsData, table.Row{
			d.Database,
			RecoveryConflictStatus(d).String(),
			formatCount(float64(d.Total)),
			formatCount(float64(d.Snapshot)),
			formatCount(float64(d.Lock)),
			formatCount(float64(d.Bufferpin)),
			formatCount(float64(d.Deadlock)),
			formatCount(float64(d.Tablespace)),
			logicalSlot,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return RecoveryConflictsModel{table: t, allRows: rowsData, conflicts: conflicts, initialModel: initialModel}
}

func (m RecoveryConflictsModel) Init() tea.Cmd { return nil }

func (m RecoveryConflictsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, recoveryConflictValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, []int{0}, msg.Width))
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
			return CheckRecoveryConflicts(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// replaySummary describes the replay state in two lines.
func (m RecoveryConflictsModel) replaySummary() string {
	conflicts := m.conflicts
	if !conflicts.InRecovery {
		return SeverityColor("  Primary server: recovery conflicts only happen on standbys — connect to a replica to see them.", 3)
	}
	status := ReplayStatus(conflicts)
	backlog := "-"
	if conflicts.ReplayBacklogBytes != nil {
		backlog = formatBytes(*conflicts.ReplayBacklogBytes)
	}
	lag := "-"
	if conflicts.ReplayLagSeconds != nil {
		lag = formatAge(int64(*conflicts.ReplayLagSeconds))
	}
	paused := ""
	if conflicts.ReplayPaused != nil && *conflicts.ReplayPaused {
		paused = " • REPLAY PAUSED"
	}
	firstLine := SeverityColor(fmt.Sprintf("  Standby • replay backlog %s • last replayed commit %s ago%s", backlog, lag, paused), int(status))
	receiver := "no WAL receiver running"
	if r := conflicts.WALReceiver; r != nil {
		receiver = fmt.Sprintf("WAL receiver %s from %s", r.Status, r.SenderHost)
		if r.SlotName != "" {
			receiver += " (slot " + r.SlotName + ")"
		}
	}
	return firstLine + "\n" + HintStyle.Render("  "+receiver)
}

func (m RecoveryConflictsModel) View() string {
	summary := m.replaySummary()
	if m.width > 0 {
		summary = lipgloss.NewStyle().MaxWidth(m.width).Render(summary)
	}
	s := RenderHeader("Recovery Conflicts") + "\n"
	s += summary + "\n"
	s += RenderWarnings(m.conflicts.Warnings, m.width) + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), []ColorRule{statusColumnRule(1)})
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • ? help • q back")
	return s
}
