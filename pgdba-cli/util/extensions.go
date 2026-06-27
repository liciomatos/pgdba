package util

import (
	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type ExtensionsModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m ExtensionsModel) IsInputMode() bool { return m.filterMode }

func CheckExtensions(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT e.extname AS name,
               e.extversion AS version,
               n.nspname AS schema,
               COALESCE(ae.comment, '-') AS description
        FROM pg_extension e
        JOIN pg_namespace n ON n.oid = e.extnamespace
        LEFT JOIN pg_available_extensions ae ON ae.name = e.extname
        ORDER BY e.extname;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading extensions", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Version", Width: 10},
		{Title: "Schema", Width: 15},
		{Title: "Description", Width: 60},
	}

	var rowsData []table.Row
	for rows.Next() {
		var name, version, schema, description string
		if err := rows.Scan(&name, &version, &schema, &description); err != nil {
			return NewErrorModel(err, "Scanning extensions row", initialModel)
		}
		rowsData = append(rowsData, table.Row{name, version, schema, description})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ExtensionsModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
}

func (m ExtensionsModel) Init() tea.Cmd { return nil }

func (m ExtensionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := StretchColumn(m.table.Columns(), 3, msg.Width)
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
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckExtensions(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m ExtensionsModel) View() string {
	s := RenderHeader("Extensions") + "\n"
	s += m.table.View()
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}
