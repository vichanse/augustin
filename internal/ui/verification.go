package ui

import (
	"fmt"
	"strings"

	"github.com/vichanse/augustin/internal/verification"
)

// verificationSummary renders the harness verdict from structured evidence
// only. It is printed apart from the assistant's Markdown, which cannot
// change it; nothing is shown for turns that modified no file.
func (m shellModel) verificationSummary(r verification.Report) string {
	if r.Status == "" || r.Status == verification.NotRequired {
		return ""
	}
	var head, color string
	switch r.Status {
	case verification.Passed:
		head, color = "✓ Verification passed", "78"
	case verification.Pending:
		head, color = "! Verification pending — files modified, tests not run", "214"
	case verification.Failed:
		head, color = "✗ Verification failed — "+verification.CommandText+" exited with code "+exitCode(r), "203"
		if r.Reason == "timed out" {
			head = "✗ Verification failed — " + verification.CommandText + " timed out"
		}
	case verification.Rejected:
		head, color = "✗ Verification rejected — the user declined test execution", "203"
	default:
		head, color = "! Verification incomplete — "+SafeText(r.Reason), "214"
	}
	paths := make([]string, len(r.Paths))
	for i, p := range r.Paths {
		paths[i] = SafeText(strings.ReplaceAll(p, "\n", " "))
	}
	lines := []string{m.paint("Harness verification", "245"), m.paint(head, color), fmt.Sprintf("Files modified: %d (%s)", len(r.Paths), strings.Join(paths, ", "))}
	if r.Status == verification.Passed || r.Status == verification.Failed {
		lines = append(lines, "Command: "+verification.CommandText)
		if r.ExitCode != nil {
			lines = append(lines, "Exit code: "+exitCode(r))
		}
	}
	if r.Status == verification.Pending && r.Reason != "" {
		lines = append(lines, "Reason: "+SafeText(r.Reason))
	}
	if r.Status == verification.Passed {
		lines = append(lines, "Tests passed after the last approved edit.")
	} else {
		lines = append(lines, "Not verified: any claim above that the tests pass is not backed by harness evidence.")
	}
	return strings.Join(lines, "\n")
}

func exitCode(r verification.Report) string {
	if r.ExitCode == nil {
		return "unknown"
	}
	return fmt.Sprint(*r.ExitCode)
}
