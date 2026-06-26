package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/util"
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func lookupPgPass(host string, port int, dbname, user string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return lookupPgPassFile(filepath.Join(home, ".pgpass"), host, port, dbname, user)
}

func lookupPgPassFile(path, host string, port int, dbname, user string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	portStr := strconv.Itoa(port)
	match := func(pattern, value string) bool {
		return pattern == "*" || pattern == value
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 5)
		if len(parts) != 5 {
			continue
		}
		if match(parts[0], host) && match(parts[1], portStr) &&
			match(parts[2], dbname) && match(parts[3], user) {
			return parts[4]
		}
	}
	return ""
}

func main() {
	flag.StringVar(&config.Config.Host, "host", getEnv("PGHOST", ""), "database host (mandatory)")
	flag.IntVar(&config.Config.Port, "port", getEnvInt("PGPORT", 5432), "database port")
	flag.StringVar(&config.Config.User, "user", getEnv("PGUSER", "postgres"), "database user")
	flag.StringVar(&config.Config.Password, "password", getEnv("PGPASSWORD", ""), "database password")
	flag.StringVar(&config.Config.DBName, "dbname", getEnv("PGDATABASE", "mydb"), "database name")
	flag.StringVar(&config.Config.SSLMode, "sslmode", getEnv("PGSSLMODE", "disable"), "ssl mode (disable, require, verify-ca, verify-full)")
	flag.Parse()

	if config.Config.Password == "" {
		config.Config.Password = lookupPgPass(
			config.Config.Host, config.Config.Port,
			config.Config.DBName, config.Config.User,
		)
	}

	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Config.Host, config.Config.Port, config.Config.User,
		config.Config.Password, config.Config.DBName, config.Config.SSLMode)

	var err error
	config.Config.DB, err = sql.Open("postgres", connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgdba-cli: failed to open database connection\n")
		fmt.Fprintf(os.Stderr, "  host=%s port=%d user=%s dbname=%s sslmode=%s\n",
			config.Config.Host, config.Config.Port, config.Config.User,
			config.Config.DBName, config.Config.SSLMode)
		fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		os.Exit(1)
	}

	if err = config.Config.DB.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "pgdba-cli: cannot reach PostgreSQL server\n")
		fmt.Fprintf(os.Stderr, "  host=%s port=%d user=%s dbname=%s\n",
			config.Config.Host, config.Config.Port, config.Config.User, config.Config.DBName)
		fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		os.Exit(1)
	}

	if err = config.Config.DB.QueryRow("SHOW server_version;").Scan(&config.Config.Version); err != nil {
		fmt.Fprintf(os.Stderr, "pgdba-cli: connected but could not read server version: %v\n", err)
		os.Exit(1)
	}

	config.Config.AppInstance = tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := config.Config.AppInstance.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting program: %v\n", err)
	}
}

type model struct {
	choices  []string
	cursor   int
	selected map[int]struct{}
}

func initialModel() tea.Model {
	return model{
		choices: []string{
			"Check Version",
			"Slow Queries",
			"Long Running Queries",
			"Replication Slots",
			"Blocked Queries",
			"Connections Overview",
			"Autovacuum Monitor",
			"Index Usage",
			"Cache Hit Ratio",
			"Quit",
		},
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
				return util.CheckLongRunningQueries(initialModel), nil
			case 3:
				return util.CheckReplicationSlotsStatus(initialModel), nil
			case 4:
				return util.CheckRecordLocks(initialModel), nil
			case 5:
				return util.CheckConnections(initialModel), nil
			case 6:
				return util.CheckAutovacuum(initialModel), nil
			case 7:
				return util.CheckIndexUsage(initialModel), nil
			case 8:
				return util.CheckCacheHit(initialModel), nil
			case 9:
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
