package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type AutovacuumActivityModel struct {
	table        table.Model
	workerCount  int
	saturation   AutovacuumWorkerSaturation
	initialModel func() tea.Model
	width        int
	height       int
}

func workersColumns() []table.Column {
	return []table.Column{
		{Title: "PID", Width: 8},
		{Title: "Type", Width: 11},
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 20},
		{Title: "Phase", Width: 22},
		{Title: "Progress", Width: 10},
		{Title: "Duration", Width: 10},
	}
}

func CheckAutovacuumActivity(initialModel func() tea.Model) tea.Model {
	saturation, err := FetchAutovacuumWorkerSaturation(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum worker saturation", initialModel)
	}
	workers, err := FetchAutovacuumActivity(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum activity", initialModel)
	}

	var rows []table.Row
	for _, w := range workers {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", w.PID),
			workerTypeText(w.IsAutovacuum),
			w.SchemaName,
			w.TableName,
			w.Phase,
			formatVacuumProgress(w.HeapBlksScanned, w.HeapBlksTotal),
			formatDuration(w.DurationSeconds),
		})
	}

	t := table.New(
		table.WithColumns(workersColumns()),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumActivityModel{
		table:        t,
		workerCount:  len(rows),
		saturation:   saturation,
		initialModel: initialModel,
		height:       30,
	}
}

func workerTypeText(isAutovacuum bool) string {
	if isAutovacuum {
		return "autovacuum"
	}
	return "manual"
}

func formatVacuumProgress(scanned, total int64) string {
	if total <= 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.1f%%", 100.0*float64(scanned)/float64(total))
}

func formatDuration(seconds *int64) string {
	if seconds == nil {
		return "N/A"
	}
	return (time.Duration(*seconds) * time.Second).String()
}

func (m AutovacuumActivityModel) Init() tea.Cmd { return nil }

func (m AutovacuumActivityModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// -4 for the saturation line, hint line, blank line, and "Active Vacuums" label
		// that sit above the table (beyond the header/footer TableHeight already accounts for).
		m.table.SetHeight(TableHeight(msg.Height) - 4)
		cols := StretchColumn(m.table.Columns(), 3, msg.Width)
		m.table.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckAutovacuumActivity(m.initialModel), nil
		case "enter":
			if m.workerCount == 0 {
				return m, nil
			}
			row := m.table.SelectedRow()
			if len(row) < 4 {
				return m, nil
			}
			schema, tableName := row[2], row[3]
			parent := m.initialModel
			return CheckAutovacuumDetail(schema, tableName, func() tea.Model {
				return CheckAutovacuumActivity(parent)
			}), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m AutovacuumActivityModel) View() string {
	renderKey := func(s string) string { return HintStyle.Render(s) }

	level := 0
	switch {
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers:
		level = 2
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers/2:
		level = 1
	}
	saturationLine := fmt.Sprintf("  %s %s",
		renderKey("Autovacuum Workers:"),
		SeverityColor(fmt.Sprintf("%d / %d running", m.saturation.RunningWorkers, m.saturation.MaxWorkers), level),
	)

	s := RenderHeader("Autovacuum Activity") + "\n"
	s += saturationLine + "\n"
	s += HintStyle.Render("  All workers busy means newly-eligible tables queue until one frees up.") + "\n\n"
	s += fmt.Sprintf("Active Vacuums  (%d)\n", m.workerCount)
	if m.workerCount == 0 {
		s += HintStyle.Render("  No vacuum or autovacuum currently running.") + "\n"
		s += strings.Repeat("\n", max2(m.table.Height()-1, 0))
	} else {
		s += ColorizeTable(m.table.View(), m.table.Columns(), nil)
	}

	s += "\n" + FooterStyle.Render("↑↓ navigate • enter table detail • r refresh • q back")
	return s
}
