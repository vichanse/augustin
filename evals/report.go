package evals

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/version"
)

// ReportSchema changes whenever the report format changes incompatibly.
const ReportSchema = 1

// Attempt holds metrics only: no prompts, source code, diffs, tool output or
// credentials are recorded.
type Attempt struct {
	Case                 string   `json:"case"`
	Scenario             string   `json:"scenario,omitempty"`
	Attempt              int      `json:"attempt"`
	Outcome              Outcome  `json:"outcome"`
	Expected             Outcome  `json:"expected,omitempty"`
	Detail               string   `json:"detail,omitempty"`
	Verification         string   `json:"verification"`
	VisibleTests         string   `json:"visible_tests"`
	HiddenTests          string   `json:"hidden_tests"`
	ModifiedFiles        int      `json:"modified_files"`
	UnauthorizedChanges  int      `json:"unauthorized_changes"`
	ApprovedActions      int      `json:"approved_actions"`
	RejectedActions      int      `json:"rejected_actions"`
	Iterations           int      `json:"iterations"`
	ToolCalls            int      `json:"tool_calls"`
	InputTokens          int      `json:"input_tokens"`
	OutputTokens         int      `json:"output_tokens"`
	VerificationAttempts int      `json:"verification_attempts"`
	AgentSeconds         float64  `json:"agent_seconds"`
	EvaluationSeconds    float64  `json:"evaluation_seconds"`
	TotalSeconds         float64  `json:"total_seconds"`
	EstimatedCostUSD     *float64 `json:"estimated_cost_usd,omitempty"`
}

type ReportConfig struct {
	MaxIterations      int      `json:"max_iterations"`
	MaxToolCalls       int      `json:"max_tool_calls"`
	MaxOutputBytes     int      `json:"max_output_bytes"`
	TurnTimeoutSeconds float64  `json:"turn_timeout_seconds"`
	Repeat             int      `json:"repeat"`
	MaxDurationSeconds float64  `json:"max_duration_seconds"`
	InputPerMTokUSD    *float64 `json:"input_price_per_mtok_usd,omitempty"`
	OutputPerMTokUSD   *float64 `json:"output_price_per_mtok_usd,omitempty"`
}

type Summary struct {
	Attempts             int     `json:"attempts"`
	Resolved             int     `json:"resolved"`
	Evaluated            int     `json:"evaluated"` // resolution-rate denominator: attempts minus infrastructure errors
	InfrastructureErrors int     `json:"infrastructure_errors"`
	ResolutionRate       float64 `json:"resolution_rate"`
	AverageSeconds       float64 `json:"average_seconds"`
	InputTokens          int     `json:"input_tokens"`
	OutputTokens         int     `json:"output_tokens"`
	Unexpected           int     `json:"unexpected,omitempty"` // deterministic mode: outcome differs from the scenario's expectation
	Canceled             bool    `json:"canceled,omitempty"`
}

type Report struct {
	Schema          int          `json:"schema"`
	AugustinVersion string       `json:"augustin_version"`
	DatasetVersion  string       `json:"dataset_version"`
	DatasetHash     string       `json:"dataset_hash"`
	Mode            string       `json:"mode"`
	Provider        string       `json:"provider"`
	Model           string       `json:"model"`
	StartedAt       time.Time    `json:"started_at"`
	Config          ReportConfig `json:"config"`
	Attempts        []Attempt    `json:"attempts"`
	Summary         Summary      `json:"summary"`
}

// NewReport builds a report for attempts produced by r.
func NewReport(r *Runner, mode, provider, modelName string, repeat int, started time.Time, attempts []Attempt) Report {
	b := r.Budget
	if b == (agent.Budget{}) {
		b = agent.Budget{MaxToolCalls: 32, MaxOutputBytes: 2 << 20, Timeout: 2 * time.Minute} // agent.Loop defaults
	}
	rep := Report{
		Schema: ReportSchema, AugustinVersion: version.Current(), DatasetVersion: DatasetVersion, DatasetHash: DatasetHash(),
		Mode: mode, Provider: provider, Model: modelName, StartedAt: started.UTC(),
		Config: ReportConfig{MaxIterations: r.MaxIterations, MaxToolCalls: b.MaxToolCalls, MaxOutputBytes: b.MaxOutputBytes,
			TurnTimeoutSeconds: b.Timeout.Seconds(), Repeat: max(1, repeat), MaxDurationSeconds: r.MaxDuration.Seconds(),
			InputPerMTokUSD: r.InputPerMTok, OutputPerMTokUSD: r.OutputPerMTok},
		Attempts: attempts,
	}
	s := &rep.Summary
	total := 0.0
	for _, a := range attempts {
		s.Attempts++
		total += a.TotalSeconds
		s.InputTokens += a.InputTokens
		s.OutputTokens += a.OutputTokens
		switch a.Outcome {
		case InfrastructureError:
			s.InfrastructureErrors++
		case Resolved:
			s.Resolved++
		}
		if a.Expected != "" && a.Expected != a.Outcome {
			s.Unexpected++
		}
	}
	s.Evaluated = s.Attempts - s.InfrastructureErrors
	if s.Evaluated > 0 {
		s.ResolutionRate = float64(s.Resolved) / float64(s.Evaluated)
	}
	if s.Attempts > 0 {
		s.AverageSeconds = total / float64(s.Attempts)
	}
	if rep.Attempts == nil {
		rep.Attempts = []Attempt{}
	}
	return rep
}

// WriteJSON writes the report to path.
func (rep Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Print writes the terminal summary.
func (rep Report) Print(w io.Writer) {
	fmt.Fprintf(w, "Augustin %s · dataset v%s (%s) · %s", rep.AugustinVersion, rep.DatasetVersion, rep.DatasetHash[:12], rep.Mode)
	if rep.Model != "" {
		fmt.Fprintf(w, " · %s/%s", rep.Provider, rep.Model)
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	expectations := rep.Summary.Unexpected > 0 || (len(rep.Attempts) > 0 && rep.Attempts[0].Expected != "")
	header := "CASE\tRESULT\tTIME\tTOKENS\tVERIFICATION\tHIDDEN"
	if expectations {
		header += "\tEXPECTED"
	}
	fmt.Fprintln(tw, header)
	for _, a := range rep.Attempts {
		name := a.Case
		if a.Scenario != "" {
			name += "/" + a.Scenario
		}
		if rep.Config.Repeat > 1 {
			name += fmt.Sprintf(" #%d", a.Attempt)
		}
		line := fmt.Sprintf("%s\t%s\t%.1fs\t%d\t%s\t%s", name, strings.ToUpper(string(a.Outcome)), a.TotalSeconds, a.InputTokens+a.OutputTokens, a.Verification, a.HiddenTests)
		if expectations {
			mark := "ok"
			if a.Expected != a.Outcome {
				mark = "MISMATCH (" + string(a.Expected) + ")"
			}
			line += "\t" + mark
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
	s := rep.Summary
	fmt.Fprintf(w, "\nResolved: %d/%d (%.0f%%) · denominator excludes %d infrastructure error(s)\n", s.Resolved, s.Evaluated, 100*s.ResolutionRate, s.InfrastructureErrors)
	fmt.Fprintf(w, "Attempts: %d · average %.1fs · tokens %d in / %d out\n", s.Attempts, s.AverageSeconds, s.InputTokens, s.OutputTokens)
	if s.Unexpected > 0 {
		fmt.Fprintf(w, "Unexpected outcomes: %d\n", s.Unexpected)
	}
	if s.Canceled {
		fmt.Fprintln(w, "Canceled: partial results.")
	}
}
