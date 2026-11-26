package ui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type trustModel struct {
	path     string
	color    bool
	selected bool
	accepted bool
}

func (m trustModel) Init() tea.Cmd { return nil }

func (m trustModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "down", "tab":
			m.selected = !m.selected
		case "enter":
			m.accepted = m.selected
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
	no, yes := "❯ No, exit", "  Yes, trust this workspace for this session"
	if m.selected {
		no, yes = "  No, exit", "❯ Yes, trust this workspace for this session"
	}
	return title + "\n\nAccessing workspace:\n" + SafeText(m.path) +
		"\n\nOnly continue if you trust this directory and its contents." +
		"\nAugustin can read permitted files and send relevant content to the configured model provider." +
		"\nFile editing and command execution are disabled." +
		"\nTrust applies only to this session; remote-data consent is still required." +
		"\n\n" + no + "\n" + yes + "\n\n↑ ↓ Navigate · Enter confirm · Esc cancel\n"
}

func ConfirmWorkspace(ctx context.Context, in io.Reader, out io.Writer, path string, color bool) (bool, error) {
	p := tea.NewProgram(trustModel{path: path, color: color}, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
	result, err := p.Run()
	if err != nil {
		return false, err
	}
	m, ok := result.(trustModel)
	return ok && m.accepted, nil
}
