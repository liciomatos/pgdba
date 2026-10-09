package util

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

// IOWALModel shows WAL generation (pg_stat_wal, PG14+) on top and buffer I/O by
// backend type and context (pg_stat_io, PG16+) below.
type IOWALModel struct {
	table        table.Model
	wal          WALStats
	walErr       error
	io           IOStats
	ioErr        error
	initialModel func() tea.Model
	width        int
	height       int
}

// fetchErrorText reports an unsupported version plainly and other errors as such.
func fetchErrorText(err error) string {
	var unsupported UnsupportedError
	if errors.As(err, &unsupported) {
		return fmt.Sprintf("not available: %s", unsupported.Error())
	}
	return "error: " + err.Error()
}

func CheckIOWAL(initialModel func() tea.Model) tea.Model {
	ctx := context.Background()
	wal, walErr := FetchWALStats(ctx, config.Config.DB)
	ioStats, ioErr := FetchIOStats(ctx, config.Config.DB)

	columns := []table.Column{
		{Title: "Backend Type", Width: 20},
		{Title: "Object", Width: 13},
		{Title: "Context", Width: 9},
		{Title: "Reads", Width: 10},
		{Title: "Read", Width: 9},
		{Title: "Writes", Width: 10},
		{Title: "Written", Width: 9},
		{Title: "Extends", Width: 9},
		{Title: "Hits", Width: 12},
		{Title: "Evictions", Width: 10},
		{Title: "Fsyncs", Width: 8},
	}
	var rowsData []table.Row
	for _, row := range ioStats.Rows {
		rowsData = append(rowsData, table.Row{
			row.BackendType, row.Object, row.Context,
			formatCount(float64(row.Reads)), formatBytes(row.ReadBytes),
			formatCount(float64(row.Writes)), formatBytes(row.WriteBytes),
			formatCount(float64(row.Extends)),
			formatCount(float64(row.Hits)),
			formatCount(float64(row.Evictions)),
			formatCount(float64(row.Fsyncs)),
		})
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return IOWALModel{table: t, wal: wal, walErr: walErr, io: ioStats, ioErr: ioErr, initialModel: initialModel}
}

func (m IOWALModel) Init() tea.Cmd { return nil }

func (m IOWALModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetColumns(StretchColumn(m.table.Columns(), 0, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckIOWAL(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func formatOptionalFloat(value *float64, format string) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf(format, *value)
}

// walSection is the WAL summary: volume, full-page image share and the settings that
// drive it.
func (m IOWALModel) walSection() []string {
	renderLabel := HintStyle.Render
	if m.walErr != nil {
		return []string{renderLabel("  WAL: ") + fetchErrorText(m.walErr)}
	}
	since := "never reset"
	if m.wal.StatsReset != nil {
		since = "since " + m.wal.StatsReset.Format("2006-01-02 15:04")
	}
	status := WALStatsStatus(m.wal)
	rate := "-"
	if perSecond := m.wal.BytesPerSecond(); perSecond != nil {
		rate = formatBytes(int64(*perSecond)) + "/s"
	}
	return []string{
		fmt.Sprintf("  %s %s   %s %s   %s %s   %s %s   %s",
			renderLabel("WAL:"), formatBytes(m.wal.Bytes),
			renderLabel("records"), formatCount(float64(m.wal.Records)),
			renderLabel("full-page images"), SeverityColor(formatOptionalFloat(m.wal.FPIPct(), "%.1f%%"), int(status)),
			renderLabel("buffers full"), formatCount(float64(m.wal.BuffersFull)),
			renderLabel("("+since+", avg "+rate+")")),
		fmt.Sprintf("  %s full_page_writes=%s  wal_compression=%s  checkpoint_timeout=%s  max_wal_size=%s  wal_buffers=%s",
			renderLabel("Settings:"), m.wal.FullPageWrites, m.wal.WALCompression, m.wal.CheckpointTimeout,
			m.wal.MaxWALSize, m.wal.WALBuffers),
	}
}

// writersLine is who writes relation blocks, e.g. "checkpointer 61% • client backend 30%".
func (m IOWALModel) writersLine() string {
	if m.ioErr != nil {
		return HintStyle.Render("  I/O: ") + fetchErrorText(m.ioErr)
	}
	parts := make([]string, 0, len(m.io.WritesByBackend))
	for _, share := range m.io.WritesByBackend {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", share.BackendType, share.Pct))
	}
	if len(parts) == 0 {
		parts = append(parts, "no relation writes yet")
	}
	line := HintStyle.Render("  Relation writes by: ") + strings.Join(parts, " • ")
	if IOStatsStatus(m.io) == StatusWarning {
		line += "  " + SeverityColor(fmt.Sprintf("client backends write %.0f%% in normal context", *m.io.ClientBackendWritePct), 1)
	}
	return line
}

func (m IOWALModel) View() string {
	lineStyle := lipgloss.NewStyle()
	if m.width > 0 {
		lineStyle = lineStyle.MaxWidth(m.width)
	}
	s := RenderHeader("I/O & WAL") + "\n"
	for _, line := range append(m.walSection(), m.writersLine()) {
		s += lineStyle.Render(line) + "\n"
	}
	s += "\n"
	if m.ioErr == nil {
		s += m.table.View()
	}
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • ? help • q back")
	return s
}
