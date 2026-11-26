package ui

import (
	"context"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type shellModel struct {
	path, provider, model string
	width, height         int
	color                 bool
	composer              textarea.Model
	submitted             string
}

func newShellModel(path, provider, model string, color bool) shellModel {
	composer := textarea.New()
	composer.Placeholder = "Ask Augustin about this workspace"
	composer.Prompt = "❯ "
	composer.ShowLineNumbers = false
	composer.CharLimit = 8192
	composer.SetWidth(80)
	composer.SetHeight(3)
	if !color {
		composer.FocusedStyle = textarea.Style{}
		composer.BlurredStyle = textarea.Style{}
	}
	return shellModel{path: path, provider: provider, model: model, color: color, width: 80, height: 24, composer: composer}
}

func (m shellModel) Init() tea.Cmd { return textarea.Blink }

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
		m.composer.SetWidth(max(1, e.Width-2))
	case tea.KeyMsg:
		switch e.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "ctrl+s":
			if value := strings.TrimSpace(m.composer.Value()); value != "" {
				m.submitted = value
				m.composer.Reset()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
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
	if w < 32 || h < 16 {
		return lipgloss.NewStyle().MaxWidth(w).MaxHeight(h).Render("Resize terminal\nEsc to exit")
	}
	rule := m.paint(strings.Repeat("─", w), "240")
	logo := m.paint("  ▄█▄  \n ██ ██ \n██▄▄▄██\n██   ██", "99")
	meta := m.paint("AUGUSTIN", "99") + "\nThe Harness-First Coding Agent\n" +
		SafeText(m.provider) + " · " + SafeText(m.model) + "\n" + SafeText(m.path)
	header := lipgloss.JoinHorizontal(lipgloss.Top, logo, "   ", meta)
	header = lipgloss.NewStyle().MaxWidth(w).Render(header)
	welcome := "Workspace trusted for this session.\n\nRead-only access · Editing and command execution disabled."
	if m.submitted != "" {
		welcome = "Prompt captured locally (not sent to a model):\n" + SafeText(m.submitted)
	}
	footer := strings.Join([]string{
		m.paint("● READ ONLY", "78") + " · Workspace trusted",
		rule,
		m.composer.View(),
		rule,
		"Ctrl+S submit preview · Enter newline · Esc exit",
	}, "\n")
	footer = lipgloss.NewStyle().MaxWidth(w).Render(footer)
	top := header + "\n\n" + lipgloss.NewStyle().MaxWidth(w).Render(welcome)
	top = lipgloss.NewStyle().MaxHeight(max(1, h-lipgloss.Height(footer))).Render(top)
	space := max(0, h-lipgloss.Height(top)-lipgloss.Height(footer))
	return lipgloss.NewStyle().MaxHeight(h).Render(top + strings.Repeat("\n", space+1) + footer)
}

func RunShell(ctx context.Context, in io.Reader, out io.Writer, path, provider, model string, color bool) error {
	m := newShellModel(path, provider, model, color)
	m.composer.Focus()
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen(), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
