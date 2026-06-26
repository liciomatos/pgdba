package util

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type VersionModel struct {
	version      string
	initialModel func() tea.Model
}

func CheckVersion(initialModel func() tea.Model) tea.Model {
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
		case "r":
			return CheckVersion(m.initialModel), nil
		}
	}
	return m, nil
}

func (m *VersionModel) View() string {
	s := fmt.Sprintf("PostgreSQL Version: %s\n\n", m.version)
	s += lipgloss.NewStyle().Faint(true).Render("r refresh • q back")
	return s
}
