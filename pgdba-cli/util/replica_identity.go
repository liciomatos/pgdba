package util

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type ReplicaIdentityModel struct {
	table      table.Model
	allRows    []table.Row
	filterText string
	filterMode bool
	// fixes is keyed by "schema.table" (never by row index, which breaks under filter).
	fixes         map[string]string
	criticalCount int
	totalCount    int
	// publishedOnly hides tables that aren't in any publication or pglogical set (p).
	publishedOnly bool
	initialModel  func() tea.Model
	width         int
	height        int
}

// replicaIdentityNameColumns (Schema, Table, Publications) size to their content.
var replicaIdentityNameColumns = []int{0, 1, 4}

// replicaIdentityValueColumns are short values that take exactly their content width.
var replicaIdentityValueColumns = []int{2, 3, 5, 6}

func (m ReplicaIdentityModel) IsInputMode() bool { return m.filterMode }

// ConsumesKey claims "p" (replicated-only toggle) over the global PgConfig shortcut.
func (m ReplicaIdentityModel) ConsumesKey(key string) bool { return key == "p" }

// replicaIdentityPublicationsColumn holds "—" for tables in no publication or pglogical set.
const replicaIdentityPublicationsColumn = 4

// visibleRows applies the published-only toggle, then the text filter.
func (m ReplicaIdentityModel) visibleRows() []table.Row {
	rows := m.allRows
	if m.publishedOnly {
		rows = nil
		for _, row := range m.allRows {
			if row[replicaIdentityPublicationsColumn] != "—" {
				rows = append(rows, row)
			}
		}
	}
	if m.filterText != "" {
		rows = FilterRows(rows, m.filterText)
	}
	return rows
}

// replicaIdentityProblem explains in a few words why the identity isn't usable.
func replicaIdentityProblem(issue ReplicaIdentityIssue) string {
	switch issue.ReplicaIdentity {
	case "nothing":
		return "NOTHING"
	case "index":
		return "index dropped"
	case "full":
		return "FULL (pglogical)"
	}
	return "no PK"
}

func CheckReplicaIdentity(initialModel func() tea.Model) tea.Model {
	issues, err := FetchReplicaIdentityIssues(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading replica identity check", initialModel)
	}

	columns := []table.Column{
		{Title: "Schema", Width: 10},
		{Title: "Table", Width: 25},
		{Title: "Identity", Width: 9},
		{Title: "Problem", Width: 13},
		{Title: "Replicated by", Width: 20},
		{Title: "Status", Width: 8},
		{Title: "Size", Width: 10},
	}

	var rowsData []table.Row
	fixes := make(map[string]string, len(issues))
	criticalCount := 0
	for _, issue := range issues {
		// pglogical sets are prefixed so they can't be confused with publications of the
		// same name (e.g. both have a "default").
		replicatedBy := append([]string{}, issue.Publications...)
		for _, set := range issue.PglogicalSets {
			replicatedBy = append(replicatedBy, "pglogical:"+set)
		}
		publications := strings.Join(replicatedBy, ", ")
		if publications == "" {
			publications = "—"
		}
		status := "warning"
		if issue.IsCritical() {
			status = "critical"
			criticalCount++
		}
		rowsData = append(rowsData, table.Row{
			issue.SchemaName, issue.TableName, issue.ReplicaIdentity, replicaIdentityProblem(issue),
			publications, status, issue.TotalSize,
		})
		fixes[issue.SchemaName+"."+issue.TableName] = issue.SuggestedFix()
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ReplicaIdentityModel{
		table:         t,
		allRows:       rowsData,
		fixes:         fixes,
		criticalCount: criticalCount,
		totalCount:    len(issues),
		initialModel:  initialModel,
	}
}

func (m ReplicaIdentityModel) Init() tea.Cmd { return nil }

func (m ReplicaIdentityModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cols := SizeColumnsToContent(m.table.Columns(), m.allRows, replicaIdentityValueColumns)
		m.table.SetColumns(FitColumnsToContent(cols, m.allRows, replicaIdentityNameColumns, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		if m.filterMode {
			switch msg.Type {
			case tea.KeyEsc:
				m.filterMode = false
				m.filterText = ""
				m.table.SetRows(m.visibleRows())
			case tea.KeyBackspace:
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.table.SetRows(m.visibleRows())
				}
			case tea.KeyRunes:
				m.filterText += msg.String()
				m.table.SetRows(m.visibleRows())
			case tea.KeyEnter:
				m.filterMode = false
			}
			return m, nil
		}
		switch msg.String() {
		case "/":
			m.filterMode = true
			return m, nil
		case "p":
			m.publishedOnly = !m.publishedOnly
			m.table.SetRows(m.visibleRows())
			m.table.GotoTop()
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			refreshed := CheckReplicaIdentity(m.initialModel)
			if model, ok := refreshed.(ReplicaIdentityModel); ok && m.publishedOnly {
				model.publishedOnly = true
				model.table.SetRows(model.visibleRows())
				return model, nil
			}
			return refreshed, nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// selectedFixTip is the suggested statement for the selected table, shown above the
// footer so the DBA can copy it; this screen never runs DDL itself.
func (m ReplicaIdentityModel) selectedFixTip() string {
	row := m.table.SelectedRow()
	if len(row) < 2 {
		return ""
	}
	fix := m.fixes[row[0]+"."+row[1]]
	if fix == "" {
		return ""
	}
	return "  Fix: " + fix
}

func (m ReplicaIdentityModel) View() string {
	renderKey := lipgloss.NewStyle().Foreground(ColorGray).Render

	s := RenderHeader("Replica Identity — logical replication readiness") + "\n"
	var summary string
	switch {
	case m.totalCount == 0:
		summary = SeverityColor("All tables have a usable replica identity.", 0)
	default:
		criticalLevel := 0
		if m.criticalCount > 0 {
			criticalLevel = 2
		}
		summary = fmt.Sprintf("%s %s   %s %s",
			renderKey("Tables without replica identity:"), SeverityColor(fmt.Sprintf("%d", m.totalCount), 1),
			renderKey("Replicated with UPDATE/DELETE (broken today):"), SeverityColor(fmt.Sprintf("%d", m.criticalCount), criticalLevel))
	}
	if m.publishedOnly {
		summary += "   " + SeverityColor("[replicated only]", 1)
	}
	s += "  " + summary + "\n"

	rules := []ColorRule{
		{Column: 5, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "critical":
				return 2
			case "warning":
				return 1
			}
			return -1
		}},
	}
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)

	// Always reserve the tip line and truncate it, so the frame height never changes.
	tip := m.selectedFixTip()
	if tip != "" && m.width > 0 {
		tip = lipgloss.NewStyle().MaxWidth(m.width).Render(tip)
	}
	s += "\n" + HintStyle.Render(tip)
	toggleHint := "p replicated only"
	if m.publishedOnly {
		toggleHint = "p show all"
	}
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • "+toggleHint+" • r refresh • q back")
	return s
}
