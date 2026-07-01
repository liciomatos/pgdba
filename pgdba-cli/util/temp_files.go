package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type TempFilesModel struct {
	table        table.Model
	usage        []TempFileUsage
	initialModel func() tea.Model
	height       int
	width        int
}

func CheckTempFiles(initialModel func() tea.Model) tea.Model {
	usage, err := FetchTempFileUsage(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading temp file usage", initialModel)
	}

	columns := []table.Column{
		{Title: "Database", Width: 25},
		{Title: "Temp Files", Width: 12},
		{Title: "Temp Size", Width: 15},
		{Title: "Stats Reset", Width: 25},
	}

	var rowsData []table.Row
	for _, u := range usage {
		rowsData = append(rowsData, table.Row{
			u.Database,
			fmt.Sprintf("%d", u.TempFiles),
			u.TempPretty,
			u.StatsReset,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return TempFilesModel{
		table:        t,
		usage:        usage,
		initialModel: initialModel,
		height:       30,
		width:        120,
	}
}

func (m TempFilesModel) Init() tea.Cmd { return nil }

func (m TempFilesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.table.SetHeight(TableHeight(msg.Height))
		cols := StretchColumn(m.table.Columns(), 3, msg.Width)
		m.table.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckTempFiles(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m TempFilesModel) View() string {
	hint := "  Temp files are created when a query needs more memory than work_mem for sorts, hashes, or materializations — persistent growth here is a sign work_mem may be too low."
	s := RenderHeader("Temp File Usage") + "\n"
	s += HintStyle.Render(hint) + "\n\n"

	if len(m.usage) == 0 {
		s += FooterStyle.Render("No database activity recorded.") + "\n"
	} else {
		rules := []ColorRule{
			{Column: 1, Colorize: func(v string) int {
				if v == "0" {
					return -1
				}
				return 1
			}},
		}
		s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	}

	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
