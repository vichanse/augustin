package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/events"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/version"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TurnFunc runs one turn with the mode snapshotted when the prompt was sent.
type TurnFunc func(context.Context, []model.Message, string, tools.Mode, events.Sink) (string, error)

type liveEvent events.Event

type liveToolCall model.ToolCall

type eventSink struct {
	emit  func(events.Event)
	delta func(string)
	call  func(liveToolCall)
}

func (s eventSink) Emit(e events.Event)          { s.emit(e) }
func (s eventSink) TextDelta(text string)        { s.delta(text) }
func (s eventSink) ToolCall(call model.ToolCall) { s.call(liveToolCall(call)) }

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
	toolLabel             string
	spinner               spinner.Model
	mdStyle               string
	prompts               []string        // submitted this run, oldest first
	promptIndex           int             // == len(prompts) when not browsing
	draft                 string          // unsent text saved while browsing
	pastes                map[rune]string // pasted blocks by marker rune; see paste.go
	nextPaste             rune
	approvals             *approval.Broker
	mode                  tools.Mode        // local user choice; never changed by the model
	approval              *approval.Request // pending decision; blocks normal input
	approvalChoice        int
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
	spin := spinner.New(spinner.WithSpinner(spinner.Dot))
	if color {
		spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))
	}
	return shellModel{path: path, provider: provider, model: model, color: color, width: 80, height: 24, composer: composer, spinner: spin, mdStyle: "dark", mode: tools.ReadOnly}
}

func (m shellModel) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tea.SetWindowTitle("▲ Augustin Code"), tea.Println(m.banner()))
}

// Update prints finished output once into the terminal scrollback, so native
// scrolling, selection and search keep working; View only draws the live area.
func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	s := next.(shellModel)
	s.collectPastes()
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
	case spinner.TickMsg:
		if !m.busy {
			return m, nil // stop the tick chain between turns
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(e)
		return m, cmd
	case liveToolCall:
		// Tools run one at a time, so the last line is the one ToolRequested opened.
		if m.busy && len(m.activity) > 0 {
			m.toolLabel = describeTool(model.ToolCall(e))
			m.activity[len(m.activity)-1] = "◌ " + m.toolLabel
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
		case events.ModelCompleted:
			m.tokens += e.InputTokens + e.OutputTokens
		case events.ToolRequested:
			m.partial = ""
			m.partialRendered = ""
			m.toolCalls++
			m.toolLabel = SafeText(e.Tool)
			m.activity = append(m.activity, "◌ "+m.toolLabel)
		case events.ToolCompleted:
			m.setToolLine("✓ " + m.toolLabel)
		case events.ToolFailed:
			mark := "✗ "
			if e.Recoverable {
				mark = "! "
			}
			m.setToolLine(mark + m.toolLabel + " (" + SafeText(string(e.Reason)) + ")")
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
		m.approval = nil
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
			m.history = append(m.history, model.Message{Role: model.RoleUser, Content: m.expand(m.submitted)}, model.Message{Role: model.RoleAssistant, Content: e.answer}) // expanded: the model and saved sessions never see labels
			if len(m.history) > 20 {
				m.history = append([]model.Message(nil), m.history[len(m.history)-20:]...)
			}
			m.status = ""
			m.pending = append(m.pending, m.userLine(m.compact(m.submitted))+"\n"+m.activitySummary(), m.paint("Augustin", "99")+"\n"+m.markdown(e.answer))
			if err := m.saveSession(); err != nil {
				m.commandNotice = "Cannot save session locally."
			}
		}
		return m, nil
	case approvalUpdate:
		if e.Open {
			req := e.Request
			m.approval, m.approvalChoice = &req, choiceDeny
		} else if m.approval != nil && m.approval.ID == e.Request.ID {
			m.approval = nil // withdrawn: canceled, deadline or closed
		}
		return m, nil
	case disarmQuit:
		m.quitArmed = false
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
		m.composer.SetWidth(max(1, e.Width-2))
		if m.approval != nil && !m.previewComplete() {
			m.approvalChoice = choiceDeny // a smaller terminal must never leave Approve selected
		}
	case tea.KeyMsg:
		// A pending approval owns the keyboard; Ctrl+C keeps its interrupt/exit meaning.
		if m.approval != nil && e.String() != "ctrl+c" {
			return m.approvalKey(e), nil
		}
		// Pastes are text, never keys: Bubble Tea delivers bracketed pastes as
		// one message whose String() is "[...]", so they match no shortcut.
		if e.Type == tea.KeyRunes && (e.Paste || hasPasteRune(string(e.Runes))) && m.insertPaste(e.Runes) {
			return m, nil
		}
		switch e.String() {
		case "up":
			// History wins over the slash menu once browsing, so a recalled command cannot trap ↑.
			if m.composer.Line() == 0 && (m.browsing() || len(matchingCommands(m.composer.Value())) == 0) {
				if m.promptIndex == 0 {
					if m.browsing() {
						return m, nil // already at the oldest prompt
					}
					break
				}
				if !m.browsing() {
					m.draft = m.composer.Value()
				}
				m.promptIndex--
				m.setComposer(m.prompts[m.promptIndex])
				return m, nil
			}
		case "down":
			if m.browsing() && m.composer.Line() == strings.Count(m.composer.Value(), "\n") {
				m.promptIndex++
				if m.browsing() {
					m.setComposer(m.prompts[m.promptIndex])
				} else {
					m.setComposer(m.draft)
				}
				return m, nil
			}
		}
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
					m.remember(matches[m.commandIndex].name)
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
				m.remember(value)
				// Pasted content is user input: a prompt holding a block is never a local command.
				if strings.HasPrefix(value, "/") && !strings.Contains(value, "\n") && !hasPasteRune(value) {
					return m.runCommand(value)
				}
				m.commandNotice = ""
				m.submitted = value
				prompt := m.expand(value)
				m.composer.Reset()
				m.resizeComposer()
				if m.turn != nil {
					m.busy = true
					m.started = time.Now()
					m.activity = nil
					m.toolCalls, m.tokens = 0, 0
					m.status = "Working…"
					m.toolLabel = ""
					m.partial = ""
					m.partialRendered = ""
					m.lastRender = time.Time{}
					turnCtx, cancel := context.WithCancel(m.ctx)
					m.cancelTurn = cancel
					history := append([]model.Message(nil), m.history...)
					mode := m.mode // snapshot: a later /mode cannot change a running turn
					return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
						answer, err := m.turn(turnCtx, history, prompt, mode, m.sink)
						// Providers may surface cancellation as a transport error, so ask the context.
						return turnDone{answer: answer, err: err, canceled: turnCtx.Err() != nil}
					})
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
	mode := m.modeBadge() + " · Workspace trusted"
	if m.approval != nil {
		// The panel must stay whole: crop the live area from the top to make room.
		footer := strings.Join([]string{mode, m.approvalPanel(), m.keyHint()}, "\n")
		live := strings.Split(strings.TrimSuffix(m.liveArea(), "\n"), "\n")
		keep := max(0, m.height-lipgloss.Height(footer))
		top := ""
		if keep > 0 && live[0] != "" {
			top = strings.Join(live[max(0, len(live)-keep):], "\n") + "\n"
		}
		return lipgloss.NewStyle().MaxWidth(w).Render(top + footer)
	}
	input := []string{rule, m.composerView(), m.commandMenu(), rule}
	footer := strings.Join(append(append([]string{mode}, input...), m.keyHint()), "\n")
	footer = strings.ReplaceAll(footer, "\n\n", "\n")
	return lipgloss.NewStyle().MaxWidth(w).Render(m.liveArea() + footer)
}

// liveArea is the running turn: prompt, tool activity, spinner status and stream tail.
func (m shellModel) liveArea() string {
	w := max(1, m.width)
	var live string
	if m.busy || m.status != "" {
		status := m.status
		if m.busy {
			status = m.spinner.View() + " " + status
		}
		live = m.userLine(m.compact(m.submitted)) + "\n" + m.activitySummary() + "\n" + status
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
	return live
}

// RunShell runs the interactive session. approvals may be nil; when set, the
// shell answers its requests and closes it on exit, failing pending ones closed.
func RunShell(ctx context.Context, in io.Reader, out io.Writer, path, provider, model string, color bool, turn TurnFunc, approvals *approval.Broker) error {
	m := newShellModel(path, provider, model, color)
	m.approvals = approvals
	if color {
		m.mdStyle = markdownStyle(out) // query once, before Bubble Tea owns the terminal input
	}
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
		call:  func(c liveToolCall) { p.Send(c) },
	}
	p = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
	if approvals != nil {
		approvals.Attach(func(u approval.Update) { p.Send(approvalUpdate(u)) })
		defer approvals.Close()
	}
	_, err := p.Run()
	return err
}

func (m shellModel) activitySummary() string {
	elapsed := m.elapsed
	if m.busy {
		elapsed = time.Since(m.started)
	}
	precision := time.Millisecond
	if m.busy {
		precision = time.Second // a ticking live timer reads better without milliseconds
	}
	stats := m.paint(fmt.Sprintf("%d tools · %d tokens · %s", m.toolCalls, m.tokens, elapsed.Round(precision)), "245")
	if len(m.activity) == 0 {
		return stats
	}
	return strings.Join(m.activity, "\n") + "\n" + stats
}

func (m shellModel) markdown(answer string) string {
	return strings.TrimSpace(answerWidth(answer, m.color, max(20, m.width-4), m.mdStyle))
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
	case m.approval != nil:
		return "↑↓ select · Enter confirm · Esc deny · Ctrl+C interrupt"
	case m.quitArmed:
		return "Press Ctrl+C again to exit"
	case m.interrupting:
		return "Ctrl+C force exit"
	case m.busy:
		return "Esc interrupt"
	default:
		return "Enter send · Alt+Enter newline · ↑ history · Ctrl+C exit"
	}
}

func (m *shellModel) setToolLine(line string) {
	if len(m.activity) > 0 {
		m.activity[len(m.activity)-1] = line
	}
}

// describeTool renders a call like Read(path) from untrusted model arguments.
func describeTool(call model.ToolCall) string {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(call.Arguments, &args) // best effort: display only
	target := SafeText(strings.ReplaceAll(args.Path, "\n", " "))
	if r := []rune(target); len(r) > 60 {
		target = "…" + string(r[len(r)-59:])
	}
	switch call.Name {
	case "read_file":
		return "Read(" + target + ")"
	case "list_files":
		if target == "" {
			target = "."
		}
		return "List(" + target + ")"
	case "edit_file":
		return "Edit(" + target + ")"
	case "run_command":
		var run struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		_ = json.Unmarshal(call.Arguments, &run) // best effort: display only
		label := SafeText(strings.ReplaceAll(strings.Join(append([]string{run.Command}, run.Args...), " "), "\n", " "))
		if r := []rune(label); len(r) > 60 {
			label = string(r[:59]) + "…"
		}
		return "Run(" + label + ")"
	case "workspace_overview":
		return "Overview"
	default:
		return SafeText(call.Name)
	}
}

func (m shellModel) browsing() bool { return m.promptIndex < len(m.prompts) }

// remember records a submitted prompt for ↑ recall, skipping repeats.
func (m *shellModel) remember(prompt string) {
	if n := len(m.prompts); n == 0 || m.prompts[n-1] != prompt {
		m.prompts = append(m.prompts, prompt)
		if len(m.prompts) > 100 {
			m.prompts = m.prompts[len(m.prompts)-100:]
		}
	}
	m.promptIndex, m.draft = len(m.prompts), ""
}

func (m *shellModel) setComposer(value string) {
	m.composer.SetValue(value)
	m.resizeComposer()
	m.commandIndex = 0
}

func (m shellModel) modeBadge() string {
	switch m.mode {
	case tools.EditWithApproval:
		return m.paint("● EDIT", "214") + " · every change needs approval"
	case tools.ExecuteWithApproval:
		return m.paint("● EXECUTE", "203") + " · every command and change needs approval"
	}
	return m.paint("● READ ONLY", "78")
}
