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

	var entries []shortcuts.Entry
	for _, entry := range shortcuts.Registry {
		for _, context := range entry.Contexts {
			if context == shortcuts.Modal && !entry.DocOnly {
				entries = append(entries, entry)
				break
			}
		}
	}

	for _, modal := range scenarios {
		for _, entry := range entries {
			for _, sequence := range entry.Sequences {
				name := fmt.Sprintf("%s/%s/%q", modal.name, entry.CommandID, sequence)
				t.Run(name, func(t *testing.T) {
					direct, invoked := modal.open(t), modal.open(t)
					available, err := invoked.model.CommandAvailable(entry.CommandID)
					wantAvailable := invoked.model.availability(entry.Availability)
					if available != wantAvailable || err != nil {
						t.Fatalf("availability=%v err=%v want=%v predicate=%q", available, err, wantAvailable, entry.Availability)
					}
					if !available {
						if _, err := invoked.model.InvokeCommand(entry.CommandID); err == nil {
							t.Fatal("unavailable binding invoked")
						}
						return
					}
					matched, ok := shortcuts.Match(sequence, shortcuts.Modal, invoked.model.availability)
					if !ok || !invoked.model.availability(matched.Availability) || matched.CommandID != entry.CommandID {
						t.Fatalf("binding resolved to %q availability=%q", matched.CommandID, matched.Availability)
					}
					direct.pressKeys(sequence)
					if _, err := invoked.model.InvokeCommand(entry.CommandID); err != nil {
						t.Fatal(err)
					}
					if got, want := modalParityState(invoked), modalParityState(direct); got != want {
						t.Fatalf("Invoke differs from Update\ninvoke: %s\ndirect: %s", got, want)
					}
				})
			}
		}
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
	return fmt.Sprintf("mode=%s kind=%s scroll=%d flash=%q quitting=%v work=%v pending=%d editor-pending=%v content=%s",
		h.model.Mode(), kind, scroll, h.model.FlashMessage(), h.model.quitting,
		work, pending, editorPending, strings.TrimSpace(h.content()))
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
