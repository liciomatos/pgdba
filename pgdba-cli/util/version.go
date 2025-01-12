package util

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type VersionModel struct {
	version      string
	err          error
	initialModel func() tea.Model
}

func CheckVersion(initialModel func() tea.Model) *VersionModel {
	return &VersionModel{version: config.Config.Version, initialModel: initialModel}
}

func (m *VersionModel) Init() tea.Cmd {
	return nil
}

func (m *VersionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m.initialModel(), nil
		}
	}
	return m, nil
}

func (m *VersionModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}
	return fmt.Sprintf("PostgreSQL Version: %s\n\nPress q to quit.", m.version)
}
