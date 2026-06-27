package util

import (
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type IndexUsageModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m IndexUsageModel) IsInputMode() bool { return m.filterMode }

func CheckIndexUsage(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            s.schemaname,
            s.relname AS tablename,
            s.indexrelname AS indexname,
            s.idx_scan,
            s.idx_tup_read,
            s.idx_tup_fetch,
            pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size,
            i.indisvalid AS is_valid,
            COALESCE((
                SELECT string_agg(a.attname, ', ' ORDER BY x.ord)
                FROM pg_index ix
                JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS x(attnum, ord) ON true
                JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = x.attnum
                WHERE ix.indexrelid = s.indexrelid AND x.attnum > 0
            ), '(expression)') AS index_columns
        FROM pg_stat_user_indexes s
        JOIN pg_index i ON i.indexrelid = s.indexrelid
        ORDER BY s.idx_scan ASC
        LIMIT 20;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading index usage", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 20},
		{Title: "Index", Width: 28},
		{Title: "Columns", Width: 20},
		{Title: "Valid", Width: 9},
		{Title: "Scans", Width: 8},
		{Title: "Tup Read", Width: 10},
		{Title: "Tup Fetch", Width: 10},
		{Title: "Size", Width: 8},
	}

	var rowsData []table.Row
	for rows.Next() {
		var schemaname, tablename, indexname, indexSize, indexColumns string
		var idxScan, idxTupRead, idxTupFetch int64
		var isValid bool

		if err := rows.Scan(&schemaname, &tablename, &indexname, &idxScan, &idxTupRead, &idxTupFetch, &indexSize, &isValid, &indexColumns); err != nil {
			return NewErrorModel(err, "Scanning index usage row", initialModel)
		}

		scanStr := fmt.Sprintf("%d", idxScan)

		validStr := "yes"
		if !isValid {
			validStr = "INVALID"
		}

		rowsData = append(rowsData, table.Row{
			schemaname, tablename, indexname, indexColumns,
			validStr, scanStr,
			fmt.Sprintf("%d", idxTupRead),
			fmt.Sprintf("%d", idxTupFetch),
			indexSize,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return IndexUsageModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
}

func (m IndexUsageModel) Init() tea.Cmd { return nil }

func (m IndexUsageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "enter":
			row := m.table.SelectedRow()
			if len(row) >= 3 {
				schema := row[0]
				indexName := row[2]
				back := func() tea.Model { return CheckIndexUsage(m.initialModel) }
				return CheckIndexDetail(schema, indexName, back), nil
			}
			return m, nil
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckIndexUsage(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m IndexUsageModel) View() string {
	rules := []ColorRule{
		{Column: 4, Colorize: func(v string) int {
			switch v {
			case "yes":
				return 0
			case "INVALID":
				return 2
			}
			return -1
		}},
		{Column: 5, Colorize: func(v string) int {
			if v == "0" {
				return 2
			}
			return -1
		}},
	}
	s := RenderHeader("Index Usage") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • / filter • r refresh • q back")
	return s
}
