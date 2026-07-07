package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type dashboardMetric struct {
	label string
	value string
	level int // 0=ok 1=warn 2=crit
}

type DashboardModel struct {
	metrics    []dashboardMetric
	connPct    float64
	connUsed   int
	connMax    int
	connLevel  int
	cacheHit   *float64 // nil when pg_stat_io has no data
	cacheLevel int
	width      int
	height     int
}

func CheckDashboard() tea.Model {
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	data, err := FetchDashboard(context.Background(), config.Config.DB, threshold)
	if err != nil {
		return DashboardModel{
			metrics: []dashboardMetric{{"Error loading dashboard", err.Error(), 2}},
			width:   80, height: 24,
		}
	}

	connLevel := 0
	if data.ConnectionPct >= 90 {
		connLevel = 2
	} else if data.ConnectionPct >= 70 {
		connLevel = 1
	}

	cacheLevel := 0
	if data.CacheHitRatio != nil {
		if *data.CacheHitRatio < 70 {
			cacheLevel = 2
		} else if *data.CacheHitRatio < 90 {
			cacheLevel = 1
		}
	}

	var metrics []dashboardMetric

	metrics = append(metrics, dashboardMetric{"Active queries", fmt.Sprintf("%d", data.ActiveQueries), 0})

	blockedLevel := 0
	if data.BlockedQueries > 0 {
		blockedLevel = 2
	}
	metrics = append(metrics, dashboardMetric{"Blocked queries", fmt.Sprintf("%d", data.BlockedQueries), blockedLevel})

	slowLabel := fmt.Sprintf("Slow queries (>%dms)", threshold)
	if data.SlowQueryCount == -1 {
		metrics = append(metrics, dashboardMetric{slowLabel, "N/A (pg_stat_statements not enabled)", 1})
	} else {
		slowLevel := 0
		if data.SlowQueryCount > 20 {
			slowLevel = 2
		} else if data.SlowQueryCount > 5 {
			slowLevel = 1
		}
		metrics = append(metrics, dashboardMetric{slowLabel, fmt.Sprintf("%d", data.SlowQueryCount), slowLevel})
	}

	deadLevel := 0
	if data.DeadTuples > 100000 {
		deadLevel = 2
	} else if data.DeadTuples > 10000 {
		deadLevel = 1
	}
	metrics = append(metrics, dashboardMetric{"Dead tuples", fmt.Sprintf("%d", data.DeadTuples), deadLevel})

	invalidLevel := 0
	if data.InvalidIndexes > 0 {
		invalidLevel = 2
	}
	metrics = append(metrics, dashboardMetric{"Invalid indexes", fmt.Sprintf("%d", data.InvalidIndexes), invalidLevel})

	metrics = append(metrics, dashboardMetric{"Replication slots", fmt.Sprintf("%d", data.ReplicationSlots), 0})

	if data.FreezeOldestDB != "" {
		freezeLevel := 0
		if data.FreezePctToward > 8.6 {
			freezeLevel = 2
		} else if data.FreezePctToward > 7.1 {
			freezeLevel = 1
		}
		metrics = append(metrics, dashboardMetric{
			"Freeze status",
			fmt.Sprintf("oldest: %s  %dM txns (%.1f%%)",
				data.FreezeOldestDB, data.FreezeOldestDBAge/1_000_000, data.FreezePctToward),
			freezeLevel,
		})
	}

	return DashboardModel{
		metrics:    metrics,
		connPct:    data.ConnectionPct,
		connUsed:   data.UsedConnections,
		connMax:    data.MaxConnections,
		connLevel:  connLevel,
		cacheHit:   data.CacheHitRatio,
		cacheLevel: cacheLevel,
		width:      80,
		height:     24,
	}
}

func (m DashboardModel) Init() tea.Cmd { return nil }

func (m DashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			return CheckDashboard(), nil
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// barWidth returns the number of block characters for the progress bar,
// scaled to the terminal width so the bar fills the available space.
func (m DashboardModel) barWidth() int {
	// 2 indent + 28 label + 2 gap + 12 summary + 2 gap = 46 chars fixed left
	// RenderBar appends " xx.x%" = 7 chars fixed right
	w := m.width - 46 - 7
	if w < 8 {
		return 8
	}
	if w > 60 {
		return 60
	}
	return w
}

func (m DashboardModel) View() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("pgdba")
	sep := lipgloss.NewStyle().Foreground(ColorGray).Render(" › ")
	name := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render("Dashboard")
	conn := lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("%s@%s:%d/%s  (v%s)",
			config.Config.User, config.Config.Host, config.Config.Port,
			config.Config.DBName, config.Config.Version),
	)

	labelStyle := lipgloss.NewStyle().Width(28).Foreground(ColorGray)
	colors := []lipgloss.Color{ColorGreen, ColorYellow, ColorRed}
	bw := m.barWidth()

	s := fmt.Sprintf("%s%s%s\n%s\n", logo, sep, name, conn)
	s += "\n"

	// Connection utilization bar
	connSummary := fmt.Sprintf("%d / %d", m.connUsed, m.connMax)
	connBar := SeverityColor(RenderBar(m.connPct, bw), m.connLevel)
	s += fmt.Sprintf("  %-28s  %-12s  %s\n",
		labelStyle.Render("Connections"), connSummary, connBar)

	// Cache hit bar (blank placeholder when data unavailable)
	if m.cacheHit != nil {
		cacheBar := SeverityColor(RenderBar(*m.cacheHit, bw), m.cacheLevel)
		s += fmt.Sprintf("  %-28s  %-12s  %s\n",
			labelStyle.Render("Cache hit ratio"), "", cacheBar)
	} else {
		s += fmt.Sprintf("  %-28s  %s\n",
			labelStyle.Render("Cache hit ratio"),
			lipgloss.NewStyle().Foreground(ColorGray).Render("N/A"))
	}
	s += "\n"

	// Remaining counters (no bar)
	for _, metric := range m.metrics {
		label := labelStyle.Render(metric.label)
		value := lipgloss.NewStyle().Foreground(colors[metric.level]).Bold(metric.level > 0).Render(metric.value)
		s += fmt.Sprintf("  %s  %s\n", label, value)
	}

	// Height-aware padding: push shortcuts to the bottom of the terminal.
	// Content lines: 2 header + 1 blank + 2 bars + 1 blank + len(metrics) = 6 + len(metrics)
	// Footer lines: 1 blank + 1 divider + 3 rows = 5
	contentLines := 6 + len(m.metrics)
	footerLines := 5
	padding := m.height - contentLines - footerLines
	if padding > 0 {
		s += strings.Repeat("\n", padding)
	}

	renderKey   := lipgloss.NewStyle().Foreground(ColorBlue).Bold(true).Render
	renderLabel := lipgloss.NewStyle().Foreground(ColorGray).Render

	divider := lipgloss.NewStyle().Foreground(ColorGray).Render("─────────────────────────────────────────────────────────────────")
	shortcutRow1 := renderKey("1") + " " + renderLabel("slow") + "  " +
		renderKey("2") + " " + renderLabel("longrun") + "  " +
		renderKey("3") + " " + renderLabel("slots") + "  " +
		renderKey("4") + " " + renderLabel("locks") + "  " +
		renderKey("5") + " " + renderLabel("conn") + "  " +
		renderKey("6") + " " + renderLabel("autovac") + "  " +
		renderKey("7") + " " + renderLabel("index") + "  " +
		renderKey("8") + " " + renderLabel("cache")
	shortcutRow2 := renderKey("9") + " " + renderLabel("users") + "  " +
		renderKey("0") + " " + renderLabel("roles") + "  " +
		renderKey("p") + " " + renderLabel("config") + "  " +
		renderKey("s") + " " + renderLabel("schema") + "  " +
		renderKey("e") + " " + renderLabel("ext") + "  " +
		renderKey("D") + " " + renderLabel("switch-db") + "  " +
		renderKey("L") + " " + renderLabel("load") + "  " +
		renderKey("w") + " " + renderLabel("waits") + "  " +
		renderKey("f") + " " + renderLabel("freeze")
	shortcutRow3 := renderKey("S") + " " + renderLabel("db-size") + "  " +
		renderKey("t") + " " + renderLabel("temp-files") + "  " +
		renderKey("m") + " " + renderLabel("memory") + "  " +
		renderKey("R") + " " + renderLabel("pub/sub") + "  " +
		renderKey("r") + " " + renderLabel("refresh") + "  " +
		renderKey("q") + " " + renderLabel("quit")

	s += "\n" + divider + "\n" + shortcutRow1 + "\n" + shortcutRow2 + "\n" + shortcutRow3
	return s
}
