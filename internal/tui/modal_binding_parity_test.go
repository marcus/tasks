package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

func TestExportedModalBindingsMatchDirectDispatchAcrossModalFamilies(t *testing.T) {
	type scenario struct {
		name string
		open func(*testing.T) *modelHarness
	}
	plain := func(t *testing.T, kind ModalKind) *modelHarness {
		h := newModelHarness(t, harnessOptions{})
		h.model.OpenModal(ModalContent{Title: string(kind), Lines: []string{"one", "two"}}, kind, false)
		return h
	}
	project := func(t *testing.T, key string) *modelHarness {
		h := newModelHarness(t, harnessOptions{})
		h.model.SwitchView(ViewProjects)
		if !selectFirstProject(h) {
			t.Fatal("fixture has no project")
		}
		h.pressKeys(key)
		return h
	}
	deleteLeaf := func(t *testing.T) *modelHarness {
		h := newModelHarness(t, harnessOptions{})
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixPR)
		h.pressKeys("#")
		return h
	}
	deleteCascade := func(t *testing.T) *modelHarness {
		const nested = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Parent with kids"}
{"type":"task","id":"aaaa0005","parent":"aaaa0004","state":"NEXT","title":"Child one"}
`
		h := newModelHarness(t, harnessOptions{live: nested})
		h.model.SwitchView(ViewOutline)
		h.selectRowByID(fixFlight)
		h.pressKeys("#")
		return h
	}
	agentQueue := func(t *testing.T) *modelHarness {
		h := newAgentHarness(t,
			&fakeAdapter{available: true, chunks: 99, output: "running"},
			scripted("second", true))
		h.submit("one")
		h.submit("two")
		h.model.CancelQueuedAgentRequests()
		return h.modelHarness
	}
	agentActivity := func(t *testing.T) *modelHarness {
		h := newAgentHarness(t, &fakeAdapter{available: true, chunks: 99, output: "running"})
		h.submit("one")
		h.pressKeys("A")
		return h.modelHarness
	}
	agentQuit := func(t *testing.T) *modelHarness {
		h := newAgentHarness(t, &fakeAdapter{available: true, chunks: 99, output: "running"})
		h.submit("one")
		h.pressKeys("\x03")
		return h.modelHarness
	}
	draftQuit := func(t *testing.T) *modelHarness {
		h := newModelHarness(t, harnessOptions{})
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixFlight)
		h.pressKeys("\r", "e", "!", "\x03")
		return h
	}

	scenarios := []scenario{
		{name: "help", open: func(t *testing.T) *modelHarness { h := plain(t, ModalHelp); h.model.modal.filterable = true; return h }},
		{name: "archive confirm", open: func(t *testing.T) *modelHarness {
			h := newModelHarness(t, harnessOptions{})
			h.model.SwitchView(ViewNext)
			h.pressKeys("x")
			return h
		}},
		{name: "archive blocked", open: func(t *testing.T) *modelHarness {
			h := newModelHarness(t, harnessOptions{live: blockedArchiveFixture})
			h.pressKeys("x")
			return h
		}},
		{name: "delete", open: deleteLeaf},
		{name: "delete cascade", open: deleteCascade},
		{name: "project complete", open: func(t *testing.T) *modelHarness { return project(t, "c") }},
		{name: "project archive", open: func(t *testing.T) *modelHarness { return project(t, "x") }},
		{name: "unsupported schema", open: func(t *testing.T) *modelHarness { return plain(t, ModalUnsupportedSchema) }},
		{name: "task draft quit", open: draftQuit},
		{name: "agent quit", open: agentQuit},
		{name: "agent activity", open: agentActivity},
		{name: "agent queue cancel", open: agentQueue},
	}

	exportedBindings := shortcuts.ExportBindings()
	exportedCommands := shortcuts.ExportCommands()

	for _, modal := range scenarios {
		probe := modal.open(t)
		focus := probe.model.FocusContext()
		commands := map[string]bool{}
		for _, command := range exportedCommands {
			if command.Context == focus {
				commands[command.ID] = true
			}
		}
		for _, binding := range exportedBindings {
			if binding.Context != focus {
				continue
			}
			name := fmt.Sprintf("%s/%s/%s", modal.name, binding.CommandID, binding.Key)
			t.Run(name, func(t *testing.T) {
				if !commands[binding.CommandID] {
					t.Fatal("binding has no exported command metadata")
				}
				sequence := exportedKeySequence(t, binding.Key)
				direct, invoked := modal.open(t), modal.open(t)
				available, err := invoked.model.CommandAvailable(binding.CommandID)
				if err != nil {
					t.Fatalf("availability error: %v", err)
				}
				if !available {
					if _, err := invoked.model.InvokeCommand(binding.CommandID); err == nil {
						t.Fatal("unavailable projected binding invoked")
					}
					return
				}
				direct.pressKeys(sequence)
				if _, err := invoked.model.InvokeCommand(binding.CommandID); err != nil {
					t.Fatal(err)
				}
				if got, want := modalParityState(invoked), modalParityState(direct); got != want {
					t.Fatalf("Invoke differs from Update\ninvoke: %s\ndirect: %s", got, want)
				}
			})
		}
	}
}

func exportedKeySequence(t *testing.T, key string) string {
	t.Helper()
	switch key {
	case "enter":
		return "\r"
	case "esc":
		return "\x1b"
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "pgup":
		return "\x1b[5~"
	case "pgdown":
		return "\x1b[6~"
	case "ctrl+b":
		return "\x02"
	case "ctrl+c":
		return "\x03"
	case "ctrl+d":
		return "\x04"
	case "ctrl+f":
		return "\x06"
	case "ctrl+u":
		return "\x15"
	default:
		if len([]rune(key)) == 1 {
			return key
		}
		t.Fatalf("unhandled exported modal key %q", key)
		return ""
	}
}

func modalParityState(h *modelHarness) string {
	kind := ModalKind("")
	scroll := 0
	if h.model.Modal() != nil {
		kind = h.model.Modal().Kind()
		scroll = h.model.Modal().Scroll()
	}
	work, pending := false, 0
	if h.model.Queue() != nil {
		work, pending = h.model.Queue().Work(), h.model.pendingCount()
	}
	editorPending := false
	if h.model.TaskEditor() != nil {
		editorPending = h.model.TaskEditor().PendingQuit()
	}
	returnKind := ModalKind("")
	if h.model.quitReturnModal != nil {
		returnKind = h.model.quitReturnModal.Kind()
	}
	return fmt.Sprintf("mode=%s kind=%s scroll=%d flash=%q quitting=%v work=%v pending=%d editor-pending=%v return-mode=%s return-kind=%s content=%s",
		h.model.Mode(), kind, scroll, h.model.FlashMessage(), h.model.quitting,
		work, pending, editorPending, h.model.quitReturnMode, returnKind,
		strings.TrimSpace(h.content()))
}

func TestInheritedQuitBindingCannotCorruptPersistentConfirmationReturnState(t *testing.T) {
	t.Run("task draft", func(t *testing.T) {
		h := newModelHarness(t, harnessOptions{})
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixFlight)
		h.pressKeys("\r", "e", "!", "\x03")
		returnMode, returnModal := h.model.quitReturnMode, h.model.quitReturnModal
		if available, err := h.model.CommandAvailable("quit"); err != nil || available {
			t.Fatalf("quit availability=%v err=%v", available, err)
		}
		h.pressKeys("\x03")
		if h.model.quitReturnMode != returnMode || h.model.quitReturnModal != returnModal {
			t.Fatal("direct ctrl-c overwrote the retained editor return state")
		}
		if _, err := h.model.InvokeCommand("quit"); err == nil {
			t.Fatal("inherited quit invoked during draft confirmation")
		}
		if available, err := h.model.CommandAvailable("quit-confirmation-reminder"); err != nil || !available {
			t.Fatalf("reminder availability=%v err=%v", available, err)
		}
		if _, err := h.model.InvokeCommand("quit-confirmation-reminder"); err != nil {
			t.Fatal(err)
		}
		if h.model.quitReturnMode != returnMode || h.model.quitReturnModal != returnModal {
			t.Fatal("invoked ctrl-c reminder overwrote retained editor state")
		}
		h.pressKeys("n")
		if h.model.Mode() != ModeTaskEdit || h.model.TaskEditor() == nil || !h.model.TaskEditor().Dirty("title") {
			t.Fatal("cancelling did not restore the dirty task editor")
		}
	})

	t.Run("agent activity", func(t *testing.T) {
		h := newAgentHarness(t, &fakeAdapter{available: true, chunks: 99, output: "running"})
		h.submit("one")
		h.pressKeys("A")
		activity := h.model.Modal()
		h.pressKeys("\x03")
		returnMode, returnModal := h.model.quitReturnMode, h.model.quitReturnModal
		if returnModal != activity {
			t.Fatal("agent confirmation did not retain activity modal")
		}
		if available, err := h.model.CommandAvailable("quit"); err != nil || available {
			t.Fatalf("quit availability=%v err=%v", available, err)
		}
		h.pressKeys("\x03")
		if h.model.quitReturnMode != returnMode || h.model.quitReturnModal != returnModal {
			t.Fatal("direct ctrl-c overwrote retained activity state")
		}
		if _, err := h.model.InvokeCommand("quit"); err == nil {
			t.Fatal("inherited quit invoked during agent confirmation")
		}
		if available, err := h.model.CommandAvailable("quit-confirmation-reminder"); err != nil || !available {
			t.Fatalf("reminder availability=%v err=%v", available, err)
		}
		if _, err := h.model.InvokeCommand("quit-confirmation-reminder"); err != nil {
			t.Fatal(err)
		}
		if h.model.quitReturnMode != returnMode || h.model.quitReturnModal != returnModal {
			t.Fatal("invoked ctrl-c reminder overwrote retained activity state")
		}
		h.pressKeys("n")
		if h.model.Mode() != ModeModal || h.model.Modal() != activity || !h.model.Queue().Work() {
			t.Fatal("cancelling did not restore active agent activity")
		}
	})
}

func TestSpecialNoticeQuestionMarkBindingsRemainInactive(t *testing.T) {
	tests := []struct {
		name string
		open func(*testing.T) *modelHarness
	}{
		{name: "archive blocked", open: func(t *testing.T) *modelHarness {
			h := newModelHarness(t, harnessOptions{live: blockedArchiveFixture})
			h.pressKeys("x")
			return h
		}},
		{name: "unsupported schema", open: func(t *testing.T) *modelHarness {
			h := newModelHarness(t, harnessOptions{})
			h.model.ShowUnsupportedSchemaNotice()
			return h
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := test.open(t)
			before := modalParityState(h)
			h.pressKeys("?")
			if after := modalParityState(h); after != before {
				t.Fatalf("direct question mark changed modal\nbefore: %s\nafter: %s", before, after)
			}
			if available, _ := h.model.CommandAvailable("close-modal-question"); available {
				t.Fatal("question-mark close advertised")
			}
		})
	}
}

func TestModalCommandIDsNeverSpanDifferentAvailabilityPredicates(t *testing.T) {
	predicates := map[string]string{}
	for _, entry := range shortcuts.Registry {
		if entry.CommandID == "" {
			continue
		}
		if previous, found := predicates[entry.CommandID]; found && previous != entry.Availability {
			t.Fatalf("command %q spans predicates %q and %q", entry.CommandID, previous, entry.Availability)
		}
		predicates[entry.CommandID] = entry.Availability
	}
}
