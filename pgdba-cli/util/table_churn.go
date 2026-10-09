package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

// TableChurnModel ranks tables by updates and shows how many were HOT, to find the
// tables where a lower fillfactor (or fewer indexed columns) would cut index writes
// and vacuum work.
type TableChurnModel struct {
	table        table.Model
	allRows      []table.Row
	hints        map[string]string // "schema.table" → TableChurnHint
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m TableChurnModel) IsInputMode() bool { return m.filterMode }

// tableChurnNameColumns (Schema, Table) shrink first on narrow terminals.
var tableChurnNameColumns = []int{0, 1}
var tableChurnValueColumns = []int{2, 3, 4, 5, 6, 7, 8, 9, 10}

func CheckTableChurn(initialModel func() tea.Model) tea.Model {
	tables, err := FetchTableChurn(context.Background(), config.Config.DB, NoRowLimit)
	if err != nil {
		return NewErrorModel(err, "Loading table churn", initialModel)
	}

	columns := []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 25},
		{Title: "Status", Width: 8},
		{Title: "Inserts", Width: 10},
		{Title: "Updates", Width: 10},
		{Title: "Deletes", Width: 10},
		{Title: "HOT %", Width: 6},
		{Title: "New-page Upd", Width: 12},
		{Title: "Fillfactor", Width: 10},
		{Title: "Dead", Width: 10},
		{Title: "Size", Width: 10},
	}

	var rowsData []table.Row
	hints := make(map[string]string)
	for _, churn := range tables {
		hotPct := "-"
		if churn.HotPct != nil {
			hotPct = fmt.Sprintf("%.1f%%", *churn.HotPct)
		}
		newPage := "-"
		if churn.NewPageUpdates != nil {
			newPage = formatCount(float64(*churn.NewPageUpdates))
		}
		fillfactor := fmt.Sprintf("%d", churn.Fillfactor)
		if !churn.FillfactorSet {
			fillfactor += " (def)"
		}
		if hint := TableChurnHint(churn); hint != "" {
			hints[churn.SchemaName+"."+churn.TableName] = hint
		}
		rowsData = append(rowsData, table.Row{
			churn.SchemaName,
			churn.TableName,
			TableChurnStatus(churn).String(),
			formatCount(float64(churn.Inserts)),
			formatCount(float64(churn.Updates)),
			formatCount(float64(churn.Deletes)),
			hotPct,
			newPage,
			fillfactor,
			formatCount(float64(churn.DeadTuples)),
			churn.TotalSize,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return TableChurnModel{table: t, allRows: rowsData, hints: hints, initialModel: initialModel}
}

func (m TableChurnModel) Init() tea.Cmd { return nil }

func (m TableChurnModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, tableChurnValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, tableChurnNameColumns, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
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
			return CheckTableChurn(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m TableChurnModel) View() string {
	rules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			if strings.TrimSpace(v) == "warning" {
				return 1
			}
			return 0
		}},
	}
	// The selected table's hint line is always reserved (and cut to one line) so the
	// frame height doesn't change while moving through the list.
	tip := ""
	if row := m.table.SelectedRow(); len(row) > 1 {
		if hint := m.hints[row[0]+"."+row[1]]; hint != "" {
			tip = "  " + row[1] + ": " + hint
		}
	}
	tipStyle := HintStyle
	if m.width > 0 {
		tipStyle = tipStyle.MaxWidth(m.width)
	}
	s := RenderHeader("Table Churn (HOT updates)") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + lipgloss.NewStyle().Foreground(ColorYellow).Inherit(tipStyle).Render(tip)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • ? help • q back")
	return s
}
