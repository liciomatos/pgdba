package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type MemoryStatsModel struct {
	table        table.Model
	stats        MemoryStats
	initialModel func() tea.Model
	height       int
	width        int
}

func CheckMemoryStats(initialModel func() tea.Model) tea.Model {
	stats, err := FetchMemoryStats(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading memory stats", initialModel)
	}

	columns := []table.Column{
		{Title: "Parameter", Width: 25},
		{Title: "Setting", Width: 15},
		{Title: "Unit", Width: 6},
		{Title: "Description", Width: 55},
	}

	var rowsData []table.Row
	for _, c := range stats.Configs {
		rowsData = append(rowsData, table.Row{
			c.Name,
			c.Setting,
			c.Unit,
			c.ShortDesc,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return MemoryStatsModel{
		table:        t,
		stats:        stats,
		initialModel: initialModel,
		height:       30,
		width:        120,
	}
}

func (m MemoryStatsModel) Init() tea.Cmd { return nil }

func (m MemoryStatsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		cols := StretchColumn(m.table.Columns(), 3, msg.Width)
		m.table.SetColumns(cols)
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckMemoryStats(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m MemoryStatsModel) View() string {
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render
	renderValue := lipgloss.NewStyle().Foreground(ColorWhite).Render

	s := RenderHeader("Memory & Checkpoint Stats") + "\n"
	s += m.table.View() + "\n"

	renderHit := func(pct *float64) string {
		return SeverityColor(formatHitPct(pct), int(CacheHitStatus(pct)))
	}
	hit := m.stats.CacheHit
	s += fmt.Sprintf("\n  %s %s   %s %s   %s %s\n",
		renderLabel("Cache hit — heap:"), renderHit(hit.HeapHitPct),
		renderLabel("index:"), renderHit(hit.IdxHitPct),
		renderLabel("database (all blocks):"), renderHit(hit.DatabaseHitPct))

	cp := m.stats.Checkpoint
	buffersBackendStr := "N/A (moved to pg_stat_io in PG17+)"
	if cp.BuffersBackend != nil {
		buffersBackendStr = fmt.Sprintf("%d", *cp.BuffersBackend)
	}
	s += "\n  " + renderLabel("Checkpoints & background writer (since stats reset):") + "\n"
	s += fmt.Sprintf("    %s %s   %s %s\n",
		renderLabel("Timed:"), renderValue(fmt.Sprintf("%d", cp.CheckpointsTimed)),
		renderLabel("Requested:"), renderValue(fmt.Sprintf("%d", cp.CheckpointsReq)))
	s += fmt.Sprintf("    %s %s   %s %s   %s %s\n",
		renderLabel("Buffers checkpoint:"), renderValue(fmt.Sprintf("%d", cp.BuffersCheckpoint)),
		renderLabel("Buffers clean:"), renderValue(fmt.Sprintf("%d", cp.BuffersClean)),
		renderLabel("Buffers backend:"), renderValue(buffersBackendStr))
	if cp.CheckpointsReq > cp.CheckpointsTimed {
		s += "    " + lipgloss.NewStyle().Foreground(ColorYellow).Render(
			"More checkpoints happened on demand than on schedule — consider raising max_wal_size.") + "\n"
	}

	if len(m.stats.Warnings) > 0 {
		s += "\n" + RenderWarnings(m.stats.Warnings, m.width)
	}
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • ? help • q back")
	return s
}
