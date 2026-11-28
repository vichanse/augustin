//go:build !unix

package tools

import "os/exec"

// Isolate is best effort outside Unix: cancellation kills the direct child
// only, and its own children may keep running (see ADR 0004).
func Isolate(*exec.Cmd) func() { return func() {} }
