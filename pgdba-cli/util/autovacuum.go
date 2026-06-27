package util

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/lib/pq"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type AutovacuumModel struct {
	table         table.Model
	initialModel  func() tea.Model
	confirmVacuum bool
	schemaName    string
	tableName     string
	height        int
}

func CheckAutovacuum(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            schemaname,
            relname,
            n_dead_tup,
            n_live_tup,
            CASE WHEN n_live_tup + n_dead_tup = 0 THEN NULL
                 ELSE ROUND(100.0 * n_dead_tup / (n_live_tup + n_dead_tup), 1)
            END AS dead_pct,
            last_vacuum,
            last_analyze,
            last_autovacuum,
            last_autoanalyze,
            autovacuum_count
        FROM pg_stat_user_tables
        ORDER BY n_dead_tup DESC
        LIMIT 20;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum monitor", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 25},
		{Title: "Dead Tuples", Width: 12},
		{Title: "Live Tuples", Width: 12},
		{Title: "Dead %", Width: 8},
		{Title: "Last Vacuum", Width: 18},
		{Title: "Last Analyze", Width: 18},
		{Title: "Last Autovacuum", Width: 18},
		{Title: "Last Autoanalyze", Width: 18},
		{Title: "Vac Count", Width: 10},
	}

	var rowsData []table.Row
	for rows.Next() {
		var schemaname, relname string
		var nDeadTup, nLiveTup, autovacuumCount int64
		var deadPct sql.NullFloat64
		var lastVacuum, lastAnalyze, lastAutovacuum, lastAutoanalyze *time.Time

		if err := rows.Scan(&schemaname, &relname, &nDeadTup, &nLiveTup, &deadPct,
			&lastVacuum, &lastAnalyze, &lastAutovacuum, &lastAutoanalyze, &autovacuumCount); err != nil {
			return NewErrorModel(err, "Scanning autovacuum row", initialModel)
		}

		formatTime := func(t *time.Time) string {
			if t == nil {
				return "never"
			}
			return t.Format("2006-01-02 15:04")
		}

		deadPctStr := "N/A"
		if deadPct.Valid {
			raw := fmt.Sprintf("%.1f%%", deadPct.Float64)
			switch {
			case deadPct.Float64 > 30:
				deadPctStr = SeverityColor(raw, 2)
			case deadPct.Float64 > 10:
				deadPctStr = SeverityColor(raw, 1)
			default:
				deadPctStr = SeverityColor(raw, 0)
			}
		}

		rowsData = append(rowsData, table.Row{
			schemaname,
			relname,
			fmt.Sprintf("%d", nDeadTup),
			fmt.Sprintf("%d", nLiveTup),
			deadPctStr,
			formatTime(lastVacuum),
			formatTime(lastAnalyze),
			formatTime(lastAutovacuum),
			formatTime(lastAutoanalyze),
			fmt.Sprintf("%d", autovacuumCount),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumModel{table: t, initialModel: initialModel, height: 30}
}

func (m AutovacuumModel) Init() tea.Cmd { return nil }

func (m AutovacuumModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.table.SetHeight(TableHeight(msg.Height))
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckAutovacuum(m.initialModel), nil
		case "v":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			m.schemaName = selectedRow[0]
			m.tableName = selectedRow[1]
			m.confirmVacuum = true
			return m, nil
		case "y":
			if m.confirmVacuum {
				query := fmt.Sprintf("VACUUM ANALYZE %s.%s",
					pq.QuoteIdentifier(m.schemaName),
					pq.QuoteIdentifier(m.tableName))
				if _, err := config.Config.DB.Exec(query); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("VACUUM ANALYZE %s.%s", m.schemaName, m.tableName),
						m.initialModel), nil
				}
				return CheckAutovacuum(m.initialModel), nil
			}
		case "n":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m AutovacuumModel) View() string {
	s := RenderHeader("Autovacuum Monitor") + "\n"
	s += m.table.View()
	if m.confirmVacuum {
		s += fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n", m.schemaName, m.tableName)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • v vacuum analyze • r refresh • q back")
	}
	return s
}
