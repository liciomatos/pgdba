package util

import (
	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type RolesModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m RolesModel) IsInputMode() bool { return m.filterMode }

func CheckRoles(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT r.rolname,
               CASE WHEN r.rolsuper THEN 'yes' ELSE 'no' END AS superuser,
               CASE WHEN r.rolinherit THEN 'yes' ELSE 'no' END AS inherit,
               CASE WHEN r.rolcreaterole THEN 'yes' ELSE 'no' END AS createrole,
               CASE WHEN r.rolcreatedb THEN 'yes' ELSE 'no' END AS createdb,
               COALESCE(string_agg(m.rolname, ', ' ORDER BY m.rolname), '-') AS members
        FROM pg_roles r
        LEFT JOIN pg_auth_members am ON am.roleid = r.oid
        LEFT JOIN pg_roles m ON m.oid = am.member
        WHERE r.rolcanlogin = false AND r.rolname NOT LIKE 'pg_%'
        GROUP BY r.rolname, r.rolsuper, r.rolinherit, r.rolcreaterole, r.rolcreatedb
        ORDER BY r.rolname;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading roles", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Role", Width: 20},
		{Title: "Super", Width: 6},
		{Title: "Inherit", Width: 8},
		{Title: "CreateRole", Width: 11},
		{Title: "CreateDB", Width: 9},
		{Title: "Members", Width: 40},
	}

	var rowsData []table.Row
	for rows.Next() {
		var rolname, superuser, inherit, createrole, createdb, members string
		if err := rows.Scan(&rolname, &superuser, &inherit, &createrole, &createdb, &members); err != nil {
			return NewErrorModel(err, "Scanning roles row", initialModel)
		}

		rowsData = append(rowsData, table.Row{rolname, superuser, inherit, createrole, createdb, members})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return RolesModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
}

func (m RolesModel) Init() tea.Cmd { return nil }

func (m RolesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return CheckRoles(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m RolesModel) View() string {
	rules := []ColorRule{
		{Column: 1, Colorize: func(v string) int {
			if v == "yes" {
				return 2
			}
			return -1
		}},
	}
	s := RenderHeader("Roles & Permissions") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}
