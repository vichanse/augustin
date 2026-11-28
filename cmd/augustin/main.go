package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/config"
	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/prompt"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/trust"
	"github.com/vichanse/augustin/internal/ui"
	"github.com/vichanse/augustin/internal/verification"
	"github.com/vichanse/augustin/internal/version"
)

func run(args []string, out, errOut io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintln(out, "Augustin Code "+version.Current())
		return 0
	}
	if len(args) == 1 && args[0] == "update" {
		cmd := exec.CommandContext(ctx, "go", "install", "github.com/vichanse/augustin/cmd/augustin@latest")
		cmd.Stdout, cmd.Stderr = out, errOut
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(errOut, "Augustin update failed; check Go installation and network access")
			return 1
		}
		fmt.Fprintln(out, "Augustin installed in Go bin directory. Restart Augustin to use the installed version.")
		return 0
	}
	if len(args) > 0 && args[0] == "trust" {
		return runTrust(args[1:], out, errOut)
	}
	if len(args) == 0 && ui.Interactive(os.Stdin) && ui.Interactive(out) {
		path, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(errOut, "cannot resolve current workspace")
			return 1
		}
		path, err = trust.Canonical(path)
		if err != nil {
			return 1
		}
		store, err := trust.Default()
		if err != nil {
			fmt.Fprintln(errOut, "cannot locate trust store")
			return 1
		}
		accepted, err := store.Contains(path)
		if err != nil {
			fmt.Fprintln(errOut, "cannot read trust store; use augustin trust clear to reset")
			return 1
		}
		if !accepted {
			var permanent bool
			accepted, permanent, err = ui.ConfirmWorkspace(ctx, os.Stdin, out, path, ui.ColorEnabled(out, os.LookupEnv), trust.PersistentAllowed(path))
			if err != nil {
				return 1
			}
			if accepted && permanent {
				if err := store.Add(path); err != nil {
					fmt.Fprintln(errOut, "cannot save workspace trust")
					return 1
				}
			}
		}
		if accepted {
			c, err := config.Load(os.LookupEnv)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 2
			}
			w, err := tools.OpenWorkspace(path)
			if err != nil {
				fmt.Fprintln(errOut, "cannot open workspace")
				return 2
			}
			defer w.Close()
			p, err := config.NewProvider(os.LookupEnv)
			if err != nil {
				fmt.Fprintln(errOut, "cannot initialize provider")
				return 2
			}
			approvals := approval.New()
			loop := agent.Loop{Provider: p, MaxIterations: c.MaxIterations, Budget: c.Budget, ReserveFinalResponse: true}
			turn := func(ctx context.Context, history []model.Message, input string, mode tools.Mode, sink events.Sink) (ui.TurnResult, error) {
				messages := append([]model.Message{prompt.System(mode)}, history...)
				messages = append(messages, model.Message{Role: model.RoleUser, Content: input})
				diagnostic := &turnDiagnostic{sink: sink}
				current := loop
				current.Events = diagnostic
				executor := executorFor(w, mode, approvals)
				tracker := verification.New(mode == tools.ExecuteWithApproval) // harness-owned, per turn
				executor.Observe(func(e tools.Evidence) {
					tracker.Record(e)
					// Display only: the shell shows a command's real exit status.
					if display, ok := sink.(interface{ ToolEvidence(tools.Evidence) }); ok {
						display.ToolEvidence(e)
					}
				})
				current.Executor, current.Verification = executor, tracker
				result, err := current.Run(ctx, prompt.Request(messages, mode))
				out := ui.TurnResult{Verification: result.Verification}
				if err != nil {
					return out, fmt.Errorf("%s: %w", diagnostic.summary, err)
				}
				out.Answer = result.Messages[len(result.Messages)-1].Content
				return out, nil
			}
			if err := ui.RunShell(ctx, os.Stdin, out, path, c.Provider, c.Model, ui.ColorEnabled(out, os.LookupEnv), turn, approvals); err != nil {
				fmt.Fprintln(errOut, "interactive session failed")
				return 1
			}
		}
		return 0
	}
	return runWith(ctx, args, out, errOut, os.LookupEnv, config.NewProvider)
}

func runWith(ctx context.Context, args []string, out, errOut io.Writer, lookup config.Lookup, factory func(config.Lookup) (model.Provider, error)) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprintln(out, "Augustin — harness-first coding agent\nUsage: augustin run [--json] --workspace PATH PROMPT")
		return 0
	}
	if args[0] != "run" {
		fmt.Fprintln(errOut, "unsupported arguments")
		return 2
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonMode := flags.Bool("json", false, "emit JSONL execution events on stderr")
	workspace := flags.String("workspace", "", "trusted disposable workspace")
	if err := flags.Parse(args[1:]); err != nil || *workspace == "" || flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" {
		fmt.Fprintln(errOut, "Usage: augustin run [--json] --workspace PATH PROMPT")
		return 2
	}
	c, err := config.Load(lookup)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	w, err := tools.OpenWorkspace(*workspace)
	if err != nil {
		fmt.Fprintln(errOut, "cannot open workspace")
		return 2
	}
	defer w.Close()
	p, err := factory(lookup)
	if err != nil {
		fmt.Fprintln(errOut, "cannot initialize provider")
		return 2
	}
	var dashboard *ui.Dashboard
	var sink interface {
		events.Sink
		Err() error
	}
	if *jsonMode || !ui.Interactive(errOut) {
		sink = events.NewJSONL(errOut)
	} else {
		dashboard = ui.StartDashboard(ctx, errOut, ui.ColorEnabled(errOut, lookup), *workspace, c.Provider, c.Model, flags.Arg(0))
		sink = dashboard
	}
	loop := agent.Loop{
		Provider:             p,
		Executor:             executorFor(w, tools.ReadOnly, nil), // headless: never edits, never asks
		MaxIterations:        c.MaxIterations,
		Budget:               c.Budget,
		ReserveFinalResponse: true,
		Events:               sink,
	}
	result, err := loop.Run(ctx, prompt.Request([]model.Message{prompt.System(tools.ReadOnly), {Role: model.RoleUser, Content: flags.Arg(0)}}, tools.ReadOnly))
	if dashboard != nil {
		dashboard.Finish()
	}
	if err != nil || sink.Err() != nil || len(result.Messages) == 0 {
		fmt.Fprintln(errOut, "Augustin run failed; inspect execution events")
		return 1
	}
	if _, err := fmt.Fprintln(out, ui.AnswerForOutput(result.Messages[len(result.Messages)-1].Content, !*jsonMode && ui.ColorEnabled(out, lookup), out)); err != nil {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// executorFor builds the registry for one turn. Only an interactive edit-mode
// turn with an approval broker gets edit_file; everything else is read-only.
func executorFor(w *tools.Workspace, mode tools.Mode, approvals *approval.Broker) *tools.Registry {
	if approvals == nil || (mode != tools.EditWithApproval && mode != tools.ExecuteWithApproval) {
		return tools.NewWithPolicy(w.Handlers(), tools.ReadTools, tools.ReadPolicy())
	}
	if mode == tools.EditWithApproval {
		policy := tools.EditPolicy()
		policy.Approve = approvals.Approve
		return tools.NewWithPreparers(w.Handlers(), w.Preparers(), tools.EditTools, policy)
	}
	policy := tools.ExecutePolicy()
	policy.Approve = approvals.Approve
	preparers := w.Preparers()
	preparers["run_command"] = w.CommandPreparer(os.Environ()) // only PATH and an allowlist are passed on
	return tools.NewWithPreparers(w.Handlers(), preparers, tools.ExecuteTools, policy)
}

type turnDiagnostic struct {
	summary string
	sink    events.Sink
}

func (d *turnDiagnostic) Emit(e events.Event) {
	if d.sink != nil {
		d.sink.Emit(e)
	}
	switch e.Kind {
	case events.AgentStarted:
		d.summary = "execution_error"
	case events.ModelFailed:
		d.summary = "model_failed"
	case events.ToolFailed:
		d.summary = string(e.Kind) + ": " + e.Tool + " (" + string(e.Reason) + ")"
	case events.AgentFailed:
		if e.Reason != events.ExecutionError {
			d.summary = string(e.Reason)
		}
	}
}

func runTrust(args []string, out, errOut io.Writer) int {
	store, err := trust.Default()
	if err != nil {
		fmt.Fprintln(errOut, "cannot locate trust store")
		return 1
	}
	switch {
	case len(args) == 1 && args[0] == "list":
		var paths []string
		paths, err = store.List()
		if err == nil {
			for _, path := range paths {
				fmt.Fprintln(out, ui.SafeText(path))
			}
		}
	case len(args) == 2 && args[0] == "revoke":
		var path string
		path, err = trust.Canonical(args[1])
		if err == nil {
			err = store.Revoke(path)
		}
	case len(args) == 1 && args[0] == "clear":
		err = store.Clear()
	default:
		fmt.Fprintln(errOut, "Usage: augustin trust list | revoke PATH | clear")
		return 2
	}
	if err != nil {
		fmt.Fprintln(errOut, "trust operation failed")
		return 1
	}
	return 0
}

func (d *turnDiagnostic) ToolCall(call model.ToolCall) {
	if sink, ok := d.sink.(interface{ ToolCall(model.ToolCall) }); ok {
		sink.ToolCall(call)
	}
}

func (d *turnDiagnostic) TextDelta(text string) {
	if sink, ok := d.sink.(interface{ TextDelta(string) }); ok {
		sink.TextDelta(text)
	}
}
