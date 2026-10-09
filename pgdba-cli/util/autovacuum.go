package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type AutovacuumModel struct {
	table         table.Model
	allRows       []table.Row
	filterText    string
	filterMode    bool
	initialModel  func() tea.Model
	confirmVacuum bool
	schemaName    string
	tableName     string
	saturation    AutovacuumWorkerSaturation
	width         int
	height        int
}

// autovacuumNameColumns (Schema, Table) are sized to their content and shrink first on
// narrow terminals; the value columns take exactly the width of their longest value.
var autovacuumNameColumns = []int{0, 1}
var autovacuumValueColumns = []int{2, 3, 4, 5, 6, 7, 8, 9}

// throttleText summarizes a table's autovacuum cost settings: "global" when it doesn't
// override them, otherwise "limit/delay" plus "xN" when that is N times slower than the
// global settings ("x∞"-like cases, where the global setting is unthrottled, show "slower").
// ASCII only: ColorizeTable locates later columns by byte offset.
func throttleText(cost AutovacuumCost) string {
	if cost.DelaySource == SourceGlobal && cost.LimitSource == SourceGlobal {
		return "global"
	}
	text := fmt.Sprintf("%g/%gms", cost.Limit, cost.DelayMS)
	if cost.DelayMS == 0 {
		text = "unthrottled"
	}
	switch {
	case cost.SlowerThanGlobal && cost.SlowdownFactor != nil:
		text += fmt.Sprintf(" x%.0f", *cost.SlowdownFactor)
	case cost.SlowerThanGlobal:
		text += " slower"
	}
	return text
}

// throttleLevel colors throttleText with the same thresholds as AutovacuumTableStatus.
func throttleLevel(text string) int {
	switch {
	case text == "global":
		return 3
	case strings.HasSuffix(text, " slower"):
		return 2
	}
	if _, factorText, found := strings.Cut(text, " x"); found {
		if factor, err := strconv.ParseFloat(factorText, 64); err == nil && factor >= 10 {
			return 2
		}
		return 1
	}
	return -1
}

func (m AutovacuumModel) IsInputMode() bool { return m.filterMode }

// vacuumStatusText returns the Status column value for a table, cross-referencing
// live pg_stat_progress_vacuum activity against the dead-tuple snapshot below —
// "idle" doesn't mean unattended, just that nothing is running on it right now.
func vacuumStatusText(isAutovacuum bool, active bool) string {
	if !active {
		return "idle"
	}
	if isAutovacuum {
		return "auto vacuum"
	}
	return "manual vacuum"
}

func CheckAutovacuum(initialModel func() tea.Model) tea.Model {
	tables, err := FetchAutovacuum(context.Background(), config.Config.DB, NoRowLimit)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum monitor", initialModel)
	}
	saturation, err := FetchAutovacuumWorkerSaturation(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum worker saturation", initialModel)
	}
	activity, err := FetchVacuumProgress(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading autovacuum activity", initialModel)
	}
	activeIsAutovacuum := make(map[string]bool, len(activity))
	for _, w := range activity {
		if w.TableName != "" { // vacuums in other databases have no resolvable table
			activeIsAutovacuum[w.SchemaName+"."+w.TableName] = w.IsAutovacuum
		}
	}

	columns := []table.Column{
		{Title: "Schema", Width: 12},
		{Title: "Table", Width: 25},
		{Title: "Status", Width: 14},
		{Title: "Dead Tuples", Width: 12},
		{Title: "Dead %", Width: 8},
		{Title: "Trigger", Width: 8},
		{Title: "Size", Width: 10},
		{Title: "Throttle", Width: 12},
		{Title: "Last Autovacuum", Width: 18},
		{Title: "Autovacs", Width: 9},
	}

	formatTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	}

	var rowsData []table.Row
	for _, av := range tables {
		deadPctStr := "N/A"
		if av.DeadPct != nil {
			deadPctStr = fmt.Sprintf("%.1f%%", *av.DeadPct)
		}
		triggerStr := "N/A"
		if av.TriggerRatio != nil {
			triggerStr = fmt.Sprintf("%.0f%%", *av.TriggerRatio*100)
		}
		isAutovacuum, active := activeIsAutovacuum[av.SchemaName+"."+av.TableName]
		rowsData = append(rowsData, table.Row{
			av.SchemaName,
			av.TableName,
			vacuumStatusText(isAutovacuum, active),
			fmt.Sprintf("%d", av.DeadTuples),
			deadPctStr,
			triggerStr,
			av.TotalSize,
			throttleText(av.Cost),
			formatTime(av.LastAutovacuum),
			fmt.Sprintf("%d", av.AutovacuumCount),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumModel{
		table:        t,
		allRows:      rowsData,
		initialModel: initialModel,
		saturation:   saturation,
		height:       30,
	}
}

func (m AutovacuumModel) Init() tea.Cmd { return nil }

func (m AutovacuumModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, autovacuumValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, autovacuumNameColumns, msg.Width))
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
			if !m.confirmVacuum {
				m.filterMode = true
			}
			return m, nil
		case "q", "esc":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckAutovacuum(m.initialModel), nil
		case "enter":
			if m.confirmVacuum {
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) < 2 {
				return m, nil
			}
			schema, tableName := selectedRow[0], selectedRow[1]
			parent := m.initialModel
			return CheckAutovacuumDetail(schema, tableName, func() tea.Model {
				return CheckAutovacuum(parent)
			}), nil
		case "v":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			m.schemaName = selectedRow[0]
			m.tableName = selectedRow[1]
			m.confirmVacuum = true
			return m, nil
		case "y":
			if m.confirmVacuum {
				query := fmt.Sprintf("VACUUM ANALYZE %s.%s",
					pq.QuoteIdentifier(m.schemaName),
					pq.QuoteIdentifier(m.tableName))
				if _, err := config.Config.DB.Exec(query); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("VACUUM ANALYZE %s.%s", m.schemaName, m.tableName),
						m.initialModel), nil
				}
				return CheckAutovacuum(m.initialModel), nil
			}
		case "n":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m AutovacuumModel) View() string {
	rules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			if v == "idle" {
				return 3
			}
			return 0
		}},
		{Column: 4, Colorize: func(v string) int {
			// Values are "N/A" or "30.1%"; strip % and parse.
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f > 30:
				return 2
			case f > 10:
				return 1
			default:
				return 0
			}
		}},
		// Same thresholds as AutovacuumTableStatus: ≥ 100% of the trigger = vacuum due.
		{Column: 5, Colorize: func(v string) int {
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if err != nil {
				return -1
			}
			switch {
			case f >= 200:
				return 2
			case f >= 100:
				return 1
			}
			return -1
		}},
		{Column: 7, Colorize: func(v string) int { return throttleLevel(strings.TrimSpace(v)) }},
	}

	level := 0
	switch {
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers:
		level = 2
	case m.saturation.MaxWorkers > 0 && m.saturation.RunningWorkers >= m.saturation.MaxWorkers/2:
		level = 1
	}
	saturationLine := fmt.Sprintf("  %s %s",
		HintStyle.Render("Autovacuum Workers:"),
		SeverityColor(fmt.Sprintf("%d / %d running", m.saturation.RunningWorkers, m.saturation.MaxWorkers), level),
	)

	s := RenderHeader("Autovacuum Monitor") + "\n"
	s += saturationLine + "\n"
	// MaxWidth keeps the hint on one line, so FitTableHeight's measurement stays exact.
	hint := HintStyle
	if m.width > 0 {
		hint = hint.MaxWidth(m.width)
	}
	s += hint.Render("  Trigger = dead tuples vs autovacuum threshold • Throttle = per-table cost override, xN = N times slower than global") + "\n\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmVacuum {
		s += fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n", m.schemaName, m.tableName)
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • v vacuum analyze • r refresh • ? help • q back")
	}
	return s
}
