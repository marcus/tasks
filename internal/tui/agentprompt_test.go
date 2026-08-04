package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/agent"
)

// Every test here uses a FAKE adapter. No test may invoke a real provider, and
// nothing in this file spawns a process.

// fakeAdapter is one scripted agent run.
type fakeAdapter struct {
	available bool
	startErr  string
	output    string
	// chunks is how many Pump calls it takes to finish, so a test can observe a
	// request while it is still running.
	chunks    int
	pumped    int
	succeeded bool
	cancelled bool
	started   []string
}

func (f *fakeAdapter) Available() bool { return f.available }

func (f *fakeAdapter) Start(prompt, model string) error {
	f.started = append(f.started, prompt+"|"+model)
	if f.startErr != "" {
		return &fakeError{f.startErr}
	}
	return nil
}

func (f *fakeAdapter) Pump() (bool, error) {
	f.pumped++
	return f.pumped >= f.chunks, nil
}

func (f *fakeAdapter) Cancel() error  { f.cancelled = true; return nil }
func (f *fakeAdapter) Output() string { return f.output }
func (f *fakeAdapter) Success() bool  { return f.succeeded }
func (f *fakeAdapter) ExitStatus() (int, bool) {
	if f.succeeded {
		return 0, true
	}
	return 1, true
}
func (f *fakeAdapter) ProcessStatus() agent.ProcessStatus {
	return agent.ProcessStatus{Present: true, Exited: true}
}

type fakeError struct{ text string }

func (e *fakeError) Error() string { return e.text }

// agentHarness is a model with a queue over scripted adapters.
type agentHarness struct {
	*modelHarness
	adapters []*fakeAdapter
	clock    float64
}

func newAgentHarness(t *testing.T, adapters ...*fakeAdapter) *agentHarness {
	t.Helper()
	harness := &agentHarness{adapters: adapters}
	index := 0
	queue, err := agent.NewQueue(agent.Options{
		Factory: func(agent.Entry) (agent.Adapter, error) {
			if index >= len(harness.adapters) {
				return nil, &fakeError{"no more scripted adapters"}
			}
			adapter := harness.adapters[index]
			index++
			return adapter, nil
		},
		Availability: func(agent.Entry) bool {
			return index >= len(harness.adapters) || harness.adapters[index].available
		},
		Clock:      func() float64 { return harness.clock },
		MaxPending: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.modelHarness = newModelHarness(t, harnessOptions{
		entries: []AgentEntry{
			{ProviderName: "claude", ModelName: "opus", Label: "claude:opus"},
			{ProviderName: "claude", ModelName: "haiku", Label: "claude:haiku"},
		},
		queue: queue,
	})
	return harness
}

func scripted(output string, succeeded bool) *fakeAdapter {
	return &fakeAdapter{available: true, output: output, chunks: 1, succeeded: succeeded}
}

// -- the prompt --------------------------------------------------------------------

func TestTabOpensThePromptAndTabLeavesIt(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.pressKeys("\t")
	if harness.model.Mode() != ModePrompt {
		t.Fatalf("tab produced mode %s", harness.model.Mode())
	}
	harness.pressKeys("\t")
	if harness.model.Mode() != ModeList {
		t.Errorf("tab did not leave the prompt: %s", harness.model.Mode())
	}
}

func TestPromptAcceptsTypingPasteAndUnicode(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.pressKeys("\t")
	for _, key := range strings.Split("界a", "") {
		harness.pressKeys(key)
	}
	harness.model.PromptPaste("🙂 pasted")
	if got := harness.model.PromptText(); got != "界a🙂 pasted" {
		t.Errorf("the prompt holds %q", got)
	}
}

// A bracketed paste from the LIST opens the prompt rather than being dropped.
func TestPasteFromTheListOpensThePrompt(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.model.PromptPaste("from the list")
	if harness.model.Mode() != ModePrompt {
		t.Fatalf("a paste produced mode %s", harness.model.Mode())
	}
	if harness.model.PromptText() != "from the list" {
		t.Errorf("the paste did not land: %q", harness.model.PromptText())
	}
}

func TestPasteFromAModalClosesItAndOpensThePrompt(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.pressKeys("?")
	harness.model.PromptPaste("from a modal")
	if harness.model.Mode() != ModePrompt || harness.model.Modal() != nil {
		t.Errorf("a paste from a modal left mode %s with modal %v",
			harness.model.Mode(), harness.model.Modal())
	}
}

// `p` quotes the reference, because a bare id in a sentence is ambiguous to a
// model and to a human reading the transcript later.
func TestPasteRefQuotesTheSelectedReference(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("p")
	if harness.model.Mode() != ModePrompt {
		t.Fatalf("p produced mode %s", harness.model.Mode())
	}
	if got := harness.model.PromptText(); got != `"`+fixFlight+`" ` {
		t.Errorf("the reference reads %q", got)
	}
	// A second paste separates itself from the first.
	harness.selectRowByID(fixPR)
	harness.model.PasteRef()
	if got := harness.model.PromptText(); !strings.Contains(got, `" "`) {
		t.Errorf("two references ran together: %q", got)
	}
}

// The prompt wraps by CELLS. A CJK paste is two cells per character, and
// folding by rune count would run the line off the frame.
func TestPromptWrapsByCellsAndDrawsTheCaret(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.pressKeys("\t")
	harness.model.PromptPaste(strings.Repeat("界", 12))
	lines := harness.model.wrappedPrompt(10)
	if len(lines) < 3 {
		t.Fatalf("24 cells wrapped into %d lines of 10: %v", len(lines), lines)
	}
	for _, line := range lines {
		if got := harness.model.styler.Width(line); got > 10 {
			t.Errorf("a wrapped line is %d cells: %q", got, line)
		}
	}
}

// -- the queue ----------------------------------------------------------------------

func TestSubmitStartsARequestAndReportsIt(t *testing.T) {
	adapter := scripted("all done", true)
	harness := newAgentHarness(t, adapter)
	harness.pressKeys("\t")
	for _, key := range strings.Split("do a thing", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if harness.model.Mode() != ModeList {
		t.Fatalf("submitting left the mode at %s", harness.model.Mode())
	}
	if harness.model.PromptText() != "" {
		t.Errorf("an accepted submit kept the text: %q", harness.model.PromptText())
	}
	if !strings.Contains(harness.model.FlashMessage(), "starting agent request #1") {
		t.Errorf("the start was not reported: %q", harness.model.FlashMessage())
	}
	if len(adapter.started) != 1 || !strings.HasPrefix(adapter.started[0], "do a thing|") {
		t.Errorf("the adapter received %v", adapter.started)
	}
}

// A REFUSED submit keeps the text. The prompt is the one place a user may have
// typed a paragraph, and losing it would be the worst loss the TUI can inflict.
func TestAFullQueueRefusesAndKeepsTheTypedPrompt(t *testing.T) {
	harness := newAgentHarness(t,
		&fakeAdapter{available: true, chunks: 99, output: "running"},
		scripted("second", true), scripted("third", true))
	for _, prompt := range []string{"one", "two", "three"} {
		harness.pressKeys("\t")
		for _, key := range strings.Split(prompt, "") {
			harness.pressKeys(key)
		}
		harness.pressKeys("\r")
	}
	harness.pressKeys("\t")
	for _, key := range strings.Split("overflow", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")

	if harness.model.Mode() != ModePrompt {
		t.Fatalf("a refused submit left the mode at %s", harness.model.Mode())
	}
	if harness.model.PromptText() != "overflow" {
		t.Errorf("the refused submit lost the text: %q", harness.model.PromptText())
	}
	if harness.model.FlashMessage() == "" {
		t.Error("the refusal said nothing")
	}
}

// The queue is FIFO and serial: one runs, the rest wait in order.
func TestTheQueueIsSerialAndFIFO(t *testing.T) {
	first := &fakeAdapter{available: true, chunks: 2, output: "first", succeeded: true}
	second := scripted("second", true)
	harness := newAgentHarness(t, first, second)

	harness.submit("one")
	harness.submit("two")
	if !strings.Contains(harness.model.FlashMessage(), "queued agent request #2 · 1 waiting") {
		t.Errorf("the second submit said %q", harness.model.FlashMessage())
	}
	if len(second.started) != 0 {
		t.Error("the second request started while the first was running")
	}

	harness.tick() // finishes the first, starts the second
	harness.tick()
	if len(second.started) != 1 {
		t.Errorf("the second request never started: %v", second.started)
	}
}

func TestAFinishedRequestOpensTheResponsePane(t *testing.T) {
	harness := newAgentHarness(t, scripted("the agent said this", true))
	harness.submit("go")
	harness.tick()
	if !harness.model.respOpen {
		t.Fatal("the response pane did not open")
	}
	joined := strings.Join(harness.model.ResponseLines(), "\n")
	if !strings.Contains(joined, "the agent said this") {
		t.Errorf("the pane shows %q", joined)
	}
}

func TestAnEmptyResultSaysSoRatherThanShowingNothing(t *testing.T) {
	harness := newAgentHarness(t, scripted("   ", true))
	harness.submit("go")
	harness.tick()
	if !strings.Contains(strings.Join(harness.model.ResponseLines(), ""), "(no output)") {
		t.Errorf("an empty result rendered %v", harness.model.ResponseLines())
	}
}

func TestAFailedRequestShowsItsError(t *testing.T) {
	adapter := scripted("partial", false)
	harness := newAgentHarness(t, adapter)
	harness.submit("go")
	harness.tick()
	joined := strings.Join(harness.model.resp, "\n")
	if !strings.Contains(joined, "partial") {
		t.Errorf("the failed transcript is missing: %q", joined)
	}
}

func TestTheResponsePaneScrolls(t *testing.T) {
	long := strings.Repeat("a line of output\n", 40)
	harness := newAgentHarness(t, scripted(long, true))
	harness.submit("go")
	harness.tick()
	if len(harness.model.resp) <= respMax {
		t.Skip("the fixture did not wrap past one page")
	}
	harness.pressKeys("\x1b[6~") // pgdn
	if harness.model.respScroll == 0 {
		t.Error("pgdn did not scroll the pane")
	}
	harness.pressKeys("\x1b[5~") // pgup
	if harness.model.respScroll != 0 {
		t.Errorf("pgup left the pane at %d", harness.model.respScroll)
	}
}

// -- the activity modal ---------------------------------------------------------------

func TestActivityModalListsRequestsAndFilters(t *testing.T) {
	harness := newAgentHarness(t, scripted("first result", true))
	harness.submit("first prompt")
	harness.tick()

	harness.pressKeys("A")
	if harness.model.Mode() != ModeModal ||
		harness.model.Modal().Kind() != ModalAgentActivity {
		t.Fatalf("A produced mode %s (%q)", harness.model.Mode(), harness.model.FlashMessage())
	}
	joined := strings.Join(harness.model.Modal().AllLines(), "\n")
	if !strings.Contains(joined, "first prompt") {
		t.Errorf("the activity modal does not list the request: %q", joined)
	}
	// It is filterable, and a filter keeps the matched request's whole block.
	harness.pressKeys("f")
	if harness.model.Mode() != ModeModalFilter {
		t.Errorf("typing in the activity modal produced %s", harness.model.Mode())
	}
}

// With no requests the key is UNAVAILABLE, so the registry consumes it in
// silence — the same narrow rule Ruby applies to every non-ordering,
// non-delegation refusal. Calling the handler directly still explains itself,
// which is what the palette route reaches.
func TestActivityIsRefusedWithNoRequests(t *testing.T) {
	harness := newAgentHarness(t)
	harness.pressKeys("A")
	if harness.model.Mode() == ModeModal {
		t.Fatal("A opened an empty activity modal")
	}
	if got := harness.model.FlashMessage(); got != "" {
		t.Errorf("A said %q; an unavailable key is consumed silently", got)
	}
	harness.model.OpenAgentActivity()
	if !strings.Contains(harness.model.FlashMessage(), "no agent requests") {
		t.Errorf("the handler said %q", harness.model.FlashMessage())
	}
}

// An OPEN activity modal stays live as requests progress, without losing the
// reader's filter or scroll position.
func TestActivityModalRefreshesInPlace(t *testing.T) {
	running := &fakeAdapter{available: true, chunks: 3, output: "working", succeeded: true}
	harness := newAgentHarness(t, running)
	harness.submit("go")
	harness.pressKeys("A")
	harness.model.Modal().SetFilter("go")
	scroll := harness.model.Modal().Scroll()
	harness.tick()
	if harness.model.Modal() == nil || harness.model.Modal().Kind() != ModalAgentActivity {
		t.Fatal("the modal closed on a refresh")
	}
	if harness.model.Modal().Filter() != "go" {
		t.Errorf("the refresh dropped the filter: %q", harness.model.Modal().Filter())
	}
	if harness.model.Modal().Scroll() != scroll {
		t.Error("the refresh moved the reader's scroll position")
	}
}

func TestCancellingQueuedRequestsConfirmsFirst(t *testing.T) {
	harness := newAgentHarness(t,
		&fakeAdapter{available: true, chunks: 99, output: "running"},
		scripted("second", true))
	harness.submit("one")
	harness.submit("two")

	harness.model.CancelQueuedAgentRequests()
	if harness.model.Modal() == nil ||
		harness.model.Modal().Kind() != ModalAgentQueueCancel {
		t.Fatalf("cancellation produced %v", harness.model.Modal())
	}
	if got := harness.model.Modal().Title(); got != "Cancel queued agent requests?" {
		t.Fatalf("confirmation title = %q", got)
	}
	wantLines := []string{
		"Discard 1 waiting request?",
		"The active request will keep running.",
		"Press y to discard waiting work · n / esc cancels",
	}
	if got := harness.model.Modal().AllLines(); strings.Join(got, "\n") != strings.Join(wantLines, "\n") {
		t.Fatalf("confirmation lines = %#v, want %#v", got, wantLines)
	}
	harness.pressKeys("n")
	if harness.model.pendingCount() != 1 {
		t.Error("declining cancelled the queue anyway")
	}
	if got := harness.model.FlashMessage(); got != "queued requests kept" {
		t.Fatalf("declining said %q", got)
	}

	harness.model.CancelQueuedAgentRequests()
	harness.pressKeys("y")
	if harness.model.pendingCount() != 0 {
		t.Error("confirming did not cancel the queued request")
	}
	if _, running := harness.model.Queue().ActiveRequest(); !running {
		t.Error("cancelling the queue also stopped the running request")
	}
	if !strings.Contains(harness.model.FlashMessage(), "cancelled 1 queued agent request") {
		t.Errorf("cancelling said %q", harness.model.FlashMessage())
	}
}

// -- model cycling -----------------------------------------------------------------

func TestModelCyclingWrapsAndSaysWhenItApplies(t *testing.T) {
	harness := newAgentHarness(t, &fakeAdapter{available: true, chunks: 99, output: "x"})
	first := harness.model.CurrentEntry().UILabel()
	harness.pressKeys("M")
	if harness.model.CurrentEntry().UILabel() == first {
		t.Fatal("M did not cycle the entry")
	}
	if !strings.Contains(harness.model.FlashMessage(), "agent: claude:haiku") {
		t.Errorf("M said %q", harness.model.FlashMessage())
	}
	harness.pressKeys("M")
	if harness.model.CurrentEntry().UILabel() != first {
		t.Error("M did not wrap back")
	}

	// A running request keeps the entry it was submitted with, so the message
	// has to distinguish a setting from a retroactive change.
	harness.submit("go")
	harness.pressKeys("M")
	if !strings.Contains(harness.model.FlashMessage(), "applies to new requests") {
		t.Errorf("M during a run said %q", harness.model.FlashMessage())
	}
}

// -- the footer -------------------------------------------------------------------

func TestTheFooterShowsARunningRequestAndItsTranscript(t *testing.T) {
	harness := newAgentHarness(t,
		&fakeAdapter{available: true, chunks: 99, output: "line one\nline two"})
	harness.submit("go")
	footer := strings.Join(harness.model.Footer(), "\n")
	if !strings.Contains(footer, "is working") {
		t.Errorf("the footer does not show the running request:\n%s", footer)
	}
	if !strings.Contains(footer, "line two") {
		t.Errorf("the footer does not show the live transcript:\n%s", footer)
	}
}

func TestTheFooterAdvertisesThePromptKey(t *testing.T) {
	harness := newAgentHarness(t)
	if !strings.Contains(strings.Join(harness.model.Footer(), "\n"), "tab to ask the agent") {
		t.Errorf("the footer does not name the prompt key: %v", harness.model.Footer())
	}
}

// submit types a prompt and sends it.
func (h *agentHarness) submit(prompt string) {
	h.t.Helper()
	h.pressKeys("\t")
	for _, key := range strings.Split(prompt, "") {
		h.pressKeys(key)
	}
	h.pressKeys("\r")
}
