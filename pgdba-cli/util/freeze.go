package util

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/lib/pq"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type FreezeModel struct {
	dbTable      table.Model // informational only — no keyboard focus
	tableModel   table.Model // current-DB tables, always focused
	dbStatus     []FreezeDatabaseStatus
	allTableRows []table.Row
	initialModel func() tea.Model
	confirmFreeze bool
	schemaName    string
	tableName     string
	height        int
	width         int
}

func dbFreezeColumns() []table.Column {
	return []table.Column{
		{Title: "Database", Width: 20},
		{Title: "XID Age", Width: 16},
		{Title: "% Toward Shutdown", Width: 20},
		{Title: "Status", Width: 10},
	}
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

// buildDBRows pre-colors Status and % Toward Shutdown so the severity is visible
// even on the selected row (short values are safe from runewidth ANSI miscounting).
func buildDBRows(dbStatus []FreezeDatabaseStatus) []table.Row {
	var rows []table.Row
	for _, db := range dbStatus {
		level := db.Status
		rows = append(rows, table.Row{
			db.DatabaseName,
			fmt.Sprintf("%d", db.DBXIDAge),
			SeverityColor(fmt.Sprintf("%.2f%%", db.PctTowardShutdown), level),
			SeverityColor([]string{"OK", "Warning", "Critical"}[min3(level, 2)], level),
		})
	}
	return rows
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
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

	dbTbl := table.New(
		table.WithColumns(dbFreezeColumns()),
		table.WithRows(buildDBRows(dbStatus)),
		table.WithFocused(false),
		table.WithHeight(len(dbStatus)),
		table.WithStyles(InfoTableStyles()),
	)

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

	tableTbl := table.New(
		table.WithColumns(tableXIDColumns()),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return FreezeModel{
		dbTable:      dbTbl,
		tableModel:   tableTbl,
		dbStatus:     dbStatus,
		allTableRows: tableRows,
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
		m.height = msg.Height
		// db table: 2 header lines + N rows + 1 blank + 1 hint line
		dbRenderedLines := 2 + len(m.dbStatus) + 2
		tableH := TableHeight(msg.Height) - dbRenderedLines
		if tableH < 4 {
			tableH = 4
		}
		m.tableModel.SetHeight(tableH)
		dbCols := StretchColumn(m.dbTable.Columns(), 0, msg.Width)
		m.dbTable.SetColumns(dbCols)
		tblCols := StretchColumn(m.tableModel.Columns(), 1, msg.Width)
		m.tableModel.SetColumns(tblCols)
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

	hint := "  XIDs overflow at ~2.1B transactions — PostgreSQL will refuse writes. VACUUM FREEZE resets the counter before that threshold."

	s := RenderHeader("Freeze Monitor") + "\n"
	s += HintStyle.Render(hint) + "\n"
	s += m.dbTable.View()
	s += "\n"

	currentDB := config.Config.DBName
	s += HintStyle.Render(fmt.Sprintf("  Tables in current database: %s  (use D to switch databases)", currentDB)) + "\n"
	s += ColorizeTable(m.tableModel.View(), m.tableModel.Columns(), tableRules)

	if m.confirmFreeze {
		s += fmt.Sprintf("\nVACUUM (FREEZE, ANALYZE) %s.%s? (y/n) — this can be slow on large tables\n",
			m.schemaName, m.tableName)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • f vacuum freeze • D switch database • r refresh • q back")
	}
	return s
}
