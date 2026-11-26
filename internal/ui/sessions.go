package ui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vichanse/augustin/internal/model"
)

type savedSession struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Updated time.Time       `json:"updated"`
	History []model.Message `json:"history"`
}

var sessionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func sessionDirectory(workspace string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(path))
	return filepath.Join(root, "augustin", "sessions", hex.EncodeToString(hash[:])), nil
}

func (m *shellModel) saveSession() error {
	if m.sessionDir == "" || len(m.history) == 0 {
		return nil
	}
	if m.session.ID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		m.session.ID = hex.EncodeToString(id[:])
		m.session.Name = "Conversation"
	}
	m.session.History = append([]model.Message(nil), m.history...)
	m.session.Updated = time.Now().UTC()
	return writeSession(m.sessionDir, m.session)
}

func writeSession(dir string, s savedSession) error {
	if !sessionID.MatchString(s.ID) {
		return errors.New("invalid session ID")
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > 4<<20 {
		return errors.New("invalid or oversized session")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, s.ID+".json"))
}

func readSession(dir, id string) (savedSession, error) {
	var s savedSession
	if !sessionID.MatchString(id) {
		return s, errors.New("invalid session ID")
	}
	f, err := os.Open(filepath.Join(dir, id+".json"))
	if err != nil {
		return s, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(data) > 4<<20 || json.Unmarshal(data, &s) != nil || s.ID != id || len(s.History) > 20 || len(s.History)%2 != 0 {
		return savedSession{}, errors.New("invalid session")
	}
	for i, msg := range s.History {
		role := model.RoleUser
		if i%2 == 1 {
			role = model.RoleAssistant
		}
		if msg.Role != role || len(msg.ToolCalls) != 0 || msg.ToolCallID != "" {
			return savedSession{}, errors.New("invalid session message")
		}
	}
	return s, nil
}

func (m *shellModel) sessionCommand(command string) {
	if m.sessionDir == "" {
		m.commandNotice = "Session storage disabled. Start with AUGUSTIN_SAVE_SESSIONS=true to enable local storage."
		return
	}
	name, arg, _ := strings.Cut(command, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "/sessions", "/resume":
		if name == "/resume" && arg != "" {
			s, err := readSession(m.sessionDir, arg)
			if err != nil {
				m.commandNotice = "Cannot load session."
				return
			}
			m.resetConversation()
			m.session = s
			m.history = s.History
			for _, msg := range m.history {
				if msg.Role == model.RoleAssistant {
					m.rendered = append(m.rendered, m.markdown(msg.Content))
					m.turnDetails = append(m.turnDetails, "")
				}
			}
			m.commandNotice = "Resumed " + SafeText(s.Name)
			return
		}
		entries, err := os.ReadDir(m.sessionDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			m.commandNotice = "Cannot list sessions."
			return
		}
		var sessions []savedSession
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
				if s, err := readSession(m.sessionDir, strings.TrimSuffix(entry.Name(), ".json")); err == nil {
					sessions = append(sessions, s)
				}
			}
		}
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Updated.After(sessions[j].Updated) })
		lines := []string{"Use /resume ID to restore a session."}
		for _, s := range sessions[:min(20, len(sessions))] {
			lines = append(lines, fmt.Sprintf("%s · %s · %s", s.ID, SafeText(s.Name), s.Updated.Format("2006-01-02 15:04")))
		}
		m.commandNotice = strings.Join(lines, "\n")
	case "/rename":
		if m.session.ID == "" || arg == "" || len(arg) > 120 {
			m.commandNotice = "Usage: /rename NAME (after a completed turn)"
			return
		}
		m.session.Name = arg
		if err := m.saveSession(); err != nil {
			m.commandNotice = "Cannot save session."
		} else {
			m.commandNotice = "Session renamed."
		}
	case "/delete":
		if !sessionID.MatchString(arg) {
			m.commandNotice = "Usage: /delete ID"
			return
		}
		if err := os.Remove(filepath.Join(m.sessionDir, arg+".json")); err != nil {
			m.commandNotice = "Cannot delete session."
			return
		}
		if m.session.ID == arg {
			m.resetConversation()
		}
		m.commandNotice = "Session deleted."
	}
}

func (m *shellModel) resetConversation() {
	m.history, m.rendered, m.turnDetails, m.activity = nil, nil, nil, nil
	m.partial, m.partialRendered, m.status, m.submitted = "", "", "", ""
	m.tokens, m.toolCalls, m.elapsed = 0, 0, 0
	m.session = savedSession{}
}
