package config

import (
	"database/sql"

	tea "github.com/charmbracelet/bubbletea"
)

type AppConfig struct {
	User            string
	Password        string
	DBName          string
	SSLMode         string
	Host            string
	Port            int
	URL             string
	DB              *sql.DB
	Version         string
	AppInstance     *tea.Program
	SlowThresholdMS int
	// Replicas are extra servers the MCP tools can query with target=<name>, so
	// per-instance statistics (index usage, cache hit) can be read on standbys too.
	Replicas []*Target
}

// Target is a named server connection besides the primary one (--replica name=URL).
type Target struct {
	Name    string
	Host    string
	Port    int
	DBName  string
	Version string
	DB      *sql.DB
}

var Config = &AppConfig{}
