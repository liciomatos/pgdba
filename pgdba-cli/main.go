package main

import (
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/lib/pq"
	"github.com/liciomatos/pgdba-cli/config"
	"github.com/liciomatos/pgdba-cli/mcpserver"
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

// buildConnStr returns a lib/pq connection string. When --url is given, it
// parses the URI to populate config fields (for display in headers/errors) and
// returns the URI itself directly to the driver. Without --url, it assembles the
// string from individual flags, falling back to ~/.pgpass for missing passwords.
func buildConnStr() (string, error) {
	if config.Config.URL != "" {
		u, err := url.Parse(config.Config.URL)
		if err != nil {
			return "", err
		}
		config.Config.Host = u.Hostname()
		if portStr := u.Port(); portStr != "" {
			config.Config.Port, _ = strconv.Atoi(portStr)
		}
		if u.User != nil {
			config.Config.User = u.User.Username()
			config.Config.Password, _ = u.User.Password()
		}
		config.Config.DBName = strings.TrimPrefix(u.Path, "/")
		if sslmode := u.Query().Get("sslmode"); sslmode != "" {
			config.Config.SSLMode = sslmode
		}
		return config.Config.URL, nil
	}

	if config.Config.Password == "" {
		config.Config.Password = lookupPgPass(
			config.Config.Host, config.Config.Port,
			config.Config.DBName, config.Config.User,
		)
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Config.Host, config.Config.Port, config.Config.User,
		config.Config.Password, config.Config.DBName, config.Config.SSLMode), nil
}

func main() {
	flag.StringVar(&config.Config.URL, "url", getEnv("DATABASE_URL", ""), "PostgreSQL connection URI (postgres://user:pass@host:5432/db?sslmode=disable)")
	flag.StringVar(&config.Config.Host, "host", getEnv("PGHOST", ""), "database host")
	flag.IntVar(&config.Config.Port, "port", getEnvInt("PGPORT", 5432), "database port")
	flag.StringVar(&config.Config.User, "user", getEnv("PGUSER", "postgres"), "database user")
	flag.StringVar(&config.Config.Password, "password", getEnv("PGPASSWORD", ""), "database password")
	flag.StringVar(&config.Config.DBName, "dbname", getEnv("PGDATABASE", "mydb"), "database name")
	flag.StringVar(&config.Config.SSLMode, "sslmode", getEnv("PGSSLMODE", "disable"), "ssl mode (disable, require, verify-ca, verify-full)")
	flag.IntVar(&config.Config.SlowThresholdMS, "slow-ms", getEnvInt("PG_SLOW_MS", 1000), "slow query threshold in ms (default 1000)")
	var serveMCP bool
	var mcpPort int
	flag.BoolVar(&serveMCP, "mcp", false, "Start MCP server mode (HTTP/SSE on --mcp-port)")
	flag.IntVar(&mcpPort, "mcp-port", 8811, "Port for MCP SSE server (used with --mcp)")
	flag.Parse()

	connStr, err := buildConnStr()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgdba-cli: invalid connection URL: %v\n", err)
		os.Exit(1)
	}

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

	if serveMCP {
		if err := mcpserver.Serve(mcpPort); err != nil {
			fmt.Fprintf(os.Stderr, "pgdba-cli: MCP server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	config.Config.AppInstance = tea.NewProgram(navigator{child: util.CheckDashboard()}, tea.WithAltScreen())
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
	child  tea.Model
	width  int
	height int
}

func dashboard() tea.Model { return util.CheckDashboard() }

// wrapChild wraps m in a navigator and immediately delivers the current terminal
// size so the new screen renders at the correct height from the first frame.
func (n navigator) wrapChild(m tea.Model) navigator {
	m, _ = m.Update(tea.WindowSizeMsg{Width: n.width, Height: n.height})
	return navigator{child: m, width: n.width, height: n.height}
}

func (n navigator) Init() tea.Cmd { return n.child.Init() }
func (n navigator) View() string  { return n.child.View() }

func (n navigator) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		n.width = wsm.Width
		n.height = wsm.Height
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		inInputMode := false
		if im, ok := n.child.(inputModer); ok {
			inInputMode = im.IsInputMode()
		}
		if !inInputMode {
			switch key.String() {
			case "1":
				return n.wrapChild(util.IdentifySlowQueries(dashboard)), nil
			case "2":
				return n.wrapChild(util.CheckLongRunningQueries(dashboard)), nil
			case "3":
				return n.wrapChild(util.CheckReplicationSlotsStatus(dashboard)), nil
			case "4":
				return n.wrapChild(util.CheckRecordLocks(dashboard)), nil
			case "5":
				return n.wrapChild(util.CheckConnections(dashboard)), nil
			case "6":
				return n.wrapChild(util.CheckAutovacuum(dashboard)), nil
			case "7":
				return n.wrapChild(util.CheckIndexUsage(dashboard)), nil
			case "8":
				return n.wrapChild(util.CheckCacheHit(dashboard)), nil
			case "9":
				return n.wrapChild(util.CheckUsers(dashboard)), nil
			case "0":
				return n.wrapChild(util.CheckRoles(dashboard)), nil
			case "p":
				return n.wrapChild(util.CheckPgConfig(dashboard)), nil
			case "s":
				return n.wrapChild(util.CheckSchemaBrowser(dashboard)), nil
			case "e":
				return n.wrapChild(util.CheckExtensions(dashboard)), nil
			case "D":
				return n.wrapChild(util.CheckDatabases(dashboard)), nil
			case "L":
				return n.wrapChild(util.CheckQueryLoad(dashboard)), nil
			case "w":
				return n.wrapChild(util.CheckWaitEvents(dashboard)), nil
			case "f":
				return n.wrapChild(util.CheckFreezeMonitor(dashboard)), nil
			}
		}
	}
	newChild, cmd := n.child.Update(msg)
	n.child = newChild
	return n, cmd
}
