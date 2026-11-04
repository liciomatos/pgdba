package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type WaitEventsModel struct {
	table    table.Model
	sampling WaitEventSampling
	// detailEvent is the event whose queries are shown (enter on a row); nil on the
	// event list.
	detailEvent  *WaitEvent
	initialModel func() tea.Model
	width        int
	height       int
}

// The TUI samples for one second so opening the screen stays snappy; the MCP tool
// defaults to 20 one-second samples.
const (
	waitEventScreenSamples  = 10
	waitEventScreenInterval = 100 * time.Millisecond
	// waitEventScreenQueries is how many statements the per-event view lists.
	waitEventScreenQueries = 10
)

// CheckWaitEvents samples pg_stat_activity and groups sessions by wait_event_type and
// wait_event, averaged into active sessions (AAS). Sessions running on CPU (no wait
// event) are shown as type "CPU"; idle sessions are left out. The distribution bar
// shows each event's share of the total.
func CheckWaitEvents(initialModel func() tea.Model) tea.Model {
	sampling, err := FetchWaitEvents(context.Background(), config.Config.DB, WaitEventOptions{
		Samples:         waitEventScreenSamples,
		Interval:        waitEventScreenInterval,
		QueriesPerEvent: waitEventScreenQueries,
	})
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
		if m.detailEvent != nil {
			switch msg.String() {
			case "q", "esc", "enter":
				m.detailEvent = nil
			}
			return m, nil
		}
		switch msg.String() {
		case "enter":
			if row := m.table.SelectedRow(); len(row) > 1 {
				for i := range m.sampling.Events {
					if m.sampling.Events[i].EventType == row[0] && m.sampling.Events[i].Event == row[1] {
						m.detailEvent = &m.sampling.Events[i]
					}
				}
			}
			return m, nil
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

// queriesView lists the statements behind the selected wait event, one line each, cut
// to the terminal width.
func (m WaitEventsModel) queriesView() string {
	event := m.detailEvent
	lineStyle := lipgloss.NewStyle()
	if m.width > 0 {
		lineStyle = lineStyle.MaxWidth(m.width)
	}
	s := RenderHeader("Wait Events › "+event.EventType+":"+event.Event) + "\n"
	s += HintStyle.Render(fmt.Sprintf("  %.2f average active sessions on this event — the statements behind it:", event.AAS)) + "\n\n"
	s += HintStyle.Render(fmt.Sprintf("  %6s  %5s  %s", "AAS", "Share", "Query")) + "\n"
	if len(event.Queries) == 0 {
		s += HintStyle.Render("  no statement recorded") + "\n"
	}
	for _, query := range event.Queries {
		s += lineStyle.Render(fmt.Sprintf("  %6.2f  %4.0f%%  %s", query.AAS, query.Pct, query.Label())) + "\n"
	}
	s += "\n" + FooterStyle.Render("? help • q back")
	return s
}

func (m WaitEventsModel) View() string {
	if m.detailEvent != nil {
		return m.queriesView()
	}
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
	s += "\n" + FooterStyle.Render("↑↓ navigate • enter queries • r refresh • ? help • q back")
	return s
}
