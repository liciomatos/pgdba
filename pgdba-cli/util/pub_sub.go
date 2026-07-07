package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type PubSubModel struct {
	pubTable      table.Model
	subTable      table.Model
	pubCount      int
	subCount      int
	pubH          int // allocated height for the pub section (including table header lines)
	subH          int // allocated height for the sub section (including table header lines)
	activeSection int // 0=publications, 1=subscriptions
	width         int
	height        int
	initialModel  func() tea.Model
}

func pubColumns() []table.Column {
	return []table.Column{
		{Title: "Name", Width: 22},
		{Title: "Tables", Width: 7},
		{Title: "All", Width: 5},
		{Title: "Insert", Width: 7},
		{Title: "Update", Width: 7},
		{Title: "Delete", Width: 7},
		{Title: "Truncate", Width: 9},
		{Title: "Via Root", Width: 9},
	}
}

func subColumns() []table.Column {
	return []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Status", Width: 10},
		{Title: "Publications", Width: 28},
		{Title: "PID", Width: 7},
		{Title: "Received LSN", Width: 14},
		{Title: "Last Receive", Width: 18},
		{Title: "Errors", Width: 11},
	}
}

func renderBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func CheckPubSub(initialModel func() tea.Model) tea.Model {
	pubs, err := FetchPublications(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading publications", initialModel)
	}
	subs, err := FetchSubscriptions(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading subscriptions", initialModel)
	}

	var pubRows []table.Row
	for _, pub := range pubs {
		pubRows = append(pubRows, table.Row{
			pub.PubName,
			fmt.Sprintf("%d", pub.TableCount),
			renderBool(pub.AllTables),
			renderBool(pub.Insert),
			renderBool(pub.Update),
			renderBool(pub.Delete),
			renderBool(pub.Truncate),
			renderBool(pub.ViaRoot),
		})
	}

	var subRows []table.Row
	for _, sub := range subs {
		subRows = append(subRows, table.Row{
			sub.SubName,
			subStatusText(sub),
			sub.Publications,
			formatPID(sub.WorkerPID),
			sub.ReceivedLSN,
			formatReceiveTime(sub.LastReceiveTime),
			formatErrors(sub.ApplyErrorCount, sub.SyncErrorCount),
		})
	}

	// +2 accounts for the 2-line table header (title row + border) that DefaultTableStyles
	// renders. SetHeight(h) sets viewport.Height = h - headerLines, so passing just the
	// data count results in 0 visible rows.
	const tableHeaderLines = 2
	initPubH := max2(len(pubRows), 1) + tableHeaderLines
	initSubH := max2(len(subRows), 1) + tableHeaderLines

	// Start on the subscriptions section if there are no publications.
	activeSection := 0
	if len(pubs) == 0 && len(subs) > 0 {
		activeSection = 1
	}

	// Active section uses DefaultTableStyles (shows selection highlight for navigation).
	// Inactive section uses InfoTableStyles (no highlight — plain list appearance).
	pubStyles := InfoTableStyles()
	subStyles := InfoTableStyles()
	if activeSection == 0 && len(pubs) > 0 {
		pubStyles = DefaultTableStyles()
	} else if activeSection == 1 && len(subs) > 0 {
		subStyles = DefaultTableStyles()
	}

	pubTbl := table.New(
		table.WithColumns(pubColumns()),
		table.WithRows(pubRows),
		table.WithFocused(true),
		table.WithHeight(initPubH),
		table.WithStyles(pubStyles),
	)
	subTbl := table.New(
		table.WithColumns(subColumns()),
		table.WithRows(subRows),
		table.WithFocused(true),
		table.WithHeight(initSubH),
		table.WithStyles(subStyles),
	)

	return PubSubModel{
		pubTable:      pubTbl,
		subTable:      subTbl,
		pubCount:      len(pubs),
		subCount:      len(subs),
		pubH:          initPubH,
		subH:          initSubH,
		activeSection: activeSection,
		width:         120,
		height:        40,
		initialModel:  initialModel,
	}
}

// subStatusText returns a plain-text status derived from enabled flag and worker PID.
// Color is applied post-render via buildSubColorRules — never embed ANSI in cell data.
func subStatusText(sub Subscription) string {
	switch {
	case !sub.Enabled:
		return "disabled"
	case sub.WorkerPID != nil:
		return "active"
	default:
		return "down"
	}
}

func formatPID(pid *int) string {
	if pid == nil {
		return ""
	}
	return fmt.Sprintf("%d", *pid)
}

func formatReceiveTime(t *string) string {
	if t == nil {
		return "never"
	}
	// Truncate to "YYYY-MM-DD HH:MM" for display
	s := *t
	if len(s) > 16 {
		s = s[:16]
	}
	return s
}

// formatErrors returns a plain-text error summary ("N/A", "0 / 0", "3 / 1", …).
// Color is applied post-render via buildSubColorRules — never embed ANSI in cell data.
func formatErrors(applyErr, syncErr *int64) string {
	if applyErr == nil {
		return "N/A"
	}
	return fmt.Sprintf("%d / %d", *applyErr, *syncErr)
}

// buildSubColorRules returns ColorRules for the subscriptions table:
// col 1 (Status): active=green, down=red, disabled=gray.
// col 6 (Errors): N/A=gray, "0 / 0"=green, any errors=red.
func buildSubColorRules() []ColorRule {
	return []ColorRule{
		{Column: 1, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "active":
				return 0
			case "down":
				return 2
			case "disabled":
				return 3
			}
			return -1
		}},
		{Column: 6, Colorize: func(v string) int {
			v = strings.TrimSpace(v)
			if v == "N/A" {
				return 3
			}
			parts := strings.SplitN(v, " / ", 2)
			if len(parts) == 2 {
				a, errA := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
				b, errB := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
				if errA == nil && errB == nil {
					if a+b > 0 {
						return 2
					}
					return 0
				}
			}
			return -1
		}},
	}
}

// max2 returns the larger of two ints (Go 1.21 adds max/min builtins; use helper for compat).
func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m PubSubModel) Init() tea.Cmd { return nil }

func (m PubSubModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// overhead = RenderHeader(3) + pub-label(1) + gap(1) + sub-label(1) + gap(1) + footer(1) = 8
		// available is split between both tables; each table height includes 2 header lines.
		const tableHeaderLines = 2
		overhead := 8
		available := m.height - overhead

		// Publications: at most 1/3 of available, but not more rows than we have data.
		pubDataRows := max2(m.pubCount, 1)
		maxPubDataRows := max2((available/3)-tableHeaderLines, 1)
		if pubDataRows > maxPubDataRows {
			pubDataRows = maxPubDataRows
		}
		pubH := pubDataRows + tableHeaderLines

		// Subscriptions get the rest; ensure at least 1 data row visible.
		subH := max2(available-pubH, tableHeaderLines+1)

		m.pubH = pubH
		m.subH = subH
		m.pubTable.SetHeight(pubH)
		m.subTable.SetHeight(subH)

		pubCols := StretchColumn(m.pubTable.Columns(), 0, msg.Width)
		m.pubTable.SetColumns(pubCols)
		subCols := StretchColumn(m.subTable.Columns(), 2, msg.Width)
		m.subTable.SetColumns(subCols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckPubSub(m.initialModel), nil

		case "tab":
			// Switch active section; only move if the target section has rows.
			if m.activeSection == 0 && m.subCount > 0 {
				m.activeSection = 1
				m.pubTable.SetStyles(InfoTableStyles())
				m.subTable.SetStyles(DefaultTableStyles())
			} else if m.activeSection == 1 && m.pubCount > 0 {
				m.activeSection = 0
				m.subTable.SetStyles(InfoTableStyles())
				m.pubTable.SetStyles(DefaultTableStyles())
			}
			return m, nil

		case "up", "down", "k", "j", "pgup", "pgdown":
			var cmd tea.Cmd
			if m.activeSection == 0 && m.pubCount > 0 {
				m.pubTable, cmd = m.pubTable.Update(msg)
			} else if m.activeSection == 1 && m.subCount > 0 {
				m.subTable, cmd = m.subTable.Update(msg)
			}
			return m, cmd

		case "enter":
			parentInitial := m.initialModel
			if m.activeSection == 0 && m.pubCount > 0 {
				row := m.pubTable.SelectedRow()
				if len(row) > 0 {
					pubName := row[0]
					return CheckPubTableDetail(pubName, func() tea.Model {
						return CheckPubSub(parentInitial)
					}), nil
				}
			} else if m.activeSection == 1 && m.subCount > 0 {
				row := m.subTable.SelectedRow()
				if len(row) > 0 {
					subName := row[0]
					return CheckSubTableDetail(subName, func() tea.Model {
						return CheckPubSub(parentInitial)
					}), nil
				}
			}
			return m, nil
		}
	}
	return m, nil
}

func (m PubSubModel) View() string {
	// Active section: bold blue. Inactive: gray faint. No prefix added — both labels
	// and the table below them start at column 0, keeping horizontal alignment intact.
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue)
	inactiveStyle := lipgloss.NewStyle().Foreground(ColorGray).Faint(true)

	pubStyle := inactiveStyle
	subStyle := inactiveStyle
	if m.activeSection == 0 {
		pubStyle = activeStyle
	} else {
		subStyle = activeStyle
	}

	s := RenderHeader("Replication — Pub/Sub") + "\n"

	// Publications section
	pubLabel := pubStyle.Render(fmt.Sprintf("Publications  (%d)", m.pubCount))
	s += pubLabel + "\n"
	if m.pubCount == 0 {
		s += HintStyle.Render("  No publications defined on this server.") + "\n"
		// Pad so the sub section starts at the same vertical position as when the pub
		// table is present. pubH-1 = pubH lines expected minus the 1 hint line rendered.
		s += strings.Repeat("\n", max2(m.pubH-1, 0))
	} else {
		// ColorizeTable colors columns: All/Insert/Update/Delete/Truncate/ViaRoot (cols 2–7) yes=green no=gray
		pubRules := buildBoolColorRules(2, 7)
		s += ColorizeTable(m.pubTable.View(), m.pubTable.Columns(), pubRules)
	}
	s += "\n"

	// Subscriptions section
	subLabel := subStyle.Render(fmt.Sprintf("Subscriptions  (%d)", m.subCount))
	s += subLabel + "\n"
	if m.subCount == 0 {
		s += HintStyle.Render("  No subscriptions defined on this server.") + "\n"
		// Same padding logic: fill the height the sub table would have occupied.
		s += strings.Repeat("\n", max2(m.subH-1, 0))
	} else {
		s += ColorizeTable(m.subTable.View(), m.subTable.Columns(), buildSubColorRules())
	}

	s += "\n" + FooterStyle.Render("↑↓ navigate • tab switch section • enter detail • r refresh • q back")
	return s
}

// buildBoolColorRules returns ColorRules coloring "yes" green and "no" gray
// for all columns in the range [startCol, endCol] inclusive.
func buildBoolColorRules(startCol, endCol int) []ColorRule {
	var rules []ColorRule
	for i := startCol; i <= endCol; i++ {
		rules = append(rules, ColorRule{
			Column: i,
			Colorize: func(v string) int {
				v = strings.TrimSpace(v)
				if v == "yes" {
					return 0
				}
				if v == "no" {
					return 3
				}
				return -1
			},
		})
	}
	return rules
}
