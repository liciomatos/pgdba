package util

import (
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type SchemaBrowserModel struct {
	table          table.Model
	allRows        []table.Row
	tableRows      []table.Row // saved table-list rows to restore from columns mode
	filterText     string
	filterMode     bool
	mode           string // "tables" | "columns"
	selectedSchema string
	selectedTable  string
	initialModel   func() tea.Model
	width          int
	height         int
}

func (m SchemaBrowserModel) IsInputMode() bool { return m.filterMode }

var schemaBrowserTableColumns = []table.Column{
	{Title: "Schema", Width: 15},
	{Title: "Table", Width: 30},
	{Title: "Size", Width: 12},
	{Title: "Est. Rows", Width: 12},
}

var schemaBrowserColumnColumns = []table.Column{
	{Title: "Column", Width: 25},
	{Title: "Type", Width: 20},
	{Title: "Length", Width: 8},
	{Title: "Nullable", Width: 9},
	{Title: "Default", Width: 30},
}

func CheckSchemaBrowser(initialModel func() tea.Model) tea.Model {
	rows, allRows, err := loadSchemaTables()
	if err != nil {
		return NewErrorModel(err, "Loading schema tables", initialModel)
	}

	t := table.New(
		table.WithColumns(schemaBrowserTableColumns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return SchemaBrowserModel{
		table:        t,
		allRows:      allRows,
		tableRows:    allRows,
		mode:         "tables",
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func loadSchemaTables() ([]table.Row, []table.Row, error) {
	query := `
        SELECT t.table_schema, t.table_name,
               pg_size_pretty(pg_total_relation_size(
                   quote_ident(t.table_schema)||'.'||quote_ident(t.table_name)
               )) AS size,
               c.reltuples::bigint AS est_rows
        FROM information_schema.tables t
        JOIN pg_class c ON c.relname = t.table_name
        JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = t.table_schema
        WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
          AND t.table_type = 'BASE TABLE'
        ORDER BY t.table_schema, t.table_name;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var rowsData []table.Row
	for rows.Next() {
		var schema, tableName, size string
		var estRows int64
		if err := rows.Scan(&schema, &tableName, &size, &estRows); err != nil {
			return nil, nil, err
		}
		rowsData = append(rowsData, table.Row{schema, tableName, size, fmt.Sprintf("%d", estRows)})
	}
	return rowsData, rowsData, nil
}

func loadTableColumns(schema, tableName string) ([]table.Row, error) {
	query := `
        SELECT column_name, data_type,
               COALESCE(character_maximum_length::text,
                        numeric_precision::text, '') AS length,
               is_nullable,
               COALESCE(column_default, '') AS default_val
        FROM information_schema.columns
        WHERE table_schema = $1 AND table_name = $2
        ORDER BY ordinal_position;
    `

	rows, err := config.Config.DB.Query(query, schema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rowsData []table.Row
	for rows.Next() {
		var colName, dataType, length, nullable, defaultVal string
		if err := rows.Scan(&colName, &dataType, &length, &nullable, &defaultVal); err != nil {
			return nil, err
		}
		rowsData = append(rowsData, table.Row{colName, dataType, length, nullable, defaultVal})
	}
	return rowsData, nil
}

func (m SchemaBrowserModel) Init() tea.Cmd { return nil }

func (m SchemaBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
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
			if m.mode == "tables" {
				m.filterMode = true
			}
			return m, nil
		case "q", "esc":
			if m.mode == "columns" {
				m.mode = "tables"
				m.allRows = m.tableRows
				m.filterText = ""
				m.table.SetRows([]table.Row{})
				m.table.SetColumns(schemaBrowserTableColumns)
				m.table.SetRows(m.tableRows)
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			if m.mode == "columns" {
				m.mode = "tables"
				m.allRows = m.tableRows
				m.filterText = ""
				m.table.SetRows([]table.Row{})
				m.table.SetColumns(schemaBrowserTableColumns)
				m.table.SetRows(m.tableRows)
				return m, nil
			}
			return CheckSchemaBrowser(m.initialModel), nil
		case "enter":
			if m.mode == "tables" {
				selectedRow := m.table.SelectedRow()
				if len(selectedRow) < 2 {
					return m, nil
				}
				schema := selectedRow[0]
				tableName := selectedRow[1]
				cols, err := loadTableColumns(schema, tableName)
				if err != nil {
					return NewErrorModel(err, fmt.Sprintf("Describing %s.%s", schema, tableName), m.initialModel), nil
				}
				m.selectedSchema = schema
				m.selectedTable = tableName
				m.mode = "columns"
				m.allRows = cols
				m.filterText = ""
				m.table.SetRows([]table.Row{})
				m.table.SetColumns(schemaBrowserColumnColumns)
				m.table.SetRows(cols)
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m SchemaBrowserModel) View() string {
	var title string
	var hints string
	if m.mode == "columns" {
		title = fmt.Sprintf("Schema Browser — %s.%s", m.selectedSchema, m.selectedTable)
		hints = "↑↓ navigate • esc/r back to tables • q back"
	} else {
		title = "Schema Browser"
		hints = "↑↓ navigate • enter describe • r refresh • q back"
	}
	s := RenderHeader(title) + "\n"
	s += m.table.View()
	if m.mode == "columns" {
		s += "\n" + FooterStyle.Render(hints)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, hints)
	}
	return s
}
