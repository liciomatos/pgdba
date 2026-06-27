package util

import (
	"fmt"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type ReplicationSlotsModel struct {
	table         table.Model
	initialModel  func() tea.Model
	confirmDelete bool
	slotToDelete  string
	height        int
}

func CheckReplicationSlotsStatus(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            slot_name,
            pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS size,
            active
        FROM
            pg_replication_slots;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading replication slots", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Slot Name", Width: 20},
		{Title: "Size (GB)", Width: 10},
		{Title: "Active", Width: 10},
	}

	var rowsData []table.Row
	for rows.Next() {
		var slotName, size string
		var active bool

		err := rows.Scan(&slotName, &size, &active)
		if err != nil {
			return NewErrorModel(err, "Scanning replication slots row", initialModel)
		}

		activeStr := fmt.Sprintf("%t", active)

		rowsData = append(rowsData, table.Row{
			slotName,
			size,
			activeStr,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ReplicationSlotsModel{table: t, initialModel: initialModel, height: 30}
}

func (m ReplicationSlotsModel) Init() tea.Cmd {
	return nil
}

func (m ReplicationSlotsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.table.SetHeight(TableHeight(msg.Height))
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.confirmDelete {
				m.confirmDelete = false
				return m, nil
			}
			return m.initialModel(), nil
		case "d":
			if m.confirmDelete {
				m.confirmDelete = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			m.slotToDelete = selectedRow[0]
			m.confirmDelete = true
			return m, nil
		case "r":
			return CheckReplicationSlotsStatus(m.initialModel), nil
		case "y":
			if m.confirmDelete {
				if err := dropReplicationSlot(m.slotToDelete); err != nil {
					return NewErrorModel(err, "Dropping replication slot "+m.slotToDelete, m.initialModel), nil
				}
				return CheckReplicationSlotsStatus(m.initialModel), nil
			}
		case "n":
			if m.confirmDelete {
				m.confirmDelete = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m ReplicationSlotsModel) View() string {
	rules := []ColorRule{
		{Column: 2, Colorize: func(v string) int {
			switch v {
			case "true":
				return 0
			case "false":
				return 2
			}
			return -1
		}},
	}
	s := RenderHeader("Replication Slots") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmDelete {
		s += fmt.Sprintf("\nDrop replication slot '%s'? (y/n)\n", m.slotToDelete)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • d drop • r refresh • q back")
	}
	return s
}

func dropReplicationSlot(slotName string) error {
	_, err := config.Config.DB.Exec("SELECT pg_drop_replication_slot($1)", slotName)
	return err
}
