package ui

import (
	"context"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type shellModel struct {
	path, provider, model string
	width, height         int
	color                 bool
}

func (m shellModel) Init() tea.Cmd { return nil }

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
	case tea.KeyMsg:
		switch e.String() {
		case "esc", "ctrl+c", "ctrl+d":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m shellModel) paint(s, color string) string {
	if !m.color {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
}

func (m shellModel) View() string {
	w := max(1, m.width)
	h := max(1, m.height)
	if w < 32 || h < 12 {
		return lipgloss.NewStyle().MaxWidth(w).MaxHeight(h).Render("Resize terminal\nEsc to exit")
	}
	rule := m.paint(strings.Repeat("─", w), "240")
	logo := m.paint("  ▄█▄  \n ██ ██ \n██▄▄▄██\n██   ██", "99")
	meta := m.paint("AUGUSTIN", "99") + "\nThe Harness-First Coding Agent\n" +
		SafeText(m.provider) + " · " + SafeText(m.model) + "\n" + SafeText(m.path)
	header := lipgloss.JoinHorizontal(lipgloss.Top, logo, "   ", meta)
	header = lipgloss.NewStyle().MaxWidth(w).Render(header)
	welcome := "Workspace trusted for this session.\n\nRead-only access · Editing and command execution disabled."
	footer := strings.Join([]string{
		m.paint("● READ ONLY", "78") + " · Workspace trusted",
		rule,
		m.paint("❯", "99") + " Prompt composer coming next",
		rule,
		"Esc / Ctrl+C exit · Interactive shell preview",
	}, "\n")
	footer = lipgloss.NewStyle().MaxWidth(w).Render(footer)
	top := header + "\n\n" + lipgloss.NewStyle().MaxWidth(w).Render(welcome)
	space := max(0, h-lipgloss.Height(top)-lipgloss.Height(footer))
	return lipgloss.NewStyle().MaxHeight(h).Render(top + strings.Repeat("\n", space+1) + footer)
}

func RunShell(ctx context.Context, in io.Reader, out io.Writer, path, provider, model string, color bool) error {
	m := shellModel{path: path, provider: provider, model: model, color: color, width: 80, height: 24}
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen(), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
