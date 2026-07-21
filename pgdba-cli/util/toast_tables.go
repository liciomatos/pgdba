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

type ToastTablesModel struct {
	table         table.Model
	allRows       []table.Row
	filterText    string
	filterMode    bool
	// toastRelnames maps "schema.table" → toast relname (e.g. "pg_toast_16384") for VACUUM.
	toastRelnames map[string]string
	confirmVacuum bool
	schemaName    string
	tableName     string
	toastRelname  string
	initialModel  func() tea.Model
	width         int
	height        int
}

func (m ToastTablesModel) IsInputMode() bool { return m.filterMode }

func toastTableColumns() []table.Column {
	return []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 22},
		{Title: "Toast Size", Width: 12},
		{Title: "Toast %", Width: 9},
		{Title: "Dead Tuples", Width: 12},
		{Title: "Cache Hit %", Width: 12},
		{Title: "Last Autovacuum", Width: 18},
		{Title: "Toast Columns", Width: 24}, // stretch col — column names benefit more from width
	}
}

func CheckToastTables(initialModel func() tea.Model) tea.Model {
	toastTables, err := FetchToastTables(context.Background(), config.Config.DB, 50)
	if err != nil {
		return NewErrorModel(err, "Loading TOAST tables", initialModel)
	}

	formatTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	}

	toastRelnames := make(map[string]string, len(toastTables))
	var rowsData []table.Row
	for _, tt := range toastTables {
		cacheHitStr := "N/A"
		if tt.CacheHitPct != nil {
			cacheHitStr = fmt.Sprintf("%.1f%%", *tt.CacheHitPct)
		}
		rowsData = append(rowsData, table.Row{
			tt.SchemaName,
			tt.TableName,
			tt.ToastSizePretty,
			fmt.Sprintf("%.1f%%", tt.ToastPct),
			fmt.Sprintf("%d", tt.ToastDeadTuples),
			cacheHitStr,
			formatTime(tt.LastAutovacuum),
			tt.ToastColumns,
		})
		toastRelnames[tt.SchemaName+"."+tt.TableName] = tt.ToastRelname
	}

	columns := toastTableColumns()
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ToastTablesModel{
		table:         t,
		allRows:       rowsData,
		toastRelnames: toastRelnames,
		initialModel:  initialModel,
		height:        30,
	}
}

func (m ToastTablesModel) Init() tea.Cmd { return nil }

func (m ToastTablesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := StretchColumn(m.table.Columns(), 7, msg.Width)
		m.table.SetColumns(cols)
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
			return CheckToastTables(m.initialModel), nil
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
				return CheckToastTables(parent)
			}), nil
		case "v":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) < 2 {
				return m, nil
			}
			m.schemaName = selectedRow[0]
			m.tableName = selectedRow[1]
			m.toastRelname = m.toastRelnames[m.schemaName+"."+m.tableName]
			m.confirmVacuum = true
			return m, nil
		case "y":
			if m.confirmVacuum {
				// VACUUM on TOAST tables requires schema-qualified name; cannot use $1 params.
				query := fmt.Sprintf("VACUUM pg_toast.%s",
					pq.QuoteIdentifier(m.toastRelname))
				if _, err := config.Config.DB.Exec(query); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("VACUUM pg_toast.%s", m.toastRelname),
						m.initialModel), nil
				}
				return CheckToastTables(m.initialModel), nil
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

func (m ToastTablesModel) View() string {
	rules := []ColorRule{
		{Column: 3, Colorize: func(v string) int {
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f > 50:
				return 2
			case f > 20:
				return 1
			default:
				return 0
			}
		}},
		{Column: 4, Colorize: func(v string) int {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return -1
			}
			switch {
			case n > 100000:
				return 2
			case n > 10000:
				return 1
			default:
				return 0
			}
		}},
		{Column: 5, Colorize: func(v string) int {
			if strings.TrimSpace(v) == "N/A" {
				return 3
			}
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f < 80:
				return 2
			case f < 95:
				return 1
			default:
				return 0
			}
		}},
	}

	s := RenderHeader("TOAST Tables") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmVacuum {
		s += fmt.Sprintf("\nVACUUM pg_toast.%s? (y/n)\n", m.toastRelname)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText,
			"enter parent detail • v vacuum toast • r refresh • q back")
	}
	return s
}
