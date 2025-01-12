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
}

func CheckReplicationSlotsStatus(initialModel func() tea.Model) ReplicationSlotsModel {
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
		fmt.Printf("Error executing query: %v\n", err)
		return ReplicationSlotsModel{}
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
			fmt.Printf("Error scanning row: %v\n", err)
			return ReplicationSlotsModel{}
		}

		rowsData = append(rowsData, table.Row{
			slotName,
			size,
			fmt.Sprintf("%t", active),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
	)

	return ReplicationSlotsModel{table: t, initialModel: initialModel}
}

func (m ReplicationSlotsModel) Init() tea.Cmd {
	return nil
}

func (m ReplicationSlotsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
				if msg.String() == "y" {
					err := dropReplicationSlot(m.slotToDelete)
					if err != nil {
						fmt.Printf("Error dropping replication slot: %v\n", err)
					} else {
						return CheckReplicationSlotsStatus(m.initialModel), nil
					}
				}
				m.confirmDelete = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			m.slotToDelete = selectedRow[0]
			m.confirmDelete = true
			return m, nil
		case "y":
			if m.confirmDelete {
				err := dropReplicationSlot(m.slotToDelete)
				if err != nil {
					fmt.Printf("Error dropping replication slot: %v\n", err)
				} else {
					return CheckReplicationSlotsStatus(m.initialModel), nil
				}
				m.confirmDelete = false
				return m, nil
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
	s := fmt.Sprintf("PostgreSQL Version: %s\n", config.Config.Version)
	s += fmt.Sprintf("Connected to: %s@%s:%d/%s\n\n", config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName)
	s += m.table.View()
	if m.confirmDelete {
		s += fmt.Sprintf("\nAre you sure you want to delete the replication slot '%s'? (y/n)\n", m.slotToDelete)
	} else {
		s += "\nPress 'd' to drop the selected replication slot. Press 'q' to quit.\n"
	}
	return s
}

func dropReplicationSlot(slotName string) error {
	_, err := config.Config.DB.Exec(fmt.Sprintf("SELECT pg_drop_replication_slot('%s');", slotName))
	return err
}
