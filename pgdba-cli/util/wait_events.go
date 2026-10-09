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

type WaitEventsModel struct {
	table        table.Model
	sampling     WaitEventSampling
	initialModel func() tea.Model
	width        int
	height       int
}

// The TUI samples for one second so opening the screen stays snappy; the MCP tool
// defaults to 20 one-second samples.
const (
	waitEventScreenSamples  = 10
	waitEventScreenInterval = 100 * time.Millisecond
)

// CheckWaitEvents samples pg_stat_activity and groups sessions by wait_event_type and
// wait_event, averaged into active sessions (AAS). Sessions running on CPU (no wait
// event) are shown as type "CPU"; idle sessions are left out. The distribution bar
// shows each event's share of the total.
func CheckWaitEvents(initialModel func() tea.Model) tea.Model {
	sampling, err := FetchWaitEvents(context.Background(), config.Config.DB, waitEventScreenSamples, waitEventScreenInterval, false)
	if err != nil {
		return NewErrorModel(err, "Loading wait events", initialModel)
	}

	// Distribution bar is last column (multi-byte block chars safe for ColorizeTable).
	columns := []table.Column{
		{Title: "Type", Width: 18},
		{Title: "Event", Width: 25},
		{Title: "AAS", Width: 8},
		{Title: "Distribution", Width: 22},
	}

	var rowsData []table.Row
	for _, e := range sampling.Events {
		rowsData = append(rowsData, table.Row{
			e.EventType,
			e.Event,
			fmt.Sprintf("%.2f", e.AAS),
			RenderBar(e.Pct, 12),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return WaitEventsModel{table: t, sampling: sampling, initialModel: initialModel, height: 30}
}

func (m WaitEventsModel) Init() tea.Cmd { return nil }

func (m WaitEventsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckWaitEvents(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m WaitEventsModel) View() string {
	// Color the Type column: Lock waits are critical, IO waits are a warning,
	// CPU means the session is actively running (no wait).
	rules := []ColorRule{
		{Column: 0, Colorize: func(v string) int {
			switch strings.ToLower(v) {
			case "lock":
				return 2
			case "io", "bufferpin", "lwlock":
				return 1
			case "cpu":
				return 0
			}
			return -1
		}},
	}

	dot := func(level int, label string) string {
		return SeverityColor("■", level) + FooterStyle.Render(" "+label)
	}
	legend := "  " + dot(2, "Lock") + "   " + dot(1, "IO · LWLock · BufferPin") + "   " + dot(0, "CPU")

	summary := HintStyle.Render(fmt.Sprintf("  %d samples over %s • %.2f average active sessions (idle sessions excluded)",
		m.sampling.Samples, time.Duration(m.sampling.Samples)*m.sampling.Interval, m.sampling.TotalAAS))
	s := RenderHeader("Wait Events") + "\n"
	s += summary + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + legend
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • ? help • q back")
	return s
}
