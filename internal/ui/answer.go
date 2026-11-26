package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/glamour"
	"golang.org/x/term"
)

// Answer formats sanitized model text for a color-capable terminal.
func Answer(content string, styled bool) string {
	return answerWidth(content, styled, 80)
}

func AnswerForOutput(content string, styled bool, out io.Writer) string {
	width := 80
	if f, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = max(20, min(w-2, 120))
		}
	}
	return answerWidth(content, styled, width)
}

func answerWidth(content string, styled bool, width int) string {
	content = SafeText(content)
	if !styled {
		return content
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
	if err != nil {
		return content
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return rendered
}
