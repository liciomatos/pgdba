package config

import (
	"database/sql"

	tea "github.com/charmbracelet/bubbletea"
)

type AppConfig struct {
	User        string
	Password    string
	DBName      string
	SSLMode     string
	Host        string
	Port        int
	DB          *sql.DB
	Version     string
	AppInstance *tea.Program
}

var Config = &AppConfig{}
