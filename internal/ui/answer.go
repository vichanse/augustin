package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Answer formats sanitized model text for a color-capable terminal.
func Answer(content string, styled bool) string {
	return answerWidth(content, styled, 80, "dark")
}

func AnswerForOutput(content string, styled bool, out io.Writer) string {
	width := 80
	if f, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = max(20, min(w-2, 120))
		}
	}
	return answerWidth(content, styled, width, markdownStyle(out))
}

// markdownStyle picks the glamour theme matching the terminal background.
// Unknown backgrounds count as dark, the lipgloss default.
func markdownStyle(out io.Writer) string {
	if lipgloss.NewRenderer(out).HasDarkBackground() {
		return "dark"
	}
	return "light"
}

func answerWidth(content string, styled bool, width int, style string) string {
	content = SafeText(content)
	if !styled {
		return content
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return content
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return rendered
}
