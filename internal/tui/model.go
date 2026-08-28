package tui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/term/input"
)

// Mode is the interaction mode. The shell implements list and filter; the
// remaining modes are the seams the editor/forms/modals packet and the
// rendering/agent packet plug into. They are NAMED here rather than invented
// later so the two halves agree on the vocabulary at integration, and so an
// unimplemented mode is a visible absence rather than a silent branch.
type Mode string

// The modes, in the order lib/tui/ui_state.rb declares them.
const (
	ModeList           Mode = "list"
	ModePrompt         Mode = "prompt"
	ModeFilter         Mode = "filter"
	ModeModal          Mode = "modal"
	ModeForm           Mode = "form"
	ModePalette        Mode = "palette"
	ModeContextPalette Mode = "context_palette"
	ModeLinkPicker     Mode = "link_picker"
	ModeTaskEdit       Mode = "task_edit"
)

// Minimum frame that can retain borders, margins, and content.
const (
	MinWidth  = 8
	MinHeight = 6
	// TickInterval is the file-watch poll. Bubble Tea drives it as a command
	// rather than as an IO.select timeout; the interval is Ruby's.
	TickInterval = 250 * time.Millisecond
	// FlashDuration is how long a status message stays up. Ruby's three
	// seconds, unchanged: it is long enough to read one line and short enough
	// that a stale message never gets mistaken for a live one.
	FlashDuration = 3 * time.Second
)

// Options builds a Model. Every seam a test needs to pin is here: the clock,
// the environment, and the styler.
type Options struct {
	App     *application.Application
	Paths   config.Paths
	Env     determinism.Env
	Styler  Styler
	Now     func() time.Time
	Session SessionState
	// Opener launches a task's link. A nil one refuses out loud rather than
	// reaching for a platform launcher, which is what keeps a test suite from
	// opening browser windows.
	Opener Opener
	// CopyToClipboard is a test seam for yank actions. Nil uses the platform
	// clipboard command; tests inject a recorder and never touch the real one.
	CopyToClipboard func(string) bool
	// Entries are the provider/model pairs the prompt can be submitted to, and
	// Queue is the coordinator that runs them. Both are injected: a test and
	// the differential supply fakes, and a nil queue refuses the prompt out
	// loud rather than reaching for a real provider.
	Entries []AgentEntry
	Queue   *agentQueue
	// Embedded keeps nested quit commands inside the model: quit latches a
	// request the host reads through QuitRequested rather than terminating.
	// SuppressQuit says the HOST owns the quit affordance, so Tasks drops quit
	// from its own key hint; the request still latches, because a host that
	// suppresses quit is precisely the host that needs to observe it.
	// SuppressFooter drops the ENTIRE footer stack — agent transcript, store-read
	// banner, flash, filter lines, prompt, and key hints — for a host that paints
	// its own. SuppressKeyHints drops ONLY Tasks' ordinary key-hint row, on the
	// same "the host owns the affordance" reading as SuppressQuit; everything
	// else in the stack, the prompt above all, keeps rendering.
	// SuppressViewKeyHints is the same reading applied to the header: Tasks drops
	// the numeric prefixes from its view tab strip, keeping the view names and the
	// active-tab highlight, and keeps acting on 1-6.
	Embedded             bool
	SuppressFooter       bool
	SuppressKeyHints     bool
	SuppressViewKeyHints bool
	SuppressQuit         bool
	// SaveSession overrides standalone persistence for a host namespace.
	SaveSession func(SessionState) error
}

// Model is the Bubble Tea root — the port of Tui::App's state, minus its event
// loop.
//
// STRUCTURAL CHANGE from Ruby, deliberate and worth naming: Ruby's App is one
// 3,500-line object that owns the terminal, the read model, every key handler,
// and every cache, and invalidates the caches by hand from twenty places. Here
// the read model is rebuilt into `rows` by ONE function (refreshRows) from ONE
// value (BuildRequest), so there is no cache to invalidate and no way for two
// caches to disagree. Ruby needed the caches because it repainted on a 0.25s
// tick whether or not anything changed; Bubble Tea only calls View after an
// Update, so the pressure that justified them is gone.
type Model struct {
	app    *application.Application
	paths  config.Paths
	env    determinism.Env
	styler Styler
	now    func() time.Time

	// Durable UI state — the half that survives a restart.
	view           string
	collapsed      map[string]bool
	panelMode      string
	panelOffset    int
	contextFilters []string

	// Interaction state.
	mode         Mode
	filter       string
	filterInput  *input.Editor
	showDeferred bool
	// showRejected reveals recently declined proposals in Inbox/APPROVALS. Like
	// showDeferred it is interaction state, not durable: a reveal is for the
	// review pass you are in, and intake opens clean next time.
	showRejected bool
	// showClosed reveals DONE and CANCELLED rows in the Outline. Session-only
	// for the same reason showDeferred is: looking at what you finished is an
	// errand, not a preference, and the next session should open on the work.
	showClosed   bool
	selected     int
	selectedID   string
	panel        *RightPanel
	spatialFocus SpatialFocus

	// rowWidth is the list width the current rows were built at, so a frame
	// that changed it can rebuild them — see reconcileRowWidth.
	rowWidth int
	// railDrag is the in-flight pointer drag on the split rule, or nil.
	railDrag *railDrag

	// Overlay state. Each one is owned by exactly one mode, and SetMode
	// refuses to enter that mode while its overlay is nil — see uistate.go.
	modal               *Modal
	modalFilterInput    *input.Editor
	form                *QuickForm
	fieldModal          *FieldModal
	actionPalette       *ActionPalette
	contextPalette      *ContextPalette
	linkPicker          *LinkPicker
	taskEditor          *TaskEditorSession
	suspendedTaskEditor *TaskEditorSession
	suspendedTaskPanel  *RightPanel

	// Quit confirmations retain the overlay they interrupted. q and ctrl-c do
	// not answer either question; only an explicit y/return or n/escape does.
	quitReturnModal   *Modal
	quitReturnMode    Mode
	quitReturnMessage string
	agentQuitPending  bool
	// fieldModalQuitPending is the same latch for a dirty multi-field modal.
	fieldModalQuitPending bool
	// pendingProject is the project a confirmation modal is about. It is held
	// separately from the selection because the selection can move underneath
	// an open confirmation, and answering `y` must act on what was asked.
	pendingProject *taskquery.ProjectView
	// pendingDelete is the hard-delete confirmation: id, title, cascade flag,
	// expected revision, and how many tasks would leave. Held separately so
	// answering `y` acts on what was asked, not whatever is selected now.
	pendingDelete *pendingDelete
	// archivePreview is the sweep the open confirmation described. It is handed
	// BACK to the store on confirmation, so a list that changed while the modal
	// was open refuses rather than archiving a set nobody saw.
	archivePreview *store.ArchivePreview
	// archiveContext is the clock the OPEN archive confirmation was built with.
	// The store's pinned-preview fingerprint includes the day, so the preview
	// the user read and the sweep they confirm have to agree about what day it
	// is — otherwise a confirmation left open across local midnight refuses
	// with "task list changed" although not one byte moved.
	archiveContext temporal.Context
	// opener launches a URL. It is injected so a test never opens a browser.
	opener          Opener
	copyToClipboard func(string) bool

	// The agent surface. entries are the provider/model pairs `M` cycles;
	// queue is the serial request coordinator; the resp* fields are the
	// response pane the last finished request opened.
	entries            []AgentEntry
	entryIndex         int
	queue              *agentQueue
	promptInput        *input.Editor
	resp               []string
	respOpen           bool
	respScroll         int
	respRequestID      int
	agentActivityWidth int

	// Frame state.
	width  int
	height int
	rows   []Row
	read   *application.ReadModel
	today  temporal.Date

	flash      string
	flashUntil time.Time
	// editorMessage is the editor panel's own status line. It is separate from
	// the flash because it is not transient: "press escape again to discard"
	// has to stay visible until the user answers it.
	editorMessage string

	// readErr is the last read failure. A TUI that cannot read its store must
	// SAY so rather than paint an empty list that looks like an empty store.
	// It covers both an I/O failure of the read itself and a store whose bytes
	// the readers coerced past — see storeReadError.
	readErr error

	quitting             bool
	embedded             bool
	suppressFooter       bool
	suppressKeyHints     bool
	suppressViewKeyHints bool
	suppressQuit         bool
	quitRequested        bool
	saveSession          func(SessionState) error
}

// New builds the model from saved session state and one application facade.
func New(options Options) *Model {
	styler := options.Styler
	if styler == nil {
		styler = PlainStyler{}
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	model := &Model{
		app:              options.App,
		paths:            options.Paths,
		env:              options.Env,
		styler:           styler,
		now:              now,
		view:             restoreView(options.Session.View),
		collapsed:        restoreCollapsed(options.Session.Collapsed),
		panelMode:        normalizePanelMode(options.Session.PanelMode),
		panelOffset:      options.Session.PanelOffset,
		contextFilters:   restoreContextFilters(options.Session),
		mode:             ModeList,
		spatialFocus:     SpatialFocusList,
		width:            80,
		height:           24,
		opener:           options.Opener,
		copyToClipboard:  options.CopyToClipboard,
		entries:          options.Entries,
		queue:            options.Queue,
		embedded:         options.Embedded,
		suppressFooter:   options.SuppressFooter,
		suppressKeyHints: options.SuppressKeyHints,

		suppressViewKeyHints: options.SuppressViewKeyHints,
		suppressQuit:         options.SuppressQuit,
		saveSession:          options.SaveSession,
	}
	return model
}

func restoreView(saved string) string {
	// "approvals" was a tab of its own before it merged into Inbox. A session
	// saved by that build must not strand the user on a view that no longer
	// exists, so it lands on the tab that absorbed it.
	if saved == "approvals" {
		return ViewInbox
	}
	for _, key := range ViewKeys() {
		if key == saved {
			return saved
		}
	}
	return ViewAgenda
}

func restoreCollapsed(saved []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range saved {
		out[id] = true
	}
	return out
}

func restoreContextFilters(session SessionState) []string {
	if len(session.ContextFilters) > 0 {
		return NormalizeContextFilters(session.ContextFilters)
	}
	if session.ContextFilter != "" {
		return NormalizeContextFilters([]string{session.ContextFilter})
	}
	return nil
}

// -- Bubble Tea -------------------------------------------------------------

type tickMsg time.Time

// Init starts the file-watch tick.
func (m *Model) Init() tea.Cmd {
	m.Refresh()
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(TickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update is the whole event surface. It is a plain method on a value the tests
// construct directly, so every interaction contract below is checked by driving
// messages, not by scraping a terminal.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(typed.Width, MinWidth)
		m.height = max(typed.Height, MinHeight)
		m.reconcileEditorLayout()
		m.reconcileSpatialFocus()
		return m, nil
	case tickMsg:
		m.clearExpiredFlash()
		// Drain the running agent process FIRST, so a request that just
		// finished writing is picked up by the same tick that noticed it
		// finished rather than by the next one.
		m.PumpQueue()
		// External change: pick up an edit made by the CLI, an agent, or an
		// editor in another window. Selection survives it — see syncSelection.
		if m.read == nil || m.read.Stale() || m.today != m.currentDate() {
			m.Refresh()
		}
		return m, tick()
	case tea.KeyPressMsg:
		return m, m.handleKey(typed)
	case tea.PasteMsg:
		m.handlePaste(typed.Content)
		return m, nil
	case tea.MouseMsg:
		m.HandleMouse(typed)
		return m, nil
	}
	return m, nil
}

// MouseEnabled reports whether the resolved config asks for mouse reporting.
// The entry point reads it so View can set MouseMode each frame; turning mouse
// tracking on takes the terminal's own text selection away from the user.
func (m *Model) MouseEnabled() bool { return m.mouseEnabled() }

// View renders one frame as a Bubble Tea v2 View. Alt screen and mouse mode
// live on the View (not program options), matching Sidecar so an external v2
// host can host this model without translating tea.Msg values.
func (m *Model) View() tea.View {
	content := ""
	if !m.quitting {
		content = m.Render()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	if m.mouseEnabled() {
		v.MouseMode = tea.MouseModeCellMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}
	return v
}

// QuitRequested reports an embedded quit request without terminating its host.
func (m *Model) QuitRequested() bool { return m.quitRequested }

// ClearQuitRequest acknowledges an embedded quit request.
func (m *Model) ClearQuitRequest() { m.quitRequested = false }

// -- the read model ---------------------------------------------------------

func (m *Model) currentDate() temporal.Date {
	context := m.temporalContext()
	if context.Timezone == nil {
		return temporal.DateOf(m.now())
	}
	return context.LocalDate()
}

// operation mints a per-read operation context on the session's OWN clock. The
// TUI is a first-class source, so its reads carry `tui` rather than borrowing
// the CLI's identity.
//
// Pinning the clock here is what makes the model's injected `now` the single
// clock the whole surface reads. Without it the application falls back to its
// own factory, and the TUI ends up with TWO clocks — the one `currentDate`,
// flash expiry and the spinner read, and the one every write stamps from. In
// the shipping binary both are `time.Now()` so they agree by luck; a test or a
// harness that pins one and not the other would be measuring nothing.
func (m *Model) operation() *application.OperationContext {
	return m.operationAt(m.temporalContext())
}

// operationAt mints an operation carrying a SPECIFIC clock.
//
// A fresh identity every call, a caller-chosen instant: the two are separate on
// purpose. Archive is the case that needs it — the preview the user reads and
// the sweep they confirm have to agree about what day it is, because the
// store's pinned-preview fingerprint includes the day, but they are still two
// distinct operations in the journal and the audit trail.
func (m *Model) operationAt(context temporal.Context) *application.OperationContext {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		buffer = []byte(fmt.Sprintf("%016x", m.now().UnixNano()))[:8]
	}
	built, err := application.NewOperationContext("tui_"+hex.EncodeToString(buffer),
		application.SourceTUI, "")
	if err != nil {
		return nil
	}
	if context.Timezone == nil {
		// An unresolvable zone leaves the operation unpinned rather than
		// carrying a zero context, which has no location and would panic the
		// moment anything asked it for a local date.
		return built
	}
	return built.WithTemporalContext(context)
}

// Refresh rebuilds the read model and the rows from it. It is the ONLY path
// that changes what the list shows, so a refresh caused by an external write
// and a refresh caused by a keypress cannot behave differently.
func (m *Model) Refresh() {
	priorMode := m.mode
	read, err := m.app.ReadTasks(m.operation())
	if err != nil {
		m.readErr = err
		return
	}
	m.read = read
	m.today = read.Queries().Today()
	m.RefreshRows()
	m.reconcileOpenOverlays(priorMode)
	m.reportFormatErrors()
}

// reconcileOpenOverlays keeps target-bound input attached to the exact task it
// was opened for after an external reload. A vanished target closes the input;
// it is never allowed to fall through onto the replacement selection.
func (m *Model) reconcileOpenOverlays(prior Mode) {
	switch prior {
	case ModeForm:
		if m.form != nil && m.form.TargetID != "" && !m.selectedTarget(m.form.TargetID) {
			m.form = nil
			m.mode = ModeList
			m.Flash("task no longer exists")
		}
	case ModeFieldModal:
		if m.fieldModal != nil && m.fieldModal.TargetID() != "" &&
			!m.selectedTarget(m.fieldModal.TargetID()) {
			m.fieldModal = nil
			m.mode = ModeList
			m.Flash("task no longer exists")
		}
	case ModePalette:
		if m.actionPalette != nil && m.actionPalette.TargetID() != "" &&
			!m.selectedTarget(m.actionPalette.TargetID()) {
			m.actionPalette = nil
			m.mode = ModeList
		}
	case ModeContextPalette:
		if m.contextPalette != nil {
			m.contextPalette.RefreshOptions(m.tokensFromStore(func(item store.Item) []string {
				return item.Contexts
			}), m.contextFilters)
		}
	case ModeLinkPicker:
		if m.linkPicker != nil && !m.selectedTarget(m.linkPicker.TargetID()) {
			m.linkPicker = nil
			m.mode = ModeList
		}
	case ModeTaskEdit:
		if m.taskEditor != nil {
			outcome := m.taskEditor.Refresh()
			m.editorMessage = outcome.Message
			if outcome.Status == EditorMissing {
				m.editorMessage = "Task no longer exists; local field retained for copy or discard" +
					" · y copies field · esc discards editor"
				m.Flash(m.editorMessage)
			} else if outcome.Status == EditorConflicted {
				m.Flash(outcome.Message)
			}
		}
	}
	if m.suspendedTaskEditor != nil {
		outcome := m.suspendedTaskEditor.Refresh()
		m.editorMessage = outcome.Message
		if outcome.Status == EditorMissing || !m.suspendedTargetVisible() {
			m.showSuspendedEditorPanel()
		} else if m.panel != nil && m.panel.Kind == PanelDetail {
			m.refreshOpenPanel()
		}
	}
}

func (m *Model) selectedTarget(id string) bool {
	if item := m.CurrentItem(); item != nil && item.ID == id {
		return true
	}
	return m.CurrentProject() != nil && m.CurrentProject().ID == id
}

// reportFormatErrors is Ruby's post-reload `Tasks::Check.check` flash.
//
// It matters more than it looks. The store's readers coerce defensively so a
// malformed record can never crash a reader, which means a broken file reads as
// a store with FEWER TASKS IN IT rather than as an error. Without this the TUI
// would paint a half-empty list that is indistinguishable from a half-empty
// store — the single most dangerous thing a task list can do.
// It also has to be STICKY. A flash expires; the condition does not. So the
// same assessment sets readErr, which the footer keeps painting until the
// store is readable again, and which an embedded host reads through
// pkg/tui.Model.LoadError to render its own diagnostic.
//
// What counts as a failure, deliberately:
//
//   - the tasks directory does not exist — a misconfiguration, not a store;
//   - the store path is a directory, or cannot be opened (permissions);
//   - the store has bytes that are not valid UTF-8 or not valid Tasks JSONL.
//
// What does NOT count is the first-run state: no store file yet inside an
// existing tasks directory, or a zero-length file. Tasks writes the store on
// the first mutation, so a brand-new install legitimately has nothing to read,
// and calling that an error would greet every new user with a broken banner. A
// store that exists and holds only its meta record is likewise healthy and
// empty. The distinction the caller actually needs — "no tasks" versus "no
// read" — survives, because every genuinely unreadable case above reports.
func (m *Model) reportFormatErrors() {
	m.readErr = nil
	path := m.paths.Org
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		directory := filepath.Dir(path)
		if _, dirErr := os.Stat(directory); dirErr != nil {
			m.readErr = fmt.Errorf("task directory is missing: %s", directory)
		}
		return
	case err != nil:
		m.readErr = err
		return
	case info.IsDir():
		m.readErr = fmt.Errorf("%s is a directory, not a task file", path)
		return
	case info.Size() == 0:
		return
	}
	// The read-only check VIEW validates against the vocabulary the APPLICATION's
	// store enforces, not the built-in one. It refuses nothing, but it is what
	// the format-error flash quotes, and a display that called a user's own
	// configured mode invalid would send them looking for a fault that is not
	// there.
	result := check.CheckWith(path, check.Options{Modes: m.app.DelegationModes()})
	if result.OK() {
		return
	}
	m.readErr = fmt.Errorf("%s: %s", filepath.Base(path), result.Errors[0].Message)
	m.Flash(m.styler.Paint("error", fmt.Sprintf(
		"⚠ %s: %d format error(s) — run `tasks check`",
		filepath.Base(path), len(result.Errors))))
}

// ReadError is the sticky store-read failure, or nil when the last read was
// healthy. An empty store is healthy; see reportFormatErrors.
func (m *Model) ReadError() error { return m.readErr }

// ReadModel is the model's current read, for tests and for the packets that
// need the same snapshot the list was built from.
func (m *Model) ReadModel() *application.ReadModel { return m.read }

// RefreshRows rebuilds the row list from the current read model, then
// reconciles the selection against it.
func (m *Model) RefreshRows() {
	if m.read == nil {
		m.rows = nil
		return
	}
	queries := m.read.Queries()
	items := m.filteredItems()
	m.rowWidth = max(m.layout().ListWidth-CursorField, 0)
	request := BuildRequest{
		Width:          m.rowWidth,
		View:           m.view,
		Styler:         m.styler,
		Queries:        queries,
		Items:          items,
		Tree:           queries.Tree().Roots,
		UseTree:        m.useTree(),
		Collapsed:      m.collapsed,
		ShowDeferred:   m.showDeferred,
		ShowRejected:   m.showRejected,
		ShowClosed:     m.showClosed,
		UrgentDays:     m.paths.UrgentDays,
		ContextFilters: m.contextFilters,
		TextFilter:     m.activeFilter(),
		IntakeCounts:   m.intakeCounts(items),
	}
	if m.view == ViewProjects && request.UseTree {
		request.Projects = queries.Projects()
	}
	m.rows = BuildRows(request)
	m.syncSelection()
}

// CONTEXT_TREE_VIEWS: the list views that keep the outliner tree under an
// active `@` context filter. Outline and Projects stay on their flat filtered
// path, whose shape those builders rely on.
var contextTreeViews = map[string]bool{
	ViewAgenda: true, ViewNext: true, ViewQuadrants: true, ViewInbox: true,
}

// useTree decides tree versus flat. A `/` search always renders flat. A `@`
// context filter keeps the tree on the list views so subtasks stay visible.
func (m *Model) useTree() bool {
	if m.activeFilter() != "" {
		return false
	}
	if len(m.contextFilters) > 0 {
		return contextTreeViews[m.view]
	}
	return true
}

// activeFilter is the filter narrowing the views right now: the live buffer
// while typing, the committed filter otherwise.
func (m *Model) activeFilter() string {
	text := m.filter
	if m.mode == ModeFilter {
		text = m.filterEditor().Text()
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// filteredItems applies the active `@` context and `/` search filters. Shared
// by the list builders and the tab badges so a badge can never advertise rows
// the current filter hides — an @home count of work approvals is a lie you
// cannot act on.
func (m *Model) filteredItems() []store.Item {
	items := m.read.Items()
	if len(m.contextFilters) > 0 {
		kept := []store.Item{}
		for _, item := range items {
			if contextMatches(item, m.contextFilters) {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	if query := strings.ToLower(m.activeFilter()); query != "" {
		kept := []store.Item{}
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Title), query) {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	return items
}

func contextMatches(item store.Item, filters []string) bool {
	for _, context := range item.Contexts {
		for _, wanted := range filters {
			if context == wanted {
				return true
			}
		}
	}
	return false
}

// intakeCounts counts TASKS IN THE TAB, not rows the tab paints, and the two
// are deliberately not the same number. Tree mode rides non-matching
// descendants along under a matching anchor for context, and collapsing an
// anchor hides rows without emptying the inbox. Counting rows would make the
// badge shrink when you fold a subtree; counting tasks keeps it the same number
// `tasks inbox` reports.
func (m *Model) intakeCounts(items []store.Item) IntakeCounts {
	query := NewViewQuery(ViewInbox, m.read.Queries(), m.paths.UrgentDays, m.showDeferred, nil)
	counts := IntakeCounts{}
	for _, item := range items {
		if query.Eligible(item) {
			counts.Inbox++
		}
		if isProposedState(item.State) {
			counts.Approvals++
		}
	}
	return counts
}

// OpenTaskCount is the header's "N open".
func (m *Model) OpenTaskCount() int {
	if m.read == nil {
		return 0
	}
	queries := m.read.Queries()
	total := 0
	for _, item := range m.read.Items() {
		if isOpenState(item.State) && queries.AvailabilityFor(item).Available() {
			total++
		}
	}
	return total
}

// OverdueTaskCount is the header's "N overdue": open, available tasks whose
// primary date has already passed. It counts across the whole store, not the
// current view — the header describes what is true, not what is on screen.
func (m *Model) OverdueTaskCount() int {
	if m.read == nil {
		return 0
	}
	queries := m.read.Queries()
	total := 0
	for _, item := range m.read.Items() {
		if !isOpenState(item.State) || !queries.AvailabilityFor(item).Available() {
			continue
		}
		// The SAME definition the bands and the agenda's OVERDUE block use —
		// see bandDays. Counting any past date here made the header claim two
		// overdue while the block under it held one, because a scheduled date
		// in the past means a task became available, not that it is late.
		if days, ok := bandDays(BuildRequest{Queries: queries}, item); ok && days < 0 {
			total++
		}
	}
	return total
}

// -- selection --------------------------------------------------------------

// Rows is the current row list.
func (m *Model) Rows() []Row { return m.rows }

// Selected is the current row index.
func (m *Model) Selected() int { return m.selected }

// SelectedID is the durable identity the cursor follows.
func (m *Model) SelectedID() string { return m.selectedID }

// CurrentItem is the selected task, or nil on a header or project row.
func (m *Model) CurrentItem() *store.Item {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return nil
	}
	return m.rows[m.selected].Item
}

// CurrentProject is the selected project header's view, or nil.
func (m *Model) CurrentProject() *taskquery.ProjectView {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return nil
	}
	return m.rows[m.selected].Project
}

func (m *Model) selectableIndexes() []int {
	out := []int{}
	for index, row := range m.rows {
		if row.Selectable() {
			out = append(out, index)
		}
	}
	return out
}

func (m *Model) selectRow(index int) {
	id := ""
	if index >= 0 && index < len(m.rows) {
		id = m.rows[index].ID()
	}
	if m.selected == index && m.selectedID == id {
		// A row rebuild can preserve both coordinates while replacing the
		// underlying read model. Keep an open detail panel synchronized even
		// when selection itself has nothing to change (for example, deciding a
		// proposal preselects the next id before Refresh rebuilds the Inbox).
		if m.mode != ModeTaskEdit {
			m.refreshOpenPanel()
		}
		return
	}
	m.selected = index
	m.selectedID = id
	if m.mode != ModeTaskEdit {
		m.refreshOpenPanel()
	}
}

func (m *Model) move(delta int) {
	selectable := m.selectableIndexes()
	if len(selectable) == 0 {
		return
	}
	current := 0
	for offset, index := range selectable {
		if index == m.selected {
			current = offset
			break
		}
	}
	m.selectRow(selectable[clamp(current+delta, 0, len(selectable)-1)])
}

// syncSelection reconciles stable identity with the current rendered rows.
//
// THIS IS THE CONTRACT MOST EASILY LOST IN A REWRITE. A refresh — a keystroke,
// a file watch tick, an agent's write — rebuilds every row from scratch, and a
// cursor that follows a row INDEX would slide onto a different task whenever
// anything above it moved. That is how a user completes the wrong task.
//
// So the cursor follows an ID. If the id is no longer visible, it lands on the
// selectable row nearest the prior coordinate, deterministically. And because a
// task with several contexts appears more than once in the Next view, the
// CURRENT OCCURRENCE is preferred when it still represents the id, so a refresh
// does not teleport the cursor to that task's first occurrence.
func (m *Model) syncSelection() {
	selectable := m.selectableIndexes()
	if len(selectable) == 0 {
		m.selected = 0
		m.selectedID = ""
		if m.mode != ModeTaskEdit && m.panel != nil && m.panel.Kind == PanelDetail {
			m.panel = nil
		}
		return
	}
	index := -1
	if m.selectedID != "" && containsInt(selectable, m.selected) &&
		m.rows[m.selected].ID() == m.selectedID {
		index = m.selected
	}
	if index < 0 && m.selectedID != "" {
		for _, candidate := range selectable {
			if m.rows[candidate].ID() == m.selectedID {
				index = candidate
				break
			}
		}
	}
	if index < 0 {
		index = nearestSelectable(selectable, m.selected)
	}
	m.selectRow(index)
}

// nearestSelectable is `sels.min_by { |i| [(i - sel).abs, i] }`: nearest by
// distance, lowest index breaking a tie. Deterministic on purpose — a fallback
// that depended on map order would move the cursor differently on two runs over
// identical data.
func nearestSelectable(selectable []int, target int) int {
	best, bestDistance := selectable[0], abs(selectable[0]-target)
	for _, candidate := range selectable[1:] {
		distance := abs(candidate - target)
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// -- navigation -------------------------------------------------------------

// SwitchView moves to the named tab.
func (m *Model) SwitchView(view string) {
	for _, key := range ViewKeys() {
		if key == view {
			m.view = view
			if m.suspendedTaskEditor != nil && !m.suspendedTaskEditor.Missing() {
				m.selectedID = m.suspendedTaskEditor.TargetID()
			}
			m.RefreshRows()
			m.reconcileSuspendedAfterNavigation()
			return
		}
	}
}

// CycleView moves by tabs, wrapping.
func (m *Model) CycleView(delta int) {
	keys := ViewKeys()
	index := 0
	for offset, key := range keys {
		if key == m.view {
			index = offset
			break
		}
	}
	m.SwitchView(keys[((index+delta)%len(keys)+len(keys))%len(keys)])
}

// CollapseSelected folds the selected subtree — a task's or, in the outline, a
// section's. On an expandable, not-yet-folded node it folds and keeps the
// cursor there; otherwise it climbs to the parent row, so a second press walks
// up the tree.
func (m *Model) CollapseSelected() {
	node := m.currentNode()
	if node == nil || node.ID == "" {
		return
	}
	if !m.collapsed[node.ID] && m.nodeFoldable(node) {
		m.collapsed[node.ID] = true
		m.reselect(node.ID)
		return
	}
	m.jumpToParent(node)
}

// nodeFoldable reports whether folding the node would hide anything. The
// outline renders every child it is SHOWING — sections included — so any
// painted descendant suffices there; the other tree views render only visible
// task children.
func (m *Model) nodeFoldable(node *taskquery.Node) bool {
	if m.view == ViewOutline {
		return outlineRenders(m.treeRequest(), node)
	}
	// A Projects heading folds its whole subtree's worth of anchors, which is
	// what the view renders under it — not just its direct children, since a
	// sub-section under a project rolls its tasks up into that project.
	if m.view == ViewProjects && node.Section() {
		return len(anchorsUnder(m.treeRequest(), node)) > 0
	}
	return len(m.visibleChildrenOf(node)) > 0
}

// treeRequest is the minimum BuildRequest the tree helpers need to answer
// visibility questions outside a render.
func (m *Model) treeRequest() BuildRequest {
	return BuildRequest{
		Queries: m.read.Queries(), Tree: m.read.Queries().Tree().Roots,
		ShowDeferred: m.showDeferred, ShowClosed: m.showClosed,
	}
}

// ExpandSelected unfolds the selected node if it is folded.
func (m *Model) ExpandSelected() {
	node := m.currentNode()
	if node == nil || node.ID == "" || !m.collapsed[node.ID] {
		return
	}
	delete(m.collapsed, node.ID)
	m.reselect(node.ID)
}

// CollapseAll folds every foldable node, across the whole tree. In the outline
// that includes sections, so H collapses the view down to its headings — the
// overview an outliner's fold-all exists for. The selection may have been on a
// now-hidden row, so rows are rebuilt and the selection reconciled.
func (m *Model) CollapseAll() {
	if m.read == nil {
		return
	}
	outline := m.view == ViewOutline
	request := m.treeRequest()
	var walk func(*taskquery.Node)
	walk = func(node *taskquery.Node) {
		if node.ID != "" && (node.Task() || outline) && hasTaskChildren(request, node, outline) {
			m.collapsed[node.ID] = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, root := range m.read.Queries().Tree().Roots {
		walk(root)
	}
	m.RefreshRows()
}

func hasTaskChildren(request BuildRequest, node *taskquery.Node, outline bool) bool {
	if outline {
		// Same question the outline's own marker asks: would folding hide a row
		// that is actually painted? Collapsing a node whose only children are
		// hidden closed work would arm a fold nothing can see.
		return outlineRenders(request, node)
	}
	for _, child := range node.Children {
		if child.Task() {
			return true
		}
	}
	return false
}

// ExpandAll unfolds everything.
func (m *Model) ExpandAll() {
	m.collapsed = map[string]bool{}
	m.RefreshRows()
}

func (m *Model) currentNode() *taskquery.Node {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return nil
	}
	return m.rows[m.selected].Node
}

func (m *Model) visibleChildrenOf(node *taskquery.Node) []*taskquery.Node {
	return visibleChildren(BuildRequest{
		Queries: m.read.Queries(), ShowDeferred: m.showDeferred,
	}, node)
}

func (m *Model) jumpToParent(node *taskquery.Node) {
	parent := node.Parent
	if parent == nil {
		return
	}
	// Outside the outline a section has no row to land on.
	if !parent.Task() && m.view != ViewOutline {
		return
	}
	for index, row := range m.rows {
		if parent.ID != "" && row.ID() == parent.ID && row.Selectable() {
			m.selectRow(index)
			return
		}
	}
}

// reselect rebuilds the rows and puts the cursor back on a known id.
func (m *Model) reselect(id string) {
	m.selectedID = id
	m.RefreshRows()
}

// ToggleRejected flips whether recently declined proposals are revealed in
// intake. Hidden by default; the reveal is what makes a mistaken reject
// recoverable without leaving the Inbox tab.
func (m *Model) ToggleRejected() {
	m.showRejected = !m.showRejected
	m.RefreshRows()
	if m.showRejected {
		m.Flash(fmt.Sprintf("showing proposals rejected in the last %d days — a restores one",
			taskquery.RejectedRecentDays))
		return
	}
	m.Flash("hiding rejected proposals")
}

// ToggleDeferred flips whether unavailable work is shown.
func (m *Model) ToggleDeferred() {
	m.showDeferred = !m.showDeferred
	if m.suspendedTaskEditor != nil && !m.suspendedTaskEditor.Missing() {
		m.selectedID = m.suspendedTaskEditor.TargetID()
	}
	m.RefreshRows()
	m.reconcileSuspendedAfterNavigation()
	if m.showDeferred {
		m.Flash("showing unavailable tasks")
	} else {
		m.Flash("hiding unavailable tasks")
	}
}

// closedToggleViews are the tabs the closed-row toggle actually changes. The
// key is bound list-wide, the way Z is, so the choice can be made before
// arriving — but the header only claims closed rows are shown on a tab where
// they would be.
var closedToggleViews = map[string]bool{ViewOutline: true}

// ToggleClosed flips whether finished work — DONE and CANCELLED — is shown in
// the Outline. It is the Z gesture applied to the other reason a row is not
// part of today's work: Z hides what you cannot do yet, this hides what you no
// longer have to.
//
// It composes with everything rather than replacing it: `/`, `@` and Z all
// still narrow what the reveal reveals, and nothing here is written to the
// session file.
func (m *Model) ToggleClosed() {
	m.showClosed = !m.showClosed
	if m.suspendedTaskEditor != nil && !m.suspendedTaskEditor.Missing() {
		m.selectedID = m.suspendedTaskEditor.TargetID()
	}
	m.RefreshRows()
	m.reconcileSuspendedAfterNavigation()
	if m.showClosed {
		m.Flash("showing closed tasks in the outline")
		return
	}
	m.Flash("hiding closed tasks in the outline")
}

// -- the right panel --------------------------------------------------------

// OpenDetail opens the panel on the selection, or closes it if it is already
// showing that selection.
func (m *Model) OpenDetail() {
	if m.panel != nil && m.panel.Identity == m.selectedID {
		m.panel = nil
		m.spatialFocus = SpatialFocusList
		return
	}
	m.showDetail()
}

func (m *Model) showDetail() {
	if project := m.CurrentProject(); project != nil {
		content := BuildProjectDetails(m.styler, m.read.Queries(), *project,
			m.projectTasks(*project), m.panelContentWidth())
		m.panel = NewRightPanel(content.Title, PanelProjectDetail, project.ID, content.Lines)
		m.spatialFocus = SpatialFocusDetail
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	content := BuildTaskDetails(m.styler, m.read.Queries(), *item, m.panelContentWidth(),
		m.projectNameOf(*item))
	m.panel = NewRightPanel(content.Title, PanelDetail, item.ID, content.Lines)
	m.spatialFocus = SpatialFocusDetail
}

// refreshOpenPanel keeps an open detail panel pointed at the selection. Because
// RightPanel.Replace resets scroll only when the identity CHANGES, a refresh
// that lands on the same task keeps the reader's place in a long note.
func (m *Model) refreshOpenPanel() {
	if m.panel == nil || m.read == nil {
		return
	}
	switch m.panel.Kind {
	case PanelDetail:
		item := m.CurrentItem()
		if item == nil {
			m.panel = nil
			m.spatialFocus = SpatialFocusList
			return
		}
		content := BuildTaskDetails(m.styler, m.read.Queries(), *item, m.panelContentWidth(),
			m.projectNameOf(*item))
		m.panel.Replace(content.Title, item.ID, content.Lines)
	case PanelProjectDetail:
		project := m.CurrentProject()
		if project == nil {
			return
		}
		content := BuildProjectDetails(m.styler, m.read.Queries(), *project,
			m.projectTasks(*project), m.panelContentWidth())
		m.panel.Replace(content.Title, project.ID, content.Lines)
	}
}

// Panel is the open right panel, or nil.
func (m *Model) Panel() *RightPanel { return m.panel }

// ClosePanel closes the right panel.
func (m *Model) ClosePanel() {
	m.panel = nil
	m.spatialFocus = SpatialFocusList
}

func (m *Model) projectNameOf(item store.Item) string {
	node := m.read.Queries().NodeFor(item)
	if node == nil {
		return ""
	}
	if section := projectSection(node); section != nil {
		return section.Title
	}
	return ""
}

func (m *Model) projectTasks(project taskquery.ProjectView) []store.Item {
	byID := map[string]store.Item{}
	for _, item := range m.read.Items() {
		byID[item.ID] = item
	}
	out := []store.Item{}
	for _, id := range project.TaskIDs {
		if item, found := byID[id]; found {
			out = append(out, item)
		}
	}
	return out
}

// panelContentWidth is the width the panel WILL have, which is not the same as
// the width it has: the detail content is built before the panel exists, so
// asking the live layout would measure a frame with no panel in it and wrap
// every line to nothing.
func (m *Model) panelContentWidth() int {
	return NewScreenLayout(m.styler, LayoutRequest{
		Width:       m.width,
		Height:      m.height,
		Footer:      m.Footer(),
		Panel:       true,
		PanelMode:   m.panelMode,
		PanelOffset: m.panelOffset,
	}).PanelContentWidth
}

// CyclePanelMode steps the panel width ladder.
func (m *Model) CyclePanelMode(delta int) {
	index := indexOfMode(m.panelMode)
	m.panelMode = PanelModes[((index+delta)%len(PanelModes)+len(PanelModes))%len(PanelModes)]
	m.panelOffset = 0
	m.refreshOpenPanel()
}

// ResizePanel nudges the panel by whole columns on top of its mode width.
func (m *Model) ResizePanel(delta int) {
	m.panelOffset += delta
	m.refreshOpenPanel()
}

// -- flash ------------------------------------------------------------------

// Flash shows a transient status message.
func (m *Model) Flash(message string) {
	m.flash = message
	m.flashUntil = m.now().Add(FlashDuration)
}

// FlashMessage is the live flash, or "".
func (m *Model) FlashMessage() string {
	m.clearExpiredFlash()
	return m.flash
}

func (m *Model) clearExpiredFlash() {
	if m.flash != "" && m.now().After(m.flashUntil) {
		m.flash = ""
	}
}

// unimplemented is how this build refuses a key another packet owns. A half
// built action is worse than an absent one: the user must be told the keypress
// did nothing, and told why, rather than left to assume it worked.
func (m *Model) unimplemented(what, owner string) {
	m.Flash(fmt.Sprintf("%s is not implemented in this build (%s)", what, owner))
}

// -- session ----------------------------------------------------------------

// SessionState is the state to persist on exit. The collapsed set and the
// context filters are intersected with what the store actually still holds, so
// a saved id for a deleted task cannot accumulate forever.
func (m *Model) SessionState() SessionState {
	live := map[string]bool{}
	liveContexts := map[string]bool{}
	if m.read != nil {
		for _, item := range m.read.Items() {
			if item.ID != "" {
				live[item.ID] = true
			}
			for _, context := range item.Contexts {
				liveContexts[context] = true
			}
		}
		// Sections fold too now, so their ids are as live as any task's — a
		// collapsed project must stay collapsed across a restart.
		var walk func(*taskquery.Node)
		walk = func(node *taskquery.Node) {
			if node.Section() && node.ID != "" {
				live[node.ID] = true
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		for _, root := range m.read.Queries().Tree().Roots {
			walk(root)
		}
	}
	collapsed := []string{}
	for id := range m.collapsed {
		if live[id] {
			collapsed = append(collapsed, id)
		}
	}
	sort.Strings(collapsed)

	filters := []string{}
	for _, context := range m.contextFilters {
		if len(liveContexts) == 0 || liveContexts[context] {
			filters = append(filters, context)
		}
	}
	return SessionState{
		View:           m.view,
		Collapsed:      collapsed,
		PanelMode:      m.panelMode,
		PanelOffset:    m.panelOffset,
		ContextFilters: filters,
	}
}

// Save persists the session. Best effort — a read-only state directory must not
// keep the TUI from exiting.
func (m *Model) Save() {
	if m.saveSession != nil {
		_ = m.saveSession(m.SessionState())
		return
	}
	_ = SaveSession(m.SessionState(), m.env)
}

// CurrentView is the stable active view key.
func (m *Model) CurrentView() string { return m.view }

// ConsumesTextInput reports whether printable keys belong to an input buffer.
func (m *Model) ConsumesTextInput() bool {
	return m.mode == ModePrompt || m.mode == ModeFilter || m.mode == ModeModalFilter ||
		m.mode == ModeForm || m.mode == ModeFieldModal ||
		m.mode == ModePalette || m.mode == ModeContextPalette || m.mode == ModeLinkPicker ||
		m.mode == ModeTaskEdit
}

// FocusContext is a stable host-facing name for the active interaction layer.
func (m *Model) FocusContext() string {
	switch m.mode {
	case ModePrompt:
		return "prompt"
	case ModeFilter:
		return "filter"
	case ModeModalFilter:
		if m.modal != nil && m.modal.Kind() == ModalAgentActivity {
			return "agent_activity_filter"
		}
		return "modal_filter"
	case ModeModal:
		if m.modal != nil && m.modal.Kind() == ModalAgentActivity {
			return "agent_activity"
		}
		return "modal"
	case ModeForm:
		return "form"
	case ModeFieldModal:
		return "field_modal"
	case ModePalette:
		return "picker"
	case ModeContextPalette:
		return "context_picker"
	case ModeLinkPicker:
		return "picker"
	case ModeTaskEdit:
		return "task_edit"
	default:
		if m.respOpen {
			if m.CurrentSpatialFocus() == SpatialFocusDetail && m.panel != nil && m.panel.Kind == PanelDetail {
				return "response_detail"
			}
			return "response"
		}
		if m.CurrentSpatialFocus() == SpatialFocusDetail && m.panel != nil && m.panel.Kind == PanelDetail {
			return "detail"
		}
		return "list"
	}
}
