package ui

import (
	"strings"
	"testing"
)

func TestAnswerFencedCode(t *testing.T) {
	content := "```go\nfunc FinalPrice(price, percent int) int {\n    return price - price*percent/100\n}\n```"
	if got := Answer(content, false); got != content {
		t.Fatalf("plain answer changed: %q", got)
	}
	got := answerWidth(content, true, 100, "dark")
	if !strings.Contains(got, "FinalPrice") || !strings.Contains(got, "return") {
		t.Fatalf("rendered answer lost code: %q", got)
	}
	if strings.Contains(got, "```go") {
		t.Fatal("code fence was not rendered")
	}
}

func TestShellUsesDetectedMarkdownStyle(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", true)
	dark := m.markdown("# Title\n\nSome `code`.")
	m.mdStyle = "light"
	light := m.markdown("# Title\n\nSome `code`.")
	if dark == light || !strings.Contains(light, "Title") {
		t.Fatal("light terminals still get the dark theme")
	}
}

func TestMarkdownStyleDefaultsToDark(t *testing.T) {
	var out strings.Builder // not a terminal: background is unknown
	if got := markdownStyle(&out); got != "dark" {
		t.Fatalf("unexpected style for unknown background: %s", got)
	}
}
