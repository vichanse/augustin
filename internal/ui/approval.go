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
		if m.previewComplete() {
			m.approvalChoice = 1 - m.approvalChoice
		} else {
			m.approvalChoice = choiceDeny
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

// footerChrome is the mode line above the panel and the key hint below it.
const footerChrome = 2

// previewComplete reports whether the whole panel, with every line of the
// action, fits the real terminal size. Only a complete preview can be approved.
func (m shellModel) previewComplete() bool {
	if m.approval == nil || m.approval.Truncated || m.actionWidth() < 1 {
		return false // below one action column, lipgloss would rewrap action lines
	}
	panel := m.renderPanel(actionLines(m.approval.Action, m.actionWidth()), true)
	return lipgloss.Width(panel) <= m.width && lipgloss.Height(panel)+footerChrome <= m.height
}

// actionWidth is the panel content width (terminal minus border and padding)
// minus the two-space action indent. It may be < 1 on tiny terminals.
func (m shellModel) actionWidth() int { return m.width - 6 }

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
	lines := actionLines(m.approval.Action, max(1, m.actionWidth()))
	if m.previewComplete() {
		return m.renderPanel(lines, true)
	}
	// Show as much as fits; approval stays disabled anyway.
	keep := max(0, m.height-footerChrome-lipgloss.Height(m.renderPanel(nil, false)))
	return m.renderPanel(lines[:min(len(lines), keep)], false)
}

func (m shellModel) renderPanel(lines []string, complete bool) string {
	r := m.approval
	parts := []string{
		m.paint("Approval required", "214"),
		"Capability: " + visible(string(r.Capability)),
		"Tool: " + visible(r.Tool),
		"Action:",
	}
	for _, l := range lines {
		parts = append(parts, "  "+l)
	}
	approve := "Approve once"
	if !complete {
		reason := "does not fit the terminal"
		if r.Truncated {
			reason = "exceeds the preview size limit"
		}
		parts = append(parts, m.paint("⚠ Preview incomplete: action "+reason+". Approval disabled.", "214"))
		approve = m.paint("Approve once (unavailable)", "245")
	}
	parts = append(parts, "")
	for i, option := range []string{approve, "Deny"} {
		prefix := "  "
		if i == m.approvalChoice {
			prefix = m.paint("❯ ", "99")
		}
		parts = append(parts, prefix+option)
	}
	// Width counts padding but not the border: the panel spans exactly m.width.
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(max(1, m.width-2)).Render(strings.Join(parts, "\n"))
}
