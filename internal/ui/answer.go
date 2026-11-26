package ui

import "github.com/charmbracelet/glamour"

// Answer formats sanitized model text for a color-capable terminal.
func Answer(content string, styled bool) string {
	content = SafeText(content)
	if !styled {
		return content
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(80))
	if err != nil {
		return content
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return rendered
}
