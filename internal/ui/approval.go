package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/approval"
)

type approvalUpdate approval.Update

const (
	choiceApprove = 0
	choiceDeny    = 1 // default: Enter without moving never approves
)

func (m shellModel) approvalKey(key tea.KeyMsg) shellModel {
	switch key.String() {
	case "up", "down", "left", "right", "tab", "shift+tab":
		m.approvalChoice = choiceDeny
		if m.previewComplete() {
			m.approvalChoice = 1 - m.approvalChoice
		}
	case "enter":
		return m.decide(m.approvalChoice == choiceApprove)
	case "esc":
		return m.decide(false)
	}
	return m // every other key is swallowed: no prompt or command while pending
}

// decide hands the decision to the broker, which ignores it if the request
// is no longer the pending one. The policy still enforces the outcome.
func (m shellModel) decide(approved bool) shellModel {
	if approved && !m.previewComplete() {
		// Re-checked at decision time: the terminal may have shrunk since the choice.
		m.approvalChoice = choiceDeny
		return m
	}
	id := m.approval.ID
	m.approval = nil
	if m.approvals != nil {
		m.approvals.Resolve(id, approved)
	}
	return m
}

// previewComplete reports whether the whole action fits the panel at the
// current terminal size. Only a complete preview can be approved.
func (m shellModel) previewComplete() bool {
	return m.approval != nil && !m.approval.Truncated &&
		len(actionLines(m.approval.Action, m.actionWidth())) <= m.maxActionLines()
}

func (m shellModel) actionWidth() int { return max(16, m.width-10) }

// maxActionLines keeps the panel within the screen; the inline renderer cannot draw above it.
func (m shellModel) maxActionLines() int { return max(4, m.height-14) }

// visible writes every rune that would not show as itself as a JSON \u
// escape instead of dropping it. For the JSON actions the policy executes,
// the result is an equivalent JSON text: it decodes to exactly the same
// value, so what is shown is what runs. Invalid UTF-8 bytes appear as \xNN.
func visible(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == ' ' || strconv.IsPrint(r):
			b.WriteRune(r)
		case r > 0xFFFF:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// actionLines hard-wraps by display width without dropping any character
// (word wrapping may swallow spaces at break points).
func actionLines(action string, width int) []string {
	var lines []string
	var line strings.Builder
	used := 0
	for _, r := range visible(action) {
		w := lipgloss.Width(string(r))
		if used+w > width && used > 0 {
			lines = append(lines, line.String())
			line.Reset()
			used = 0
		}
		line.WriteRune(r)
		used += w
	}
	return append(lines, line.String())
}

func (m shellModel) approvalPanel() string {
	r := m.approval
	w := max(24, m.width-2)
	lines := actionLines(r.Action, m.actionWidth())
	complete := m.previewComplete()
	if len(lines) > m.maxActionLines() {
		lines = lines[:m.maxActionLines()]
	}
	action := "  " + strings.Join(lines, "\n  ")
	approve := "Approve once"
	if !complete {
		reason := "does not fit the terminal"
		if r.Truncated {
			reason = "exceeds the 4 KiB preview limit"
		}
		action += "\n" + m.paint("⚠ Preview incomplete: action "+reason+". Approval disabled.", "214")
		approve = m.paint("Approve once (unavailable)", "245")
	}
	options := []string{approve, "Deny"}
	for i := range options {
		if i == m.approvalChoice {
			options[i] = m.paint("❯ ", "99") + options[i]
		} else {
			options[i] = "  " + options[i]
		}
	}
	body := m.paint("Approval required", "214") +
		"\nCapability: " + visible(string(r.Capability)) +
		"\nTool: " + visible(r.Tool) +
		"\nAction:\n" + action +
		"\n\n" + strings.Join(options, "\n")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(w - 2).Render(body)
}
