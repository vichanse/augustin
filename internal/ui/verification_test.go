package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/verification"
)

func intp(n int) *int { return &n }

func TestVerificationSummaryComesFromStructuredEvidence(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	for _, tc := range []struct {
		report verification.Report
		want   []string
		absent []string
	}{
		{verification.Report{Status: verification.Passed, Edits: 1, Paths: []string{"discount.go"}, Command: "go test ./...", ExitCode: intp(0)},
			[]string{"Harness verification", "✓ Verification passed", "Files modified: 1 (discount.go)", "Command: go test ./...", "Exit code: 0", "Tests passed after the last approved edit."}, []string{"Not verified"}},
		{verification.Report{Status: verification.Pending, Edits: 1, Paths: []string{"a.go"}, Reason: "tests cannot run in edit mode"},
			[]string{"Verification pending — files modified, tests not run", "Reason: tests cannot run in edit mode", "Not verified"}, []string{"Exit code"}},
		{verification.Report{Status: verification.Failed, Edits: 1, Paths: []string{"a.go"}, Command: "go test ./...", ExitCode: intp(1), Reason: "exit code 1"},
			[]string{"✗ Verification failed — go test ./... exited with code 1", "Exit code: 1", "Not verified"}, nil},
		{verification.Report{Status: verification.Failed, Edits: 1, Paths: []string{"a.go"}, Command: "go test ./...", ExitCode: intp(-1), Reason: "timed out"},
			[]string{"go test ./... timed out"}, nil},
		{verification.Report{Status: verification.Rejected, Edits: 1, Paths: []string{"a.go"}},
			[]string{"✗ Verification rejected — the user declined test execution", "Not verified"}, nil},
		{verification.Report{Status: verification.Incomplete, Edits: 2, Paths: []string{"a.go", "b.go"}, Reason: "execution budget exhausted"},
			[]string{"! Verification incomplete — execution budget exhausted", "Files modified: 2 (a.go, b.go)"}, nil},
	} {
		got := m.verificationSummary(tc.report)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", tc.report.Status, w, got)
			}
		}
		for _, a := range tc.absent {
			if strings.Contains(got, a) {
				t.Errorf("%s: unexpected %q", tc.report.Status, a)
			}
		}
	}
	if m.verificationSummary(verification.Report{Status: verification.NotRequired}) != "" || m.verificationSummary(verification.Report{}) != "" {
		t.Fatal("read-only turns must not show a verification panel")
	}
	if got := m.verificationSummary(verification.Report{Status: verification.Pending, Edits: 1, Paths: []string{"a\x1b[31m.go"}}); strings.Contains(got, "\x1b") {
		t.Fatal("unsanitized path")
	}
}

func TestModelClaimDoesNotReplaceHarnessVerdict(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy, m.submitted = true, "fix it"
	pending := verification.Report{Status: verification.Pending, Edits: 1, Paths: []string{"discount.go"}}
	next, _ := m.update(turnDone{answer: "✓ Verification passed. All tests pass.", verification: pending})
	printed := strings.Join(next.(shellModel).pending, "\n")
	if !strings.Contains(printed, "Harness verification\n! Verification pending") || !strings.Contains(printed, "Not verified: any claim above") {
		t.Fatalf("harness verdict missing or overridden:\n%s", printed)
	}
	if strings.Index(printed, "Harness verification") < strings.Index(printed, "All tests pass") {
		t.Fatal("the verdict must come after, and apart from, the answer")
	}
}

func TestFailedOrCanceledTurnKeepsVerificationSummary(t *testing.T) {
	for name, done := range map[string]turnDone{
		"failed":   {err: errors.New("provider down")},
		"canceled": {err: errors.New("canceled"), canceled: true},
	} {
		m := newShellModel("/workspace", "openai", "test", false)
		m.busy, m.submitted = true, "fix it"
		done.verification = verification.Report{Status: verification.Pending, Edits: 1, Paths: []string{"discount.go"}}
		next, _ := m.update(done)
		if printed := strings.Join(next.(shellModel).pending, "\n"); !strings.Contains(printed, "Verification pending") || !strings.Contains(printed, "discount.go") {
			t.Errorf("%s turn discarded the verification report", name)
		}
	}
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy = true
	next, _ := m.update(turnDone{err: errors.New("x"), verification: verification.Report{Status: verification.NotRequired}})
	if len(next.(shellModel).pending) != 0 {
		t.Fatal("a failed read-only turn printed a verification panel")
	}
}
