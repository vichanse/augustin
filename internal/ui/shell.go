package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vichanse/augustin/internal/events"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/version"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type TurnFunc func(context.Context, []model.Message, string, events.Sink) (string, error)

type liveEvent events.Event

type eventSink struct {
	emit  func(events.Event)
	delta func(string)
}

func (s eventSink) Emit(e events.Event)   { s.emit(e) }
func (s eventSink) TextDelta(text string) { s.delta(text) }

type liveDelta string

type turnDone struct {
	answer string
	err    error
}

type shellModel struct {
	path, provider, model string
	width, height         int
	color                 bool
	composer              textarea.Model
	submitted             string
	ctx                   context.Context
	turn                  TurnFunc
	history               []model.Message
	busy                  bool
	status                string
	sink                  events.Sink
	activity              []string
	started               time.Time
	elapsed               time.Duration
	toolCalls             int
	tokens                int
	scroll                int
	turnDetails           []string
	rendered              []string
	partial               string
	partialRendered       string
	lastRender            time.Time
	commandIndex          int
	commandNotice         string
	sessionDir            string
	session               savedSession
}

func newShellModel(path, provider, model string, color bool) shellModel {
	composer := textarea.New()
	composer.Placeholder = ""
	composer.Prompt = "❯ "
	composer.FocusedStyle.CursorLine = lipgloss.NewStyle()
	composer.BlurredStyle.CursorLine = lipgloss.NewStyle()
	composer.ShowLineNumbers = false
	composer.CharLimit = 8192
	composer.SetWidth(80)
	composer.SetHeight(1)
	if !color {
		composer.FocusedStyle = textarea.Style{}
		composer.BlurredStyle = textarea.Style{}
	}
	return shellModel{path: path, provider: provider, model: model, color: color, width: 80, height: 24, composer: composer}
}

func (m shellModel) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tea.SetWindowTitle("▲ Augustin Code"))
}

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case liveDelta:
		if m.busy {
			m.partial += string(e)
			if time.Since(m.lastRender) >= 150*time.Millisecond {
				m.partialRendered = m.markdown(m.partial)
				m.lastRender = time.Now()
			}
		}
		return m, nil
	case liveEvent:
		if !m.busy {
			return m, nil
		}
		switch e.Kind {
		case events.ModelRequested:
			m.partial = ""
			m.partialRendered = ""
			m.activity = append(m.activity, fmt.Sprintf("Model request · iteration %d", e.Iteration))
		case events.ModelCompleted:
			m.tokens += e.InputTokens + e.OutputTokens
		case events.ToolRequested:
			m.partial = ""
			m.partialRendered = ""
			m.toolCalls++
			m.activity = append(m.activity, "Running "+SafeText(e.Tool))
		case events.ToolCompleted:
			m.activity = append(m.activity, "✓ "+SafeText(e.Tool))
		case events.ToolFailed:
			label := "Failed"
			if e.Recoverable {
				label = "Recoverable"
			}
			m.activity = append(m.activity, label+": "+SafeText(e.Tool)+" ("+SafeText(string(e.Reason))+")")
		}
		if len(m.activity) > 5 {
			m.activity = append([]string(nil), m.activity[len(m.activity)-5:]...)
		}
		return m, nil
	case turnDone:
		if e.err == nil {
			m.partial = ""
			m.partialRendered = ""
		} else {
			m.partialRendered = m.markdown(m.partial)
		}
		m.elapsed = time.Since(m.started)
		m.busy = false
		if e.err != nil {
			m.status = "Request failed: " + SafeText(e.err.Error()) + "; retry your prompt."
			m.composer.SetValue(m.submitted)
			m.resizeComposer()
		} else {
			m.history = append(m.history, model.Message{Role: model.RoleUser, Content: m.submitted}, model.Message{Role: model.RoleAssistant, Content: e.answer})
			if len(m.history) > 20 {
				m.history = append([]model.Message(nil), m.history[len(m.history)-20:]...)
			}
			m.status = ""
			m.turnDetails = append(m.turnDetails, m.activitySummary())
			m.rendered = append(m.rendered, m.markdown(e.answer))
			if len(m.turnDetails) > 10 {
				m.turnDetails = m.turnDetails[len(m.turnDetails)-10:]
				m.rendered = m.rendered[len(m.rendered)-10:]
			}
			m.scroll = 0
			if err := m.saveSession(); err != nil {
				m.commandNotice = "Cannot save session locally."
			}
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
		m.composer.SetWidth(max(1, e.Width-2))
		m.rendered = nil
		for _, msg := range m.history {
			if msg.Role == model.RoleAssistant {
				m.rendered = append(m.rendered, m.markdown(msg.Content))
			}
		}
	case tea.KeyMsg:
		if !m.busy {
			matches := matchingCommands(m.composer.Value())
			if len(matches) > 0 {
				m.commandIndex = min(m.commandIndex, len(matches)-1)
				switch e.String() {
				case "up":
					m.commandIndex = (m.commandIndex + len(matches) - 1) % len(matches)
					return m, nil
				case "down":
					m.commandIndex = (m.commandIndex + 1) % len(matches)
					return m, nil
				case "tab":
					m.composer.SetValue(matches[m.commandIndex].name)
					m.resizeComposer()
					m.commandIndex = 0
					return m, nil
				case "enter":
					return m.runCommand(matches[m.commandIndex].name)
				}
			}
		}
		switch e.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "pgup":
			m.scroll += max(1, m.height/2)
			return m, nil
		case "pgdown":
			m.scroll = max(0, m.scroll-max(1, m.height/2))
			return m, nil
		case "alt+enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "enter", "ctrl+s":
			if m.busy {
				return m, nil
			}
			if value := strings.TrimSpace(m.composer.Value()); value != "" {
				if strings.HasPrefix(value, "/") && !strings.Contains(value, "\n") {
					return m.runCommand(value)
				}
				m.commandNotice = ""
				m.scroll = 0
				m.submitted = value
				m.composer.Reset()
				m.resizeComposer()
				if m.turn != nil {
					m.busy = true
					m.started = time.Now()
					m.activity = nil
					m.toolCalls, m.tokens = 0, 0
					m.status = "Working…"
					m.partial = ""
					m.partialRendered = ""
					m.lastRender = time.Time{}
					return m, func() tea.Msg {
						answer, err := m.turn(m.ctx, append([]model.Message(nil), m.history...), value, m.sink)
						return turnDone{answer: answer, err: err}
					}
				}
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	before := m.composer.Value()
	m.composer, cmd = m.composer.Update(msg)
	if before != m.composer.Value() {
		m.commandIndex = 0
	}
	m.resizeComposer()
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
	meta := m.paint("AUGUSTIN", "99") + " " + version.Current() + "\nThe Harness-First Coding Agent\n" +
		SafeText(m.provider) + " · " + SafeText(m.model) + "\n" + SafeText(m.path)
	header := lipgloss.JoinHorizontal(lipgloss.Top, logo, "   ", meta)
	header = lipgloss.NewStyle().MaxWidth(w).Render(header)
	welcome := "Workspace trusted.\n\nRead-only access · Editing and command execution disabled."
	if len(m.history) > 0 {
		welcome = ""
		for i, msg := range m.history {
			if msg.Role == model.RoleUser {
				welcome += m.paint("❯ "+SafeText(msg.Content), "99") + "\n\n"
				continue
			}
			if i/2 < len(m.turnDetails) {
				welcome += m.turnDetails[i/2] + "\n\n"
			}
			answer := SafeText(msg.Content)
			if i/2 < len(m.rendered) {
				answer = m.rendered[i/2]
			}
			welcome += m.paint("Augustin", "99") + "\n" + answer + "\n\n"
		}
	}
	if m.busy || m.status != "" {
		welcome += "\n" + m.paint("❯ "+SafeText(m.submitted), "99") + "\n" + m.activitySummary()
		welcome += "\n" + m.status
		if m.partial != "" {
			preview := m.partialRendered
			if preview == "" {
				preview = SafeText(m.partial)
			}
			welcome += "\n\n" + preview
		}
	}
	if m.commandNotice != "" {
		welcome += "\n\n" + m.commandNotice
	}
	menu := m.commandMenu()
	footer := strings.Join([]string{
		m.paint("● READ ONLY", "78") + " · Workspace trusted",
		rule,
		m.composer.View(),
		menu,
		rule,
		"Enter send · Alt+Enter newline · PgUp/PgDn scroll · Esc exit",
	}, "\n")
	footer = strings.ReplaceAll(footer, "\n\n", "\n")
	footer = lipgloss.NewStyle().MaxWidth(w).Render(footer)
	body := lipgloss.NewStyle().Width(w).Render(welcome)
	lines := strings.Split(body, "\n")
	available := max(1, h-lipgloss.Height(footer)-lipgloss.Height(header)-2)
	if len(lines) > available {
		end := len(lines) - min(m.scroll, len(lines)-available)
		lines = lines[end-available : end]
	}
	top := header + "\n\n" + strings.Join(lines, "\n")
	space := max(0, h-lipgloss.Height(top)-lipgloss.Height(footer))
	return lipgloss.NewStyle().MaxHeight(h).Render(top + strings.Repeat("\n", space+1) + footer)
}

func RunShell(ctx context.Context, in io.Reader, out io.Writer, path, provider, model string, color bool, turn TurnFunc) error {
	m := newShellModel(path, provider, model, color)
	if os.Getenv("AUGUSTIN_SAVE_SESSIONS") == "true" {
		dir, err := sessionDirectory(path)
		if err != nil {
			return err
		}
		m.sessionDir = dir
		m.commandNotice = "Local session storage enabled. Conversations may contain sensitive code."
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.ctx, m.turn = sessionCtx, turn
	m.composer.Focus()
	var p *tea.Program
	m.sink = eventSink{
		emit:  func(e events.Event) { p.Send(liveEvent(e)) },
		delta: func(text string) { p.Send(liveDelta(text)) },
	}
	p = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen(), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}

func (m shellModel) activitySummary() string {
	elapsed := m.elapsed
	if m.busy {
		elapsed = time.Since(m.started)
	}
	return strings.Join(m.activity, "\n") + fmt.Sprintf("\n%d tools · %d tokens · %s", m.toolCalls, m.tokens, elapsed.Round(time.Millisecond))
}

func (m shellModel) markdown(answer string) string {
	safe := SafeText(answer)
	if !m.color {
		return safe
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, m.width-4)))
	if err != nil {
		return safe
	}
	rendered, err := renderer.Render(safe)
	if err != nil {
		return safe
	}
	return strings.TrimSpace(rendered)
}

func (m *shellModel) resizeComposer() {
	width := max(1, m.composer.Width()-2)
	lines := 0
	for _, line := range strings.Split(m.composer.Value(), "\n") {
		lines += max(1, (lipgloss.Width(line)+width-1)/width)
	}
	m.composer.SetHeight(min(lines, max(1, min(6, m.height/4))))
}
