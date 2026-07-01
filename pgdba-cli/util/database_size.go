package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type DatabaseSizeModel struct {
	table        table.Model
	report       DatabaseSizeReport
	initialModel func() tea.Model
	height       int
	width        int
}

func CheckDatabaseSizes(initialModel func() tea.Model) tea.Model {
	report, err := FetchDatabaseSizes(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading database sizes", initialModel)
	}

	columns := []table.Column{
		{Title: "Database", Width: 25},
		{Title: "Owner", Width: 15},
		{Title: "Encoding", Width: 10},
		{Title: "Size", Width: 15},
	}

	var rowsData []table.Row
	for _, d := range report.Databases {
		rowsData = append(rowsData, table.Row{
			d.Name,
			d.Owner,
			d.Encoding,
			d.SizePretty,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return DatabaseSizeModel{
		table:        t,
		report:       report,
		initialModel: initialModel,
		height:       30,
		width:        120,
	}
}

func (m DatabaseSizeModel) Init() tea.Cmd { return nil }

func (m DatabaseSizeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		// -4: total line, tablespaces heading, blank line, footer
		m.table.SetHeight(TableHeight(msg.Height) - 4 - len(m.report.Tablespaces))
		cols := StretchColumn(m.table.Columns(), 0, msg.Width)
		m.table.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckDatabaseSizes(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m DatabaseSizeModel) View() string {
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render
	renderValue := lipgloss.NewStyle().Foreground(ColorWhite).Render

	s := RenderHeader("Database Sizes") + "\n"
	if len(m.report.Databases) == 0 {
		s += FooterStyle.Render("No databases found.") + "\n"
	} else {
		s += m.table.View() + "\n"
	}

	s += fmt.Sprintf("\n  %s %s\n", renderLabel("Total across databases:"), renderValue(m.report.TotalPretty))

	if len(m.report.Tablespaces) > 0 {
		s += "\n  " + renderLabel("Tablespaces:") + "\n"
		for _, ts := range m.report.Tablespaces {
			location := ts.Location
			if location == "" {
				location = "(default PGDATA)"
			}
			s += fmt.Sprintf("    %s %s  %s\n",
				renderValue(ts.Name), renderValue(ts.SizePretty), renderLabel(location))
		}
	}

	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
