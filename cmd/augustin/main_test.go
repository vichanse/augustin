package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Augustin") {
		t.Fatalf("help: code=%d output=%q", code, out.String())
	}
	if code := run([]string{"unknown"}, &out, &errOut); code != 2 || errOut.Len() == 0 {
		t.Fatalf("invalid arguments: code=%d stderr=%q", code, errOut.String())
	}
}
