package util

import (
	"context"
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

	var metrics []dashboardMetric

	connLevel := 0
	if data.ConnectionPct >= 90 {
		connLevel = 2
	} else if data.ConnectionPct >= 70 {
		connLevel = 1
	}
	metrics = append(metrics, dashboardMetric{
		"Connections",
		fmt.Sprintf("%d / %d  (%.0f%%)", data.UsedConnections, data.MaxConnections, data.ConnectionPct),
		connLevel,
	})

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

	if data.CacheHitRatio != nil {
		cacheLevel := 0
		if *data.CacheHitRatio < 70 {
			cacheLevel = 2
		} else if *data.CacheHitRatio < 90 {
			cacheLevel = 1
		}
		metrics = append(metrics, dashboardMetric{"Cache hit ratio", fmt.Sprintf("%.1f%%", *data.CacheHitRatio), cacheLevel})
	} else {
		metrics = append(metrics, dashboardMetric{"Cache hit ratio", "N/A", 0})
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
		renderKey("L") + " " + renderLabel("load") + "  " +
		renderKey("w") + " " + renderLabel("waits") + "  " +
		renderKey("f") + " " + renderLabel("freeze") + "  " +
		renderKey("r") + " " + renderLabel("refresh") + "  " +
		renderKey("q") + " " + renderLabel("quit")

	s += "\n" + divider + "\n" + shortcutRow1 + "\n" + shortcutRow2
	return s
}
