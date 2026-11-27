package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/approval"
)

type approvalUpdate approval.Update

const (
	choiceApprove = 0
	choiceDeny    = 1 // default: Enter without moving never approves
)

const maxActionLines = 8

func (m shellModel) approvalKey(key tea.KeyMsg) shellModel {
	switch key.String() {
	case "up", "down", "left", "right", "tab", "shift+tab":
		m.approvalChoice = 1 - m.approvalChoice
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
	id := m.approval.ID
	m.approval = nil
	if m.approvals != nil {
		m.approvals.Resolve(id, approved)
	}
	return m
}

func (m shellModel) approvalPanel() string {
	r := m.approval
	w := max(24, m.width-2)
	action := lipgloss.NewStyle().Width(w - 8).Render(SafeText(r.Action))
	lines := strings.Split(action, "\n")
	truncated := r.Truncated // never write through r: the request snapshot is shared
	if len(lines) > maxActionLines {
		lines, truncated = lines[:maxActionLines], true
	}
	if truncated {
		lines = append(lines, m.paint("… truncated", "245"))
	}
	options := []string{"Approve once", "Deny"}
	for i := range options {
		if i == m.approvalChoice {
			options[i] = m.paint("❯ "+options[i], "99")
		} else {
			options[i] = "  " + options[i]
		}
	}
	body := m.paint("Approval required", "214") +
		"\nCapability: " + SafeText(string(r.Capability)) +
		"\nTool: " + SafeText(r.Tool) +
		"\nAction:\n  " + strings.Join(lines, "\n  ") +
		"\n\n" + strings.Join(options, "\n")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(w - 2).Render(body)
}
