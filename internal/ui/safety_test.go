package ui

import (
	"bytes"
	"testing"
)

func TestSafeText(t *testing.T) {
	input := "hello\x1b[2J\x07\r\u009b31m\u202e\n\t世界"
	want := "hello[2J31m\n\t世界"
	if got := SafeText(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedirectedOutput(t *testing.T) {
	var out bytes.Buffer
	lookup := func(string) (string, bool) { return "xterm", true }
	if Interactive(&out) || ColorEnabled(&out, lookup) {
		t.Fatal("buffer must not be treated as interactive")
	}
}
