package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/liciomatos/pgdba-cli/config"
	"github.com/lib/pq"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type AutovacuumModel struct {
	table         table.Model
	allRows       []table.Row
	filterText    string
	filterMode    bool
	initialModel  func() tea.Model
	confirmVacuum bool
	schemaName    string
	tableName     string
	saturation    AutovacuumWorkerSaturation
	height        int
}

func (m AutovacuumModel) IsInputMode() bool { return m.filterMode }

// vacuumStatusText returns the Status column value for a table, cross-referencing
// live pg_stat_progress_vacuum activity against the dead-tuple snapshot below —
// "idle" doesn't mean unattended, just that nothing is running on it right now.
func vacuumStatusText(isAutovacuum bool, active bool) string {
	if !active {
		return "idle"
	}
	if isAutovacuum {
		return "auto vacuum"
	}
	return "manual vacuum"
}

func CheckAutovacuum(initialModel func() tea.Model) tea.Model {
	tables, err := FetchAutovacuum(context.Background(), config.Config.DB, 20)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum monitor", initialModel)
	}
	saturation, err := FetchAutovacuumWorkerSaturation(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum worker saturation", initialModel)
	}
	activity, err := FetchAutovacuumActivity(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum activity", initialModel)
	}
	activeIsAutovacuum := make(map[string]bool, len(activity))
	for _, w := range activity {
		activeIsAutovacuum[w.SchemaName+"."+w.TableName] = w.IsAutovacuum
	}

	columns := []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 25},
		{Title: "Status", Width: 14},
		{Title: "Dead Tuples", Width: 12},
		{Title: "Dead %", Width: 8},
		{Title: "Size", Width: 10},
		{Title: "Last Autovacuum", Width: 18},
		{Title: "Autovac Count", Width: 14},
	}

	formatTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	}

	var rowsData []table.Row
	for _, av := range tables {
		deadPctStr := "N/A"
		if av.DeadPct != nil {
			deadPctStr = fmt.Sprintf("%.1f%%", *av.DeadPct)
		}
		isAutovacuum, active := activeIsAutovacuum[av.SchemaName+"."+av.TableName]
		rowsData = append(rowsData, table.Row{
			av.SchemaName,
			av.TableName,
			vacuumStatusText(isAutovacuum, active),
			fmt.Sprintf("%d", av.DeadTuples),
			deadPctStr,
			av.TotalSize,
			formatTime(av.LastAutovacuum),
			fmt.Sprintf("%d", av.AutovacuumCount),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumModel{
		table:        t,
		allRows:      rowsData,
		initialModel: initialModel,
		saturation:   saturation,
		height:       30,
	}
}

func (m AutovacuumModel) Init() tea.Cmd { return nil }

func (m AutovacuumModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		// -3 for the worker-saturation line, hint line, and blank line above the table.
		m.table.SetHeight(TableHeight(msg.Height) - 3)
		return m, nil
	case tea.KeyMsg:
		if m.filterMode {
			switch msg.Type {
			case tea.KeyEsc:
				m.filterMode = false
				m.filterText = ""
				m.table.SetRows(m.allRows)
			case tea.KeyBackspace:
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.table.SetRows(FilterRows(m.allRows, m.filterText))
				}
			case tea.KeyRunes:
				m.filterText += msg.String()
				m.table.SetRows(FilterRows(m.allRows, m.filterText))
			case tea.KeyEnter:
				m.filterMode = false
			}
			return m, nil
		}
		switch msg.String() {
		case "/":
			if !m.confirmVacuum {
				m.filterMode = true
			}
			return m, nil
		case "q", "esc":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckAutovacuum(m.initialModel), nil
		case "enter":
			if m.confirmVacuum {
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) < 2 {
				return m, nil
			}
			schema, tableName := selectedRow[0], selectedRow[1]
			parent := m.initialModel
			return CheckAutovacuumDetail(schema, tableName, func() tea.Model {
				return CheckAutovacuum(parent)
			}), nil
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
	rules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			if v == "idle" {
				return 3
			}
			return 0
		}},
		{Column: 4, Colorize: func(v string) int {
			// Values are "N/A" or "30.1%"; strip % and parse.
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f > 30:
				return 2
			case f > 10:
				return 1
			default:
				return 0
			}
		}},
	}

	level := 0
	switch {
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers:
		level = 2
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers/2:
		level = 1
	}
	saturationLine := fmt.Sprintf("  %s %s",
		HintStyle.Render("Autovacuum Workers:"),
		SeverityColor(fmt.Sprintf("%d / %d running", m.saturation.RunningWorkers, m.saturation.MaxWorkers), level),
	)

	s := RenderHeader("Autovacuum Monitor") + "\n"
	s += saturationLine + "\n"
	s += HintStyle.Render("  Status shows what's vacuuming right now; the list below is ranked by dead tuples.") + "\n\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmVacuum {
		s += fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n", m.schemaName, m.tableName)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "enter detail • v vacuum analyze • r refresh • q back")
	}
	return s
}
