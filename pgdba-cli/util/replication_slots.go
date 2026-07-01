package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type ReplicationSlotsModel struct {
	table         table.Model
	initialModel  func() tea.Model
	confirmDelete bool
	slotToDelete  string
	height        int
	width         int
}

func CheckReplicationSlotsStatus(initialModel func() tea.Model) tea.Model {
	slots, err := FetchReplicationSlots(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading replication slots", initialModel)
	}

	columns := []table.Column{
		{Title: "Slot Name", Width: 22},
		{Title: "Plugin", Width: 14},
		{Title: "Type", Width: 10},
		{Title: "Active", Width: 8},
		{Title: "WAL Lag", Width: 12},
		{Title: "Safe WAL", Width: 12},
	}

	var rowsData []table.Row
	for _, s := range slots {
		safeWAL := ""
		if s.SafeWALSize != nil {
			safeWAL = *s.SafeWALSize
		}
		rowsData = append(rowsData, table.Row{
			s.SlotName,
			s.Plugin,
			s.SlotType,
			fmt.Sprintf("%t", s.Active),
			s.WALLag,
			safeWAL,
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

func (m ReplicationSlotsModel) Init() tea.Cmd { return nil }

// ConsumesKey prevents the navigator from intercepting "p" (replication config),
// which would otherwise route to the global PgConfig screen.
// "S" (standbys) uses uppercase to avoid the conflict entirely.
func (m ReplicationSlotsModel) ConsumesKey(key string) bool {
	return key == "p"
}

func (m ReplicationSlotsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		cols := StretchColumn(m.table.Columns(), 0, msg.Width)
		m.table.SetColumns(cols)
		m.table.SetHeight(TableHeight(msg.Height) - 1) // -1 for hint line
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
			if len(selectedRow) == 0 {
				return m, nil
			}
			m.slotToDelete = selectedRow[0]
			m.confirmDelete = true
			return m, nil
		case "r":
			return CheckReplicationSlotsStatus(m.initialModel), nil
		case "S":
			if m.confirmDelete {
				return m, nil
			}
			parent := m.initialModel
			return CheckReplicationStandbys(func() tea.Model {
				return CheckReplicationSlotsStatus(parent)
			}), nil
		case "p":
			if m.confirmDelete {
				return m, nil
			}
			parent := m.initialModel
			return CheckReplicationConfig(func() tea.Model {
				return CheckReplicationSlotsStatus(parent)
			}), nil
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
	// Column 3 = Active: color false slots red (inactive = potential WAL accumulation risk)
	rules := []ColorRule{
		{Column: 3, Colorize: func(v string) int {
			switch v {
			case "true":
				return 0
			case "false":
				return 2
			}
			return -1
		}},
	}
	hint := "  Slots guarantee WAL is kept until consumers catch up. Inactive slots (red) accumulate WAL silently — drop unused ones to prevent disk exhaustion."
	s := RenderHeader("Replication Slots") + "\n"
	s += HintStyle.Render(hint) + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmDelete {
		s += fmt.Sprintf("\nDrop replication slot '%s'? (y/n)\n", m.slotToDelete)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • d drop • S all standbys • p repl config • r refresh • q back")
	}
	return s
}

func dropReplicationSlot(slotName string) error {
	_, err := config.Config.DB.Exec("SELECT pg_drop_replication_slot($1)", slotName)
	return err
}