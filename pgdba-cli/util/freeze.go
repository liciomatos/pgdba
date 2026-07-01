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
	dbStatus      []FreezeDatabaseStatus
	table         table.Model
	allRows       []table.Row
	initialModel  func() tea.Model
	confirmFreeze bool
	schemaName    string
	tableName     string
	height        int
	width         int
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

	columns := []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 28},
		{Title: "XID Age", Width: 12},
		{Title: "MXI Age", Width: 12},
		{Title: "% Freeze", Width: 10},
		{Title: "Size", Width: 10},
		{Title: "Last Autovacuum", Width: 18},
	}

	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	}

	var rowsData []table.Row
	for _, t := range tableStatus {
		rowsData = append(rowsData, table.Row{
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
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return FreezeModel{
		dbStatus:     dbStatus,
		table:        tbl,
		allRows:      rowsData,
		initialModel: initialModel,
		height:       40,
		width:        120,
	}
}

func (m FreezeModel) Init() tea.Cmd { return nil }

func (m FreezeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Database summary takes ~(len(dbStatus)+3) lines; rest for table
		dbHeaderLines := len(m.dbStatus) + 4
		tableH := m.height - dbHeaderLines - 3
		if tableH < 5 {
			tableH = 5
		}
		m.table.SetHeight(tableH)
		cols := StretchColumn(m.table.Columns(), 1, m.width)
		m.table.SetColumns(cols)
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
			selectedRow := m.table.SelectedRow()
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
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m FreezeModel) View() string {
	s := RenderHeader("Freeze Monitor") + "\n"

	renderLabel := lipgloss.NewStyle().Width(20).Foreground(ColorGray).Render

	// Database summary
	s += lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("DATABASE XID STATUS") + "\n"
	s += fmt.Sprintf("  %-22s  %-16s  %-12s  %s\n",
		lipgloss.NewStyle().Foreground(ColorGray).Render("Database"),
		lipgloss.NewStyle().Foreground(ColorGray).Render("XID Age"),
		lipgloss.NewStyle().Foreground(ColorGray).Render("% Shutdown"),
		lipgloss.NewStyle().Foreground(ColorGray).Render("Status"))
	for _, db := range m.dbStatus {
		statusStr := SeverityColor("OK", 0)
		if db.Status == 1 {
			statusStr = SeverityColor("Warning", 1)
		} else if db.Status >= 2 {
			statusStr = SeverityColor("Critical", 2)
		}
		s += fmt.Sprintf("  %-22s  %s  %s  %s\n",
			db.DatabaseName,
			renderLabel(fmt.Sprintf("%d", db.DBXIDAge)),
			SeverityColor(fmt.Sprintf("%.2f%%", db.PctTowardShutdown), db.Status),
			statusStr)
	}
	s += "\n"

	// Table section
	s += lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("TOP TABLES BY XID AGE") + "\n"

	rules := []ColorRule{
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
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)

	if m.confirmFreeze {
		s += fmt.Sprintf("\nVACUUM (FREEZE, ANALYZE) %s.%s? (y/n) — this can be slow on large tables\n",
			m.schemaName, m.tableName)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • f vacuum freeze selected table • r refresh • q back")
	}
	return s
}