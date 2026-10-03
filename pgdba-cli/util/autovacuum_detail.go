package util

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
)

type bloatLoadedMsg struct {
	bloat *AutovacuumBloatDetail
	err   error
}

type AutovacuumDetailModel struct {
	schema        string
	tableName     string
	stats         AutovacuumDetailStats
	params        []AutovacuumParam
	thresholds    *AutovacuumThresholds // nil when they couldn't be computed
	bloat         *AutovacuumBloatDetail
	bloatLoading  bool
	bloatError    string
	confirmVacuum bool
	paramTable    table.Model
	initialModel  func() tea.Model
	width         int
	height        int
}

// formatCount renders an integer-valued float with thousands separators (12,345,678).
func formatCount(value float64) string {
	digits := strconv.FormatInt(int64(math.Round(value)), 10)
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	var grouped strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	if negative {
		return "-" + grouped.String()
	}
	return grouped.String()
}

// thresholdCells returns the Threshold / Current / Status cells for one parameter
// row. The computed value goes on the row of the parameter that defines the check
// (e.g. autovacuum_vacuum_threshold carries base + scale_factor × reltuples); rows
// that only feed another check (scale factors, cost settings) stay blank.
func thresholdCells(name string, thresholds *AutovacuumThresholds) (string, string, string) {
	if thresholds == nil {
		return "", "", ""
	}
	rowCheck := func(check RowThreshold) (string, string, string) {
		status := "ok"
		if check.Due {
			status = "DUE"
		}
		return formatCount(check.Threshold), formatCount(float64(check.Current)), status
	}
	ageCheck := func(check AgeThreshold, dueText string) (string, string, string) {
		status := "ok"
		if check.Due {
			status = dueText
		}
		return formatCount(check.Limit.Value), formatCount(float64(check.Current)), status
	}
	switch name {
	case "autovacuum_vacuum_threshold":
		return rowCheck(thresholds.DeadTuples)
	case "autovacuum_vacuum_insert_threshold":
		if thresholds.Inserts == nil {
			return "", "", "disabled"
		}
		return rowCheck(*thresholds.Inserts)
	case "autovacuum_analyze_threshold":
		return rowCheck(thresholds.Analyze)
	case "autovacuum_freeze_max_age":
		return ageCheck(thresholds.XIDWraparound, "FORCED")
	case "autovacuum_freeze_table_age":
		return ageCheck(thresholds.XIDAggressive, "aggressive")
	case "autovacuum_freeze_min_age":
		return formatCount(thresholds.XIDFreezeMinAge.Value), "", ""
	case "autovacuum_multixact_freeze_max_age":
		return ageCheck(thresholds.MXIDWraparound, "FORCED")
	case "autovacuum_multixact_freeze_table_age":
		return ageCheck(thresholds.MXIDAggressive, "aggressive")
	case "autovacuum_multixact_freeze_min_age":
		return formatCount(thresholds.MXIDFreezeMinAge.Value), "", ""
	}
	return "", "", ""
}

// formatSetting renders a setting value with its source, e.g. "0.01 (table)".
func formatSetting(setting ThresholdSetting) string {
	value := strconv.FormatFloat(setting.Value, 'f', -1, 64)
	if setting.Value >= 1000 || setting.Value <= -1000 {
		value = formatCount(setting.Value)
	}
	return fmt.Sprintf("%s (%s)", value, setting.Source)
}

func rowThresholdFormula(label string, check RowThreshold, reltuples float64) string {
	formula := fmt.Sprintf("%s %s + %s × %s reltuples", label,
		formatSetting(check.Base), formatSetting(check.ScaleFactor), formatCount(reltuples))
	if check.UnfrozenFraction != nil {
		formula += fmt.Sprintf(" × %.0f%% unfrozen", *check.UnfrozenFraction*100)
	}
	if check.MaxThreshold != nil {
		formula += fmt.Sprintf(", capped at %s", formatSetting(*check.MaxThreshold))
	}
	return formula + " = " + formatCount(check.Threshold)
}

// parameterFormula explains how the selected parameter turns into its threshold; it's
// shown as a footer tip while navigating the table. Scale factors and the max threshold
// show the formula of the threshold they feed.
func parameterFormula(name string, thresholds *AutovacuumThresholds) string {
	if thresholds == nil {
		return ""
	}
	switch name {
	case "autovacuum_enabled":
		return "Autovacuum runs only if both the global autovacuum and the table's autovacuum_enabled are on — anti-wraparound vacuums run regardless"
	case "autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor", "autovacuum_vacuum_max_threshold":
		return rowThresholdFormula("Vacuum when dead tuples >", thresholds.DeadTuples, thresholds.Reltuples)
	case "autovacuum_vacuum_insert_threshold", "autovacuum_vacuum_insert_scale_factor":
		if thresholds.Inserts == nil {
			return "Insert-triggered vacuum disabled (autovacuum_vacuum_insert_threshold = -1)"
		}
		return rowThresholdFormula("Vacuum when inserts since last vacuum >", *thresholds.Inserts, thresholds.Reltuples)
	case "autovacuum_analyze_threshold", "autovacuum_analyze_scale_factor":
		return rowThresholdFormula("Analyze when modified rows >", thresholds.Analyze, thresholds.Reltuples)
	case "autovacuum_freeze_max_age":
		return "Anti-wraparound vacuum forced when age(relfrozenxid) > " + formatSetting(thresholds.XIDWraparound.Limit) +
			" — a table value can only lower the global"
	case "autovacuum_freeze_table_age":
		return "Aggressive vacuum when age(relfrozenxid) > " + formatSetting(thresholds.XIDAggressive.Limit) +
			" — capped at 95% of autovacuum_freeze_max_age"
	case "autovacuum_freeze_min_age":
		return "VACUUM freezes rows whose xmin is older than " + formatSetting(thresholds.XIDFreezeMinAge) +
			" — capped at 50% of autovacuum_freeze_max_age"
	case "autovacuum_multixact_freeze_max_age":
		return "Anti-wraparound vacuum forced when mxid_age(relminmxid) > " + formatSetting(thresholds.MXIDWraparound.Limit) +
			" — a table value can only lower the global"
	case "autovacuum_multixact_freeze_table_age":
		return "Aggressive vacuum when mxid_age(relminmxid) > " + formatSetting(thresholds.MXIDAggressive.Limit) +
			" — capped at 95% of autovacuum_multixact_freeze_max_age"
	case "autovacuum_multixact_freeze_min_age":
		return "VACUUM replaces multixacts older than " + formatSetting(thresholds.MXIDFreezeMinAge) +
			" — capped at 50% of autovacuum_multixact_freeze_max_age"
	}
	return ""
}

// formatParamValue adds thousands separators to integer settings (100000000 →
// 100,000,000) to match the Threshold column; decimals, booleans and anything else
// are shown exactly as PostgreSQL reports them.
func formatParamValue(raw string) string {
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return formatCount(float64(value))
	}
	return raw
}

func buildParamRows(params []AutovacuumParam, thresholds *AutovacuumThresholds) []table.Row {
	var rows []table.Row
	for _, p := range params {
		tableVal := "(inherited)"
		if p.TableValue != "" {
			tableVal = formatParamValue(p.TableValue) + " *"
		}
		globalVal := formatParamValue(p.GlobalValue)
		if p.Unit != "" {
			globalVal += " " + p.Unit
		}
		threshold, current, status := thresholdCells(p.Name, thresholds)
		rows = append(rows, table.Row{p.Name, tableVal, globalVal, threshold, current, status})
	}
	return rows
}

func paramTableColumns() []table.Column {
	return []table.Column{
		{Title: "Parameter", Width: 40},
		{Title: "Table value", Width: 14},
		{Title: "Global default", Width: 14},
		{Title: "Threshold", Width: 15},
		{Title: "Current", Width: 15},
		{Title: "Status", Width: 11},
	}
}

func CheckAutovacuumDetail(schema, tableName string, initialModel func() tea.Model) tea.Model {
	stats, err := FetchAutovacuumDetail(context.Background(), config.Config.DB, schema, tableName)
	if err != nil {
		return NewErrorModel(err, fmt.Sprintf("Loading autovacuum detail for %s.%s", schema, tableName), initialModel)
	}
	params, _ := FetchAutovacuumParams(context.Background(), config.Config.DB, schema, tableName)
	var thresholds *AutovacuumThresholds
	if computed, err := FetchAutovacuumThresholds(context.Background(), config.Config.DB, schema, tableName); err == nil {
		thresholds = &computed
	}

	tbl := table.New(
		table.WithColumns(paramTableColumns()),
		table.WithRows(buildParamRows(params, thresholds)),
		table.WithFocused(true),
		table.WithHeight(10),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumDetailModel{
		schema:       schema,
		tableName:    tableName,
		stats:        stats,
		params:       params,
		thresholds:   thresholds,
		paramTable:   tbl,
		initialModel: initialModel,
	}
}

func (m AutovacuumDetailModel) Init() tea.Cmd { return nil }

func loadBloatCmd(schema, tableName string) tea.Cmd {
	return func() tea.Msg {
		bloat, err := FetchAutovacuumBloat(context.Background(), config.Config.DB, schema, tableName)
		return bloatLoadedMsg{bloat: bloat, err: err}
	}
}

func (m AutovacuumDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.paramTable.SetColumns(StretchColumn(paramTableColumns(), 0, msg.Width))
		// The summary block above the table varies (bloat line), so measure the frame.
		FitTableHeight(&m.paramTable, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil

	case bloatLoadedMsg:
		m.bloatLoading = false
		if msg.err != nil {
			m.bloatError = msg.err.Error()
		} else {
			m.bloat = msg.bloat
			if m.bloat == nil {
				m.bloatError = "pgstattuple extension not installed"
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckAutovacuumDetail(m.schema, m.tableName, m.initialModel), nil
		case "b":
			if !m.bloatLoading && m.bloat == nil && m.bloatError == "" {
				m.bloatLoading = true
				return m, loadBloatCmd(m.schema, m.tableName)
			}
			return m, nil
		case "v":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
			m.confirmVacuum = true
			return m, nil
		case "y":
			if m.confirmVacuum {
				m.confirmVacuum = false
				query := fmt.Sprintf("VACUUM ANALYZE %s.%s",
					pq.QuoteIdentifier(m.schema),
					pq.QuoteIdentifier(m.tableName))
				if _, err := config.Config.DB.Exec(query); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("VACUUM ANALYZE %s.%s", m.schema, m.tableName),
						m.initialModel), nil
				}
				return CheckAutovacuumDetail(m.schema, m.tableName, m.initialModel), nil
			}
		case "n":
			if m.confirmVacuum {
				m.confirmVacuum = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.paramTable, cmd = m.paramTable.Update(msg)
	return m, cmd
}

func (m AutovacuumDetailModel) View() string {
	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04:05")
	}
	fmtNum := func(n int64) string { return fmt.Sprintf("%d", n) }

	renderKey := lipgloss.NewStyle().Foreground(ColorGray).Render
	renderVal := lipgloss.NewStyle().Foreground(ColorWhite).Render

	header := RenderHeader(fmt.Sprintf("Autovacuum Detail — %s.%s", m.schema, m.tableName))

	// Stats summary — one line, key metrics side by side
	statsLine := fmt.Sprintf("  %s %s   %s %s   %s %s   %s %s",
		renderKey("Live:"), renderVal(fmtNum(m.stats.LiveTuples)),
		renderKey("Dead:"), renderVal(fmtNum(m.stats.DeadTuples)),
		renderKey("Modified:"), renderVal(fmtNum(m.stats.ModSinceAnalyze)),
		renderKey("Size:"), renderVal(m.stats.TotalSize))
	// reltuples is what the Threshold column scales by, so show it next to the counts.
	if m.thresholds != nil {
		reltuplesText := formatCount(m.thresholds.Reltuples)
		if m.thresholds.ReltuplesUnknown {
			reltuplesText = "0 (never analyzed)"
		}
		dueText := SeverityColor("no", 0)
		if m.thresholds.AutovacuumDue() {
			dueText = SeverityColor("YES", 1)
		}
		statsLine += fmt.Sprintf("   %s %s   %s %s",
			renderKey("Reltuples:"), renderVal(reltuplesText),
			renderKey("Autovacuum due:"), dueText)
	}

	// Vacuum history — two lines
	vacuumLine := fmt.Sprintf("  %s %s (×%s)   %s %s (×%s)",
		renderKey("Last vacuum:"), renderVal(fmtTime(m.stats.LastVacuum)),
		renderVal(fmtNum(m.stats.VacuumCount)),
		renderKey("Last autovacuum:"), renderVal(fmtTime(m.stats.LastAutovacuum)),
		renderVal(fmtNum(m.stats.AutovacuumCount)))
	analyzeLine := fmt.Sprintf("  %s %s (×%s)   %s %s (×%s)",
		renderKey("Last analyze:"), renderVal(fmtTime(m.stats.LastAnalyze)),
		renderVal(fmtNum(m.stats.AnalyzeCount)),
		renderKey("Last autoanalyze:"), renderVal(fmtTime(m.stats.LastAutoanalyze)),
		renderVal(fmtNum(m.stats.AutoanalyzeCount)))

	// Freeze status — one line with bar against the effective autovacuum_freeze_max_age
	// (a table reloption can only lower the global value; see computeAutovacuumThresholds).
	freezeMaxAge := int64(200_000_000) // PostgreSQL default, used only if thresholds failed to load
	if m.thresholds != nil && m.thresholds.XIDWraparound.Limit.Value > 0 {
		freezeMaxAge = int64(m.thresholds.XIDWraparound.Limit.Value)
	}
	freezePct := 0.0
	if m.stats.FrozenXIDAge > 0 {
		freezePct = float64(m.stats.FrozenXIDAge) / float64(freezeMaxAge) * 100
		if freezePct > 100 {
			freezePct = 100
		}
	}
	freezeLevel := 0
	if freezePct > 75 {
		freezeLevel = 2
	} else if freezePct > 50 {
		freezeLevel = 1
	}
	freezeLine := fmt.Sprintf("  %s %s   %s  %s",
		renderKey("XID age:"),
		SeverityColor(fmtNum(m.stats.FrozenXIDAge), freezeLevel),
		RenderBar(freezePct, 20),
		renderKey(fmt.Sprintf("of freeze_max_age %dM", freezeMaxAge/1_000_000)))

	rules := []ColorRule{
		{Column: 1, Colorize: func(v string) int {
			if v == "(inherited)" {
				return 3 // gray
			}
			return 1 // yellow = overridden
		}},
		{Column: 5, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "ok":
				return 0
			case "DUE", "aggressive":
				return 1
			case "FORCED":
				return 2
			case "disabled":
				return 3
			}
			return -1
		}},
	}
	paramSection := ColorizeTable(m.paramTable.View(), m.paramTable.Columns(), rules)

	// Always reserve the tip line (even when empty) so the frame height doesn't change
	// while navigating; truncate rather than wrap for the same reason.
	var formulaTip string
	if selected := m.paramTable.SelectedRow(); len(selected) > 0 {
		formulaTip = parameterFormula(selected[0], m.thresholds)
	}
	if formulaTip != "" {
		formulaTip = "  " + formulaTip
		if m.width > 0 {
			formulaTip = lipgloss.NewStyle().MaxWidth(m.width).Render(formulaTip)
		}
		formulaTip = HintStyle.Render(formulaTip)
	}
	paramSection += "\n" + formulaTip

	// Bloat status line (shown below the table)
	bloatLine := ""
	switch {
	case m.bloatLoading:
		bloatLine = "  " + FooterStyle.Render("Running pgstattuple…")
	case m.bloatError != "":
		bloatLine = "  " + SeverityColor(m.bloatError, 1)
	case m.bloat != nil:
		bloatLevel := 0
		if m.bloat.RealBloatPct > 30 {
			bloatLevel = 2
		} else if m.bloat.RealBloatPct > 10 {
			bloatLevel = 1
		}
		bloatLine = fmt.Sprintf("  %s %s   %s %d bytes free",
			renderKey("Bloat:"),
			SeverityColor(fmt.Sprintf("%.2f%%", m.bloat.RealBloatPct), bloatLevel),
			renderKey("Dead tuple len:"),
			m.bloat.DeadTupleLen)
	}

	var footer string
	if m.confirmVacuum {
		footer = fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n", m.schema, m.tableName)
	} else {
		footer = "\n" + FooterStyle.Render("↑↓ navigate • b precise bloat • v vacuum analyze • r refresh • ? help • q back")
	}

	summary := statsLine + "\n" + vacuumLine + "\n" + analyzeLine + "\n" + freezeLine + "\n"
	if bloatLine != "" {
		summary += bloatLine + "\n"
	}

	return header + "\n" + summary + "\n" + paramSection + footer
}
