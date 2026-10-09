package util

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	uptime     string // shown on the connection line
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
	if data.SlowQueryCount == nil {
		slowMetric = dashboardMetric{slowLabel, "N/A", 1}
	} else {
		slowLevel := 0
		if *data.SlowQueryCount > 20 {
			slowLevel = 2
		} else if *data.SlowQueryCount > 5 {
			slowLevel = 1
		}
		slowMetric = dashboardMetric{slowLabel, fmt.Sprintf("%d", *data.SlowQueryCount), slowLevel}
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

	// ── Server section (only the freeze line; uptime moved to the header) ──
	serverSection := metricSection{name: "Server"}
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
	}

	sections := []metricSection{activitySection, storageSection, walReplicationSection()}
	if len(serverSection.pairs) > 0 {
		sections = append(sections, serverSection)
	}
	// Parts that failed are listed instead of failing the whole dashboard; the
	// pg_stat_statements reason explains the N/A slow-query count.
	warnings := data.Warnings
	if data.SlowQueryCount == nil && data.SlowQueryUnavailableReason != "" {
		warnings = append([]string{"slow queries: " + data.SlowQueryUnavailableReason}, warnings...)
	}
	if len(warnings) > 0 {
		warningSection := metricSection{name: "Warnings"}
		for _, warning := range warnings {
			part, message, found := strings.Cut(warning, ": ")
			if !found {
				part, message = "", warning
			}
			// Keep each warning on one line; the full text is in the MCP check_dashboard output.
			if runes := []rune(strings.ReplaceAll(message, "\n", " ")); len(runes) > 90 {
				message = string(runes[:89]) + "…"
			} else {
				message = string(runes)
			}
			warningSection.pairs = append(warningSection.pairs, metricPair{
				left: dashboardMetric{part, message, 1},
			})
		}
		sections = append(sections, warningSection)
	}

	return DashboardModel{
		sections:   sections,
		uptime:     formatUptime(data.UptimeSeconds),
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

// renderBody renders the bars and metric sections; compact omits the blank lines
// between blocks for terminals too short to fit them.
func (m DashboardModel) renderBody(compact bool) string {
	gap := "\n"
	if compact {
		gap = ""
	}
	labelStyle := lipgloss.NewStyle().Width(28).Foreground(ColorGray)
	bw := m.barWidth()

	s := gap

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
	s += gap

	for _, section := range m.sections {
		s += renderSectionHeader(section.name, m.width) + "\n"
		for _, pair := range section.pairs {
			s += renderMetricPair(pair) + "\n"
		}
		s += gap
	}
	return s
}

func (m DashboardModel) View() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue).Render("pgdba")
	sep := lipgloss.NewStyle().Foreground(ColorGray).Render(" › ")
	name := lipgloss.NewStyle().Bold(true).Foreground(ColorWhite).Render("Dashboard")
	conn := lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("%s@%s:%d/%s  (v%s)  •  up %s",
			config.Config.User, config.Config.Host, config.Config.Port,
			config.Config.DBName, config.Config.Version, m.uptime),
	)
	header := fmt.Sprintf("%s%s%s\n%s\n", logo, sep, name, conn)

	// Measure instead of hand-counting lines: sections vary (freeze line, WAL rows).
	// On short terminals drop the blank separators so the footer stays on screen —
	// bubbletea would otherwise cut the header off the top.
	// divider + 3 shortcut rows; the blank line above the divider is the body's
	// trailing newline, which lipgloss.Height already counts.
	const footerLines = 4
	body := m.renderBody(false)
	if m.height > 0 && lipgloss.Height(header+body)+footerLines > m.height {
		body = m.renderBody(true)
	}
	s := header + body
	if padding := m.height - lipgloss.Height(s) - footerLines; padding > 0 {
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
		renderKey("T") + " " + renderLabel("toast") + "  " +
		renderKey("I") + " " + renderLabel("replica-id") + "  " +
		renderKey("r") + " " + renderLabel("refresh") + "  " +
		renderKey("?") + " " + renderLabel("help") + "  " +
		renderKey("q") + " " + renderLabel("quit")

	s += "\n" + divider + "\n" + shortcutRow1 + "\n" + shortcutRow2 + "\n" + shortcutRow3
	return s
}

// formatAge renders a duration in seconds compactly: 45s, 12m, 3h 5m, 2d 4h 1m.
func formatAge(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", max(seconds, 0))
	}
	return formatUptime(seconds)
}

// walReplicationSection builds the "WAL & Replication" rows. Each row is full width
// because its values are longer than the 12-character two-column layout.
func walReplicationSection() metricSection {
	section := metricSection{name: "WAL & Replication"}
	status, err := FetchWALReplicationStatus(context.Background(), config.Config.DB)
	if err != nil {
		// Optional block: a permission or version problem here mustn't hide the dashboard.
		section.pairs = []metricPair{{left: dashboardMetric{"WAL & replication", "unavailable: " + err.Error(), 3}}}
		return section
	}
	section.pairs = []metricPair{
		{left: slotRetentionMetric(status)},
		{left: slotRiskMetric(status)},
		{left: archivingMetric(status)},
		{left: replicationMetric(status)},
	}
	return section
}

// slotRetentionMetric compares the WAL kept by the most demanding slot with
// max_slot_wal_keep_size. "unlimited" is a warning on purpose: without that cap a
// stalled consumer keeps WAL until the disk fills.
func slotRetentionMetric(status WALReplicationStatus) dashboardMetric {
	const label = "Slot WAL retained"
	if status.SlotCount == 0 {
		return dashboardMetric{label, "no replication slots", 3}
	}
	retained := formatBytes(status.MaxRetainedBytes)
	pct := status.SlotRetentionPct()
	if pct == nil {
		return dashboardMetric{label,
			fmt.Sprintf("%s (slot %s) • max_slot_wal_keep_size unlimited", retained, status.MaxRetainedSlot), 1}
	}
	level := 0
	switch {
	case *pct >= 80:
		level = 2
	case *pct >= 50:
		level = 1
	}
	return dashboardMetric{label, fmt.Sprintf("%s of %s max_slot_wal_keep_size (%.0f%%) • slot %s",
		retained, formatBytes(*status.MaxSlotWALKeepBytes), *pct, status.MaxRetainedSlot), level}
}

// slotRiskMetric counts slots PostgreSQL is about to invalidate (unreserved) or already
// invalidated (lost), plus — on PG18+ — the longest idle slot vs idle_replication_slot_timeout.
func slotRiskMetric(status WALReplicationStatus) dashboardMetric {
	const label = "Slots at risk"
	if status.SlotCount == 0 {
		return dashboardMetric{label, "none", 3}
	}
	level := 0
	switch {
	case status.SlotsLost > 0:
		level = 2
	case status.SlotsUnreserved > 0:
		level = 1
	}
	value := fmt.Sprintf("%d slots • %d unreserved • %d lost", status.SlotCount, status.SlotsUnreserved, status.SlotsLost)
	if status.LongestIdleSeconds != nil && status.LongestIdleSlot != "" {
		value += fmt.Sprintf(" • longest idle %s (%s)", formatAge(*status.LongestIdleSeconds), status.LongestIdleSlot)
		if status.IdleSlotTimeoutSeconds != nil && *status.IdleSlotTimeoutSeconds > 0 {
			timeout := *status.IdleSlotTimeoutSeconds
			value += " of " + formatAge(timeout) + " idle timeout"
			if float64(*status.LongestIdleSeconds) >= 0.8*float64(timeout) {
				level = max(level, 1)
			}
		}
	}
	return dashboardMetric{label, value, level}
}

// archivingMetric is red while archiving is failing: WAL can't leave pg_wal until it
// succeeds, so the disk fills even without replication slots.
func archivingMetric(status WALReplicationStatus) dashboardMetric {
	const label = "Archiving"
	if status.ArchiveMode == "off" {
		return dashboardMetric{label, "off", 3}
	}
	if status.ArchiveFailingNow {
		since := ""
		if status.LastFailedTime != nil {
			since = fmt.Sprintf(" (last failure %s ago)", formatAge(int64(time.Since(*status.LastFailedTime).Seconds())))
		}
		return dashboardMetric{label, fmt.Sprintf("FAILING%s • %d failures since stats reset", since, status.FailedCount), 2}
	}
	if status.LastArchivedTime == nil {
		return dashboardMetric{label, fmt.Sprintf("%s • nothing archived yet", status.ArchiveMode), 0}
	}
	return dashboardMetric{label, fmt.Sprintf("ok • last archived %s ago • %d archived, %d failed since stats reset",
		formatAge(int64(time.Since(*status.LastArchivedTime).Seconds())), status.ArchivedCount, status.FailedCount), 0}
}

// replicationMetric summarizes physical standbys and logical subscribers fed by this
// server (worst lag of each), and this server's own subscriptions (errors, and conflicts
// on PG18+). Error/conflict counters are cumulative since the
// stats reset, so they warn rather than alarm.
func replicationMetric(status WALReplicationStatus) dashboardMetric {
	const label = "Replication"
	if status.StandbyCount == 0 && status.LogicalSenderCount == 0 && status.SubscriptionCount == 0 {
		return dashboardMetric{label, "no standbys, subscribers or subscriptions", 3}
	}
	lagLevel := func(bytes int64) int {
		switch {
		case bytes > 1<<30:
			return 2
		case bytes > 64<<20:
			return 1
		}
		return 0
	}
	level := 0
	var parts []string
	if status.StandbyCount > 0 {
		standbys := fmt.Sprintf("%d standby(s)", status.StandbyCount)
		var lag []string
		if status.MaxStandbyLagBytes != nil {
			lag = append(lag, formatBytes(*status.MaxStandbyLagBytes)+" behind")
			level = max(level, lagLevel(*status.MaxStandbyLagBytes))
		}
		if status.MaxReplayLagSeconds != nil {
			lag = append(lag, fmt.Sprintf("replay lag %.1fs", *status.MaxReplayLagSeconds))
			switch {
			case *status.MaxReplayLagSeconds > 60:
				level = max(level, 2)
			case *status.MaxReplayLagSeconds > 10:
				level = max(level, 1)
			}
		}
		if len(lag) > 0 {
			standbys += " (worst: " + strings.Join(lag, ", ") + ")"
		}
		parts = append(parts, standbys)
	}
	if status.LogicalSenderCount > 0 {
		senders := fmt.Sprintf("%d logical subscriber(s)", status.LogicalSenderCount)
		if status.MaxLogicalLagBytes != nil {
			senders += fmt.Sprintf(" (worst: %s behind)", formatBytes(*status.MaxLogicalLagBytes))
			level = max(level, lagLevel(*status.MaxLogicalLagBytes))
		}
		parts = append(parts, senders)
	}
	if status.SubscriptionCount > 0 {
		subscriptions := fmt.Sprintf("%d subscription(s) here", status.SubscriptionCount)
		var counters []string
		if status.SubscriptionErrors != nil {
			counters = append(counters, fmt.Sprintf("%d errors", *status.SubscriptionErrors))
			if *status.SubscriptionErrors > 0 {
				level = max(level, 1)
			}
		}
		if status.SubscriptionConflicts != nil {
			counters = append(counters, fmt.Sprintf("%d conflicts", *status.SubscriptionConflicts))
			if *status.SubscriptionConflicts > 0 {
				level = max(level, 1)
			}
		}
		if len(counters) > 0 {
			subscriptions += " (" + strings.Join(counters, ", ") + ")"
		}
		parts = append(parts, subscriptions)
	}
	return dashboardMetric{label, strings.Join(parts, " • "), level}
}
