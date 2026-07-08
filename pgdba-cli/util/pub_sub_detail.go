package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

// ─── Publication Table Detail ─────────────────────────────────────────────────

type PubTableDetailModel struct {
	pubName      string
	tableModel   table.Model
	width        int
	height       int
	initialModel func() tea.Model
}

func pubTableDetailColumns() []table.Column {
	return []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 22},
		{Title: "Columns", Width: 30}, // stretched in WindowSizeMsg — content varies from "all columns" to long lists
		{Title: "Row Filter", Width: 15},
		{Title: "Live Rows", Width: 10},
		{Title: "Dead Rows", Width: 10},
		{Title: "Last Vacuum", Width: 17},
	}
}

func CheckPubTableDetail(pubname string, initialModel func() tea.Model) tea.Model {
	tables, err := FetchPublicationTables(context.Background(), config.Config.DB, pubname)
	if err != nil {
		return NewErrorModel(err, fmt.Sprintf("Loading tables for publication %q", pubname), initialModel)
	}

	var rows []table.Row
	for _, t := range tables {
		rows = append(rows, table.Row{
			t.SchemaName,
			t.TableName,
			t.Columns,
			t.RowFilter,
			fmt.Sprintf("%d", t.LiveRows),
			fmt.Sprintf("%d", t.DeadRows),
			t.LastVacuum,
		})
	}

	tbl := table.New(
		table.WithColumns(pubTableDetailColumns()),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(max2(len(rows), 1)+2),
		table.WithStyles(DefaultTableStyles()),
	)

	return PubTableDetailModel{
		pubName:      pubname,
		tableModel:   tbl,
		width:        120,
		height:       40,
		initialModel: initialModel,
	}
}

func (m PubTableDetailModel) Init() tea.Cmd { return nil }

func (m PubTableDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// -1 for the "publication: <name>" sub-heading line below the header
		m.tableModel.SetHeight(TableHeight(msg.Height) - 1)
		// Stretch col 2 (Columns) — its content is the most variable
		cols := StretchColumn(m.tableModel.Columns(), 2, msg.Width)
		m.tableModel.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckPubTableDetail(m.pubName, m.initialModel), nil
		}
		var cmd tea.Cmd
		m.tableModel, cmd = m.tableModel.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m PubTableDetailModel) View() string {
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render
	renderValue := lipgloss.NewStyle().Foreground(ColorWhite).Render

	s := RenderHeader("Publication Tables — " + m.pubName) + "\n"
	s += renderLabel("publication:") + "  " + renderValue(m.pubName) + "\n"
	s += ColorizeTable(m.tableModel.View(), m.tableModel.Columns(), nil)
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}

// ─── Subscription Table Detail ────────────────────────────────────────────────

type SubTableDetailModel struct {
	subName      string
	tableModel   table.Model
	width        int
	height       int
	initialModel func() tea.Model
}

func subTableDetailColumns() []table.Column {
	return []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 20},
		{Title: "Sync State", Width: 12},
		{Title: "Columns", Width: 30}, // stretched in WindowSizeMsg — shows subscribed table's columns from pg_attribute
		{Title: "Live Rows", Width: 10},
		{Title: "INS", Width: 8},
		{Title: "UPD", Width: 8},
		{Title: "DEL", Width: 8},
	}
}

func CheckSubTableDetail(subname string, initialModel func() tea.Model) tea.Model {
	tables, err := FetchSubscriptionTables(context.Background(), config.Config.DB, subname)
	if err != nil {
		return NewErrorModel(err, fmt.Sprintf("Loading tables for subscription %q", subname), initialModel)
	}

	var rows []table.Row
	for _, t := range tables {
		rows = append(rows, table.Row{
			t.SchemaName,
			t.TableName,
			t.SyncState,
			t.Columns,
			fmt.Sprintf("%d", t.LiveRows),
			fmt.Sprintf("%d", t.InsRows),
			fmt.Sprintf("%d", t.UpdRows),
			fmt.Sprintf("%d", t.DelRows),
		})
	}

	tbl := table.New(
		table.WithColumns(subTableDetailColumns()),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(max2(len(rows), 1)+2),
		table.WithStyles(DefaultTableStyles()),
	)

	return SubTableDetailModel{
		subName:      subname,
		tableModel:   tbl,
		width:        120,
		height:       40,
		initialModel: initialModel,
	}
}

func (m SubTableDetailModel) Init() tea.Cmd { return nil }

func (m SubTableDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// -1 for the "subscription: <name>" sub-heading line below the header
		m.tableModel.SetHeight(TableHeight(msg.Height) - 1)
		// Stretch col 3 (Columns) — column lists vary widely in length
		cols := StretchColumn(m.tableModel.Columns(), 3, msg.Width)
		m.tableModel.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckSubTableDetail(m.subName, m.initialModel), nil
		}
		var cmd tea.Cmd
		m.tableModel, cmd = m.tableModel.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m SubTableDetailModel) View() string {
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render
	renderValue := lipgloss.NewStyle().Foreground(ColorWhite).Render

	syncStateRules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "ready", "synchronized":
				return 0
			case "copying", "finished":
				return 1
			case "initialize":
				return 3
			}
			return -1
		}},
	}

	s := RenderHeader("Subscription Tables — " + m.subName) + "\n"
	s += renderLabel("subscription:") + "  " + renderValue(m.subName) + "\n"
	s += ColorizeTable(m.tableModel.View(), m.tableModel.Columns(), syncStateRules)
	s += "\n" + FooterStyle.Render("↑↓ navigate • r refresh • q back")
	return s
}
