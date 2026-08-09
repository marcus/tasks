package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// CommandAvailable reports whether a command exists in the current focus
// context and its live availability predicate passes.
func (m *Model) CommandAvailable(id string) (bool, error) {
	entry, ok := m.commandEntry(id)
	if !ok {
		return false, fmt.Errorf("command %q is not active in %s", id, m.FocusContext())
	}
	return m.commandEntryAvailable(entry), nil
}

// InvokeCommand executes one exact semantic command without replaying an
// ambiguous default key.
func (m *Model) InvokeCommand(id string) (tea.Cmd, error) {
	entry, ok := m.commandEntry(id)
	if !ok {
		return nil, fmt.Errorf("command %q is not active in %s", id, m.FocusContext())
	}
	if !m.commandEntryAvailable(entry) {
		return nil, fmt.Errorf("command %q is unavailable", id)
	}
	key := ""
	if len(entry.Sequences) > 0 {
		key = entry.Sequences[0]
	}
	if handler, ok := m.handlers()[entry.Handler]; ok {
		handler(key)
		return m.maybeQuit(), nil
	}
	return nil, fmt.Errorf("command %q has no handler", id)
}

func (m *Model) commandEntryAvailable(entry shortcuts.Entry) bool {
	if !m.availability(entry.Availability) {
		return false
	}
	if entry.Palette.Predicate != "" && !m.availability(entry.Palette.Predicate) {
		return false
	}
	return true
}

func (m *Model) commandEntry(id string) (shortcuts.Entry, bool) {
	for _, entry := range shortcuts.EntriesForHostContext(m.FocusContext()) {
		if entry.CommandID == id && !entry.DocOnly {
			return entry, true
		}
	}
	return shortcuts.Entry{}, false
}
