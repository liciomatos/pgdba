package util

import (
	"context"
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type UsersModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m UsersModel) IsInputMode() bool { return m.filterMode }

func CheckUsers(initialModel func() tea.Model) tea.Model {
	users, err := FetchUsers(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading users", initialModel)
	}

	columns := []table.Column{
		{Title: "Username", Width: 20},
		{Title: "Super", Width: 6},
		{Title: "CreateDB", Width: 9},
		{Title: "CreateRole", Width: 11},
		{Title: "Replication", Width: 11},
		{Title: "ConnLimit", Width: 10},
		{Title: "Valid Until", Width: 12},
		{Title: "Member Of", Width: 30},
	}

	boolStr := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}

	var rowsData []table.Row
	for _, u := range users {
		connLimitStr := "unlimited"
		if u.ConnLimit != -1 {
			connLimitStr = fmt.Sprintf("%d", u.ConnLimit)
		}
		validUntilStr := "never"
		if u.ValidUntil != nil {
			validUntilStr = u.ValidUntil.Format("2006-01-02")
		}
		memberOfStr := u.MemberOf
		if memberOfStr == "" {
			memberOfStr = "-"
		}
		rowsData = append(rowsData, table.Row{
			u.RoleName,
			boolStr(u.Superuser),
			boolStr(u.CreateDB),
			boolStr(u.CreateRole),
			boolStr(u.Replication),
			connLimitStr,
			validUntilStr,
			memberOfStr,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return UsersModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
}

func (m UsersModel) Init() tea.Cmd { return nil }

func (m UsersModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckUsers(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m UsersModel) View() string {
	rules := []ColorRule{
		{Column: 1, Colorize: func(v string) int {
			if v == "yes" {
				return 2
			}
			return -1
		}},
		{Column: 4, Colorize: func(v string) int {
			if v == "yes" {
				return 1
			}
			return -1
		}},
	}
	s := RenderHeader("Users & Permissions") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}
