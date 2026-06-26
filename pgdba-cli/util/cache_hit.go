package util

import (
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type CacheHitModel struct {
	table        table.Model
	initialModel func() tea.Model
}

func CheckCacheHit(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            relname,
            heap_blks_read,
            heap_blks_hit,
            CASE WHEN heap_blks_hit + heap_blks_read = 0 THEN 0
                 ELSE ROUND(100.0 * heap_blks_hit / (heap_blks_hit + heap_blks_read), 2)
            END AS cache_hit_ratio,
            idx_blks_read,
            idx_blks_hit,
            CASE WHEN idx_blks_hit + idx_blks_read = 0 THEN 0
                 ELSE ROUND(100.0 * idx_blks_hit / (idx_blks_hit + idx_blks_read), 2)
            END AS idx_cache_hit_ratio
        FROM pg_statio_user_tables
        ORDER BY heap_blks_read DESC
        LIMIT 20;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading cache hit ratios", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Table", Width: 25},
		{Title: "Heap Read", Width: 12},
		{Title: "Heap Hit", Width: 12},
		{Title: "Cache Hit %", Width: 12},
		{Title: "Idx Read", Width: 12},
		{Title: "Idx Hit", Width: 12},
		{Title: "Idx Hit %", Width: 12},
	}

	var rowsData []table.Row
	for rows.Next() {
		var relname string
		var heapRead, heapHit, idxRead, idxHit int64
		var cacheHitRatio, idxCacheHitRatio float64

		if err := rows.Scan(&relname, &heapRead, &heapHit, &cacheHitRatio, &idxRead, &idxHit, &idxCacheHitRatio); err != nil {
			return NewErrorModel(err, "Scanning cache hit row", initialModel)
		}

		rowsData = append(rowsData, table.Row{
			relname,
			fmt.Sprintf("%d", heapRead),
			fmt.Sprintf("%d", heapHit),
			fmt.Sprintf("%.2f%%", cacheHitRatio),
			fmt.Sprintf("%d", idxRead),
			fmt.Sprintf("%d", idxHit),
			fmt.Sprintf("%.2f%%", idxCacheHitRatio),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
	)

	return CacheHitModel{table: t, initialModel: initialModel}
}

func (m CacheHitModel) Init() tea.Cmd { return nil }

func (m CacheHitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckCacheHit(m.initialModel), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m CacheHitModel) View() string {
	s := fmt.Sprintf("PostgreSQL Version: %s\n", config.Config.Version)
	s += fmt.Sprintf("Connected to: %s@%s:%d/%s\n\n", config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName)
	s += m.table.View()
	s += "\n" + lipgloss.NewStyle().Faint(true).Render("↑↓ navigate • r refresh • q back")
	return s
}
