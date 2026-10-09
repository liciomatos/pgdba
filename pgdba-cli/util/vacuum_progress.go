package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

// VacuumProgressModel shows every VACUUM running in the cluster right now, with its
// phase and how far it got.
type VacuumProgressModel struct {
	table        table.Model
	progress     []VacuumProgress
	initialModel func() tea.Model
	width        int
	height       int
}

// vacuumKind labels why a vacuum runs: anti-wraparound autovacuums can't be skipped
// and don't yield to conflicting locks, so they're called out separately.
func vacuumKind(progress VacuumProgress) string {
	switch {
	case progress.IsWraparound:
		return "wraparound"
	case progress.IsAutovacuum:
		return "auto"
	}
	return "manual"
}

// vacuumDeadMemory shows how full the dead-tuple memory is: tuple counts up to PG16,
// bytes from PG17. When it fills, vacuum stops to scan every index (one more pass).
func vacuumDeadMemory(progress VacuumProgress) string {
	switch {
	case progress.DeadTupleBytes != nil && progress.MaxDeadTupleBytes != nil:
		return formatBytes(*progress.DeadTupleBytes) + " / " + formatBytes(*progress.MaxDeadTupleBytes)
	case progress.DeadTuples != nil && progress.MaxDeadTuples != nil:
		return formatCount(float64(*progress.DeadTuples)) + " / " + formatCount(float64(*progress.MaxDeadTuples))
	}
	return "-"
}

func formatOptionalPct(pct *float64) string {
	if pct == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *pct)
}

func CheckVacuumProgress(initialModel func() tea.Model) tea.Model {
	progress, err := FetchVacuumProgress(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading vacuum progress", initialModel)
	}

	columns := []table.Column{
		{Title: "PID", Width: 8},
		{Title: "Database", Width: 12},
		{Title: "Table", Width: 30},
		{Title: "Kind", Width: 10},
		{Title: "Status", Width: 8},
		{Title: "Phase", Width: 24},
		{Title: "Scanned", Width: 8},
		{Title: "Vacuumed", Width: 9},
		{Title: "Idx Passes", Width: 10},
		{Title: "Dead Memory", Width: 20},
		{Title: "Duration", Width: 10},
	}

	var rowsData []table.Row
	for _, p := range progress {
		tableName := p.SchemaName + "." + p.TableName
		if p.TableName == "" {
			tableName = fmt.Sprintf("(oid %d)", p.RelationID)
		}
		duration := "-"
		if p.DurationSeconds != nil {
			duration = formatAge(*p.DurationSeconds)
		}
		indexPasses := fmt.Sprintf("%d", p.IndexVacuumCount)
		if p.IndexesTotal != nil && p.IndexesProcessed != nil && *p.IndexesTotal > 0 {
			indexPasses += fmt.Sprintf(" (%d/%d)", *p.IndexesProcessed, *p.IndexesTotal)
		}
		rowsData = append(rowsData, table.Row{
			fmt.Sprintf("%d", p.PID),
			p.Database,
			tableName,
			vacuumKind(p),
			VacuumProgressStatus(p).String(),
			p.Phase,
			formatOptionalPct(p.ScannedPct()),
			formatOptionalPct(p.VacuumedPct()),
			indexPasses,
			vacuumDeadMemory(p),
			duration,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return VacuumProgressModel{table: t, progress: progress, initialModel: initialModel}
}

func (m VacuumProgressModel) Init() tea.Cmd { return nil }

func (m VacuumProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetColumns(StretchColumn(m.table.Columns(), 2, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckVacuumProgress(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m VacuumProgressModel) View() string {
	s := RenderHeader("Vacuum Progress") + "\n"
	if len(m.progress) == 0 {
		s += HintStyle.Render("  No VACUUM is running right now. Press r to check again.") + "\n"
	} else {
		rules := []ColorRule{
			{Column: 3, Colorize: func(v string) int {
				if strings.TrimSpace(v) == "wraparound" {
					return 2
				}
				return -1
			}},
			{Column: 4, Colorize: func(v string) int {
				if strings.TrimSpace(v) == "warning" {
					return 1
				}
				return 0
			}},
		}
		s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	}
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • ? help • q back")
	return s
}
