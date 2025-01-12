package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/util"
)

func main() {
	flag.StringVar(&config.Config.User, "user", "postgres", "database user")
	flag.StringVar(&config.Config.Password, "password", "", "database password")
	flag.StringVar(&config.Config.DBName, "dbname", "mydb", "database name")
	flag.StringVar(&config.Config.SSLMode, "sslmode", "disable", "ssl mode (disable, require, verify-ca, verify-full)")
	flag.StringVar(&config.Config.Host, "host", "", "database host (mandatory)")
	flag.IntVar(&config.Config.Port, "port", 5432, "database port (optional)")
	flag.Parse()

	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Config.Host, config.Config.Port, config.Config.User, config.Config.Password, config.Config.DBName, config.Config.SSLMode)
	var err error
	config.Config.DB, err = sql.Open("postgres", connStr)
	if err != nil {
		fmt.Printf("Error connecting to database: %v\n", err)
		os.Exit(1)
	}

	// Fetch the PostgreSQL version
	err = config.Config.DB.QueryRow("SHOW server_version;").Scan(&config.Config.Version)
	if err != nil {
		fmt.Printf("Error fetching PostgreSQL version: %v\n", err)
		os.Exit(1)
	}

	config.Config.AppInstance = tea.NewProgram(initialModel(), tea.WithAltScreen())
	if err := config.Config.AppInstance.Start(); err != nil {
		fmt.Printf("Error starting program: %v\n", err)
	}
}

type model struct {
	choices  []string
	cursor   int
	selected map[int]struct{}
}

func initialModel() tea.Model {
	return model{
		choices:  []string{"Check Version", "Slow Queries", "Replication Slots", "Blocked Queries", "Quit"},
		selected: make(map[int]struct{}),
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			switch m.cursor {
			case 0:
				return util.CheckVersion(initialModel), nil
			case 1:
				return util.IdentifySlowQueries(initialModel), nil
			case 2:
				return util.CheckReplicationSlotsStatus(initialModel), nil
			case 3:
				return util.CheckRecordLocks(initialModel), nil
			case 4:
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

func (m model) View() string {
	s := fmt.Sprintf("PostgreSQL Version: %s\n", config.Config.Version)
	s += fmt.Sprintf("Connected to: %s@%s:%d/%s\n\n", config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName)
	s += "What do you want to do?\n\n"

	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
		}

		s += fmt.Sprintf("%s %s\n", cursor, choice)
	}

	s += "\nPress q to quit.\n"

	return s
}
