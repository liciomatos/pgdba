package util

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/liciomatos/pgdba-cli/config"
)

type ErrorModel struct {
	err          error
	context      string
	initialModel func() tea.Model
}

func NewErrorModel(err error, context string, initialModel func() tea.Model) ErrorModel {
	return ErrorModel{err: err, context: context, initialModel: initialModel}
}

func (m ErrorModel) Init() tea.Cmd { return nil }

func (m ErrorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			if m.initialModel != nil {
				return m.initialModel(), nil
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m ErrorModel) View() string {
	s := fmt.Sprintf("PostgreSQL Version: %s\n", config.Config.Version)
	s += fmt.Sprintf("Connected to: %s@%s:%d/%s\n\n",
		config.Config.User, config.Config.Host, config.Config.Port, config.Config.DBName)

	errStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 2)

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")).Render("Error")
	body := fmt.Sprintf("Context: %s\n\n%v", m.context, m.err)
	s += errStyle.Render(title + "\n" + body)
	s += "\n\n" + lipgloss.NewStyle().Faint(true).Render("q back")
	return s
}
