package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tasks-go/internal/tui/term/agent"
)

func pasteMsg(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true}
}

func TestEscapeCancelsActiveRecordsPartialAndAdvancesQueue(t *testing.T) {
	first := &fakeAdapter{available: true, chunks: 99, output: "partial", succeeded: true}
	second := scripted("second", true)
	h := newAgentHarness(t, first, second)
	h.submit("one")
	h.submit("two")
	h.pressTypeEsc()
	if !first.cancelled || len(second.started) != 1 {
		t.Fatalf("escape cancelled=%v second starts=%v", first.cancelled, second.started)
	}
	if got := h.model.FlashMessage(); got != "cancelled agent request #1" {
		t.Fatalf("escape said %q", got)
	}
	if !h.model.respOpen || !strings.Contains(strings.Join(h.model.resp, "\n"), "partial") {
		t.Fatal("the cancelled request's partial response was not retained")
	}
	// With no active request left, Escape dismisses the response before it
	// touches the filter/context/panel ladder.
	h.model.Queue().CancelActive()
	h.pressTypeEsc()
	if h.model.respOpen {
		t.Fatal("second escape did not dismiss the response")
	}
}

func TestAgentQuitConfirmationIsPersistentAndRestoresTheOverlay(t *testing.T) {
	running := &fakeAdapter{available: true, chunks: 99, output: "working"}
	h := newAgentHarness(t, running)
	h.submit("one")
	h.pressKeys("A")
	underlying := h.model.Modal()
	h.pressKeys("\x03")
	if h.model.Modal() == nil || h.model.Modal().Kind() != ModalAgentQuitConfirm {
		t.Fatalf("q opened %v", h.model.Modal())
	}
	h.pressKeys("q")
	if h.model.quitting || h.model.Modal().Kind() != ModalAgentQuitConfirm {
		t.Fatal("repeated q confirmed the quit")
	}
	h.pressTypeEsc()
	if h.model.Modal() != underlying || h.model.Mode() != ModeModal {
		t.Fatal("declining did not restore the activity overlay")
	}
	h.pressKeys("\x03", "y")
	if !h.model.quitting || !running.cancelled {
		t.Fatalf("confirmed quit: quitting=%v cancelled=%v", h.model.quitting, running.cancelled)
	}
}

func TestDirtyDraftQuitUsesPersistentModalAndRestoresEditor(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.model.SwitchView(ViewNext)
	h.selectRowByID(fixFlight)
	h.pressKeys("\r", "e", "!")
	h.pressKeys("\x03")
	if h.model.Modal() == nil || h.model.Modal().Kind() != ModalTaskDraftQuitConfirm {
		t.Fatalf("dirty q opened %v", h.model.Modal())
	}
	h.pressKeys("q")
	if h.model.quitting {
		t.Fatal("repeated q discarded the draft")
	}
	h.pressKeys("n")
	if h.model.Mode() != ModeTaskEdit || h.model.TaskEditor() == nil ||
		!h.model.TaskEditor().Dirty("title") {
		t.Fatal("n did not restore the dirty editor")
	}
	h.pressKeys("\x03", "\r")
	if !h.model.quitting {
		t.Fatal("return did not explicitly confirm discard and quit")
	}
}

func TestBubbleTeaPasteRoutesByModeAndNeverRunsShortcuts(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.model.Update(pasteMsg("q\nnext\tvalue"))
	if h.model.Mode() != ModePrompt || h.model.PromptText() != "q next value" || h.model.quitting {
		t.Fatalf("list paste mode=%s text=%q quitting=%v", h.model.Mode(), h.model.PromptText(), h.model.quitting)
	}
	h.pressTypeEsc()
	h.selectRowByID(fixFlight)
	h.pressKeys("d")
	h.model.Update(pasteMsg("2026-08-09\n"))
	if got := h.model.Form().Text(); got != "2026-08-09 " {
		t.Fatalf("form paste = %q", got)
	}
	h.pressTypeEsc()
	h.pressKeys(":")
	h.model.Update(pasteMsg("archive\n"))
	if h.model.ActionPalette().Picker().Input() != "archive " {
		t.Fatalf("palette paste = %q", h.model.ActionPalette().Picker().Input())
	}
}

func TestAvailabilityRequiresADateAndAnExtractedLink(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.model.SwitchView(ViewNext)
	h.selectRowByID(fixPR) // no date, no link
	if h.model.availability("recurrence_action_available?") ||
		h.model.availability("link_action_available?") {
		t.Fatal("undated/unlinked actions were available")
	}
	h.pressKeys("r")
	if h.model.Mode() != ModeList || !strings.Contains(h.model.FlashMessage(), "recurrence needs a date") {
		t.Fatalf("unavailable recurrence feedback: %s %q", h.model.Mode(), h.model.FlashMessage())
	}
	h.pressKeys("o")
	if h.model.Mode() != ModeList || h.model.FlashMessage() != "no links on this task" {
		t.Fatalf("unavailable link feedback: %s %q", h.model.Mode(), h.model.FlashMessage())
	}
	h.model.OpenActionPalette()
	for _, entry := range h.model.ActionPalette().entries {
		if entry.Handler == "open_recur_popup" || entry.Handler == "open_link" {
			t.Fatalf("palette included unavailable %s", entry.Handler)
		}
	}
}

func TestAdvanceQueueSkipsEveryImmediateFailure(t *testing.T) {
	failedOne := &fakeAdapter{available: true, startErr: "one"}
	failedTwo := &fakeAdapter{available: false}
	started := scripted("ok", true)
	index := 0
	adapters := []*fakeAdapter{failedOne, failedTwo, started}
	queue, err := agent.NewQueue(agent.Options{
		Factory: func(agent.Entry) (agent.Adapter, error) {
			adapter := adapters[index]
			index++
			return adapter, nil
		},
		Availability: func(agent.Entry) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := AgentEntry{ProviderName: "fake", ModelName: "one", Label: "fake:one"}
	for _, prompt := range []string{"one", "two", "three"} {
		if submission := queue.Enqueue(prompt, entry); !submission.Accepted() {
			t.Fatal(submission.Error)
		}
	}
	h := newModelHarness(t, harnessOptions{entries: []AgentEntry{entry}, queue: queue})
	event := h.model.advanceQueue()
	if event.Type != agent.Started || event.Request.ID != 3 || len(started.started) != 1 {
		t.Fatalf("advance stopped at %+v; starts=%v", event, started.started)
	}
	requests := queue.Requests()
	if requests[0].Status != agent.Failed || requests[1].Status != agent.Failed {
		t.Fatalf("failed starts were not recorded: %+v", requests)
	}
}

func TestRecurrencePreviewMatchesAllRubyShapesAndShedsWholeDates(t *testing.T) {
	h := newModelHarness(t, harnessOptions{now: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)})
	if got := h.model.recurPreview("every mon wed", "2026-08-01", 120); got !=
		"every Mon, Wed → 2026-08-03 Mon · 2026-08-05 Wed · 2026-08-10 Mon" {
		t.Fatalf("projection = %q", got)
	}
	if got := h.model.recurPreview("off", "2026-08-01", 120); got != "no recurrence" {
		t.Fatalf("off = %q", got)
	}
	if got := h.model.recurPreview("", "2026-08-01", 120); got != recurPopupHint {
		t.Fatalf("empty = %q", got)
	}
	if got := h.model.recurPreview("bananas", "2026-08-01", 120); got != `unrecognized schedule: "bananas"` {
		t.Fatalf("bad grammar = %q", got)
	}
	for _, width := range []int{76, 60, 52, 40, 30} {
		if got := h.model.recurPreview("every mon wed", "2026-08-01", width); strings.Contains(got, "…") {
			t.Fatalf("width %d clipped a date: %q", width, got)
		}
	}
	if got := h.model.recurPreview("2y:02:5fri", "2027-08-01", 200); !strings.Contains(got, "2y:02:5fri — every 2 years on the 5th Friday of February —") {
		t.Fatalf("never-fire shape = %q", got)
	}
}

func TestProposalDecisionUsesTheHeldRevision(t *testing.T) {
	fixture := strings.Replace(fixtureStore, `"state":"NEXT","priority":"A","title":"Book flight`,
		`"state":"PROPOSED","priority":"A","title":"Book flight`, 1)
	h := newModelHarness(t, harnessOptions{live: fixture})
	h.model.SwitchView(ViewInbox)
	h.selectRowByID(fixFlight)
	external := strings.Replace(fixture, "Book flight in Concur", "Changed outside", 1)
	h.rewrite(external)
	h.pressKeys("a")
	if got := h.content(); got != external {
		t.Fatal("a stale proposal decision wrote over the external edit")
	}
	if !strings.Contains(h.model.FlashMessage(), "changed underneath") {
		t.Fatalf("stale decision said %q", h.model.FlashMessage())
	}
}

func TestRefreshDetachesVanishedFormAndPaletteTargets(t *testing.T) {
	withoutFlight := strings.Replace(fixtureStore,
		`{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02"}`+"\n", "", 1)
	for _, open := range []func(*modelHarness){
		func(h *modelHarness) { h.pressKeys("d") },
		func(h *modelHarness) { h.pressKeys(":") },
	} {
		h := newModelHarness(t, harnessOptions{})
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixFlight)
		open(h)
		h.rewrite(withoutFlight)
		h.model.Refresh()
		if h.model.Mode() != ModeList || h.model.Form() != nil || h.model.ActionPalette() != nil {
			t.Fatalf("vanished target left mode=%s form=%v palette=%v",
				h.model.Mode(), h.model.Form(), h.model.ActionPalette())
		}
	}
}

func TestEditSnapshotValuesAndBaselinesComeFromOneHeldRead(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	held := h.model.ReadModel()
	h.rewrite(strings.Replace(fixtureStore, "Book flight in Concur", "Fresh title", 1))
	snapshot, found := NewEditSnapshot(h.model.app, held, fixFlight)
	if !found || snapshot.Title != "Book flight in Concur" ||
		snapshot.ExpectedFor("title") != "Book flight in Concur" {
		t.Fatalf("mixed snapshot: found=%v title=%q baseline=%q", found,
			snapshot.Title, snapshot.ExpectedFor("title"))
	}
}

func TestMouseBlursPromptFocusesFooterAndScrollsResponse(t *testing.T) {
	h := newAgentHarness(t)
	h.model.paths.Mouse = true
	h.model.Update(pasteMsg("draft"))
	layout := h.model.Layout()
	begin, _ := layout.BodyRows()
	h.model.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 4, Y: begin})
	if h.model.Mode() != ModeList || h.model.PromptText() != "draft" {
		t.Fatal("list click did not blur while retaining the prompt draft")
	}
	layout = h.model.Layout()
	footerStart, _ := layout.FooterRows()
	promptRow := footerStart
	for i, line := range layout.Footer {
		if strings.Contains(line, "❯") {
			promptRow = footerStart + i
		}
	}
	h.model.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 4, Y: promptRow})
	if h.model.Mode() != ModePrompt {
		t.Fatal("footer click did not focus the prompt")
	}
	h.model.respOpen = true
	h.model.resp = strings.Split(strings.Repeat("line\n", 30), "\n")
	h.model.mode = ModeList
	h.model.height = 60
	layout = h.model.Layout()
	footerStart, _ = layout.FooterRows()
	if role := h.model.footerRole(layout, footerStart); role != "response" {
		t.Fatalf("first footer row classified as %q: %v", role, layout.Footer)
	}
	h.model.Update(tea.MouseMsg{Type: tea.MouseWheelDown, X: 4, Y: footerStart})
	if h.model.respScroll == 0 {
		t.Fatal("wheel over response moved the list instead of the response")
	}
}

func TestResizeSuspendsAndEResumesTheSameDraft(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.model.SwitchView(ViewNext)
	h.selectRowByID(fixFlight)
	h.pressKeys("\r", "e", "!")
	h.model.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	if h.model.Mode() != ModeList || h.model.suspendedTaskEditor == nil ||
		h.model.panel == nil || h.model.panel.Kind != PanelSuspendedTaskEdit {
		t.Fatalf("resize left mode=%s suspended=%v panel=%v", h.model.Mode(),
			h.model.suspendedTaskEditor, h.model.panel)
	}
	h.model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.pressKeys("e")
	if h.model.Mode() != ModeTaskEdit || h.model.TaskEditor() == nil ||
		!h.model.TaskEditor().Dirty("title") {
		t.Fatal("e did not resume the retained draft")
	}
}

func TestMissingSuspendedDraftCannotRetargetTheReplacementSelection(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.model.SwitchView(ViewNext)
	h.selectRowByID(fixFlight)
	h.pressKeys("\r", "e", "!")
	h.model.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	h.rewrite(strings.Replace(fixtureStore,
		`{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02"}`+"\n", "", 1))
	h.model.Refresh()
	h.model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.pressKeys("e")
	if h.model.TaskEditor() != nil || h.model.suspendedTaskEditor == nil ||
		!h.model.suspendedTaskEditor.Missing() {
		t.Fatal("e retargeted a missing suspended draft onto the fallback selection")
	}
	if !strings.Contains(h.model.FlashMessage(), "Task no longer exists") {
		t.Fatalf("missing draft said %q", h.model.FlashMessage())
	}
}

func TestActivityUsesTheQueueClockDomain(t *testing.T) {
	adapter := &fakeAdapter{available: true, chunks: 99, output: "working"}
	h := newAgentHarness(t, adapter)
	h.clock = 17
	h.submit("go")
	content := strings.Join(h.model.agentActivityContent(100).Lines, "\n")
	if !strings.Contains(content, "· 0s") {
		t.Fatalf("a just-started request rendered an absurd elapsed time:\n%s", content)
	}
}

func TestRefreshContextPaletteAdoptsNewOptions(t *testing.T) {
	h := newModelHarness(t, harnessOptions{})
	h.pressKeys("@")
	updated := strings.Replace(fixtureStore, `"tags":["@home"]`, `"tags":["@new"]`, 1)
	h.rewrite(updated)
	h.model.Refresh()
	if h.model.Mode() != ModeContextPalette {
		t.Fatalf("refresh closed context palette: %s", h.model.Mode())
	}
	labels := []string{}
	for _, option := range h.model.ContextPalette().Picker().Options() {
		labels = append(labels, option.Label)
	}
	joined := strings.Join(labels, " ")
	if !strings.Contains(joined, "@new") || strings.Contains(joined, "@home") {
		t.Fatalf("options were not reconciled: %q", joined)
	}
}
