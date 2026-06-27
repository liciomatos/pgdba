package util

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

type DatabaseSelectorModel struct {
	table        table.Model
	allRows      []table.Row
	filterText   string
	filterMode   bool
	initialModel func() tea.Model
	height       int
}

func (m DatabaseSelectorModel) IsInputMode() bool { return m.filterMode }

func CheckDatabases(initialModel func() tea.Model) tea.Model {
	query := `
        SELECT datname,
               pg_encoding_to_char(encoding) AS encoding,
               datcollate,
               pg_size_pretty(pg_database_size(datname)) AS size
        FROM pg_database
        WHERE datistemplate = false AND datallowconn = true
        ORDER BY datname;
    `

	rows, err := config.Config.DB.Query(query)
	if err != nil {
		return NewErrorModel(err, "Loading databases", initialModel)
	}
	defer rows.Close()

	columns := []table.Column{
		{Title: "Database", Width: 30},
		{Title: "Encoding", Width: 12},
		{Title: "Collation", Width: 20},
		{Title: "Size", Width: 12},
	}

	var rowsData []table.Row
	for rows.Next() {
		var datname, encoding, collation, size string
		if err := rows.Scan(&datname, &encoding, &collation, &size); err != nil {
			return NewErrorModel(err, "Scanning databases row", initialModel)
		}
		nameStr := datname
		if datname == config.Config.DBName {
			nameStr = SeverityColor(datname+" ◀", 0)
		}
		rowsData = append(rowsData, table.Row{nameStr, encoding, collation, size})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rowsData),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(DefaultTableStyles()),
	)

	return DatabaseSelectorModel{table: t, allRows: rowsData, initialModel: initialModel, height: 30}
}

func (m DatabaseSelectorModel) Init() tea.Cmd { return nil }

func (m DatabaseSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
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
			m.filterMode = true
			return m, nil
		case "q", "esc":
			return m.initialModel(), nil
		case "r":
			return CheckDatabases(m.initialModel), nil
		case "enter":
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, nil
			}
			// Strip ANSI from name (current db has a marker appended)
			selectedDB := strings.TrimSuffix(stripAnsi(selectedRow[0]), " ◀")
			if selectedDB == config.Config.DBName {
				return m.initialModel(), nil
			}
			if err := switchDatabase(selectedDB); err != nil {
				return NewErrorModel(err, "Connecting to "+selectedDB, m.initialModel), nil
			}
			return m.initialModel(), nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m DatabaseSelectorModel) View() string {
	s := RenderHeader("Switch Database") + "\n"
	s += m.table.View()
	s += "\n" + FilterFooter(m.filterMode, m.filterText, "↑↓ navigate • enter connect • r refresh • q back")
	return s
}

func switchDatabase(dbname string) error {
	config.Config.DB.Close()
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Config.Host, config.Config.Port, config.Config.User,
		config.Config.Password, dbname, config.Config.SSLMode)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return err
	}
	config.Config.DB = db
	config.Config.DBName = dbname
	config.Config.DB.QueryRow("SHOW server_version").Scan(&config.Config.Version)
	return nil
}
