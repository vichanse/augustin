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
	got := answerWidth(content, true, 100)
	if !strings.Contains(got, "FinalPrice") || !strings.Contains(got, "return") {
		t.Fatalf("rendered answer lost code: %q", got)
	}
	if strings.Contains(got, "```go") {
		t.Fatal("code fence was not rendered")
	}
}
