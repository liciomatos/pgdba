package util

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type lockDetail struct {
	blocked  string
	blocking string
}

type RecordLocksModel struct {
	table            table.Model
	allRows          []table.Row
	lockDetails      map[string]lockDetail // blockedPID:blockingPID → statements
	filterText       string
	filterMode       bool
	detailMode       bool
	detailText       string
	initialModel     func() tea.Model
	confirmTerminate bool
	pidToTerminate   int
	width            int
	height           int
}

func (m RecordLocksModel) IsInputMode() bool { return m.filterMode }

func CheckRecordLocks(initialModel func() tea.Model) tea.Model {
	blocked, err := FetchBlockedQueries(context.Background(), config.Config.DB)
	if err != nil {
		return NewErrorModel(err, "Loading record locks", initialModel)
	}

	columns := []table.Column{
		{Title: "Blocked PID", Width: 15},
		{Title: "Blocked User", Width: 15},
		{Title: "Blocking PID", Width: 15},
		{Title: "Blocking User", Width: 15},
		{Title: "Blocked Statement", Width: 50},
		{Title: "Blocking Statement", Width: 50},
		{Title: "Blocked App", Width: 20},
		{Title: "Blocking App", Width: 20},
	}

	var rowsData []table.Row
	details := make(map[string]lockDetail)

	for _, bq := range blocked {
		key := fmt.Sprintf("%d:%d", bq.BlockedPID, bq.BlockingPID)
		details[key] = lockDetail{blocked: bq.BlockedStatement, blocking: bq.BlockingStatement}

		blockedStmt := strings.ReplaceAll(bq.BlockedStatement, "\n", " ")
		blockingStmt := strings.ReplaceAll(bq.BlockingStatement, "\n", " ")
		if len(blockedStmt) > 50 {
			blockedStmt = blockedStmt[:50] + "..."
		}
		if len(blockingStmt) > 50 {
			blockingStmt = blockingStmt[:50] + "..."
		}

		rowsData = append(rowsData, table.Row{
			fmt.Sprintf("%d", bq.BlockedPID),
			bq.BlockedUser,
			fmt.Sprintf("%d", bq.BlockingPID),
			bq.BlockingUser,
			blockedStmt,
			blockingStmt,
			bq.BlockedApplication,
			bq.BlockingApplication,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return RecordLocksModel{
		table:        t,
		allRows:      rowsData,
		lockDetails:  details,
		initialModel: initialModel,
		width:        120,
		height:       30,
	}
}

func (m RecordLocksModel) Init() tea.Cmd { return nil }

func (m RecordLocksModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		fixedCols := []int{0, 1, 2, 3, 6, 7}
		fixed := 0
		cols := m.table.Columns()
		for _, i := range fixedCols {
			fixed += cols[i].Width
		}
		stmtWidth := (msg.Width - fixed - len(cols) - 2) / 2
		if stmtWidth < 20 {
			stmtWidth = 20
		}
		cols[4].Width = stmtWidth
		cols[5].Width = stmtWidth
		m.table.SetColumns(cols)
		m.table.SetHeight(TableHeight(msg.Height))
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
			if !m.confirmTerminate {
				row := m.table.SelectedRow()
				if len(row) >= 3 {
					key := row[0] + ":" + row[2] // blockedPID:blockingPID (plain, no ANSI)
					if d, ok := m.lockDetails[key]; ok {
						m.detailText = "── Blocked Statement ──\n" + d.blocked +
							"\n\n── Blocking Statement ──\n" + d.blocking
						m.detailMode = true
					}
				}
			}
			return m, nil
		case "/":
			if !m.confirmTerminate {
				m.filterMode = true
			}
			return m, nil
		case "q", "esc":
			if m.confirmTerminate {
				m.confirmTerminate = false
				return m, nil
			}
			return m.initialModel(), nil
		case "t":
			if m.confirmTerminate {
				m.confirmTerminate = false
				return m, nil
			}
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			m.pidToTerminate, _ = strconv.Atoi(selectedRow[2])
			m.confirmTerminate = true
			return m, nil
		case "a":
			if m.confirmTerminate {
				m.confirmTerminate = false
				return m, nil
			}
			m.confirmTerminate = true
			m.pidToTerminate = 0
			return m, nil
		case "r":
			return CheckRecordLocks(m.initialModel), nil
		case "y":
			if m.confirmTerminate {
				if m.pidToTerminate != 0 {
					if err := terminateSession(m.pidToTerminate); err != nil {
						return NewErrorModel(err, fmt.Sprintf("Terminating session PID %d", m.pidToTerminate), m.initialModel), nil
					}
				} else {
					if err := terminateAllSessions(m.table.Rows()); err != nil {
						return NewErrorModel(err, "Terminating all sessions", m.initialModel), nil
					}
				}
				return CheckRecordLocks(m.initialModel), nil
			}
		case "n":
			if m.confirmTerminate {
				m.confirmTerminate = false
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m RecordLocksModel) View() string {
	if m.detailMode {
		return RenderQueryDetail("Blocked Queries", m.detailText, m.width)
	}
	rules := []ColorRule{
		{Column: 1, Colorize: func(string) int { return 2 }}, // Blocked User → red
		{Column: 3, Colorize: func(string) int { return 1 }}, // Blocking User → yellow
	}
	s := RenderHeader("Blocked Queries") + "\n"
	s += ColorizeTable(m.table.View(), m.table.Columns(), rules)
	if m.confirmTerminate {
		if m.pidToTerminate != 0 {
			s += fmt.Sprintf("\nTerminate session with PID %d? (y/n)\n", m.pidToTerminate)
		} else {
			s += "\nTerminate all blocking sessions? (y/n)\n"
		}
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter detail • t terminate • a all • / filter • r refresh • q back")
	}
	return s
}

func terminateSession(pid int) error {
	_, err := config.Config.DB.Exec("SELECT pg_terminate_backend($1)", pid)
	return err
}

func terminateAllSessions(rows []table.Row) error {
	for _, row := range rows {
		pid, err := strconv.Atoi(row[2])
		if err != nil {
			return fmt.Errorf("error converting PID: %v", err)
		}
		if err := terminateSession(pid); err != nil {
			fmt.Printf("Error terminating session with PID %d: %v\n", pid, err)
		}
	}
	return nil
}
