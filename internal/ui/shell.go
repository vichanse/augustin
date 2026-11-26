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
	answer   string
	err      error
	canceled bool
}

// disarmQuit expires a first Ctrl+C so a later single press does not exit.
type disarmQuit struct{}

const quitWindow = 2 * time.Second

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
	cancelTurn            context.CancelFunc
	interrupting          bool
	quitArmed             bool
	status                string
	sink                  events.Sink
	activity              []string
	started               time.Time
	elapsed               time.Duration
	toolCalls             int
	tokens                int
	pending               []string
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
	return tea.Batch(textarea.Blink, tea.SetWindowTitle("▲ Augustin Code"), tea.Println(m.banner()))
}

// Update prints finished output once into the terminal scrollback, so native
// scrolling, selection and search keep working; View only draws the live area.
func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	s := next.(shellModel)
	if s.commandNotice != "" {
		s.pending = append(s.pending, s.commandNotice)
		s.commandNotice = ""
	}
	if len(s.pending) == 0 {
		return s, cmd
	}
	// One Println keeps ordering; Batch would run several prints concurrently.
	text := strings.Join(s.pending, "\n\n") + "\n"
	s.pending = nil
	return s, tea.Sequence(tea.Println(text), cmd)
}

func (m shellModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		m.busy, m.interrupting = false, false
		if m.cancelTurn != nil {
			m.cancelTurn()
			m.cancelTurn = nil
		}
		if e.canceled {
			m.status = "Interrupted. Prompt restored; edit it or press Enter to resend."
			m.composer.SetValue(m.submitted)
			m.resizeComposer()
		} else if e.err != nil {
			m.status = "Request failed: " + SafeText(e.err.Error()) + "; retry your prompt."
			m.composer.SetValue(m.submitted)
			m.resizeComposer()
		} else {
			m.history = append(m.history, model.Message{Role: model.RoleUser, Content: m.submitted}, model.Message{Role: model.RoleAssistant, Content: e.answer})
			if len(m.history) > 20 {
				m.history = append([]model.Message(nil), m.history[len(m.history)-20:]...)
			}
			m.status = ""
			m.pending = append(m.pending, m.userLine(m.submitted)+"\n"+m.activitySummary(), m.paint("Augustin", "99")+"\n"+m.markdown(e.answer))
			if err := m.saveSession(); err != nil {
				m.commandNotice = "Cannot save session locally."
			}
		}
		return m, nil
	case disarmQuit:
		m.quitArmed = false
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
		m.composer.SetWidth(max(1, e.Width-2))
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
		case "esc":
			if m.busy {
				return m.interrupt()
			}
			return m, nil
		case "ctrl+c":
			switch {
			case m.busy && !m.interrupting:
				return m.interrupt()
			case m.busy || m.quitArmed:
				// A second press, or one while an interrupt is stuck, always exits.
				return m, tea.Quit
			case m.composer.Value() != "":
				m.composer.Reset()
				m.resizeComposer()
				return m, nil
			default:
				m.quitArmed = true
				return m, tea.Tick(quitWindow, func(time.Time) tea.Msg { return disarmQuit{} })
			}
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
					turnCtx, cancel := context.WithCancel(m.ctx)
					m.cancelTurn = cancel
					history := append([]model.Message(nil), m.history...)
					return m, func() tea.Msg {
						answer, err := m.turn(turnCtx, history, value, m.sink)
						// Providers may surface cancellation as a transport error, so ask the context.
						return turnDone{answer: answer, err: err, canceled: turnCtx.Err() != nil}
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

func (m shellModel) banner() string {
	logo := m.paint("  ▄█▄  \n ██ ██ \n██▄▄▄██\n██   ██", "99")
	meta := m.paint("AUGUSTIN", "99") + " " + version.Current() + "\nThe Harness-First Coding Agent\n" +
		SafeText(m.provider) + " · " + SafeText(m.model) + "\n" + SafeText(m.path)
	header := lipgloss.JoinHorizontal(lipgloss.Top, logo, "   ", meta)
	return header + "\n\nWorkspace trusted.\nRead-only access · Editing and command execution disabled.\n"
}

func (m shellModel) userLine(prompt string) string {
	return m.paint("❯ "+SafeText(prompt), "99")
}

// View draws only the live area; finished turns already live in the scrollback.
func (m shellModel) View() string {
	w := max(1, m.width)
	rule := m.paint(strings.Repeat("─", w), "240")
	var live string
	if m.busy || m.status != "" {
		live = m.userLine(m.submitted) + "\n" + m.activitySummary() + "\n" + m.status
		if m.partial != "" {
			preview := m.partialRendered
			if preview == "" {
				preview = SafeText(m.partial)
			}
			// The inline renderer cannot draw above the screen top, so show the stream tail.
			lines := strings.Split(preview, "\n")
			keep := max(3, m.height-16)
			if len(lines) > keep {
				lines = lines[len(lines)-keep:]
			}
			live += "\n\n" + strings.Join(lines, "\n")
		}
		live = lipgloss.NewStyle().Width(w).Render(live) + "\n"
	}
	footer := strings.Join([]string{
		m.paint("● READ ONLY", "78") + " · Workspace trusted",
		rule,
		m.composer.View(),
		m.commandMenu(),
		rule,
		m.keyHint(),
	}, "\n")
	footer = strings.ReplaceAll(footer, "\n\n", "\n")
	return lipgloss.NewStyle().MaxWidth(w).Render(live + footer)
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
	p = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
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

func (m shellModel) interrupt() (tea.Model, tea.Cmd) {
	if m.cancelTurn != nil {
		m.cancelTurn()
	}
	m.interrupting = true
	m.status = "Interrupting…"
	return m, nil
}

func (m shellModel) keyHint() string {
	switch {
	case m.quitArmed:
		return "Press Ctrl+C again to exit"
	case m.interrupting:
		return "Ctrl+C force exit"
	case m.busy:
		return "Esc interrupt"
	default:
		return "Enter send · Alt+Enter newline · Ctrl+C exit"
	}
}
