package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type CacheHitModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	height       int
}

func (m CacheHitModel) IsInputMode() bool { return m.filterMode }

func CheckCacheHit(initialModel func() tea.Model) tea.Model {
	tables, err := FetchCacheHit(context.Background(), config.Config.DB, 20)
	if err != nil {
		return NewErrorModel(err, "Loading cache hit ratios", initialModel)
	}

	columns := []table.Column{
		{Title: "Table", Width: 25},
		{Title: "Heap Read", Width: 12},
		{Title: "Heap Hit", Width: 12},
		{Title: "Cache Hit %", Width: 12},
		{Title: "Idx Read", Width: 12},
		{Title: "Idx Hit", Width: 12},
		{Title: "Idx Hit %", Width: 12},
	}

	var rowsData []table.Row
	for _, ct := range tables {
		rowsData = append(rowsData, table.Row{
			ct.TableName,
			fmt.Sprintf("%d", ct.HeapBlksRead),
			fmt.Sprintf("%d", ct.HeapBlksHit),
			fmt.Sprintf("%.2f%%", ct.CacheHitRatio),
			fmt.Sprintf("%d", ct.IdxBlksRead),
			fmt.Sprintf("%d", ct.IdxBlksHit),
			fmt.Sprintf("%.2f%%", ct.IdxCacheHitRatio),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return CacheHitModel{table: t, allRows: rowsData, initialModel: initialModel, height: 30}
}

func (m CacheHitModel) Init() tea.Cmd { return nil }

func (m CacheHitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
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
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckCacheHit(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m CacheHitModel) View() string {
	// Values are formatted as "%.2f%"; strip % and parse to determine severity.
	hitColorizer := func(v string) int {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil {
			return -1
		}
		switch {
		case f < 70:
			return 2
		case f < 90:
			return 1
		default:
			return 0
		}
	}
	rules := []ColorRule{
		{Column: 3, Colorize: hitColorizer},
		{Column: 6, Colorize: hitColorizer},
	}
	s := RenderHeader("Cache Hit Ratio") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • r refresh • q back")
	return s
}
