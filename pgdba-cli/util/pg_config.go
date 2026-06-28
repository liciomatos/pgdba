package util

import (
	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type PgConfigModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m PgConfigModel) IsInputMode() bool { return m.filterMode }

func CheckPgConfig(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT name,
               setting,
               COALESCE(unit, '') AS unit,
               category,
               source,
               COALESCE(short_desc, '') AS description
        FROM pg_settings
        ORDER BY category, name;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading pg_settings", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Setting", Width: 20},
		{Title: "Unit", Width: 6},
		{Title: "Category", Width: 25},
		{Title: "Source", Width: 12},
		{Title: "Description", Width: 50},
	}

	var rowsData []table.Row
	for rows.Next() {
		var name, setting, unit, category, source, description string
		if err := rows.Scan(&name, &setting, &unit, &category, &source, &description); err != nil {
			return NewErrorModel(err, "Scanning pg_settings row", initialModel)
		}
		rowsData = append(rowsData, table.Row{name, setting, unit, category, source, description})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return PgConfigModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
}

func (m PgConfigModel) Init() tea.Cmd { return nil }

func (m PgConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := StretchColumn(m.table.Columns(), 5, msg.Width)
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
			return CheckPgConfig(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m PgConfigModel) View() string {
	s := RenderHeader("Config Parameters") + "\n"
	s += m.table.View()
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}
