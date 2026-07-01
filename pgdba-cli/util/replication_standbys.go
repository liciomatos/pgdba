package util

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type ReplicationStandbysModel struct {
	table        table.Model
	standbys     []StreamingStandby
	initialModel func() tea.Model
	confirmKill  bool
	pidToKill    int
	appToKill    string
	height       int
	width        int
}

func CheckReplicationStandbys(initialModel func() tea.Model) tea.Model {
	standbys, err := FetchStreamingStandbys(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading streaming standbys", initialModel)
	}

	columns := []table.Column{
		{Title: "App Name", Width: 18},
		{Title: "Client", Width: 16},
		{Title: "State", Width: 12},
		{Title: "Sync", Width: 8},
		{Title: "Write Lag", Width: 20},
		{Title: "Flush Lag", Width: 20},
		{Title: "Replay Lag", Width: 20},
		{Title: "Lag Bytes", Width: 12},
	}

	var rowsData []table.Row
	for _, s := range standbys {
		rowsData = append(rowsData, table.Row{
			s.ApplicationName,
			s.ClientAddr,
			s.State,
			s.SyncState,
			s.WriteLag,
			s.FlushLag,
			s.ReplayLag,
			fmt.Sprintf("%d", s.LagBytes),
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return ReplicationStandbysModel{
		table:        t,
		standbys:     standbys,
		initialModel: initialModel,
		height:       30,
		width:        120,
	}
}

func (m ReplicationStandbysModel) Init() tea.Cmd { return nil }

func (m ReplicationStandbysModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		m.table.SetHeight(TableHeight(msg.Height))
		cols := StretchColumn(m.table.Columns(), 0, msg.Width)
		m.table.SetColumns(cols)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
			return m.initialModel(), nil
		case "r":
			return CheckReplicationStandbys(m.initialModel), nil
		case "k":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			// Find the matching standby by application name + client
			for _, s := range m.standbys {
				if s.ApplicationName == selectedRow[0] && s.ClientAddr == selectedRow[1] {
					m.pidToKill = s.PID
					m.appToKill = s.ApplicationName
					m.confirmKill = true
					break
				}
			}
			return m, nil
		case "y":
			if m.confirmKill {
				m.confirmKill = false
				if _, err := config.Config.DB.Exec(
					"SELECT pg_terminate_backend($1)", m.pidToKill,
				); err != nil {
					return NewErrorModel(err,
						fmt.Sprintf("Terminating walsender PID %d", m.pidToKill),
						m.initialModel), nil
				}
				return CheckReplicationStandbys(m.initialModel), nil
			}
		case "n":
			if m.confirmKill {
				m.confirmKill = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m ReplicationStandbysModel) View() string {
	rules := []ColorRule{
		{Column: 3, Colorize: func(v string) int {
			switch v {
			case "sync", "quorum":
				return 0
			case "async":
				return 3
			default:
				return -1
			}
		}},
	}

	s := RenderHeader("Streaming Replication — Standbys") + "\n"
	if len(m.standbys) == 0 {
		s += FooterStyle.Render("No streaming standbys connected.") + "\n"
	} else {
		s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	}

	if m.confirmKill {
		s += fmt.Sprintf("\nTerminate walsender for '%s' (PID %d)? (y/n)\n",
			m.appToKill, m.pidToKill)
	} else {
		s += "\n" + FooterStyle.Render("↑↓ navigate • k terminate walsender • r refresh • q back")
	}
	return s
}