package ui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type trustModel struct {
	path           string
	color          bool
	selected       int
	accepted       bool
	permanent      bool
	allowPermanent bool
}

func (m trustModel) Init() tea.Cmd { return nil }

func (m trustModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "down", "tab":
			count := 2
			if m.allowPermanent {
				count = 3
			}
			if key.String() == "up" {
				m.selected = (m.selected + count - 1) % count
			} else {
				m.selected = (m.selected + 1) % count
			}
		case "enter":
			m.accepted = m.selected != 0
			m.permanent = m.allowPermanent && m.selected == 2
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.accepted = false
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m trustModel) View() string {
	title := "▲ AUGUSTIN — Workspace trust"
	if m.color {
		title = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true).Render(title)
	}
	options := []string{"No, exit", "Yes, trust this workspace for this session"}
	if m.allowPermanent {
		options = append(options, "Yes, always trust this workspace")
	}
	menu := ""
	for i, option := range options {
		prefix := "  "
		if i == m.selected {
			prefix = "❯ "
		}
		menu += prefix + option + "\n"
	}
	return title + "\n\nAccessing workspace:\n" + SafeText(m.path) +
		"\n\nOnly continue if you trust this directory and its contents." +
		"\nAugustin can read permitted files and send relevant content to the configured model provider." +
		"\nFile editing and command execution are disabled." +
		"\nRemote-data consent is still required." +
		"\n\n" + menu + "\n\n↑ ↓ Navigate · Enter confirm · Esc cancel\n"
}

func ConfirmWorkspace(ctx context.Context, in io.Reader, out io.Writer, path string, color, allowPermanent bool) (bool, bool, error) {
	p := tea.NewProgram(trustModel{path: path, color: color, allowPermanent: allowPermanent}, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
	result, err := p.Run()
	if err != nil {
		return false, false, err
	}
	m, ok := result.(trustModel)
	return ok && m.accepted, ok && m.permanent, nil
}
