package verification

import (
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/tools"
)

func edit(kind tools.EvidenceKind, path string) tools.Evidence {
	return tools.Evidence{Kind: kind, Capability: tools.Edit, Path: path}
}

func run(kind tools.EvidenceKind, status string, exit int, request ...string) tools.Evidence {
	return tools.Evidence{Kind: kind, Capability: tools.Execute, Request: request, Status: status, ExitCode: exit}
}

func goTest(kind tools.EvidenceKind, status string, exit int) tools.Evidence {
	return run(kind, status, exit, "go", "test", "./...")
}

func report(canExecute bool, evidence ...tools.Evidence) (Report, string) {
	t := New(canExecute)
	for _, e := range evidence {
		t.Record(e)
	}
	return t.Report(), t.Continuation()
}

func TestNoEditNeedsNoVerification(t *testing.T) {
	r, next := report(true, goTest(tools.EvidenceApplied, "exited", 1))
	if r.Status != NotRequired || next != "" {
		t.Fatalf("read-only turn: %+v %q", r, next)
	}
}

func TestEditIsPendingUntilATestCoversIt(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "discount.go"))
	if r.Status != Pending || r.Revision != 1 || r.Edits != 1 || !strings.Contains(next, "go test ./...") {
		t.Fatalf("after edit: %+v %q", r, next)
	}
	r, next = report(true, edit(tools.EvidenceApplied, "discount.go"), goTest(tools.EvidenceApplied, "exited", 0))
	if r.Status != Passed || r.VerifiedRevision != 1 || r.ExitCode == nil || *r.ExitCode != 0 || r.Attempts != 1 || next != "" {
		t.Fatalf("after passing tests: %+v %q", r, next)
	}
}

func TestTestsBeforeAnEditDoNotVerifyIt(t *testing.T) {
	r, _ := report(true, goTest(tools.EvidenceApplied, "exited", 0), edit(tools.EvidenceApplied, "a.go"))
	if r.Status != Pending || r.VerifiedRevision != 0 {
		t.Fatalf("stale test counted: %+v", r)
	}
}

func TestLaterEditInvalidatesPassedVerification(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 0), edit(tools.EvidenceApplied, "b.go"))
	if r.Status != Pending || r.Revision != 2 || r.VerifiedRevision != 1 || strings.Join(r.Paths, ",") != "a.go,b.go" || next == "" {
		t.Fatalf("second edit not invalidating: %+v", r)
	}
}

func TestFailedTestsProduceFailed(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 1))
	if r.Status != Failed || r.ExitCode == nil || *r.ExitCode != 1 || !strings.Contains(next, "fix the problem") {
		t.Fatalf("failed tests: %+v %q", r, next)
	}
	// A failure followed by a fix and a passing run is verified.
	r, _ = report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 1), edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 0))
	if r.Status != Passed || r.Attempts != 2 || r.Revision != 2 || r.Edits != 2 || len(r.Paths) != 1 {
		t.Fatalf("fix then pass: %+v", r)
	}
}

func TestRejectedOrFailedEditsAreNotMutations(t *testing.T) {
	r, _ := report(true, edit(tools.EvidenceRejected, "a.go"), edit(tools.EvidenceFailed, "a.go"))
	if r.Status != NotRequired || r.Revision != 0 {
		t.Fatalf("refused edits counted: %+v", r)
	}
}

func TestRejectedTestsAreRejectedAndNotRequestedAgain(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceRejected, "", 0))
	if r.Status != Rejected || r.Attempts != 0 || next != "" {
		t.Fatalf("rejected tests: %+v %q", r, next)
	}
}

func TestIncompleteRunsNeverPass(t *testing.T) {
	for name, e := range map[string]tools.Evidence{
		"timeout":                  goTest(tools.EvidenceApplied, "timeout", -1),
		"timeout reporting exit 0": goTest(tools.EvidenceApplied, "timeout", 0),
		"canceled or error":        goTest(tools.EvidenceFailed, "", 0),
		"unknown status":           goTest(tools.EvidenceApplied, "", 0),
	} {
		r, _ := report(true, edit(tools.EvidenceApplied, "a.go"), e)
		if r.Status == Passed || r.VerifiedRevision != 0 {
			t.Errorf("%s produced %+v", name, r)
		}
	}
	if r, _ := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceFailed, "", 0)); r.Status != Incomplete {
		t.Errorf("interrupted run: %+v", r)
	}
}

func TestOtherCommandsDoNotSubstituteForTests(t *testing.T) {
	for _, request := range [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, {"git", "diff"}, {"git", "status"}, {"go", "test"}, {"go", "test", "./...", "-run", "X"}} {
		r, _ := report(true, edit(tools.EvidenceApplied, "a.go"), run(tools.EvidenceApplied, "exited", 0, request...))
		if r.Status != Pending || r.Command != "" {
			t.Errorf("%v counted as verification: %+v", request, r)
		}
	}
}

func TestEditModeReportsUnverifiedWithoutAskingForTests(t *testing.T) {
	r, next := report(false, edit(tools.EvidenceApplied, "a.go"))
	if r.Status != Pending || r.Reason != "tests cannot run in edit mode" || next != "" {
		t.Fatalf("edit mode: %+v %q", r, next)
	}
}

func TestMarkIncompleteOnlyAffectsMissingVerification(t *testing.T) {
	tr := New(true)
	tr.Record(edit(tools.EvidenceApplied, "a.go"))
	tr.MarkIncomplete("execution budget exhausted")
	if r := tr.Report(); r.Status != Incomplete || r.Reason != "execution budget exhausted" {
		t.Fatalf("pending without budget: %+v", r)
	}
	tr.Record(goTest(tools.EvidenceApplied, "exited", 1))
	if r := tr.Report(); r.Status != Failed {
		t.Fatalf("a real failure must stay failed: %+v", r)
	}
}

// Regression: a later run on the same revision decides, even after a pass.
func TestTimeoutAfterPassOnSameRevisionIsNotPassed(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 0), goTest(tools.EvidenceApplied, "timeout", 0))
	if r.Status == Passed || r.Status != Failed || r.Reason != "timed out" || next == "" {
		t.Fatalf("timeout with exit 0 after a pass: %+v %q", r, next)
	}
}

func TestExecutionErrorWithZeroExitIsNotPassed(t *testing.T) {
	r, next := report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "error", 0))
	if r.Status != Incomplete || r.VerifiedRevision != 0 || !strings.Contains(next, "did not complete") {
		t.Fatalf("execution error with exit 0: %+v %q", r, next)
	}
	r, _ = report(true, edit(tools.EvidenceApplied, "a.go"), goTest(tools.EvidenceApplied, "exited", 0), goTest(tools.EvidenceApplied, "error", 0))
	if r.Status == Passed {
		t.Fatalf("execution error after a pass: %+v", r)
	}
}
