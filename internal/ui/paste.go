package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Large pastes are kept out of the composer text. Each one is a single
// private-use rune in the composer, so the textarea moves over it and deletes
// it as one character, and only that rune (never typed text that looks like a
// label) is rendered as a label or expanded on submit.
const (
	pasteMinLines = 5
	pasteMinChars = 1000
	maxPasteBytes = 1 << 20

	pasteFirst = rune(0x100000) // Supplementary Private Use Area-B
	pasteLast  = rune(0x10FFFD)
)

func isPasteRune(r rune) bool { return r >= pasteFirst && r <= pasteLast }

func hasPasteRune(s string) bool { return strings.IndexFunc(s, isPasteRune) >= 0 }

func logicalLines(s string) int {
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n
}

// needsBlock reports whether a paste must be stored as a block: it is large,
// or inserting it as text would alter it (control characters, our own marker
// runes) or be cut by the composer's character limit.
func (m shellModel) needsBlock(text string, runes int) bool {
	return logicalLines(text) >= pasteMinLines || runes >= pasteMinChars || hasPasteRune(text) ||
		strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 ||
		(m.composer.CharLimit > 0 && m.composer.Length()+runes > m.composer.CharLimit)
}

// insertPaste stores a paste as a block at the cursor. It reports false when
// the paste should go to the textarea as ordinary text.
func (m *shellModel) insertPaste(pasted []rune) bool {
	text := string(pasted)
	if !m.needsBlock(text, len(pasted)) {
		return false
	}
	switch {
	case len(text) > maxPasteBytes:
		m.commandNotice = fmt.Sprintf("Paste not inserted: %d bytes exceeds the %d-byte limit.", len(text), maxPasteBytes)
		return true
	case m.composer.CharLimit > 0 && m.composer.Length() >= m.composer.CharLimit:
		m.commandNotice = "Paste not inserted: the composer is full."
		return true
	case m.nextPaste > pasteLast:
		m.commandNotice = "Paste not inserted: too many pasted blocks in this session."
		return true
	}
	if m.pastes == nil {
		m.pastes = map[rune]string{}
	}
	if m.nextPaste == 0 {
		m.nextPaste = pasteFirst
	}
	r := m.nextPaste
	m.nextPaste++ // IDs are never reused, even after the block is deleted
	m.pastes[r] = text
	m.composer.InsertRune(r)
	m.resizeComposer()
	return true
}

func (m shellModel) pasteLabel(r rune) string {
	id := r - pasteFirst + 1
	text, ok := m.pastes[r]
	switch {
	case !ok:
		return fmt.Sprintf("[Pasted text #%d]", id)
	case logicalLines(text) > 1:
		return fmt.Sprintf("[Pasted text #%d +%d lines]", id, logicalLines(text))
	default:
		return fmt.Sprintf("[Pasted text #%d · %d chars]", id, utf8.RuneCountInString(text))
	}
}

func (m shellModel) mapPastes(s string, block func(rune) string) string {
	if !hasPasteRune(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isPasteRune(r) {
			b.WriteString(block(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// compact is what the user sees: blocks as labels.
func (m shellModel) compact(s string) string { return m.mapPastes(s, m.pasteLabel) }

// expand is what the model receives: blocks as their exact pasted content.
func (m shellModel) expand(s string) string {
	return m.mapPastes(s, func(r rune) string { return m.pastes[r] })
}

// composerView renders labels in place of block runes. The textarea wrapped
// each block as one cell, so lines a label pushes past the width are rewrapped.
func (m shellModel) composerView() string {
	view := m.composer.View()
	if !hasPasteRune(view) {
		return view
	}
	width := max(1, m.composer.Width()+lipgloss.Width(m.composer.Prompt))
	lines := strings.Split(m.compact(view), "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = lipgloss.NewStyle().Width(width).Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// collectPastes drops blocks nothing refers to anymore: deleting a block or
// clearing the composer frees its content, while prompt history, the saved
// draft and a running or restorable submission keep theirs.
func (m *shellModel) collectPastes() {
	if len(m.pastes) == 0 {
		return
	}
	live := map[rune]bool{}
	mark := func(s string) {
		for _, r := range s {
			if isPasteRune(r) {
				live[r] = true
			}
		}
	}
	mark(m.composer.Value())
	mark(m.draft)
	mark(m.submitted)
	for _, p := range m.prompts {
		mark(p)
	}
	for r := range m.pastes {
		if !live[r] {
			delete(m.pastes, r)
		}
	}
}
