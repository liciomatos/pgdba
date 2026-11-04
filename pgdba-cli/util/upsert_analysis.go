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

// UpsertAnalysisModel lists the tables written by INSERT … ON CONFLICT statements with
// what makes those upserts expensive: non-HOT updates, many indexes, burned sequences.
type UpsertAnalysisModel struct {
	table        table.Model
	allRows      []table.Row
	analysis     UpsertAnalysis
	hints        map[string][]string // "schema.table" → UpsertTableHints
	details      map[string]string   // "schema.table" → detail page text
	filterText   string
	filterMode   bool
	detailMode   bool
	detailText   string
	initialModel func() tea.Model
	width        int
	height       int
}

func (m UpsertAnalysisModel) IsInputMode() bool { return m.filterMode }

var upsertNameColumns = []int{0}
var upsertValueColumns = []int{1, 2, 3, 4, 5, 6, 7, 8, 9}

func CheckUpsertAnalysis(initialModel func() tea.Model) tea.Model {
	analysis, err := FetchUpsertAnalysis(context.Background(), config.Config.DB, "", NoRowLimit)
	if err != nil {
		return NewErrorModel(err, "Loading upsert analysis", initialModel)
	}

	columns := []table.Column{
		{Title: "Table", Width: 30},
		{Title: "Status", Width: 8},
		{Title: "Stmts", Width: 5},
		{Title: "Upd Share", Width: 9},
		{Title: "HOT %", Width: 6},
		{Title: "Fillfactor", Width: 10},
		{Title: "Indexes", Width: 7},
		{Title: "Unique", Width: 6},
		{Title: "Seq Used", Width: 8},
		{Title: "Dead", Width: 8},
	}

	statementsByTable := map[string][]UpsertStatement{}
	for _, statement := range analysis.Statements {
		statementsByTable[statement.ResolvedTable] = append(statementsByTable[statement.ResolvedTable], statement)
	}

	var rowsData []table.Row
	hints := make(map[string][]string)
	details := make(map[string]string)
	for _, t := range analysis.Tables {
		name := t.Churn.SchemaName + "." + t.Churn.TableName
		hints[name] = UpsertTableHints(t)
		details[name] = upsertTableDetail(t, hints[name], statementsByTable[name])
		rowsData = append(rowsData, table.Row{
			name,
			UpsertTableStatus(t).String(),
			fmt.Sprintf("%d", t.Statements),
			formatOptionalPct(t.UpdateSharePct),
			formatOptionalPct(t.Churn.HotPct),
			fmt.Sprintf("%d", t.Churn.Fillfactor),
			fmt.Sprintf("%d", t.IndexCount),
			fmt.Sprintf("%d", len(t.ArbiterIndexes)),
			upsertSequenceUsage(t.Sequences),
			formatCount(float64(t.Churn.DeadTuples)),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return UpsertAnalysisModel{table: t, allRows: rowsData, analysis: analysis, hints: hints, details: details, initialModel: initialModel}
}

// upsertSequenceUsage is the highest pct_used among the table's owned sequences.
func upsertSequenceUsage(sequences []OwnedSequence) string {
	var highest *float64
	for _, sequence := range sequences {
		if sequence.PctUsed != nil && (highest == nil || *sequence.PctUsed > *highest) {
			highest = sequence.PctUsed
		}
	}
	return formatOptionalPct(highest)
}

// upsertTableDetail is the Enter page: hints, arbiter indexes, sequences and the
// statements writing the table.
func upsertTableDetail(t UpsertTable, hints []string, statements []UpsertStatement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s.%s — %s inserts, %s updates (%s HOT), %d indexes\n",
		t.Churn.SchemaName, t.Churn.TableName, formatCount(float64(t.Churn.Inserts)),
		formatCount(float64(t.Churn.Updates)), formatOptionalPct(t.Churn.HotPct), t.IndexCount)
	for _, hint := range hints {
		b.WriteString("\n• " + hint)
	}
	b.WriteString("\n\nArbiter candidates (unique / exclusion indexes):")
	if len(t.ArbiterIndexes) == 0 {
		b.WriteString("\n  none")
	}
	for _, index := range t.ArbiterIndexes {
		fmt.Fprintf(&b, "\n  %s — %s scans, %s", index.Definition, formatCount(float64(index.Scans)), formatBytes(index.SizeBytes))
	}
	if len(t.Sequences) > 0 {
		b.WriteString("\n\nOwned sequences:")
	}
	for _, sequence := range t.Sequences {
		perRow := "-"
		if sequence.ValuesPerLiveRow != nil {
			perRow = fmt.Sprintf("%.1f", *sequence.ValuesPerLiveRow)
		}
		fmt.Fprintf(&b, "\n  %s (%s %s) — %s of range used, %s values per live row",
			sequence.Name, sequence.Column, sequence.ColumnType, formatOptionalPct(sequence.PctUsed), perRow)
	}
	for _, statement := range statements {
		fmt.Fprintf(&b, "\n\n%s calls, %s rows, %.2f ms mean:\n%s",
			formatCount(float64(statement.Calls)), formatCount(float64(statement.Rows)), statement.MeanTimeMS,
			strings.Join(strings.Fields(statement.Query), " "))
		if hint := UpsertStatementHint(statement); hint != "" {
			b.WriteString("\n• " + hint)
		}
	}
	return b.String()
}

func (m UpsertAnalysisModel) Init() tea.Cmd { return nil }

func (m UpsertAnalysisModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, upsertValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, upsertNameColumns, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		if m.detailMode {
			switch msg.String() {
			case "q", "esc", "enter":
				m.detailMode = false
			}
			return m, nil
		}
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
			if row := m.table.SelectedRow(); len(row) > 0 {
				if detail, ok := m.details[row[0]]; ok {
					m.detailText = detail
					m.detailMode = true
				}
			}
			return m, nil
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckUpsertAnalysis(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m UpsertAnalysisModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Upsert Analysis", m.detailText, m.width)
	}
	limitWidth := func(style lipgloss.Style) lipgloss.Style {
		if m.width > 0 {
			return style.MaxWidth(m.width)
		}
		return style
	}
	level := 0
	if m.analysis.SpeculativeTokenWaits > 0 || m.analysis.UpsertSessionsOnLocks > 0 {
		level = 1
	}
	summary := SeverityColor(fmt.Sprintf("  %d ON CONFLICT statements • %d active upserts (%d waiting on locks) • %d speculative-insert waits",
		len(m.analysis.Statements), m.analysis.ActiveUpsertSessions, m.analysis.UpsertSessionsOnLocks, m.analysis.SpeculativeTokenWaits), level)
	// The selected table's first hint is always reserved (one line) so the frame height
	// doesn't change while moving through the list.
	tip := ""
	if row := m.table.SelectedRow(); len(row) > 0 {
		if hints := m.hints[row[0]]; len(hints) > 0 {
			tip = "  " + hints[0]
		}
	}
	s := RenderHeader("Upsert Analysis (INSERT … ON CONFLICT)") + "\n"
	s += limitWidth(lipgloss.NewStyle()).Render(summary) + "\n"
	s += RenderWarnings(m.analysis.Warnings, m.width) + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), []ColorRule{statusColumnRule(1)})
	s += "\n" + lipgloss.NewStyle().Foreground(ColorYellow).Inherit(limitWidth(HintStyle)).Render(tip)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • r refresh • ? help • q back")
	return s
}
