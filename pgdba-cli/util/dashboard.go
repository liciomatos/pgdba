package util

import (
	"database/sql"
	"fmt"

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
	metrics []dashboardMetric
	width   int
	height  int
}

func CheckDashboard() tea.Model {
	var metrics []dashboardMetric

	// Connections
	var usedConns int
	var maxConns int
	config.Config.DB.QueryRow(`SELECT count(*) FROM pg_stat_activity`).Scan(&usedConns)
	config.Config.DB.QueryRow(`SELECT setting::int FROM pg_settings WHERE name='max_connections'`).Scan(&maxConns)
	connPct := 0.0
	if maxConns > 0 {
		connPct = float64(usedConns) / float64(maxConns) * 100
	}
	connLevel := 0
	if connPct >= 90 {
		connLevel = 2
	} else if connPct >= 70 {
		connLevel = 1
	}
	metrics = append(metrics, dashboardMetric{
		"Connections",
		fmt.Sprintf("%d / %d  (%.0f%%)", usedConns, maxConns, connPct),
		connLevel,
	})

	// Active / Blocked
	var activeQueries, blockedQueries int
	config.Config.DB.QueryRow(`
		SELECT
			count(*) FILTER (WHERE state = 'active' AND query NOT LIKE '%pg_stat_activity%'),
			count(*) FILTER (WHERE wait_event_type = 'Lock')
		FROM pg_stat_activity`).Scan(&activeQueries, &blockedQueries)
	metrics = append(metrics, dashboardMetric{"Active queries", fmt.Sprintf("%d", activeQueries), 0})
	blockedLevel := 0
	if blockedQueries > 0 {
		blockedLevel = 2
	}
	metrics = append(metrics, dashboardMetric{"Blocked queries", fmt.Sprintf("%d", blockedQueries), blockedLevel})

	// Slow queries using the configured threshold
	threshold := config.Config.SlowThresholdMS
	if threshold <= 0 {
		threshold = 1000
	}
	var slowCount int
	err := config.Config.DB.QueryRow(
		fmt.Sprintf(`SELECT count(*) FROM pg_stat_statements WHERE mean_exec_time > %d`, threshold),
	).Scan(&slowCount)
	slowLabel := fmt.Sprintf("Slow queries (>%dms)", threshold)
	if err != nil {
		metrics = append(metrics, dashboardMetric{slowLabel, "N/A (pg_stat_statements not enabled)", 1})
	} else {
		slowLevel := 0
		if slowCount > 20 {
			slowLevel = 2
		} else if slowCount > 5 {
			slowLevel = 1
		}
		metrics = append(metrics, dashboardMetric{slowLabel, fmt.Sprintf("%d", slowCount), slowLevel})
	}

	// Cache hit ratio
	var cacheHit sql.NullFloat64
	config.Config.DB.QueryRow(`
		SELECT ROUND(100.0 * sum(heap_blks_hit) / NULLIF(sum(heap_blks_hit)+sum(heap_blks_read),0), 1)
		FROM pg_statio_user_tables`).Scan(&cacheHit)
	if cacheHit.Valid {
		cacheLevel := 0
		if cacheHit.Float64 < 70 {
			cacheLevel = 2
		} else if cacheHit.Float64 < 90 {
			cacheLevel = 1
		}
		metrics = append(metrics, dashboardMetric{"Cache hit ratio", fmt.Sprintf("%.1f%%", cacheHit.Float64), cacheLevel})
	} else {
		metrics = append(metrics, dashboardMetric{"Cache hit ratio", "N/A", 0})
	}

	// Dead tuples
	var deadTuples int64
	config.Config.DB.QueryRow(`SELECT COALESCE(sum(n_dead_tup),0) FROM pg_stat_user_tables`).Scan(&deadTuples)
	deadLevel := 0
	if deadTuples > 100000 {
		deadLevel = 2
	} else if deadTuples > 10000 {
		deadLevel = 1
	}
	metrics = append(metrics, dashboardMetric{"Dead tuples", fmt.Sprintf("%d", deadTuples), deadLevel})

	// Invalid indexes
	var invalidIndexes int
	config.Config.DB.QueryRow(`SELECT count(*) FROM pg_index WHERE NOT indisvalid`).Scan(&invalidIndexes)
	invalidLevel := 0
	if invalidIndexes > 0 {
		invalidLevel = 2
	}
	metrics = append(metrics, dashboardMetric{"Invalid indexes", fmt.Sprintf("%d", invalidIndexes), invalidLevel})

	// Replication slots
	var repSlots int
	config.Config.DB.QueryRow(`SELECT count(*) FROM pg_replication_slots`).Scan(&repSlots)
	metrics = append(metrics, dashboardMetric{"Replication slots", fmt.Sprintf("%d", repSlots), 0})

	return DashboardModel{metrics: metrics, width: 80, height: 24}
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

func (m DashboardModel) View() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("pgdba")
	sep := lipgloss.NewStyle().Foreground(ColorGray).Render(" › ")
	name := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render("Dashboard")
	conn := lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("%s@%s:%d/%s  (v%s)",
			config.Config.User, config.Config.Host, config.Config.Port,
			config.Config.DBName, config.Config.Version),
	)
	s := fmt.Sprintf("%s%s%s\n%s\n\n", logo, sep, name, conn)

	labelW := 28
	labelStyle := lipgloss.NewStyle().Width(labelW).Foreground(ColorGray)
	colors := []lipgloss.Color{ColorGreen, ColorYellow, ColorRed}

	for _, metric := range m.metrics {
		label := labelStyle.Render(metric.label)
		value := lipgloss.NewStyle().Foreground(colors[metric.level]).Bold(metric.level > 0).Render(metric.value)
		s += fmt.Sprintf("  %s  %s\n", label, value)
	}

	// Footer: each shortcut is rendered as a bold blue key + a gray label so the
	// navigation hints are easy to scan without blending into surrounding text.
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
		renderKey("v") + " " + renderLabel("version") + "  " +
		renderKey("r") + " " + renderLabel("refresh") + "  " +
		renderKey("q") + " " + renderLabel("quit")

	s += "\n" + divider + "\n" + shortcutRow1 + "\n" + shortcutRow2
	return s
}
