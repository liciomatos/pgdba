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
	height        int
}

func (m AutovacuumModel) IsInputMode() bool { return m.filterMode }

func CheckAutovacuum(initialModel func() tea.Model) tea.Model {
	tables, err := FetchAutovacuum(context.Background(), config.Config.DB, 20)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum monitor", initialModel)
	}

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
		rowsData = append(rowsData, table.Row{
			av.SchemaName,
			av.TableName,
			fmt.Sprintf("%d", av.DeadTuples),
			fmt.Sprintf("%d", av.LiveTuples),
			deadPctStr,
			formatTime(av.LastVacuum),
			formatTime(av.LastAnalyze),
			formatTime(av.LastAutovacuum),
			formatTime(av.LastAutoanalyze),
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

	return AutovacuumModel{table: t, allRows: rowsData, initialModel: initialModel, height: 30}
}

func (m AutovacuumModel) Init() tea.Cmd { return nil }

func (m AutovacuumModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.table.SetHeight(TableHeight(msg.Height))
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
	s := RenderHeader("Autovacuum Monitor") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmVacuum {
		s += fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n", m.schemaName, m.tableName)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • v vacuum analyze • r refresh • q back")
	}
	return s
}
