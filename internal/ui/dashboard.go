package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/events"
)

type dashboardDone struct{}

type Dashboard struct {
	program *tea.Program
	done    chan error
	err     error
}

func StartDashboard(ctx context.Context, out io.Writer, color bool, provider, model, prompt string) *Dashboard {
	m := dashboardModel{color: color, provider: provider, model: model, prompt: prompt, width: 80, start: time.Now()}
	d := &Dashboard{done: make(chan error, 1)}
	d.program = tea.NewProgram(m, tea.WithOutput(out), tea.WithInput(nil), tea.WithoutSignalHandler(), tea.WithContext(ctx))
	go func() {
		_, err := d.program.Run()
		d.done <- err
	}()
	return d
}

func (d *Dashboard) Emit(e events.Event) { d.program.Send(e) }

func (d *Dashboard) Finish() {
	d.program.Send(dashboardDone{})
	d.err = <-d.done
}

func (d *Dashboard) Err() error { return d.err }

type dashboardModel struct {
	color                   bool
	provider, model, prompt string
	width                   int
	start                   time.Time
	elapsed                 time.Duration
	rows                    []string
	status                  string
	input, output, calls    int
	failed, finished         bool
}

func (m dashboardModel) Init() tea.Cmd { return nil }

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = e.Width
	case dashboardDone:
		return m, tea.Quit
	case events.Event:
		switch e.Kind {
		case events.AgentStarted:
			m.start = time.Now()
		case events.ModelRequested:
			m.status = fmt.Sprintf("Analyzing implementation (iteration %d)…", e.Iteration)
		case events.ModelCompleted:
			m.input += e.InputTokens
			m.output += e.OutputTokens
		case events.ToolRequested:
			m.calls++
			m.status = toolLabel(e.Tool) + "…"
		case events.ToolCompleted:
			m.rows = append(m.rows, m.paint("✓", "78") + "  " + toolLabel(e.Tool) + "  (" + SafeText(e.Tool) + ")")
			m.status = ""
		case events.ToolFailed:
			m.rows = append(m.rows, m.paint("!", "214") + "  " + SafeText(e.Tool) + ": " + SafeText(string(e.Reason)))
			m.status = ""
		case events.AgentCompleted, events.AgentFailed:
			m.finished = true
			m.failed = e.Kind == events.AgentFailed
			m.elapsed = time.Since(m.start)
			m.status = ""
		}
	}
	return m, nil
}

func (m dashboardModel) paint(s, color string) string {
	if !m.color {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
}

func (m dashboardModel) View() string {
	w := max(12, min(m.width-2, 100))
	rule := m.paint(strings.Repeat("─", w), "240")
	logo := m.paint("     ██     \n    ████    \n   ██  ██   \n  ██    ██  \n ██▄▄▄▄▄▄██ \n██        ██", "99")
	title := m.paint("A U G U S T I N", "99") + "\nThe Harness-First Coding Agent"
	header := lipgloss.JoinVertical(lipgloss.Left, logo, title)
	if w >= 55 {
		header = lipgloss.JoinHorizontal(lipgloss.Center, logo, "   ", title)
	}
	meta := SafeText(m.provider) + " · " + SafeText(m.model) + "   " + m.paint("● READ ONLY", "78")
	prompt := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(w - 2).Render("❯ " + SafeText(m.prompt))
	rows := m.rows
	if len(rows) > 8 {
		rows = rows[len(rows)-8:]
	}
	body := strings.Join(rows, "\n")
	if m.status != "" {
		body += "\n" + m.paint("◌", "99") + "  " + m.status
	}
	status := "Running"
	if m.finished {
		status = m.paint("✓ Task completed", "78")
		if m.failed {
			status = m.paint("! Task failed", "203")
		}
	}
	stats := fmt.Sprintf("Duration: %s   Tool calls: %d   Tokens: %d", m.elapsed.Round(time.Millisecond), m.calls, m.input+m.output)
	if w >= 48 {
		col := w / 3
		stats = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(col).Render(fmt.Sprintf("Duration\n%s", m.elapsed.Round(time.Millisecond))),
			lipgloss.NewStyle().Width(col).Render(fmt.Sprintf("Tool calls\n%d", m.calls)),
			lipgloss.NewStyle().Width(col).Render(fmt.Sprintf("Tokens\n%d", m.input+m.output)))
	}
	return strings.Join([]string{header, rule, meta, prompt, body, rule, status, stats}, "\n\n") + "\n"
}
