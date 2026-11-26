package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type slashCommand struct {
	name        string
	description string
}

var slashCommands = []slashCommand{
	{"/help", "Show available commands"},
	{"/clear", "Clear conversation"},
	{"/status", "Show last turn statistics"},
	{"/model", "Show active model"},
	{"/tools", "List available tools"},
	{"/trust", "Show workspace permissions"},
	{"/permissions", "Show harness capability policy"},
	{"/exit", "Exit Augustin"},
	{"/sessions", "List saved sessions"},
	{"/resume", "Resume a session by ID"},
	{"/new", "Start a new conversation"},
	{"/rename", "Rename current session"},
	{"/delete", "Delete a session by ID"},
}

func matchingCommands(value string) []slashCommand {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\n") {
		return nil
	}
	var matches []slashCommand
	for _, command := range slashCommands {
		if strings.HasPrefix(command.name, value) {
			matches = append(matches, command)
		}
	}
	return matches
}

func (m shellModel) commandMenu() string {
	if m.busy {
		return ""
	}
	matches := matchingCommands(m.composer.Value())
	if len(matches) == 0 {
		return ""
	}
	index := min(m.commandIndex, len(matches)-1)
	start := max(0, index-2)
	end := min(len(matches), start+3)
	var lines []string
	for i := start; i < end; i++ {
		line := "  " + matches[i].name + " — " + matches[i].description
		if i == index {
			line = m.paint("❯ "+strings.TrimSpace(line), "99")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m shellModel) runCommand(command string) (tea.Model, tea.Cmd) {
	m.composer.Reset()
	m.commandIndex = 0
	m.scroll = 0
	name, _, _ := strings.Cut(command, " ")
	switch name {
	case "/help":
		var lines []string
		for _, item := range slashCommands {
			lines = append(lines, item.name+" — "+item.description)
		}
		m.commandNotice = strings.Join(lines, "\n")
	case "/clear", "/new":
		m.session = savedSession{}
		m.history = nil
		m.rendered = nil
		m.turnDetails = nil
		m.activity = nil
		m.partial, m.partialRendered = "", ""
		m.status, m.submitted = "", ""
		m.tokens, m.toolCalls, m.elapsed = 0, 0, 0
		m.commandNotice = "Conversation cleared."
	case "/status":
		m.commandNotice = fmt.Sprintf("READ ONLY · Last turn: %d tools · %d tokens · %s", m.toolCalls, m.tokens, m.elapsed)
	case "/model":
		m.commandNotice = SafeText(m.provider) + " · " + SafeText(m.model)
	case "/tools":
		m.commandNotice = "Read-only tools: workspace_overview, list_files, read_file"
	case "/permissions":
		m.commandNotice = "Mode: READ ONLY\nRead: allowed for registered tools\nEdit: denied\nExecute: denied\nWorkspace trust does not grant additional capabilities."
	case "/trust":
		m.commandNotice = "Trusted workspace: " + SafeText(m.path) + "\nEditing and command execution disabled."
	case "/sessions", "/resume", "/rename", "/delete":
		m.sessionCommand(command)
	case "/exit":
		return m, tea.Quit
	default:
		m.commandNotice = "Unknown command. Type /help."
	}
	return m, nil
}
