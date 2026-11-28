// Command augustin-eval measures Augustin's bug fixing with independent
// evaluator tests. Deterministic mode (the default) uses scripted model
// behaviour and needs no network; real mode calls the configured provider and
// must be requested explicitly.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/vichanse/augustin/evals"
	"github.com/vichanse/augustin/internal/config"
	"github.com/vichanse/augustin/internal/model"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv, config.NewProvider))
}

const usage = `Usage:
  augustin-eval --list
  augustin-eval [--mode deterministic] [--case ID] [--json FILE]
  augustin-eval --mode real (--case ID | --all | --smoke) [--repeat N] [--max-duration D] [--json FILE]
                [--input-price USD_PER_MTOK --output-price USD_PER_MTOK]`

// run returns 0 when every attempt is as expected, 1 when an attempt is not
// (unresolved or invalid in real mode, a mismatch in deterministic mode),
// 2 for usage or configuration errors and 130 when canceled.
func run(ctx context.Context, args []string, out, errOut io.Writer, lookup config.Lookup, newProvider func(config.Lookup) (model.Provider, error)) int {
	flags := flag.NewFlagSet("augustin-eval", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	list := flags.Bool("list", false, "")
	mode := flags.String("mode", "deterministic", "")
	caseID := flags.String("case", "", "")
	all := flags.Bool("all", false, "")
	smoke := flags.Bool("smoke", false, "")
	repeat := flags.Int("repeat", 1, "")
	jsonPath := flags.String("json", "", "")
	maxDuration := flags.Duration("max-duration", 30*time.Minute, "")
	inputPrice := flags.Float64("input-price", -1, "")
	outputPrice := flags.Float64("output-price", -1, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *repeat < 1 || *maxDuration <= 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	cases, err := evals.Cases()
	if err != nil {
		fmt.Fprintln(errOut, "invalid dataset:", err)
		return 2
	}
	if *list {
		for _, c := range cases {
			fmt.Fprintf(out, "%-16s %-9s %s\n", c.ID, c.Mode, c.Description)
		}
		fmt.Fprintln(out, "\nDeterministic scenarios:")
		for _, s := range evals.Scenarios {
			fmt.Fprintf(out, "  %s/%s (expects %s)\n", s.Case, s.Name, s.Expect)
		}
		return 0
	}
	selected, ok := selectCases(cases, *caseID, *all, *smoke, *mode)
	if !ok {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	runner := &evals.Runner{MaxDuration: *maxDuration}
	if *inputPrice >= 0 && *outputPrice >= 0 {
		runner.InputPerMTok, runner.OutputPerMTok = inputPrice, outputPrice
	}
	var jobs []evals.Job
	providerName, modelName := "scripted", ""
	switch *mode {
	case "deterministic":
		runner.Provider, runner.MaxIterations = evals.ScriptedProvider, 10
		jobs = evals.DeterministicJobs(selected)
		*repeat = 1 // scripts are deterministic
	case "real":
		// Explicit opt-in: this flag plus the provider configuration and the
		// remote-data consent that the interactive CLI already requires.
		c, err := config.Load(lookup)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		fmt.Fprintln(errOut, "Real-model evaluation: sends prompts and fixture contents to the provider and runs project tests on this machine.\nWorkspaces and tool restrictions are not an OS sandbox: run only in a disposable environment without unrelated credentials.")
		providerName, modelName = c.Provider, c.Model
		runner.MaxIterations, runner.Budget = c.MaxIterations, c.Budget
		runner.Provider = func(evals.Target) (model.Provider, error) { return newProvider(lookup) }
		for _, c := range selected {
			jobs = append(jobs, evals.Job{Case: c})
		}
	default:
		fmt.Fprintln(errOut, usage)
		return 2
	}
	started := time.Now()
	attempts, runErr := runner.Run(ctx, jobs, *repeat)
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		fmt.Fprintln(errOut, "evaluation failed:", runErr)
		return 1
	}
	report := evals.NewReport(runner, *mode, providerName, modelName, *repeat, started, attempts)
	report.Summary.Canceled = runErr != nil
	report.Print(out)
	if *jsonPath != "" {
		if err := report.WriteJSON(*jsonPath); err != nil {
			fmt.Fprintln(errOut, "cannot write report:", err)
			return 1
		}
	}
	switch {
	case runErr != nil:
		return 130
	case *mode == "deterministic" && report.Summary.Unexpected == 0 && len(attempts) == len(jobs):
		return 0
	case *mode == "real" && report.Summary.Resolved == report.Summary.Attempts && len(attempts) == len(jobs)*(*repeat):
		return 0
	}
	return 1
}

// selectCases picks bug-fixing cases for --all, the read-only smoke case for
// --smoke, or one case. Deterministic mode defaults to every case.
func selectCases(cases []evals.Case, id string, all, smoke bool, mode string) ([]evals.Case, bool) {
	if (id != "" && (all || smoke)) || (all && smoke) {
		return nil, false
	}
	var out []evals.Case
	for _, c := range cases {
		switch {
		case id != "" && c.ID == id,
			all && !c.ReadOnly(),
			smoke && c.ReadOnly(),
			id == "" && !all && !smoke && mode == "deterministic":
			out = append(out, c)
		}
	}
	return out, len(out) > 0 && (mode != "real" || id != "" || all || smoke)
}
