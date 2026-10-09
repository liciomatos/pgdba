package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type IndexUsageModel struct {
	table        table.Model
	allRows      []table.Row
	redundancy   map[string]string // "schema.index" → full redundancy description, for the tip line
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	// includeConstraints shows PK / UNIQUE / EXCLUDE indexes too (toggled with c).
	includeConstraints bool
	width              int
	height             int
}

// indexUsageNameColumns are sized to their content: Schema, Table, Index, Columns, Redundant.
var indexUsageNameColumns = []int{0, 1, 2, 3, 9}

// indexUsageValueColumns (Status, Scans, Share, Tup/Scan, Size) take exactly the width
// of their longest value — never truncated, and no wasted space taken from the names.
var indexUsageValueColumns = []int{4, 5, 6, 7, 8}

// indexStatusText spells out why IndexUsageStatus rates an index the way it does.
func indexStatusText(idx IndexUsage) string {
	switch {
	case !idx.IsValid:
		return "INVALID"
	case len(idx.RedundantWith) > 0:
		return "redundant"
	case IndexUsageStatus(idx) == StatusWarning:
		return "unused"
	}
	return "ok"
}

// redundancyText lists the indexes that make this one look redundant, e.g.
// "dup of t_pkey, prefix of t_a_b_idx".
func redundancyText(redundancies []IndexRedundancy) string {
	labels := map[string]string{
		RedundancyDuplicate:   "dup of",
		RedundancyPrefix:      "prefix of",
		RedundancySameColumns: "same cols as",
	}
	parts := make([]string, 0, len(redundancies))
	for _, redundancy := range redundancies {
		parts = append(parts, labels[redundancy.Kind]+" "+redundancy.OtherIndex)
	}
	return strings.Join(parts, ", ")
}

// selectedIndexTip returns the full, untruncated identity of the selected index, shown
// above the footer because narrow terminals still have to cut long names with "…".
func (m IndexUsageModel) selectedIndexTip() string {
	row := m.table.SelectedRow()
	if len(row) < 4 {
		return ""
	}
	tip := fmt.Sprintf("  %s.%s on %s.%s (%s)", row[0], row[2], row[0], row[1], row[3])
	if redundancy := m.redundancy[row[0]+"."+row[2]]; redundancy != "" {
		tip += " — possibly redundant: " + redundancy
	}
	return tip
}

func (m IndexUsageModel) IsInputMode() bool { return m.filterMode }

func CheckIndexUsage(initialModel func() tea.Model) tea.Model {
	return checkIndexUsage(initialModel, false)
}

func checkIndexUsage(initialModel func() tea.Model, includeConstraints bool) tea.Model {
	indexes, err := FetchIndexUsage(context.Background(), config.Config.DB,
		IndexUsageOptions{Limit: NoRowLimit, IncludeConstraints: includeConstraints})
	if err != nil {
		return NewErrorModel(err, "Loading index usage", initialModel)
	}

	columns := []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 20},
		{Title: "Index", Width: 28},
		{Title: "Columns", Width: 20},
		{Title: "Status", Width: 9},
		{Title: "Scans", Width: 8},
		{Title: "Share", Width: 6},
		{Title: "Tup/Scan", Width: 9},
		{Title: "Size", Width: 8},
		{Title: "Redundant", Width: 20},
	}

	var rowsData []table.Row
	redundancy := make(map[string]string)
	for _, idx := range indexes {
		share, perScan := "-", "-"
		if idx.TableScanSharePct != nil {
			share = fmt.Sprintf("%.0f%%", *idx.TableScanSharePct)
		}
		if idx.TupReadPerScan != nil {
			perScan = fmt.Sprintf("%.1f", *idx.TupReadPerScan)
		}
		redundant := redundancyText(idx.RedundantWith)
		if redundant != "" {
			redundancy[idx.SchemaName+"."+idx.IndexName] = redundant
		}
		rowsData = append(rowsData, table.Row{
			idx.SchemaName, idx.TableName, idx.IndexName, idx.IndexColumns,
			indexStatusText(idx),
			fmt.Sprintf("%d", idx.IdxScan),
			share,
			perScan,
			idx.IndexSize,
			redundant,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return IndexUsageModel{
		table: t, allRows: rowsData, redundancy: redundancy, initialModel: initialModel,
		includeConstraints: includeConstraints, width: 120, height: 30,
	}
}

func (m IndexUsageModel) Init() tea.Cmd { return nil }

func (m IndexUsageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, indexUsageValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, indexUsageNameColumns, msg.Width))
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
		case "enter":
			row := m.table.SelectedRow()
			if len(row) >= 3 {
				schema := row[0]
				indexName := row[2]
				back := func() tea.Model { return checkIndexUsage(m.initialModel, m.includeConstraints) }
				return CheckIndexDetail(schema, indexName, back), nil
			}
			return m, nil
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return checkIndexUsage(m.initialModel, m.includeConstraints), nil
		case "c":
			return checkIndexUsage(m.initialModel, !m.includeConstraints), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m IndexUsageModel) View() string {
	rules := []ColorRule{
		{Column: 4, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "ok":
				return 0
			case "redundant", "unused":
				return 1
			case "INVALID":
				return 2
			}
			return -1
		}},
	}
	s := RenderHeader("Index Usage") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	// Always reserve the tip line and truncate it, so the frame height never changes.
	tip := m.selectedIndexTip()
	if tip != "" && m.width > 0 {
		tip = lipgloss.NewStyle().MaxWidth(m.width).Render(tip)
	}
	s += "\n" + HintStyle.Render(tip)
	constraintsHint := "c show PK/unique"
	if m.includeConstraints {
		constraintsHint = "c hide PK/unique"
	}
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • "+constraintsHint+" • r refresh • ? help • q back")
	return s
}
