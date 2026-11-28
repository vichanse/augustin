package evals

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"github.com/vichanse/augustin/internal/tools"
)

// Approver answers approval requests during unattended evaluations. It is
// narrower than any human decision: edits only to a case's declared source
// files, and only the allowlisted Go commands. It is never reachable from the
// interactive CLI: the evaluation runner constructs it explicitly, and
// nothing enables it through configuration or the environment.
type Approver struct {
	allowed  map[string]bool
	mu       sync.Mutex
	approved int
	rejected int
}

var evalCommands = [][]string{{"test", "./..."}, {"vet", "./..."}, {"build", "./..."}}

func NewApprover(allowedPaths []string) *Approver {
	a := &Approver{allowed: map[string]bool{}}
	for _, p := range allowedPaths {
		a.allowed[p] = true
	}
	return a
}

// Approve implements tools.Approver. The policy has already checked the mode
// and capability; this only narrows what may be approved.
func (a *Approver) Approve(_ context.Context, p tools.Proposal) (bool, error) {
	ok := false
	switch p.Capability {
	case tools.Edit:
		ok = p.Diff != "" && a.allowed[path.Clean(p.Path)]
	case tools.Execute:
		ok = len(p.Command) > 1 && filepath.Base(p.Command[0]) == "go" &&
			slices.ContainsFunc(evalCommands, func(c []string) bool { return slices.Equal(c, p.Command[1:]) })
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ok {
		a.approved++
	} else {
		a.rejected++
	}
	return ok, nil
}

// Counts returns how many actions were approved and rejected.
func (a *Approver) Counts() (approved, rejected int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.approved, a.rejected
}
