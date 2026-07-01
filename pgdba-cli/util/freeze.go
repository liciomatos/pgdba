package util

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/lib/pq"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type FreezeModel struct {
	tableModel    table.Model
	currentDB     FreezeDatabaseStatus
	initialModel  func() tea.Model
	confirmFreeze bool
	schemaName    string
	tableName     string
	width         int
}

func tableXIDColumns() []table.Column {
	return []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 28},
		{Title: "XID Age", Width: 12},
		{Title: "MXI Age", Width: 12},
		{Title: "% Freeze", Width: 10},
		{Title: "Size", Width: 10},
		{Title: "Last Autovacuum", Width: 18},
	}
}

func CheckFreezeMonitor(initialModel func() tea.Model) tea.Model {
	dbStatus, err := FetchFreezeByDatabase(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading freeze status by database", initialModel)
	}
	tableStatus, err := FetchFreezeByTable(context.Background(), config.Config.DB, 50)
	if err != nil {
		return NewErrorModel(err, "Loading freeze status by table", initialModel)
	}

	var currentDB FreezeDatabaseStatus
	for _, d := range dbStatus {
		if d.DatabaseName == config.Config.DBName {
			currentDB = d
			break
		}
	}

	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	}

	var tableRows []table.Row
	for _, t := range tableStatus {
		tableRows = append(tableRows, table.Row{
			t.SchemaName,
			t.TableName,
			fmt.Sprintf("%d", t.XIDAge),
			fmt.Sprintf("%d", t.MXIDAge),
			fmt.Sprintf("%.1f%%", t.PctTowardFreeze),
			t.TotalSize,
			fmtTime(t.LastAutovacuum),
		})
	}

	tbl := table.New(
		table.WithColumns(tableXIDColumns()),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return FreezeModel{
		tableModel:   tbl,
		currentDB:    currentDB,
		initialModel: initialModel,
	}
}

func (m FreezeModel) Init() tea.Cmd { return nil }

// ConsumesKey prevents the navigator from intercepting "f" and re-opening the
// Freeze Monitor while already on it — here "f" triggers VACUUM FREEZE.
func (m FreezeModel) ConsumesKey(key string) bool {
	return key == "f"
}

func (m FreezeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.tableModel.SetHeight(TableHeight(msg.Height))
		cols := StretchColumn(m.tableModel.Columns(), 1, msg.Width)
		m.tableModel.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.confirmFreeze {
				m.confirmFreeze = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckFreezeMonitor(m.initialModel), nil
		case "f":
			if m.confirmFreeze {
				m.confirmFreeze = false
				return m, nil
			}
			selectedRow := m.tableModel.SelectedRow()
			if len(selectedRow) < 2 {
				return m, nil
			}
			m.schemaName = selectedRow[0]
			m.tableName = selectedRow[1]
			m.confirmFreeze = true
			return m, nil
		case "y":
			if m.confirmFreeze {
				m.confirmFreeze = false
				query := fmt.Sprintf("VACUUM (FREEZE, ANALYZE) %s.%s",
					pq.QuoteIdentifier(m.schemaName),
					pq.QuoteIdentifier(m.tableName))
				if _, err := config.Config.DB.Exec(query); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("VACUUM FREEZE %s.%s", m.schemaName, m.tableName),
						m.initialModel), nil
				}
				return CheckFreezeMonitor(m.initialModel), nil
			}
		case "n":
			if m.confirmFreeze {
				m.confirmFreeze = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.tableModel, cmd = m.tableModel.Update(msg)
	return m, cmd
}

func (m FreezeModel) View() string {
	tableRules := []ColorRule{
		{Column: 4, Colorize: func(v string) int {
			var pct float64
			fmt.Sscanf(v, "%f%%", &pct)
			switch {
			case pct > 75:
				return 2
			case pct > 50:
				return 1
			default:
				return 0
			}
		}},
	}

	renderKey := lipgloss.NewStyle().Foreground(ColorGray).Render

	db := m.currentDB
	level := db.Status
	if level < 0 || level > 2 {
		level = 0
	}
	statusLabel := []string{"OK", "Warning", "Critical"}[level]

	summaryLine := fmt.Sprintf("  %s %s   %s %s   %s %s   %s %s",
		renderKey("Database:"), SeverityColor(db.DatabaseName, level),
		renderKey("XID Age:"), SeverityColor(fmt.Sprintf("%d", db.DBXIDAge), level),
		renderKey("% Shutdown:"), SeverityColor(fmt.Sprintf("%.2f%%", db.PctTowardShutdown), level),
		renderKey("Status:"), SeverityColor(statusLabel, level),
	)

	s := RenderHeader("Freeze Monitor") + "\n"
	s += summaryLine + "\n"
	s += HintStyle.Render("  XIDs wrap at ~2.1B — PostgreSQL refuses writes at the limit. VACUUM FREEZE resets the counter.") + "\n\n"
	s += ColorizeTable(m.tableModel.View(), m.tableModel.Columns(), tableRules)

	if m.confirmFreeze {
		s += fmt.Sprintf("\nVACUUM (FREEZE, ANALYZE) %s.%s? (y/n) — this can be slow on large tables\n",
			m.schemaName, m.tableName)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • f vacuum freeze • D switch database • r refresh • q back")
	}
	return s
}
