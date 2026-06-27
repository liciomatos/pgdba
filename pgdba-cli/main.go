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

	config.Config.AppInstance = tea.NewProgram(wrap(util.CheckDashboard()), tea.WithAltScreen())
	if _, err := config.Config.AppInstance.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting program: %v\n", err)
	}
}

// inputModer is implemented by screens that have an active text input (filter mode).
// When IsInputMode returns true, the navigator skips global key interception.
type inputModer interface {
	IsInputMode() bool
}

type navigator struct {
	child tea.Model
}

func wrap(m tea.Model) navigator { return navigator{child: m} }
func dashboard() tea.Model       { return util.CheckDashboard() }

func (n navigator) Init() tea.Cmd { return n.child.Init() }
func (n navigator) View() string  { return n.child.View() }

func (n navigator) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		inInputMode := false
		if im, ok := n.child.(inputModer); ok {
			inInputMode = im.IsInputMode()
		}
		if !inInputMode {
			switch key.String() {
			case "1":
				return wrap(util.IdentifySlowQueries(dashboard)), nil
			case "2":
				return wrap(util.CheckLongRunningQueries(dashboard)), nil
			case "3":
				return wrap(util.CheckReplicationSlotsStatus(dashboard)), nil
			case "4":
				return wrap(util.CheckRecordLocks(dashboard)), nil
			case "5":
				return wrap(util.CheckConnections(dashboard)), nil
			case "6":
				return wrap(util.CheckAutovacuum(dashboard)), nil
			case "7":
				return wrap(util.CheckIndexUsage(dashboard)), nil
			case "8":
				return wrap(util.CheckCacheHit(dashboard)), nil
			case "9":
				return wrap(util.CheckUsers(dashboard)), nil
			case "0":
				return wrap(util.CheckRoles(dashboard)), nil
			case "p":
				return wrap(util.CheckPgConfig(dashboard)), nil
			case "s":
				return wrap(util.CheckSchemaBrowser(dashboard)), nil
			case "e":
				return wrap(util.CheckExtensions(dashboard)), nil
			case "D":
				return wrap(util.CheckDatabases(dashboard)), nil
			case "v":
				return wrap(util.CheckVersion(dashboard)), nil
			}
		}
	}
	newChild, cmd := n.child.Update(msg)
	n.child = newChild
	return n, cmd
}
