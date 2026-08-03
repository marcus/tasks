package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleKey is the whole keyboard surface of the shell packet.
//
// Ruby decodes raw bytes itself — CSI parsing, an escape-versus-alt timing
// heuristic, bracketed paste framing, a UTF-8 continuation buffer. None of that
// is ported: Bubble Tea delivers a decoded KeyMsg, and re-deriving it would be
// re-solving a solved problem in a place where a bug shows up as a corrupted
// screen. What IS ported is which key does what.
//
// Keys this packet does not own are refused OUT LOUD (see unimplemented) rather
// than silently ignored, so an unbuilt half of the TUI never looks like a
// broken one.
func (m *Model) handleKey(message tea.KeyMsg) tea.Cmd {
	if m.mode == ModeFilter {
		return m.filterKey(message)
	}
	return m.listKey(message)
}

func (m *Model) filterKey(message tea.KeyMsg) tea.Cmd {
	switch message.Type {
	case tea.KeyEsc:
		m.mode = ModeList
		m.filterInput = ""
		m.RefreshRows()
	case tea.KeyEnter:
		m.filter = strings.TrimSpace(m.filterInput)
		m.mode = ModeList
		m.filterInput = ""
		m.RefreshRows()
	case tea.KeyBackspace:
		if runes := []rune(m.filterInput); len(runes) > 0 {
			m.filterInput = string(runes[:len(runes)-1])
			m.RefreshRows()
		}
	case tea.KeyRunes, tea.KeySpace:
		m.filterInput += string(message.Runes)
		if message.Type == tea.KeySpace {
			m.filterInput += " "
		}
		m.RefreshRows()
	}
	return nil
}

func (m *Model) listKey(message tea.KeyMsg) tea.Cmd {
	switch message.Type {
	case tea.KeyUp:
		m.move(-1)
		return nil
	case tea.KeyDown:
		m.move(1)
		return nil
	case tea.KeyLeft:
		m.CollapseSelected()
		return nil
	case tea.KeyRight:
		m.ExpandSelected()
		return nil
	case tea.KeyEnter:
		m.OpenDetail()
		return nil
	case tea.KeyTab:
		m.CycleView(1)
		return nil
	case tea.KeyShiftTab:
		m.CycleView(-1)
		return nil
	case tea.KeyEsc:
		return m.dismiss()
	case tea.KeyCtrlC:
		return m.quit()
	case tea.KeyCtrlU:
		m.scrollPanel(-1, true)
		return nil
	case tea.KeyCtrlD:
		m.scrollPanel(1, true)
		return nil
	case tea.KeyCtrlK:
		m.ResizePanel(-2)
		return nil
	case tea.KeyCtrlL:
		m.ResizePanel(2)
		return nil
	}
	if message.Type != tea.KeyRunes || len(message.Runes) != 1 {
		return nil
	}
	return m.runeKey(message.Runes[0])
}

func (m *Model) runeKey(key rune) tea.Cmd {
	switch key {
	case 'j':
		m.move(1)
	case 'k':
		m.move(-1)
	case 'g':
		m.selectFirst()
	case 'G':
		m.selectLast()
	case '1', '2', '3', '4', '5', '6':
		m.SwitchView(Tabs[key-'1'].Key)
	case 'h':
		m.CollapseSelected()
	case 'l':
		m.ExpandSelected()
	case 'H':
		m.CollapseAll()
	case 'L':
		m.ExpandAll()
	case 'v':
		m.ToggleDeferred()
	case '/':
		m.mode = ModeFilter
		m.filterInput = m.filter
	case 'w':
		m.CyclePanelMode(1)
	case 'W':
		m.CyclePanelMode(-1)
	case 'r':
		m.Refresh()
		m.Flash("reloaded")
	case 'q':
		return m.quit()

	// Keys other Wave 4 packets own. Each is named so the refusal tells the
	// user which capability is missing, not merely that nothing happened.
	case 'e':
		m.unimplemented("the task editor", "editor packet")
	case 'x', 'c':
		m.unimplemented("completing a task", "editor packet")
	case 'a', 'A':
		m.unimplemented("approving a proposal", "editor packet")
	case 'd':
		m.unimplemented("deferring a task", "editor packet")
	case 'D':
		m.unimplemented("delegation", "editor packet")
	case 'p':
		m.unimplemented("the agent prompt", "agent packet")
	case '?':
		m.unimplemented("the help modal", "rendering packet")
	case '@':
		m.unimplemented("the context palette", "editor packet")
	case ':':
		m.unimplemented("the action palette", "editor packet")
	}
	return nil
}

func (m *Model) selectFirst() {
	if selectable := m.selectableIndexes(); len(selectable) > 0 {
		m.selectRow(selectable[0])
	}
}

func (m *Model) selectLast() {
	if selectable := m.selectableIndexes(); len(selectable) > 0 {
		m.selectRow(selectable[len(selectable)-1])
	}
}

func (m *Model) scrollPanel(direction int, half bool) {
	if m.panel == nil {
		return
	}
	height := m.layout().BodyHeight
	if half {
		m.panel.ScrollHalf(direction, height)
		return
	}
	m.panel.ScrollPage(direction, height)
}

// dismiss is Escape's ladder, in Ruby's order: the search filter, then the
// context filters, then the detail panel. The two rungs this build does not
// have — cancelling a running agent request, and dismissing the response pane —
// sit ABOVE these in Ruby and belong to the agent packet; they will be inserted
// at the top rather than changing the order of what is here.
//
// Escape never quits. Quitting is `q` or ctrl-c, so a reflex press cannot end
// the session with an editor open.
func (m *Model) dismiss() tea.Cmd {
	switch {
	case m.filter != "":
		m.filter = ""
		m.RefreshRows()
		m.Flash("filter cleared")
	case len(m.contextFilters) > 0:
		m.contextFilters = nil
		m.RefreshRows()
		m.Flash("context filter cleared")
	case m.panel != nil:
		m.panel = nil
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	m.Save()
	m.quitting = true
	return tea.Quit
}
