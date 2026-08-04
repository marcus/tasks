package tui

import (
	"fmt"
	"strings"

	"time"

	"github.com/marcus/tasks/internal/tui/term/agent"
	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/input"
	"github.com/marcus/tasks/internal/tui/term/theme"
)

// The agent prompt surface: the prompt line, the serial request queue, the
// activity modal, the response pane and model cycling.
//
// The TUI does NOT mutate tasks on the agent's behalf. It hands the prompt to a
// provider adapter that runs the CLI, and the CLI performs whatever the model
// decided through the same commands a human would use. That is the whole
// architecture: a privileged TUI mutation path would be a second, unaudited way
// to change the store.

// respMax is how many response lines the pane shows at once.
const respMax = 12

// AgentEntry is one provider/model the prompt can be submitted to. It is the
// queue's Entry with a display label the header shows.
type AgentEntry = agent.SimpleEntry

// agentQueue is the queue type the model holds, aliased so model.go does not
// have to import the primitive package for one field.
type agentQueue = agent.Queue

// promptEditor is the prompt buffer, created lazily.
func (m *Model) promptEditor() *input.Editor {
	if m.promptInput == nil {
		m.promptInput = input.New("", input.Options{})
	}
	return m.promptInput
}

// PromptText is what the user has typed.
func (m *Model) PromptText() string { return m.promptEditor().Text() }

// CurrentEntry is the provider/model new requests go to.
func (m *Model) CurrentEntry() AgentEntry {
	if len(m.entries) == 0 {
		return AgentEntry{}
	}
	return m.entries[m.entryIndex%len(m.entries)]
}

// Queue is the request queue, or nil when the model was built without one.
func (m *Model) Queue() *agent.Queue { return m.queue }

// LinkOpener is the injected launcher, or nil. It is exported so the entry
// point's own test can prove the shipping constructor wired one — the defect
// that made every valid link refuse in the real binary while the fake-injected
// model tests stayed green.
func (m *Model) LinkOpener() Opener { return m.opener }

// AgentEntries is the ordered provider/model list, for the same reason.
func (m *Model) AgentEntries() []AgentEntry { return append([]AgentEntry{}, m.entries...) }

// FocusPrompt is `tab`.
func (m *Model) FocusPrompt() { _ = m.SetMode(ModePrompt) }

// PasteRef is `p`: quote the selected task's stable reference into the prompt.
//
// It is QUOTED because a bare id in a sentence is ambiguous to a model and to a
// human reading the transcript later; the quotes say "this is a handle".
func (m *Model) PasteRef() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if m.modal != nil {
		m.CloseModal()
	}
	editor := m.promptEditor()
	if text := editor.Text(); text != "" && !strings.HasSuffix(text, " ") {
		editor.Insert(" ")
	}
	editor.Insert("\"" + ExportReference(*item) + "\" ")
	_ = m.SetMode(ModePrompt)
}

// promptKey is the prompt's own key table.
func (m *Model) promptKey(sequence string) {
	switch sequence {
	case "\x1b", "\t":
		// Tab LEAVES the prompt as well as entering it, so the key that opened
		// it also closes it.
		m.mode = ModeList
	case "\r", "\n":
		m.SubmitPrompt()
	default:
		m.promptEditor().HandleKey(sequence)
	}
}

// PromptPaste inserts bracketed-pasted text, from the prompt or from a modal.
func (m *Model) PromptPaste(text string) {
	if m.mode != ModePrompt {
		if m.modal != nil {
			m.CloseModal()
		}
		_ = m.SetMode(ModePrompt)
	}
	m.promptEditor().Insert(text)
}

// SubmitPrompt enqueues the typed prompt.
//
// A REFUSED submission keeps the text. The prompt is the one place in the TUI
// where the user may have typed a paragraph, and losing it to a full queue or an
// unavailable provider would be the worst data loss the TUI can inflict.
func (m *Model) SubmitPrompt() {
	text := strings.TrimSpace(m.PromptText())
	if text == "" {
		return
	}
	if m.queue == nil {
		m.Flash("no agent is configured")
		return
	}
	submission := m.queue.Enqueue(text, m.CurrentEntry())
	if !submission.Accepted() {
		m.mode = ModePrompt
		m.Flash(submission.Error)
		return
	}

	m.promptEditor().Clear()
	m.mode = ModeList
	m.respOpen = false
	_, wasActive := m.queue.ActiveRequest()
	var started agent.Event
	if !wasActive {
		started = m.advanceQueue()
	}
	id := submission.Request.ID
	switch {
	case wasActive:
		m.Flash(fmt.Sprintf("queued agent request #%d · %d waiting", id, m.pendingCount()))
	case started.Type == agent.Started:
		m.Flash(fmt.Sprintf("starting agent request #%d", id))
	default:
		m.Flash(fmt.Sprintf("agent request #%d failed to start", id))
	}
}

func (m *Model) pendingCount() int {
	if m.queue == nil {
		return 0
	}
	total := 0
	for _, request := range m.queue.Requests() {
		if request.Status == agent.Queued {
			total++
		}
	}
	return total
}

// advanceQueue starts the next request and records a failure to start.
func (m *Model) advanceQueue() agent.Event {
	if m.queue == nil {
		return agent.Event{}
	}
	for {
		event := m.queue.StartNext()
		if !event.Occurred() || event.Type == agent.Started {
			return event
		}
		m.recordAgentResult(event.Request)
		m.Refresh()
	}
}

// PumpQueue is the per-tick drain: read whatever the running process produced,
// and pick up the next request when one finishes.
func (m *Model) PumpQueue() {
	if m.queue == nil {
		return
	}
	event := m.queue.Pump()
	m.refreshAgentActivity()
	if !event.Occurred() {
		return
	}
	m.recordAgentResult(event.Request)
	// The agent ran the CLI, which wrote through the same commands a human
	// would use — so the list has to re-read rather than trust anything the
	// TUI itself knows.
	m.Refresh()
	if next := m.advanceQueue(); next.Type == agent.Started {
		m.Flash(fmt.Sprintf("starting agent request #%d", next.Request.ID))
	}
}

// recordAgentResult opens the response pane on a finished request.
func (m *Model) recordAgentResult(request agent.Snapshot) {
	output := strings.TrimSpace(ansi.Normalize(request.Output))
	m.resp = m.styler.Wrap(output, max(m.width-8, 1))
	empty := true
	for _, line := range m.resp {
		if strings.TrimSpace(line) != "" {
			empty = false
		}
	}
	if empty {
		m.resp = []string{m.styler.Paint("muted", "(no output)")}
	}
	if request.Error != "" && request.Status == agent.Failed {
		m.resp = append(m.resp, m.styler.Paint("error", request.Error))
	}
	m.respRequestID = request.ID
	m.respOpen = true
	m.respScroll = 0
}

// ScrollResponse is pgup/pgdn over the response pane.
func (m *Model) ScrollResponse(delta int) {
	if !m.respOpen || len(m.resp) == 0 {
		return
	}
	m.respScroll = clamp(m.respScroll+delta, 0, max(len(m.resp)-respMax, 0))
}

// ResponseLines is the visible window of the response pane.
func (m *Model) ResponseLines() []string {
	if !m.respOpen {
		return nil
	}
	return sliceLines(m.resp, m.respScroll, respMax)
}

// DismissResponse closes the pane.
func (m *Model) DismissResponse() { m.respOpen = false }

// -- model cycling ---------------------------------------------------------------

// ToggleModel is `M`.
func (m *Model) ToggleModel() {
	if len(m.entries) == 0 {
		m.Flash("no agent is configured")
		return
	}
	m.entryIndex = (m.entryIndex + 1) % len(m.entries)
	suffix := ""
	if m.queueHasWork() {
		// A running request keeps the entry it was submitted with, so saying
		// "applies to new requests" is the difference between a setting and a
		// retroactive change.
		suffix = " (applies to new requests)"
	}
	m.Flash("agent: " + m.CurrentEntry().UILabel() + suffix)
}

func (m *Model) queueHasWork() bool {
	if m.queue == nil {
		return false
	}
	if _, active := m.queue.ActiveRequest(); active {
		return true
	}
	return m.pendingCount() > 0
}

// -- the activity modal -----------------------------------------------------------

// OpenAgentActivity is `A`.
func (m *Model) OpenAgentActivity() {
	if m.queue == nil || len(m.queue.Requests()) == 0 {
		m.Flash("no agent requests this session")
		return
	}
	m.agentActivityWidth = m.width
	m.OpenModal(m.agentActivityContent(m.width), ModalAgentActivity, true)
}

// agentActivityContent renders the queue as modal lines.
//
// The primitive's filter groups are request ids; Modal's are opaque strings, so
// they are stringified. Keeping the group at all is what makes filtering the
// activity modal show a matched line WITH its request header rather than
// stranded on its own.
func (m *Model) agentActivityContent(width int) ModalContent {
	content := agent.Activity(m.theme(), m.queue.Requests(), m.monotonic(), width)
	groups := make([]string, 0, len(content.FilterGroups))
	for _, id := range content.FilterGroups {
		groups = append(groups, itoa(id))
	}
	return ModalContent{Title: content.Title, Lines: content.Lines, FilterGroups: groups}
}

// theme reaches the styler's palette for the one primitive that needs the real
// theme rather than slot names. A styler that has none (the plain test one)
// yields nil, and Activity falls back to the default palette.
func (m *Model) theme() *theme.Theme {
	if provider, ok := m.styler.(interface{ Theme() *theme.Theme }); ok {
		return provider.Theme()
	}
	return nil
}

// monotonic is the clock the activity view measures elapsed time with. It rides
// the model's injected clock so a test and the differential see a fixed value.
func (m *Model) monotonic() float64 {
	if m.queue != nil {
		return m.queue.Now()
	}
	return float64(m.now().UnixNano()) / float64(time.Second)
}

// refreshAgentActivity keeps an OPEN activity modal live as requests progress.
//
// Modal.Replace is used rather than reopening, so the reader's filter and
// scroll position survive a request finishing underneath them.
func (m *Model) refreshAgentActivity() {
	if m.modal == nil || m.modal.Kind() != ModalAgentActivity {
		return
	}
	width := m.agentActivityWidth
	if width == 0 {
		width = m.width
	}
	content := m.agentActivityContent(width)
	m.modal.Replace(content.Title, content.Lines, content.FilterGroups)
}

// CancelQueuedAgentRequests is the palette action behind the confirmation.
func (m *Model) CancelQueuedAgentRequests() {
	if m.queue == nil || m.pendingCount() == 0 {
		m.Flash("no queued agent requests")
		return
	}
	count := m.pendingCount()
	plural := "s"
	if count == 1 {
		plural = ""
	}
	m.OpenModal(ModalContent{
		Title: "Cancel queued agent requests?",
		Lines: []string{
			fmt.Sprintf("Discard %d waiting request%s?", count, plural),
			"The active request will keep running.",
			"Press y to discard waiting work · n / esc cancels",
		},
	}, ModalAgentQueueCancel, false)
}

func (m *Model) agentQueueCancelKey(key string) {
	switch key {
	case "y", "Y", "\r", "\n":
		cancelled := m.queue.CancelPending()
		m.CloseModal()
		plural := "s"
		if len(cancelled) == 1 {
			plural = ""
		}
		m.Flash(fmt.Sprintf("cancelled %d queued agent request%s", len(cancelled), plural))
	case "n", "N", "\x1b", "q":
		m.CloseModal()
		m.Flash("queued requests kept")
	}
}

// -- rendering ---------------------------------------------------------------------

// PromptLines is the prompt row, or the hint that names the key that opens it.
func (m *Model) PromptLines(width int) []string {
	if m.mode != ModePrompt {
		text := "tab to ask the agent — reschedule, capture, edit anything…"
		if m.queueHasWork() {
			suffix := ""
			if pending := m.pendingCount(); pending > 0 {
				suffix = fmt.Sprintf(" · %d queued", pending)
			}
			text = "tab to ask the agent" + suffix
		}
		return []string{" " + m.styler.Paint("prompt", "❯ ") + m.styler.Paint("muted", text)}
	}
	wrapped := m.wrappedPrompt(max(width-5, 1))
	out := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		prefix := "   "
		if index == 0 {
			prefix = " " + m.styler.Paint("prompt", "❯ ")
		}
		out = append(out, prefix+line)
	}
	return out
}

// wrappedPrompt folds the buffer to cols cells and draws the caret.
//
// It wraps by CELLS, not runes: a CJK title pasted into the prompt is two cells
// per character, and folding by rune count would run the line off the frame. A
// grapheme wider than the whole column budget is blanked rather than allowed to
// overflow, which keeps every later column aligned.
func (m *Model) wrappedPrompt(cols int) []string {
	cols = max(cols, 1)
	clusters := input.Graphemes(m.PromptText())
	lines := [][]string{{}}
	starts := []int{0}
	width := 0
	for index, cluster := range clusters {
		cells := ansi.ClusterWidth(cluster)
		if cells > cols {
			cluster, cells = strings.Repeat(" ", cols), cols
		}
		if width > 0 && width+cells > cols {
			lines = append(lines, []string{})
			starts = append(starts, index)
			width = 0
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], cluster)
		width += cells
	}
	cursor := m.promptEditor().Cursor()
	if len(clusters) > 0 && cursor == len(clusters) && width >= cols {
		// The caret sits one past a line that exactly filled the width; it
		// belongs at the start of the next row, not one cell off the edge.
		lines = append(lines, []string{})
		starts = append(starts, len(clusters))
	}

	out := make([]string, 0, len(lines))
	for index, line := range lines {
		local := cursor - starts[index]
		if local < 0 || local > len(line) ||
			(index < len(lines)-1 && local == len(line)) {
			out = append(out, strings.Join(line, ""))
			continue
		}
		out = append(out, m.renderPromptSegment(line, local))
	}
	return out
}

func (m *Model) renderPromptSegment(segment []string, cursor int) string {
	before := strings.Join(segment[:cursor], "")
	at, after := " ", ""
	if cursor < len(segment) {
		at = segment[cursor]
		after = strings.Join(segment[cursor+1:], "")
	}
	return before + m.styler.Paint("selection", at) + after
}
