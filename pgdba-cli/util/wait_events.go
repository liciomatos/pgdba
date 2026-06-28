package util

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type WaitEventsModel struct {
	table        table.Model
	initialModel func() tea.Model
	height       int
}

// CheckWaitEvents loads active wait events from pg_stat_activity, grouping
// by wait_event_type and wait_event. Sessions on CPU (no wait event) are
// shown as type "CPU". Distribution bar shows each event's share of total.
func CheckWaitEvents(initialModel func() tea.Model) tea.Model {
	rows, err := config.Config.DB.Query(`
		SELECT
			COALESCE(wait_event_type, 'CPU') AS event_type,
			COALESCE(wait_event, '-') AS event,
			count(*) AS count,
			COALESCE(
				ROUND(100.0 * count(*) / NULLIF(SUM(count(*)) OVER(), 0), 1),
				0
			) AS pct
		FROM pg_stat_activity
		WHERE state = 'active' OR wait_event IS NOT NULL
		GROUP BY wait_event_type, wait_event
		ORDER BY count DESC
	`)
	if err != nil {
		return NewErrorModel(err, "Loading wait events", initialModel)
	}
	defer rows.Close()

	// Distribution bar is last column (multi-byte block chars safe for ColorizeTable).
	columns := []table.Column{
		{Title: "Type", Width: 18},
		{Title: "Event", Width: 25},
		{Title: "Count", Width: 8},
		{Title: "Distribution", Width: 22},
	}

	var rowsData []table.Row

	for rows.Next() {
		var eventType, event string
		var count int
		var pct float64

		if err := rows.Scan(&eventType, &event, &count, &pct); err != nil {
			return NewErrorModel(err, "Scanning wait events row", initialModel)
		}

		rowsData = append(rowsData, table.Row{
			eventType,
			event,
			fmt.Sprintf("%d", count),
			RenderBar(pct, 12),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return WaitEventsModel{table: t, initialModel: initialModel, height: 30}
}

func (m WaitEventsModel) Init() tea.Cmd { return nil }

func (m WaitEventsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.table.SetHeight(TableHeight(msg.Height))
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

	s := RenderHeader("Wait Events") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + legend
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
