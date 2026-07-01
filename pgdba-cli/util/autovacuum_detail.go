package util

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/lib/pq"
	tea "github.com/charmbracelet/bubbletea"
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
	bloat         *AutovacuumBloatDetail
	bloatLoading  bool
	bloatError    string
	confirmVacuum bool
	paramTable    table.Model
	initialModel  func() tea.Model
	width         int
	height        int
}

func buildParamRows(params []AutovacuumParam) []table.Row {
	var rows []table.Row
	for _, p := range params {
		tableVal := "(inherited)"
		if p.TableValue != "" {
			tableVal = p.TableValue + " *"
		}
		globalVal := p.GlobalValue
		if p.Unit != "" {
			globalVal += " " + p.Unit
		}
		rows = append(rows, table.Row{p.Name, tableVal, globalVal})
	}
	return rows
}

func paramTableColumns() []table.Column {
	return []table.Column{
		{Title: "Parameter", Width: 40},
		{Title: "Table value", Width: 18},
		{Title: "Global default", Width: 20},
	}
}

func CheckAutovacuumDetail(schema, tableName string, initialModel func() tea.Model) tea.Model {
	stats, err := FetchAutovacuumDetail(context.Background(), config.Config.DB, schema, tableName)
	if err != nil {
		return NewErrorModel(err, fmt.Sprintf("Loading autovacuum detail for %s.%s", schema, tableName), initialModel)
	}
	params, _ := FetchAutovacuumParams(context.Background(), config.Config.DB, schema, tableName)

	tbl := table.New(
		table.WithColumns(paramTableColumns()),
		table.WithRows(buildParamRows(params)),
		table.WithFocused(true),
		table.WithHeight(10),
		table.WithStyles(DefaultTableStyles()),
	)

	return AutovacuumDetailModel{
		schema:       schema,
		tableName:    tableName,
		stats:        stats,
		params:       params,
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
		// Summary header takes ~9 lines (header 3 + stats 2 + history 2 + freeze 1 + blank 1)
		tableH := TableHeight(msg.Height) - 7
		if tableH < 4 {
			tableH = 4
		}
		m.paramTable.SetHeight(tableH)
		cols := StretchColumn(m.paramTable.Columns(), 2, msg.Width)
		m.paramTable.SetColumns(cols)
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

	// Freeze status — one line with bar and effective freeze_max_age for context
	freezeMaxAge := int64(200_000_000) // PostgreSQL default
	for _, p := range m.params {
		if p.Name == "autovacuum_freeze_max_age" {
			effective := p.GlobalValue
			if p.TableValue != "" {
				effective = p.TableValue
			}
			var n int64
			if cnt, _ := fmt.Sscanf(effective, "%d", &n); cnt == 1 && n > 0 {
				freezeMaxAge = n
			}
			break
		}
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

	// Params table with color rule for table-overridden values
	rules := []ColorRule{
		{Column: 1, Colorize: func(v string) int {
			if v == "(inherited)" {
				return 3 // gray
			}
			return 1 // yellow = overridden
		}},
	}
	paramSection := ColorizeTable(m.paramTable.View(), m.paramTable.Columns(), rules)

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
		footer = "\n" + FooterStyle.Render("↑↓ navigate • b precise bloat • v vacuum analyze • r refresh • q back")
	}

	summary := statsLine + "\n" + vacuumLine + "\n" + analyzeLine + "\n" + freezeLine + "\n"
	if bloatLine != "" {
		summary += bloatLine + "\n"
	}

	return header + "\n" + summary + "\n" + paramSection + footer
}
