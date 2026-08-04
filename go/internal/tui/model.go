package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tasks-go/internal/application"
	"tasks-go/internal/check"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/term/input"
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
	filterInput  string
	showDeferred bool
	selected     int
	selectedID   string
	panel        *RightPanel

	// Overlay state. Each one is owned by exactly one mode, and SetMode
	// refuses to enter that mode while its overlay is nil — see uistate.go.
	modal            *Modal
	modalFilterInput *input.Editor
	form             *QuickForm
	actionPalette    *ActionPalette
	contextPalette   *ContextPalette
	taskEditor       *TaskEditorSession
	// pendingProject is the project a confirmation modal is about. It is held
	// separately from the selection because the selection can move underneath
	// an open confirmation, and answering `y` must act on what was asked.
	pendingProject *taskquery.ProjectView
	// archivePreview is the sweep the open confirmation described. It is handed
	// BACK to the store on confirmation, so a list that changed while the modal
	// was open refuses rather than archiving a set nobody saw.
	archivePreview *store.ArchivePreview
	// opener launches a URL. It is injected so a test never opens a browser.
	opener Opener

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
	readErr error

	quitting bool
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
		app:            options.App,
		paths:          options.Paths,
		env:            options.Env,
		styler:         styler,
		now:            now,
		view:           restoreView(options.Session.View),
		collapsed:      restoreCollapsed(options.Session.Collapsed),
		panelMode:      normalizePanelMode(options.Session.PanelMode),
		panelOffset:    options.Session.PanelOffset,
		contextFilters: restoreContextFilters(options.Session),
		mode:           ModeList,
		width:          80,
		height:         24,
		opener:         options.Opener,
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
		return m, nil
	case tickMsg:
		m.clearExpiredFlash()
		// External change: pick up an edit made by the CLI, an agent, or an
		// editor in another window. Selection survives it — see syncSelection.
		if m.read == nil || m.read.Stale() || m.today != m.currentDate() {
			m.Refresh()
		}
		return m, tick()
	case tea.KeyMsg:
		return m, m.handleKey(typed)
	case tea.MouseMsg:
		m.HandleMouse(typed)
		return m, nil
	}
	return m, nil
}

// MouseEnabled reports whether the resolved config asks for mouse reporting.
// The entry point reads it BEFORE starting the program, because turning mouse
// tracking on takes the terminal's own text selection away from the user.
func (m *Model) MouseEnabled() bool { return m.mouseEnabled() }

// View renders one frame.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	return m.Render()
}

// -- the read model ---------------------------------------------------------

func (m *Model) currentDate() temporal.Date {
	context, err := temporal.NewContext(m.now(), m.paths.Timezone, m.paths.TimeFormat)
	if err != nil {
		return temporal.DateOf(m.now())
	}
	return context.LocalDate()
}

// operation mints a per-read operation context. The TUI is a first-class
// source, so its reads carry `tui` rather than borrowing the CLI's identity.
func (m *Model) operation() *application.OperationContext {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		buffer = []byte(fmt.Sprintf("%016x", m.now().UnixNano()))[:8]
	}
	built, err := application.NewOperationContext("tui_"+hex.EncodeToString(buffer),
		application.SourceTUI, "")
	if err != nil {
		return nil
	}
	return built
}

// Refresh rebuilds the read model and the rows from it. It is the ONLY path
// that changes what the list shows, so a refresh caused by an external write
// and a refresh caused by a keypress cannot behave differently.
func (m *Model) Refresh() {
	read, err := m.app.ReadTasks(m.operation())
	if err != nil {
		m.readErr = err
		return
	}
	m.readErr = nil
	m.read = read
	m.today = read.Queries().Today()
	m.RefreshRows()
	m.reportFormatErrors()
}

// reportFormatErrors is Ruby's post-reload `Tasks::Check.check` flash.
//
// It matters more than it looks. The store's readers coerce defensively so a
// malformed record can never crash a reader, which means a broken file reads as
// a store with FEWER TASKS IN IT rather than as an error. Without this the TUI
// would paint a half-empty list that is indistinguishable from a half-empty
// store — the single most dangerous thing a task list can do.
func (m *Model) reportFormatErrors() {
	if m.paths.Org == "" {
		return
	}
	if result := check.Check(m.paths.Org); !result.OK() {
		m.Flash(m.styler.Paint("error", fmt.Sprintf(
			"⚠ %s: %d format error(s) — run `tasks check`",
			filepath.Base(m.paths.Org), len(result.Errors))))
	}
}

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
	request := BuildRequest{
		View:           m.view,
		Styler:         m.styler,
		Queries:        queries,
		Items:          items,
		Tree:           queries.Tree().Roots,
		UseTree:        m.useTree(),
		Collapsed:      m.collapsed,
		ShowDeferred:   m.showDeferred,
		UrgentDays:     m.paths.UrgentDays,
		ContextFilters: m.contextFilters,
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
		text = m.filterInput
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
		return
	}
	m.selected = index
	m.selectedID = id
	m.refreshOpenPanel()
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
		if m.panel != nil && m.panel.Kind == PanelDetail {
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
			m.RefreshRows()
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

// CollapseSelected folds the selected subtree. On an expandable, not-yet-folded
// node it folds and keeps the cursor there; otherwise it climbs to the parent
// task row, so a second press walks up the tree.
func (m *Model) CollapseSelected() {
	node := m.currentNode()
	if node == nil || node.Item == nil {
		return
	}
	id := node.Item.ID
	if id != "" && !m.collapsed[id] && len(m.visibleChildrenOf(node)) > 0 {
		m.collapsed[id] = true
		m.reselect(id)
		return
	}
	m.jumpToParent(node)
}

// ExpandSelected unfolds the selected node if it is folded.
func (m *Model) ExpandSelected() {
	node := m.currentNode()
	if node == nil || node.Item == nil || !m.collapsed[node.Item.ID] {
		return
	}
	delete(m.collapsed, node.Item.ID)
	m.reselect(node.Item.ID)
}

// CollapseAll folds every task node that has task children, across the whole
// tree. The selection may have been on a now-hidden row, so rows are rebuilt
// and the selection reconciled.
func (m *Model) CollapseAll() {
	if m.read == nil {
		return
	}
	var walk func(*taskquery.Node)
	walk = func(node *taskquery.Node) {
		if node.Task() && node.Item.ID != "" && hasTaskChildren(node, m.view == ViewOutline) {
			m.collapsed[node.Item.ID] = true
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

func hasTaskChildren(node *taskquery.Node, outline bool) bool {
	if outline {
		return len(node.Children) > 0
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
	if parent == nil || !parent.Task() {
		return
	}
	for index, row := range m.rows {
		if row.Item != nil && row.Item.ID == parent.Item.ID {
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

// ToggleDeferred flips whether unavailable work is shown.
func (m *Model) ToggleDeferred() {
	m.showDeferred = !m.showDeferred
	m.RefreshRows()
	if m.showDeferred {
		m.Flash("showing unavailable tasks")
	} else {
		m.Flash("hiding unavailable tasks")
	}
}

// -- the right panel --------------------------------------------------------

// OpenDetail opens the panel on the selection, or closes it if it is already
// showing that selection.
func (m *Model) OpenDetail() {
	if m.panel != nil && m.panel.Identity == m.selectedID {
		m.panel = nil
		return
	}
	m.showDetail()
}

func (m *Model) showDetail() {
	if project := m.CurrentProject(); project != nil {
		content := BuildProjectDetails(m.styler, m.read.Queries(), *project,
			m.projectTasks(*project), m.panelContentWidth())
		m.panel = NewRightPanel(content.Title, PanelProjectDetail, project.ID, content.Lines)
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
func (m *Model) ClosePanel() { m.panel = nil }

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
func (m *Model) Save() { _ = SaveSession(m.SessionState(), m.env) }
