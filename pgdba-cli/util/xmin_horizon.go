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

// XminHorizonModel lists the sessions, standbys, replication slots and prepared
// transactions holding back the xmin horizon — what keeps vacuum from removing dead
// rows and freezing from advancing.
type XminHorizonModel struct {
	table        table.Model
	allRows      []table.Row
	horizon      XminHorizon
	queryDetails map[string]string // "source:id" → full query text
	filterText   string
	filterMode   bool
	detailMode   bool
	detailText   string
	initialModel func() tea.Model
	width        int
	height       int
}

func (m XminHorizonModel) IsInputMode() bool { return m.filterMode }

// xminHolderKey identifies a holder row: IDs are only unique within a source.
func xminHolderKey(source, id string) string { return source + ":" + id }

func CheckXminHorizon(initialModel func() tea.Model) tea.Model {
	horizon, err := FetchXminHorizon(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading xmin horizon", initialModel)
	}

	columns := []table.Column{
		{Title: "Source", Width: 9},
		{Title: "ID", Width: 16},
		{Title: "Status", Width: 9},
		{Title: "Xmin Age", Width: 12},
		{Title: "Xact Age", Width: 10},
		{Title: "Database", Width: 12},
		{Title: "User", Width: 12},
		{Title: "State", Width: 20},
		{Title: "Query", Width: 40},
	}

	var rowsData []table.Row
	details := make(map[string]string)
	for _, holder := range horizon.Holders {
		status := XminHolderStatus(holder.XminAge, holder.TransactionAgeSeconds, horizon.FreezeMaxAge)
		transactionAge := "-"
		if holder.TransactionAgeSeconds != nil {
			transactionAge = formatAge(*holder.TransactionAgeSeconds)
		}
		state := holder.State
		if holder.CatalogOnly {
			state += " (catalog only)"
		}
		if holder.Query != "" {
			details[xminHolderKey(holder.Source, holder.ID)] = holder.Query
		}
		rowsData = append(rowsData, table.Row{
			holder.Source,
			holder.ID,
			status.String(),
			formatCount(float64(holder.XminAge)),
			transactionAge,
			holder.Database,
			holder.User,
			state,
			strings.Join(strings.Fields(holder.Query), " "),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)
	return XminHorizonModel{
		table:        t,
		allRows:      rowsData,
		horizon:      horizon,
		queryDetails: details,
		initialModel: initialModel,
	}
}

func (m XminHorizonModel) Init() tea.Cmd { return nil }

func (m XminHorizonModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetColumns(StretchColumn(m.table.Columns(), 8, msg.Width))
		FitTableHeight(&m.table, TableHeight(msg.Height), msg.Height, func() string { return m.View() })
		return m, nil
	case tea.KeyMsg:
		if m.detailMode {
			switch msg.String() {
			case "q", "esc", "enter":
				m.detailMode = false
			}
			return m, nil
		}
		if m.filterMode {
			switch msg.Type {
			case tea.KeyEsc:
				m.filterMode = false
				m.filterText = ""
				m.table.SetRows(m.allRows)
			case tea.KeyBackspace:
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.table.SetRows(FilterRows(m.allRows, m.filterText))
				}
			case tea.KeyRunes:
				m.filterText += msg.String()
				m.table.SetRows(FilterRows(m.allRows, m.filterText))
			case tea.KeyEnter:
				m.filterMode = false
			}
			return m, nil
		}
		switch msg.String() {
		case "enter":
			if row := m.table.SelectedRow(); len(row) > 1 {
				if detail, ok := m.queryDetails[xminHolderKey(row[0], row[1])]; ok {
					m.detailText = detail
					m.detailMode = true
				}
			}
			return m, nil
		case "/":
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckXminHorizon(m.initialModel), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// horizonSummary is the one-line answer to "who holds the horizon?".
func (m XminHorizonModel) horizonSummary() string {
	renderLabel := HintStyle.Render
	feedback := "off"
	if m.horizon.HotStandbyFeedback {
		feedback = "on"
	}
	contextLine := renderLabel(fmt.Sprintf("  autovacuum_freeze_max_age %s • hot_standby_feedback %s • database datfrozenxid age %s",
		formatCount(float64(m.horizon.FreezeMaxAge)), feedback, formatCount(float64(m.horizon.DatabaseFrozenXIDAge))))
	holder := m.horizon.HorizonHolder()
	if holder == nil {
		message := "  Nothing holds the xmin horizon back: vacuum can clean up to the newest committed transaction."
		if len(m.horizon.Holders) > 0 {
			message = "  Only catalog_xmin is held (by slots): user tables can be vacuumed up to the newest committed transaction."
		}
		return SeverityColor(message, 0) + "\n" + contextLine
	}
	status := XminHolderStatus(holder.XminAge, holder.TransactionAgeSeconds, m.horizon.FreezeMaxAge)
	summary := fmt.Sprintf("  Horizon held by %s %s — %s XIDs old", holder.Source, holder.ID, formatCount(float64(holder.XminAge)))
	return SeverityColor(summary, int(status)) + "\n" + contextLine
}

func (m XminHorizonModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Xmin Horizon", m.detailText, m.width)
	}
	rules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			switch strings.TrimSpace(v) {
			case "critical":
				return 2
			case "warning":
				return 1
			case "ok":
				return 0
			}
			return -1
		}},
	}
	summary := m.horizonSummary()
	if m.width > 0 {
		summary = lipgloss.NewStyle().MaxWidth(m.width).Render(summary)
	}
	s := RenderHeader("Xmin Horizon") + "\n"
	s += summary + "\n\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter query • / filter • r refresh • ? help • q back")
	return s
}
