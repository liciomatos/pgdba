package util

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	offset        int
	initialModel  func() tea.Model
	width         int
	height        int
}

func CheckAutovacuumDetail(schema, tableName string, initialModel func() tea.Model) tea.Model {
	stats, err := FetchAutovacuumDetail(context.Background(), config.Config.DB, schema, tableName)
	if err != nil {
		return NewErrorModel(err, fmt.Sprintf("Loading autovacuum detail for %s.%s", schema, tableName), initialModel)
	}
	params, _ := FetchAutovacuumParams(context.Background(), config.Config.DB, schema, tableName)
	return AutovacuumDetailModel{
		schema:       schema,
		tableName:    tableName,
		stats:        stats,
		params:       params,
		initialModel: initialModel,
		width:        120,
		height:       40,
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
		case "up", "k":
			if m.offset > 0 {
				m.offset--
			}
			return m, nil
		case "down", "j":
			m.offset++
			return m, nil
		}
	}
	return m, nil
}

func (m AutovacuumDetailModel) View() string {
	header := RenderHeader(fmt.Sprintf("Autovacuum Detail — %s.%s", m.schema, m.tableName))

	renderLabel := lipgloss.NewStyle().Width(26).Foreground(ColorGray).Render
	renderValue := lipgloss.NewStyle().Foreground(ColorWhite).Render
	renderSection := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render

	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return t.Format("2006-01-02 15:04:05")
	}
	fmtNum := func(n int64) string { return fmt.Sprintf("%d", n) }

	var lines []string

	// STATS section
	lines = append(lines, renderSection("STATS"))
	lines = append(lines, fmt.Sprintf("  %s %s     %s %s",
		renderLabel("Live tuples:"), renderValue(fmtNum(m.stats.LiveTuples)),
		renderLabel("Table size:"), renderValue(m.stats.TableSize)))
	lines = append(lines, fmt.Sprintf("  %s %s     %s %s",
		renderLabel("Dead tuples:"), renderValue(fmtNum(m.stats.DeadTuples)),
		renderLabel("Total size:"), renderValue(m.stats.TotalSize)))
	lines = append(lines, fmt.Sprintf("  %s %s     %s %s",
		renderLabel("Modified since analyze:"), renderValue(fmtNum(m.stats.ModSinceAnalyze)),
		renderLabel("Toast+Index size:"), renderValue(m.stats.ToastAndIndexSize)))
	lines = append(lines, "")

	// VACUUM HISTORY section
	lines = append(lines, renderSection("VACUUM HISTORY"))
	lines = append(lines, fmt.Sprintf("  %s %s     Count: %s",
		renderLabel("Last vacuum:"), renderValue(fmtTime(m.stats.LastVacuum)),
		renderValue(fmtNum(m.stats.VacuumCount))))
	lines = append(lines, fmt.Sprintf("  %s %s     Count: %s",
		renderLabel("Last autovacuum:"), renderValue(fmtTime(m.stats.LastAutovacuum)),
		renderValue(fmtNum(m.stats.AutovacuumCount))))
	lines = append(lines, fmt.Sprintf("  %s %s     Count: %s",
		renderLabel("Last analyze:"), renderValue(fmtTime(m.stats.LastAnalyze)),
		renderValue(fmtNum(m.stats.AnalyzeCount))))
	lines = append(lines, fmt.Sprintf("  %s %s     Count: %s",
		renderLabel("Last autoanalyze:"), renderValue(fmtTime(m.stats.LastAutoanalyze)),
		renderValue(fmtNum(m.stats.AutoanalyzeCount))))
	lines = append(lines, "")

	// FREEZE STATUS section
	lines = append(lines, renderSection("FREEZE STATUS"))
	freezePct := 0.0
	if m.stats.FrozenXIDAge > 0 {
		// Rough pct using typical 200M freeze_max_age
		freezePct = float64(m.stats.FrozenXIDAge) / 200_000_000 * 100
		if freezePct > 100 {
			freezePct = 100
		}
	}
	freezeBar := RenderBar(freezePct, 20)
	freezeLevel := 0
	if freezePct > 75 {
		freezeLevel = 2
	} else if freezePct > 50 {
		freezeLevel = 1
	}
	lines = append(lines, fmt.Sprintf("  %s %s   %s",
		renderLabel("relfrozenxid age:"),
		SeverityColor(fmt.Sprintf("%d", m.stats.FrozenXIDAge), freezeLevel),
		freezeBar))
	lines = append(lines, fmt.Sprintf("  %s %s",
		renderLabel("relminmxid age:"), renderValue(fmtNum(m.stats.MXIDAge))))
	lines = append(lines, "")

	// CUSTOM PARAMETERS section
	lines = append(lines, renderSection("CUSTOM PARAMETERS  (* = overridden for this table)"))
	lines = append(lines, fmt.Sprintf("  %-42s %-16s %s",
		lipgloss.NewStyle().Foreground(ColorGray).Render("Parameter"),
		lipgloss.NewStyle().Foreground(ColorGray).Render("Table value"),
		lipgloss.NewStyle().Foreground(ColorGray).Render("Global value")))
	for _, p := range m.params {
		tableVal := "(inherited)"
		tableStyle := lipgloss.NewStyle().Foreground(ColorGray)
		marker := " "
		if p.TableValue != "" {
			tableVal = p.TableValue + " *"
			tableStyle = lipgloss.NewStyle().Foreground(ColorYellow)
			marker = "*"
		}
		_ = marker
		lines = append(lines, fmt.Sprintf("  %-42s %-16s %s",
			renderLabel(p.Name),
			tableStyle.Render(tableVal),
			renderValue(p.GlobalValue+" "+p.Unit)))
	}
	lines = append(lines, "")

	// BLOAT section
	lines = append(lines, renderSection("PRECISE BLOAT  (pgstattuple)"))
	switch {
	case m.bloatLoading:
		lines = append(lines, "  Running pgstattuple…")
	case m.bloatError != "":
		lines = append(lines, "  "+SeverityColor(m.bloatError, 1))
	case m.bloat != nil:
		lines = append(lines, fmt.Sprintf("  %s %s   %s %s",
			renderLabel("Dead tuple len:"),
			renderValue(fmt.Sprintf("%d bytes", m.bloat.DeadTupleLen)),
			renderLabel("Free space:"),
			renderValue(fmt.Sprintf("%d bytes", m.bloat.FreeSpace))))
		bloatLevel := 0
		if m.bloat.RealBloatPct > 30 {
			bloatLevel = 2
		} else if m.bloat.RealBloatPct > 10 {
			bloatLevel = 1
		}
		lines = append(lines, fmt.Sprintf("  %s %s",
			renderLabel("Real bloat:"),
			SeverityColor(fmt.Sprintf("%.2f%%", m.bloat.RealBloatPct), bloatLevel)))
	default:
		lines = append(lines, "  b → run pgstattuple for precise bloat (full table scan)")
	}

	// Clamp offset
	visibleRows := m.height - 6 // header + footer
	if visibleRows < 5 {
		visibleRows = 5
	}
	maxOffset := len(lines) - visibleRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}

	end := m.offset + visibleRows
	if end > len(lines) {
		end = len(lines)
	}

	content := strings.Join(lines[m.offset:end], "\n")

	footer := ""
	if m.confirmVacuum {
		footer = fmt.Sprintf("\nVACUUM ANALYZE %s.%s? (y/n)\n",
			m.schema, m.tableName)
	} else {
		footer = "\n" + FooterStyle.Render("↑↓ scroll • b precise bloat • v vacuum analyze • r refresh • q back")
	}

	return header + "\n" + content + footer
}