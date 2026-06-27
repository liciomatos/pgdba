package util

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type IndexDetailModel struct {
	table        table.Model
	schema       string
	tableName    string
	indexName    string
	indexType    string
	indexSize    string
	idxScan      int64
	isUnique     bool
	isPrimary    bool
	isValid      bool
	initialModel func() tea.Model
	width        int
	height       int
}

func CheckIndexDetail(schema, indexName string, initialModel func() tea.Model) tea.Model {
	// Metadata query
	var tableName, indexType, indexSize string
	var isUnique, isPrimary, isValid bool
	var idxScan int64

	err := config.Config.DB.QueryRow(`
        SELECT
            t.relname,
            am.amname,
            i.indisunique,
            i.indisprimary,
            i.indisvalid,
            COALESCE(s.idx_scan, 0),
            pg_size_pretty(pg_relation_size(c.oid))
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_class t ON t.oid = i.indrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_am am ON am.oid = c.relam
        LEFT JOIN pg_stat_user_indexes s ON s.indexrelid = c.oid
        WHERE n.nspname = $1 AND c.relname = $2
    `, schema, indexName).Scan(&tableName, &indexType, &isUnique, &isPrimary, &isValid, &idxScan, &indexSize)
	if err != nil {
		return NewErrorModel(err, "Loading index metadata", initialModel)
	}

	// Columns query
	rows, err := config.Config.DB.Query(`
        SELECT
            x.ord::int,
            COALESCE(a.attname, '(expression)') AS column_name,
            COALESCE(pg_catalog.format_type(a.atttypid, a.atttypmod), '') AS data_type,
            CASE WHEN a.attnotnull THEN 'NOT NULL' ELSE 'nullable' END AS nullable
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS x(attnum, ord) ON true
        LEFT JOIN pg_attribute a
            ON a.attrelid = i.indrelid AND a.attnum = x.attnum AND x.attnum > 0
        WHERE n.nspname = $1 AND c.relname = $2
        ORDER BY x.ord
    `, schema, indexName)
	if err != nil {
		return NewErrorModel(err, "Loading index columns", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "#", Width: 4},
		{Title: "Column", Width: 30},
		{Title: "Type", Width: 35},
		{Title: "Nullable", Width: 10},
	}

	var rowsData []table.Row
	for rows.Next() {
		var pos int
		var colName, dataType, nullable string
		if err := rows.Scan(&pos, &colName, &dataType, &nullable); err != nil {
			return NewErrorModel(err, "Scanning index columns", initialModel)
		}
		nullStr := SeverityColor("nullable", 0)
		if nullable == "NOT NULL" {
			nullStr = lipgloss.NewStyle().Foreground(ColorGray).Render("NOT NULL")
		}
		rowsData = append(rowsData, table.Row{
			fmt.Sprintf("%d", pos),
			colName,
			dataType,
			nullStr,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(10),
		table.WithStyles(DefaultTableStyles()),
	)

	return IndexDetailModel{
		table:        t,
		schema:       schema,
		tableName:    tableName,
		indexName:    indexName,
		indexType:    indexType,
		indexSize:    indexSize,
		idxScan:      idxScan,
		isUnique:     isUnique,
		isPrimary:    isPrimary,
		isValid:      isValid,
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func (m IndexDetailModel) Init() tea.Cmd { return nil }

func (m IndexDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Meta block takes ~7 lines; leave rest for table
		th := m.height - 12
		if th < 3 {
			th = 3
		}
		m.table.SetHeight(th)
		cols := StretchColumn(m.table.Columns(), 2, m.width)
		m.table.SetColumns(cols)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckIndexDetail(m.schema, m.indexName, m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m IndexDetailModel) View() string {
	s := RenderHeader("Index Detail") + "\n"

	// Metadata block
	lbl := lipgloss.NewStyle().Width(12).Foreground(ColorGray).Render
	val := lipgloss.NewStyle().Foreground(ColorWhite).Render
	blu := lipgloss.NewStyle().Foreground(ColorBlue).Render

	validStr := SeverityColor("yes", 0)
	if !m.isValid {
		validStr = SeverityColor("INVALID", 2)
	}
	boolVal := func(b bool) string {
		if b {
			return SeverityColor("yes", 0)
		}
		return lipgloss.NewStyle().Foreground(ColorGray).Render("no")
	}

	s += fmt.Sprintf("  %s %s\n", lbl("Schema:"), val(m.schema))
	s += fmt.Sprintf("  %s %s\n", lbl("Table:"), val(m.tableName))
	s += fmt.Sprintf("  %s %s\n", lbl("Index:"), val(m.indexName))
	s += fmt.Sprintf("  %s %s   Unique: %s   Primary: %s   Valid: %s\n",
		lbl("Type:"), blu(m.indexType),
		boolVal(m.isUnique), boolVal(m.isPrimary), validStr)
	s += fmt.Sprintf("  %s %s   Scans: %s\n\n",
		lbl("Size:"), val(m.indexSize),
		lipgloss.NewStyle().Foreground(ColorWhite).Render(fmt.Sprintf("%d", m.idxScan)))

	s += m.table.View()
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q/esc back to index list")
	return s
}
