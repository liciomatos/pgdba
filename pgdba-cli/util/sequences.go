package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

// SequencesModel lists every sequence by how much of its range is used, capped by the
// type of the column it feeds.
type SequencesModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	width        int
	height       int
}

func (m SequencesModel) IsInputMode() bool { return m.filterMode }

var sequenceNameColumns = []int{0, 2}
var sequenceValueColumns = []int{1, 3, 4, 5, 6}

func CheckSequences(initialModel func() tea.Model) tea.Model {
	sequences, err := FetchSequences(context.Background(), config.Config.DB, NoRowLimit)
	if err != nil {
		return NewErrorModel(err, "Loading sequences", initialModel)
	}

	columns := []table.Column{
		{Title: "Sequence", Width: 30},
		{Title: "Status", Width: 8},
		{Title: "Used By", Width: 30},
		{Title: "Type", Width: 10},
		{Title: "Last Value", Width: 14},
		{Title: "Max Value", Width: 14},
		{Title: "Used", Width: 8},
	}
	var rowsData []table.Row
	for _, sequence := range sequences {
		usedBy := "-"
		if sequence.Table != "" {
			usedBy = sequence.Table + "." + sequence.Column
			if sequence.Usage == "default" {
				usedBy += " (default)"
			}
		}
		columnType := sequence.ColumnType
		if columnType == "" {
			columnType = sequence.DataType
		}
		lastValue := "-"
		if sequence.LastValue != nil {
			lastValue = fmt.Sprintf("%d", *sequence.LastValue)
		}
		rowsData = append(rowsData, table.Row{
			sequence.Name,
			SequenceStatus(sequence).String(),
			usedBy,
			columnType,
			lastValue,
			fmt.Sprintf("%d", sequence.MaxValue),
			formatOptionalPct(sequence.PctUsed),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return SequencesModel{table: t, allRows: rowsData, initialModel: initialModel}
}

func (m SequencesModel) Init() tea.Cmd { return nil }

func (m SequencesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, sequenceValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, sequenceNameColumns, msg.Width))
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
			return CheckSequences(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m SequencesModel) View() string {
	s := RenderHeader("Sequences") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), []ColorRule{statusColumnRule(1)})
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • ? help • q back")
	return s
}
