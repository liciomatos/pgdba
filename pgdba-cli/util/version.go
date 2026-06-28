package util

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/liciomatos/pgdba-cli/config"
)

type VersionModel struct {
	version      string
	initialModel func() tea.Model
	width        int
	height       int
}

func CheckVersion(initialModel func() tea.Model) tea.Model {
	return VersionModel{version: config.Config.Version, initialModel: initialModel}
}

func (m VersionModel) Init() tea.Cmd { return nil }

func (m VersionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
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

func (m VersionModel) View() string {
	s := RenderHeader("Check Version") + "\n"
	s += fmt.Sprintf("Server version: %s\n", m.version)
	s += "\n" + FooterStyle.Render("r refresh • q back")
	return s
}
