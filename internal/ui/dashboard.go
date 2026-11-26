package ui

import (
	"context"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
)

type dashboardDone struct{}

// Dashboard renders a one-shot `augustin run` with the shell's banner and
// live area, without a composer. The answer itself goes to stdout.
type Dashboard struct {
	program *tea.Program
	done    chan error
	err     error
}

func StartDashboard(ctx context.Context, out io.Writer, color bool, path, provider, model, prompt string) *Dashboard {
	shell := newShellModel(path, provider, model, color)
	shell.busy, shell.submitted, shell.status, shell.started = true, prompt, "Working…", time.Now()
	d := &Dashboard{done: make(chan error, 1)}
	d.program = tea.NewProgram(dashboardModel{shell: shell}, tea.WithOutput(out), tea.WithInput(nil), tea.WithoutSignalHandler(), tea.WithContext(ctx))
	go func() {
		_, err := d.program.Run()
		d.done <- err
	}()
	return d
}

func (d *Dashboard) Emit(e events.Event) { d.program.Send(liveEvent(e)) }

// ToolCall is the display-only hook; arguments are shown, never persisted.
func (d *Dashboard) ToolCall(call model.ToolCall) { d.program.Send(liveToolCall(call)) }

func (d *Dashboard) Finish() {
	d.program.Send(dashboardDone{})
	d.err = <-d.done
}

func (d *Dashboard) Err() error { return d.err }

type dashboardModel struct {
	shell shellModel
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(tea.Println(m.shell.banner()), m.shell.spinner.Tick)
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case dashboardDone:
		return m, tea.Quit
	case tea.KeyMsg:
		return m, nil // no input in run mode
	case liveEvent:
		if e.Kind == events.AgentCompleted || e.Kind == events.AgentFailed {
			m.shell.elapsed = time.Since(m.shell.started)
			m.shell.busy = false
			m.shell.status = m.shell.paint("✓ Task completed", "78")
			if e.Kind == events.AgentFailed {
				m.shell.status = m.shell.paint("! Task failed ("+SafeText(string(e.Reason))+")", "203")
			}
			return m, nil
		}
	}
	next, cmd := m.shell.update(msg)
	m.shell = next.(shellModel)
	return m, cmd
}

func (m dashboardModel) View() string {
	return lipgloss.NewStyle().MaxWidth(max(1, m.shell.width)).Render(m.shell.liveArea())
}
