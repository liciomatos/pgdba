package util

import (
	"context"

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
	roles, err := FetchRoles(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading roles", initialModel)
	}

	columns := []table.Column{
		{Title: "Role", Width: 20},
		{Title: "Super", Width: 6},
		{Title: "Inherit", Width: 8},
		{Title: "CreateRole", Width: 11},
		{Title: "CreateDB", Width: 9},
		{Title: "Members", Width: 40},
	}

	boolStr := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}

	var rowsData []table.Row
	for _, r := range roles {
		membersStr := r.Members
		if membersStr == "" {
			membersStr = "-"
		}
		rowsData = append(rowsData, table.Row{
			r.RoleName,
			boolStr(r.Superuser),
			boolStr(r.Inherit),
			boolStr(r.CreateRole),
			boolStr(r.CreateDB),
			membersStr,
		})
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
