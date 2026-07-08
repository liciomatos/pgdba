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
	level int // 0=ok/green 1=warn/yellow 2=crit/red 3=gray/faint
}

// metricPair is one two-column display row.
// right==nil renders left as a full-width single-column row.
type metricPair struct {
	left  dashboardMetric
	right *dashboardMetric
}

type metricSection struct {
	name  string
	pairs []metricPair
}

type DashboardModel struct {
	sections   []metricSection
	freezeDB   string // empty → hide freeze line
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
			sections: []metricSection{{
				name: "Error",
				pairs: []metricPair{{left: dashboardMetric{"Error loading dashboard", err.Error(), 2}}},
			}},
			width: 80, height: 24,
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

	// ── Activity section ───────────────────────────────────────────────────
	waitLevel := 0
	if data.WaitEventCount >= 20 {
		waitLevel = 2
	} else if data.WaitEventCount >= 5 {
		waitLevel = 1
	}

	blockedLevel := 0
	if data.BlockedQueries > 0 {
		blockedLevel = 2
	}

	longrunLevel := 0
	if data.LongRunningCount >= 5 {
		longrunLevel = 2
	} else if data.LongRunningCount >= 1 {
		longrunLevel = 1
	}

	slowLabel := fmt.Sprintf("Slow queries (>%dms)", threshold)
	var slowMetric dashboardMetric
	if data.SlowQueryCount == -1 {
		slowMetric = dashboardMetric{slowLabel, "N/A", 1}
	} else {
		slowLevel := 0
		if data.SlowQueryCount > 20 {
			slowLevel = 2
		} else if data.SlowQueryCount > 5 {
			slowLevel = 1
		}
		slowMetric = dashboardMetric{slowLabel, fmt.Sprintf("%d", data.SlowQueryCount), slowLevel}
	}

	var commitMetric dashboardMetric
	if data.CommitPct == nil {
		commitMetric = dashboardMetric{"Commit rate", "N/A", 3}
	} else {
		commitLevel := 0
		if *data.CommitPct < 80 {
			commitLevel = 2
		} else if *data.CommitPct < 95 {
			commitLevel = 1
		}
		commitMetric = dashboardMetric{"Commit rate", fmt.Sprintf("%.1f%%", *data.CommitPct), commitLevel}
	}

	activitySection := metricSection{
		name: "Activity",
		pairs: []metricPair{
			{
				left:  dashboardMetric{"Active queries", fmt.Sprintf("%d", data.ActiveQueries), 0},
				right: &dashboardMetric{"Wait events", fmt.Sprintf("%d", data.WaitEventCount), waitLevel},
			},
			{
				left:  dashboardMetric{"Blocked queries", fmt.Sprintf("%d", data.BlockedQueries), blockedLevel},
				right: &dashboardMetric{"Long-running (>60s)", fmt.Sprintf("%d", data.LongRunningCount), longrunLevel},
			},
			{
				left:  slowMetric,
				right: &commitMetric,
			},
		},
	}

	// ── Storage section ────────────────────────────────────────────────────
	deadLevel := 0
	if data.DeadTuples > 100000 {
		deadLevel = 2
	} else if data.DeadTuples > 10000 {
		deadLevel = 1
	}

	invalidLevel := 0
	if data.InvalidIndexes > 0 {
		invalidLevel = 2
	}

	tempLevel := 0
	if data.TempFilesBytes >= 10*1024*1024*1024 {
		tempLevel = 2
	} else if data.TempFilesBytes >= 1024*1024*1024 {
		tempLevel = 1
	}

	storageSection := metricSection{
		name: "Storage",
		pairs: []metricPair{
			{
				left:  dashboardMetric{"Dead tuples", fmt.Sprintf("%d", data.DeadTuples), deadLevel},
				right: &dashboardMetric{"Temp files (db)", formatBytes(data.TempFilesBytes), tempLevel},
			},
			{
				left:  dashboardMetric{"Invalid indexes", fmt.Sprintf("%d", data.InvalidIndexes), invalidLevel},
				right: &dashboardMetric{"DB size", data.DBSizePretty, 0},
			},
		},
	}

	// ── Server section ─────────────────────────────────────────────────────
	serverSection := metricSection{
		name: "Server",
		pairs: []metricPair{
			{
				left:  dashboardMetric{"Replication slots", fmt.Sprintf("%d", data.ReplicationSlots), 0},
				right: &dashboardMetric{"Uptime", formatUptime(data.UptimeSeconds), 0},
			},
		},
	}

	freezeDB := ""
	if data.FreezeOldestDB != "" {
		freezeLevel := 0
		if data.FreezePctToward > 8.6 {
			freezeLevel = 2
		} else if data.FreezePctToward > 7.1 {
			freezeLevel = 1
		}
		freezeValue := fmt.Sprintf("oldest: %s  %dM txns (%.1f%%)",
			data.FreezeOldestDB, data.FreezeOldestDBAge/1_000_000, data.FreezePctToward)
		serverSection.pairs = append(serverSection.pairs, metricPair{
			left: dashboardMetric{"Freeze", freezeValue, freezeLevel},
		})
		freezeDB = data.FreezeOldestDB
	}

	return DashboardModel{
		sections:   []metricSection{activitySection, storageSection, serverSection},
		freezeDB:   freezeDB,
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

// renderSectionHeader renders "  ─ Name ──────────────────────────────" in faint gray.
func renderSectionHeader(name string, termWidth int) string {
	// "  ─ " + name + " " + fill
	prefix := fmt.Sprintf("  ─ %s ", name)
	fillLen := termWidth - len(prefix)
	if fillLen < 2 {
		fillLen = 2
	}
	fill := strings.Repeat("─", fillLen)
	return lipgloss.NewStyle().Foreground(ColorGray).Faint(true).Render(prefix + fill)
}

// renderMetricPair renders a two-column row. The label is padded to 24 chars,
// and the plain value is padded to 12 chars BEFORE colorizing so ANSI bytes
// don't disturb column alignment.
func renderMetricPair(pair metricPair) string {
	colors := []lipgloss.Color{ColorGreen, ColorYellow, ColorRed, ColorGray}
	labelStyle := lipgloss.NewStyle().Width(24).Foreground(ColorGray)

	renderSide := func(m dashboardMetric) string {
		label := labelStyle.Render(m.label)
		paddedValue := fmt.Sprintf("%-12s", m.value)
		colorIndex := m.level
		if colorIndex < 0 || colorIndex >= len(colors) {
			colorIndex = 0
		}
		value := lipgloss.NewStyle().Foreground(colors[colorIndex]).Bold(m.level > 0 && m.level < 3).Render(paddedValue)
		return fmt.Sprintf("  %s  %s", label, value)
	}

	left := renderSide(pair.left)
	if pair.right == nil {
		return left
	}
	right := renderSide(*pair.right)
	return left + "    " + right
}

// formatUptime converts seconds to human-readable "Xd Xh Xm" / "Xh Xm" / "Xm".
func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "N/A"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// formatBytes converts bytes to a human-readable size string.
func formatBytes(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1024*1024*1024))
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f kB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
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
	bw := m.barWidth()

	s := fmt.Sprintf("%s%s%s\n%s\n", logo, sep, name, conn)
	s += "\n"

	// Connection utilization bar
	connSummary := fmt.Sprintf("%d / %d", m.connUsed, m.connMax)
	connBar := SeverityColor(RenderBar(m.connPct, bw), m.connLevel)
	s += fmt.Sprintf("  %-28s  %-12s  %s\n",
		labelStyle.Render("Connections"), connSummary, connBar)

	// Cache hit bar
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

	// Metric sections
	for _, section := range m.sections {
		s += renderSectionHeader(section.name, m.width) + "\n"
		for _, pair := range section.pairs {
			s += renderMetricPair(pair) + "\n"
		}
		s += "\n"
	}

	// Height budget:
	// Header block: 2 header + 1 blank + 2 bars + 1 blank = 6
	// Activity:  1 header + 3 rows + 1 blank = 5
	// Storage:   1 header + 2 rows + 1 blank = 4
	// Server:    1 header + 1 base row + 1 blank = 3 (freeze adds 1)
	// Total base content = 6 + 5 + 4 + 3 = 18; +1 when freeze present
	contentLines := 18
	if m.freezeDB != "" {
		contentLines++
	}
	footerLines := 5 // 1 blank + 1 divider + 3 shortcut rows
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
