package util

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type RecordLocksModel struct {
	table            table.Model
	allRows          []table.Row
	filterText       string
	filterMode       bool
	initialModel     func() tea.Model
	confirmTerminate bool
	pidToTerminate   int
	width            int
	height           int
}

func (m RecordLocksModel) IsInputMode() bool { return m.filterMode }

func CheckRecordLocks(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT
            blocked_locks.pid AS blocked_pid,
            blocked_activity.usename AS blocked_user,
            blocking_locks.pid AS blocking_pid,
            blocking_activity.usename AS blocking_user,
            blocked_activity.query AS blocked_statement,
            blocking_activity.query AS current_statement_in_blocking_process,
            blocked_activity.application_name AS blocked_application,
            blocking_activity.application_name AS blocking_application
        FROM
            pg_catalog.pg_locks blocked_locks
        JOIN
            pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid = blocked_locks.pid
        JOIN
            pg_catalog.pg_locks blocking_locks
            ON blocking_locks.locktype = blocked_locks.locktype
            AND blocking_locks.DATABASE IS NOT DISTINCT FROM blocked_locks.DATABASE
            AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
            AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
            AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
            AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
            AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
            AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
            AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
            AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
            AND blocking_locks.pid != blocked_locks.pid
        JOIN
            pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid = blocking_locks.pid
        WHERE
            NOT blocked_locks.GRANTED;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading record locks", initialModel)
	}
	defer rows.Close()

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
	for rows.Next() {
		var blockedPID, blockingPID int
		var blockedUser, blockingUser, blockedStatement, blockingStatement, blockedApplication, blockingApplication string

		if err := rows.Scan(&blockedPID, &blockedUser, &blockingPID, &blockingUser, &blockedStatement, &blockingStatement, &blockedApplication, &blockingApplication); err != nil {
			return NewErrorModel(err, "Scanning record locks row", initialModel)
		}

		blockedStatement = strings.ReplaceAll(blockedStatement, "\n", " ")
		blockingStatement = strings.ReplaceAll(blockingStatement, "\n", " ")
		if len(blockedStatement) > 50 {
			blockedStatement = blockedStatement[:50] + "..."
		}
		if len(blockingStatement) > 50 {
			blockingStatement = blockingStatement[:50] + "..."
		}

		rowsData = append(rowsData, table.Row{
			fmt.Sprintf("%d", blockedPID),
			SeverityColor(blockedUser, 2),
			fmt.Sprintf("%d", blockingPID),
			SeverityColor(blockingUser, 1),
			blockedStatement,
			blockingStatement,
			blockedApplication,
			blockingApplication,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return RecordLocksModel{table: t, allRows: rowsData, initialModel: initialModel, width: 120, height: 30}
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
	s := RenderHeader("Blocked Queries") + "\n"
	s += m.table.View()
	if m.confirmTerminate {
		if m.pidToTerminate != 0 {
			s += fmt.Sprintf("\nTerminate session with PID %d? (y/n)\n", m.pidToTerminate)
		} else {
			s += "\nTerminate all blocking sessions? (y/n)\n"
		}
	} else {
		s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • t terminate • a terminate all • r refresh • q back")
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
