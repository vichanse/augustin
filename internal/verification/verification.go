// Package verification tracks, from harness evidence only, whether the files
// changed in a turn are covered by a successful test run.
//
// It never reads assistant messages or tool output text: evidence comes from
// tools.Registry at the execution boundary. A passed status means one
// allowlisted test command exited 0 after the last applied edit; it is not
// proof of correctness, and it cannot see changes made outside Augustin.
package verification

import (
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/vichanse/augustin/internal/tools"
)

type Status string

const (
	NotRequired Status = "not_required" // no file was modified
	Pending     Status = "pending"      // modified, no test run covers the latest edit
	Passed      Status = "passed"
	Failed      Status = "failed"
	Rejected    Status = "rejected" // the user declined the test run
	Incomplete  Status = "incomplete"
)

// Command is the only command that verifies a change.
var Command = []string{"go", "test", "./..."}

const CommandText = "go test ./..."

// CommandArguments are the run_command arguments of the harness-initiated run.
const CommandArguments = `{"command":"go","args":["test","./..."]}`

// HarnessRunNote follows the result of a harness-initiated test run.
const HarnessRunNote = "The harness ran " + CommandText + " because you finished without a test run covering your last edit; the result is above. If it passed, give your final answer and report the actual result. If it failed, analyze the output and fix the problem with edit_file; the harness will run the tests again. If the user rejected the run, do not request it again."

// Report is the harness verdict for one turn.
type Report struct {
	Status           Status
	Edits            int
	Paths            []string
	Revision         int // mutation revision: number of applied edits
	VerifiedRevision int // revision covered by the latest passing test run, 0 if none
	Command          string
	ExitCode         *int
	Attempts         int // approved test runs, completed or not
	Reason           string
}

type testRun struct {
	revision int
	kind     tools.EvidenceKind
	status   string
	exitCode int
}

// interrupted reports a run that did not complete normally: canceled, never
// started, or ended with an execution error. Its exit code means nothing.
func (r *testRun) interrupted() bool {
	return r.kind == tools.EvidenceFailed || (r.kind == tools.EvidenceApplied && r.status != "exited" && r.status != "timeout")
}

// Tracker accumulates evidence for one turn.
type Tracker struct {
	mu         sync.Mutex
	canExecute bool
	edits      int
	paths      map[string]bool
	verified   int
	attempts   int
	last       *testRun
	incomplete string
}

// New starts a turn. canExecute is false in edit mode, where tests cannot run.
func New(canExecute bool) *Tracker {
	return &Tracker{canExecute: canExecute, paths: map[string]bool{}}
}

// Record takes one piece of registry evidence.
func (t *Tracker) Record(e tools.Evidence) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case e.Capability == tools.Edit && e.Kind == tools.EvidenceApplied:
		t.edits++ // a new revision: earlier test runs no longer cover it
		t.paths[e.Path] = true
	case e.Capability == tools.Execute && slices.Equal(e.Request, Command):
		if e.Kind != tools.EvidenceRejected {
			t.attempts++
		}
		t.last = &testRun{revision: t.edits, kind: e.Kind, status: e.Status, exitCode: e.ExitCode}
		if e.Kind == tools.EvidenceApplied && e.Status == "exited" && e.ExitCode == 0 {
			t.verified = t.edits
		}
	}
}

// MarkIncomplete records why the gate could not obtain verification.
func (t *Tracker) MarkIncomplete(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.incomplete = reason
}

// NeedsTestRun reports whether the harness should request the test run
// itself: in execute mode, after an untested edit or an interrupted run.
func (t *Tracker) NeedsTestRun() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.report()
	interrupted := t.last != nil && t.last.revision == t.edits && t.last.interrupted()
	return t.canExecute && (r.Status == Pending || (r.Status == Incomplete && interrupted))
}

// Continuation returns the instruction the loop gives the model when it tries
// to finish without valid verification, or "" when finishing is acceptable.
// It never asks again after the user rejected the test run.
func (t *Tracker) Continuation() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.report()
	if !t.canExecute {
		return ""
	}
	switch r.Status {
	case Pending:
		return "The harness has recorded successful file modifications, but no successful test run covers the latest revision. Use run_command to request " + CommandText + " before claiming verification."
	case Failed:
		return fmt.Sprintf("The harness recorded that %s did not pass after the latest edit (%s). Analyze the test output already provided, fix the problem with edit_file, then request %s again with run_command. Do not claim that the tests pass.", CommandText, r.Reason, CommandText)
	case Incomplete:
		if t.last != nil && t.last.revision == t.edits && t.last.interrupted() {
			return "The harness recorded that the last " + CommandText + " run did not complete. Request " + CommandText + " again with run_command before claiming verification."
		}
	}
	return ""
}

// Report computes the verdict from the evidence recorded so far.
func (t *Tracker) Report() Report {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.report()
}

func (t *Tracker) report() Report {
	r := Report{Edits: t.edits, Revision: t.edits, VerifiedRevision: t.verified, Attempts: t.attempts}
	for p := range t.paths {
		r.Paths = append(r.Paths, p)
	}
	sort.Strings(r.Paths)
	if t.last != nil {
		r.Command = CommandText
		if t.last.kind == tools.EvidenceApplied {
			code := t.last.exitCode
			r.ExitCode = &code
		}
	}
	current := t.last != nil && t.last.revision == t.edits // the last test ran after the last edit
	switch {
	case t.edits == 0:
		r.Status = NotRequired
	case t.verified == t.edits && current && t.last.kind == tools.EvidenceApplied && t.last.status == "exited" && t.last.exitCode == 0:
		r.Status = Passed
	case current && t.last.kind == tools.EvidenceRejected:
		r.Status, r.Reason = Rejected, "the user declined test execution"
	case current && t.last.interrupted():
		r.Status, r.Reason = Incomplete, CommandText+" did not complete"
	case current && t.last.status == "timeout":
		r.Status, r.Reason = Failed, "timed out"
	case current:
		r.Status, r.Reason = Failed, fmt.Sprintf("exit code %d", t.last.exitCode)
	case t.incomplete != "":
		r.Status, r.Reason = Incomplete, t.incomplete
	case !t.canExecute:
		r.Status, r.Reason = Pending, "tests cannot run in edit mode"
	default:
		r.Status, r.Reason = Pending, "tests not run after the last edit"
	}
	return r
}
