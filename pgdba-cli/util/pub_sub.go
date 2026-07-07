package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type PubSubModel struct {
	pubTable     table.Model
	subTable     table.Model
	pubCount     int
	subCount     int
	width        int
	height       int
	initialModel func() tea.Model
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
			deriveSubStatus(sub),
			sub.Publications,
			formatPID(sub.WorkerPID),
			sub.ReceivedLSN,
			formatReceiveTime(sub.LastReceiveTime),
			formatErrors(sub.ApplyErrorCount, sub.SyncErrorCount),
		})
	}

	pubTbl := table.New(
		table.WithColumns(pubColumns()),
		table.WithRows(pubRows),
		table.WithFocused(false),
		table.WithHeight(max2(len(pubRows), 1)),
		table.WithStyles(InfoTableStyles()),
	)
	subTbl := table.New(
		table.WithColumns(subColumns()),
		table.WithRows(subRows),
		table.WithFocused(false),
		table.WithHeight(max2(len(subRows), 1)),
		table.WithStyles(InfoTableStyles()),
	)

	return PubSubModel{
		pubTable:     pubTbl,
		subTable:     subTbl,
		pubCount:     len(pubs),
		subCount:     len(subs),
		width:        120,
		height:       40,
		initialModel: initialModel,
	}
}

// deriveSubStatus returns a severity-colored status string based on enabled flag and PID.
func deriveSubStatus(sub Subscription) string {
	switch {
	case !sub.Enabled:
		return SeverityColor("disabled", 3)
	case sub.WorkerPID != nil:
		return SeverityColor("active", 0)
	default:
		return SeverityColor("down", 2)
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

func formatErrors(applyErr, syncErr *int64) string {
	if applyErr == nil {
		return SeverityColor("N/A", 3)
	}
	total := *applyErr + *syncErr
	errStr := fmt.Sprintf("%d / %d", *applyErr, *syncErr)
	if total > 0 {
		return SeverityColor(errStr, 2)
	}
	return SeverityColor(errStr, 0)
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

		// Distribute height: publications get up to 1/3, subscriptions get the rest.
		// Each table needs header(2) + rows + at least 1 visible row.
		overhead := 8 // header(3) + pub-section-label(1) + gap(1) + sub-section-label(1) + gap(1) + footer(1)
		available := m.height - overhead
		pubH := max2(m.pubCount, 1)
		subH := max2(m.subCount, 1)
		maxPubH := available / 3
		if pubH > maxPubH {
			pubH = maxPubH
		}
		if pubH < 1 {
			pubH = 1
		}
		subH = max2(available-pubH, 1)

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
		}
	}
	return m, nil
}

func (m PubSubModel) View() string {
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorBlue)

	s := RenderHeader("Replication — Pub/Sub") + "\n"

	// Publications section
	pubLabel := sectionStyle.Render(fmt.Sprintf("Publications  (%d)", m.pubCount))
	s += pubLabel + "\n"
	if m.pubCount == 0 {
		s += HintStyle.Render("  No publications defined on this server.") + "\n"
	} else {
		// ColorizeTable colors columns: All/Insert/Update/Delete/Truncate/ViaRoot (cols 2–7) yes=green no=gray
		pubRules := buildBoolColorRules(2, 7)
		s += ColorizeTable(m.pubTable.View(), m.pubTable.Columns(), pubRules)
	}
	s += "\n"

	// Subscriptions section
	subLabel := sectionStyle.Render(fmt.Sprintf("Subscriptions  (%d)", m.subCount))
	s += subLabel + "\n"
	if m.subCount == 0 {
		s += HintStyle.Render("  No subscriptions defined on this server.") + "\n"
	} else {
		s += m.subTable.View()
	}

	s += "\n" + FooterStyle.Render("r refresh • q back")
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
