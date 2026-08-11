package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/tui/term/clipboard"
)

// The list-wide actions: archive, history, ordering and links.
//
// Each one reaches the store through an application capability rather than
// through its own path, and each one reports what happened in Ruby's words —
// the messages are the contract a user reads, so they are ported literally
// rather than paraphrased.

// -- archive ---------------------------------------------------------------------

// ArchiveSweep is `x`: preview what a sweep would move, then ask.
//
// It never sweeps directly. The preview the user is shown is PINNED and handed
// back on confirmation, so a list that changed while the modal was open refuses
// rather than archiving a set nobody saw.
func (m *Model) ArchiveSweep() {
	if project := m.CurrentProject(); project != nil {
		m.ConfirmArchiveProject(project)
		return
	}
	// Capture the session clock ONCE. Everything from here to the confirmation
	// reads this instant, not a fresh one.
	m.archiveContext = m.temporalContext()
	preview, supported := m.app.ArchivePreview(m.operationAt(m.archiveContext))
	if !supported {
		m.Flash("this store cannot archive")
		return
	}
	if preview.Roots == 0 {
		m.Flash("archive preview: 0 roots · 0 descendants — nothing to archive")
		return
	}

	noun := "descendants"
	if preview.Descendants == 1 {
		noun = "descendant"
	}
	lines := []string{fmt.Sprintf("Would move %d completed root%s and %d %s to archive.jsonl.",
		preview.Roots, plural(preview.Roots), preview.Descendants, noun)}

	if preview.Blocked() {
		// A blocked sweep is NOT a confirmation: there is nothing to say yes
		// to. It names every blocking subtree so the user can go fix them.
		has := "s have"
		if preview.BlockedRoots() == 1 {
			has = " has"
		}
		lines = append(lines, "", m.styler.Paint("error", fmt.Sprintf(
			"Cannot archive: %d closed root%s %d open descendant%s.",
			preview.BlockedRoots(), has, preview.OpenDescendants(),
			plural(preview.OpenDescendants()))))
		for _, block := range preview.Blocks {
			lines = append(lines, "  "+block.RootTitle+": "+strings.Join(block.OpenTitles, ", "))
		}
		lines = append(lines, m.styler.Paint("muted",
			"Complete, cancel, move, or unnest that work first. esc closes"))
		m.OpenModal(ModalContent{Title: "Archive blocked", Lines: lines}, ModalArchiveBlocked, false)
		return
	}

	lines = append(lines, "", m.styler.Paint("muted", "Press y to archive · n / esc cancels"))
	held := preview
	m.archivePreview = &held
	m.OpenModal(ModalContent{Title: "Confirm archive", Lines: lines}, ModalArchiveConfirm, false)
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (m *Model) archiveConfirmKey(key string) {
	switch key {
	case "y", "Y":
		expected := m.archivePreview
		// The SAME day the preview was built with. A distinct operation
		// identity, a shared instant — see Model.operationAt.
		outcome, supported := m.app.ArchiveSweep(expected, m.operationAt(m.archiveContext))
		m.CloseModal()
		if !supported {
			m.Flash("this store cannot archive")
			return
		}
		switch outcome.Refusal {
		case store.ArchiveUnsupportedSchema:
			m.ShowUnsupportedSchemaNotice()
			return
		case store.ArchivePreviewChanged:
			m.Flash("task list changed — press x to review the updated archive preview")
			return
		case store.ArchiveConflict:
			m.Flash("archive conflict — live tasks preserved; run tasks archive for details")
			return
		case store.ArchiveOpenDescendants:
			m.Flash("archive refused — open descendants remain; press x for details")
			return
		}
		m.Refresh()
		if outcome.Roots == 0 {
			m.Flash("nothing to archive")
			return
		}
		m.Flash(fmt.Sprintf("archived %d root%s", outcome.Roots, plural(outcome.Roots)))
	case "n", "N", "\x1b", "q":
		m.CloseModal()
		m.Flash("archive cancelled")
	}
}

// ShowUnsupportedSchemaNotice opens the one modal that has no action: nothing
// this build can do makes a future schema readable.
func (m *Model) ShowUnsupportedSchemaNotice() {
	m.OpenModal(ModalContent{
		Title: "Unsupported task file",
		Lines: []string{
			"This task file was written by a newer version of tasks.",
			"",
			m.styler.Paint("muted", "Upgrade tasks to read it. esc closes"),
		},
	}, ModalUnsupportedSchema, false)
}

// -- hard delete -------------------------------------------------------------------

// pendingDelete is the hard-delete confirmation the open modal is about.
// Answering `y` acts on these fields, not on whatever is selected now.
type pendingDelete struct {
	ID               string
	Title            string
	Cascade          bool
	ExpectedRevision string
	Removed          int
	Descendants      int
	OpenDescendants  int
	Recurring        bool
}

// DeleteSelected is `#` / Delete: preview a hard delete, then confirm.
//
// It never writes on the first keystroke. A leaf gets a simple confirm; a task
// with descendants gets a cascade confirm that names counts. Both refuse
// projects/sections (store rule: delete targets tasks) and both land in one
// journal step that `u` / ctrl-r already undo/redo.
func (m *Model) DeleteSelected() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if m.read == nil {
		m.Flash("task no longer exists — refresh and try again")
		return
	}
	task, found := m.read.TaskFor(item.ID)
	if !found {
		m.Flash("task no longer exists — refresh and try again")
		return
	}

	ids := subtreeIDs(m.read.Queries(), task)
	descendants := len(ids) - 1
	if descendants < 0 {
		descendants = 0
	}
	openDescendants := 0
	for _, id := range ids {
		if id == task.ID {
			continue
		}
		if child, ok := m.read.TaskFor(id); ok && isOpenState(child.State) {
			openDescendants++
		}
	}

	pending := &pendingDelete{
		ID:               task.ID,
		Title:            task.Title,
		Cascade:          descendants > 0,
		ExpectedRevision: m.read.Snapshot().RevisionFor(task),
		Removed:          len(ids),
		Descendants:      descendants,
		OpenDescendants:  openDescendants,
		Recurring:        task.Recur != "",
	}
	m.pendingDelete = pending

	if descendants > 0 {
		noun := "descendants"
		if descendants == 1 {
			noun = "descendant"
		}
		openNote := ""
		if openDescendants > 0 {
			openNote = fmt.Sprintf(" (%d open)", openDescendants)
		}
		m.OpenModal(ModalContent{
			Title: "Confirm cascade delete",
			Lines: []string{
				fmt.Sprintf("Delete permanently: %s and %d %s%s?",
					pending.Title, descendants, noun, openNote),
				"Children are removed, not reparented.",
				"",
				m.styler.Paint("muted", "Press y to cascade delete · n / esc cancels · u undoes after"),
			},
		}, ModalDeleteCascadeConfirm, false)
		return
	}

	lines := []string{fmt.Sprintf("Delete permanently: %s?", pending.Title)}
	if pending.Recurring {
		lines = append(lines, "Recurring tasks are removed entirely (not rolled).")
	}
	lines = append(lines, "",
		m.styler.Paint("muted", "Press y to delete · n / esc cancels · u undoes after"))
	m.OpenModal(ModalContent{
		Title: "Confirm delete",
		Lines: lines,
	}, ModalDeleteConfirm, false)
}

func (m *Model) deleteConfirmKey(key string) {
	switch key {
	case "y", "Y":
		pending := m.pendingDelete
		m.CloseModal()
		if pending == nil {
			return
		}
		outcome := m.app.DeleteTask(application.DeleteCommand{
			ID:               pending.ID,
			Cascade:          pending.Cascade,
			ExpectedRevision: pending.ExpectedRevision,
		}, m.operation())
		if outcome.Status == store.MutationUnsupportedSchema {
			m.ShowUnsupportedSchemaNotice()
			return
		}
		if outcome.NotFound() {
			m.Refresh()
			m.Flash("task no longer exists")
			return
		}
		if outcome.Stale() {
			m.Refresh()
			m.Flash("file changed underneath — try again")
			return
		}
		if outcome.Conflict() {
			m.Refresh()
			m.Flash(fmt.Sprintf(
				"refusing to delete: %s has %s (%d open) — press # again to cascade",
				pending.Title,
				pluralizeCount(outcome.Summary.Descendants, "descendant"),
				outcome.Summary.OpenDescendants))
			return
		}
		if !outcome.OK() {
			m.Refresh()
			message := "failed to delete"
			if len(outcome.Errors) > 0 {
				message = outcome.Errors[0]
			}
			m.Flash(message)
			return
		}
		removed := len(outcome.TouchedIDs)
		if removed == 0 {
			removed = pending.Removed
		}
		m.Refresh()
		if removed > 1 {
			m.Flash(fmt.Sprintf("deleted %d tasks: %s — u to undo", removed, pending.Title))
		} else {
			m.Flash("deleted: " + pending.Title + " — u to undo")
		}
	case "n", "N", "\x1b", "q":
		m.CloseModal()
		m.Flash("delete cancelled")
	}
}

// pluralizeCount is "1 descendant" / "2 descendants" for flash text.
func pluralizeCount(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// -- history -----------------------------------------------------------------------

// UndoLast and RedoLast are `u` and ctrl-r.
func (m *Model) UndoLast() { m.historyStep(-1, "undid", "undo") }
func (m *Model) RedoLast() { m.historyStep(1, "redid", "redo") }

// historyStep applies one journal step and reports it in Ruby's words.
//
// The three refusals are distinct on purpose and are NOT collapsed into "could
// not undo": an unsupported schema means this build must not touch the file, an
// empty history means there is nothing to undo, and a conflict names the label
// the user was about to lose — which is the only one where they could act.
func (m *Model) historyStep(delta int, verb, noun string) {
	outcome, label, supported := m.app.HistoryStep(delta)
	if !supported {
		m.Flash("this store has no history")
		return
	}
	switch outcome {
	case store.HistoryUnsupportedSchema:
		m.ShowUnsupportedSchemaNotice()
	case store.HistoryEmpty:
		m.Flash("nothing to " + noun)
	case store.HistoryConflict:
		m.Flash("file changed externally — can't " + noun + " “" + label + "”")
	default:
		m.Refresh()
		m.Flash(verb + ": " + label)
	}
}

// -- ordering ------------------------------------------------------------------------

// orderingLabels is Ruby's `ordering_label`, and the labels reach the journal,
// so they are the words a later `u` will echo back.
var orderingLabels = map[string]string{
	"up": "move up", "down": "move down", "indent": "indent", "outdent": "outdent",
}

// ReorderSelected moves the selected subtree.
//
// The placement is computed from the SIBLING LIST rather than from screen rows:
// the rows are a filtered, sorted projection, and moving a task by what is
// drawn above it would move it somewhere else entirely in the file.
func (m *Model) ReorderSelected(action string) {
	if !m.availability("ordering_action_available?") {
		m.Flash("ordering requires the unfiltered Outline tab")
		return
	}
	item := m.CurrentItem()
	if item == nil || m.read == nil {
		m.Flash("task no longer exists — refresh and try again")
		return
	}
	task, found := m.read.TaskFor(item.ID)
	if !found {
		m.Flash("task no longer exists — refresh and try again")
		return
	}

	placement, ok := m.orderingPlacement(action, task)
	if !ok {
		return
	}
	label := orderingLabels[action] + ": " + item.Title
	outcome, supported := m.app.MoveTask(item.ID, placement, label, m.operation())
	if !supported {
		m.Flash("this store cannot move tasks")
		return
	}
	if !(outcome.OK() || outcome.NoChange()) {
		m.Refresh()
		m.Flash(orderingFailureMessage(outcome, action))
		return
	}
	if action == "indent" {
		// The new parent has to be OPEN or the task the user just indented
		// would vanish under a fold they did not ask for.
		delete(m.collapsed, placement.ParentID)
	}
	m.selectedID = item.ID
	m.Refresh()
	if outcome.NoChange() {
		m.Flash("already in that position: " + item.Title)
		return
	}
	m.Flash(orderingLabels[action] + ": " + item.Title)
}

// orderingPlacement is Ruby's `ordering_placement`. The boundary notices are
// flashes rather than refusals — "already first among siblings" is information,
// not an error.
func (m *Model) orderingPlacement(action string, task store.Item) (store.Placement, bool) {
	siblings := []store.Item{}
	for _, candidate := range m.read.Items() {
		if candidate.Parent == task.Parent {
			siblings = append(siblings, candidate)
		}
	}
	index := -1
	for position, candidate := range siblings {
		if candidate.ID == task.ID {
			index = position
			break
		}
	}
	if index < 0 {
		m.Flash("task placement changed — refresh and try again")
		return store.Placement{}, false
	}

	switch action {
	case "up":
		if index == 0 {
			m.Flash("already first among siblings")
			return store.Placement{}, false
		}
		return store.Placement{ParentID: task.Parent, BeforeID: siblings[index-1].ID}, true
	case "down":
		if index == len(siblings)-1 {
			m.Flash("already last among siblings")
			return store.Placement{}, false
		}
		// Moving down means landing in front of the sibling AFTER the next one
		// — index+2, not index+1, because the task itself still occupies a slot
		// in the list the anchor is read from. An empty anchor appends.
		before := ""
		if index+2 < len(siblings) {
			before = siblings[index+2].ID
		}
		return store.Placement{ParentID: task.Parent, BeforeID: before}, true
	case "indent":
		if index == 0 {
			m.Flash("can't indent without a preceding sibling")
			return store.Placement{}, false
		}
		return store.Placement{ParentID: siblings[index-1].ID}, true
	case "outdent":
		parent, hasParent := m.read.TaskFor(task.Parent)
		if !hasParent {
			m.Flash("already at section level")
			return store.Placement{}, false
		}
		parentSiblings := []store.Item{}
		for _, candidate := range m.read.Items() {
			if candidate.Parent == parent.Parent {
				parentSiblings = append(parentSiblings, candidate)
			}
		}
		parentIndex := -1
		for position, candidate := range parentSiblings {
			if candidate.ID == parent.ID {
				parentIndex = position
				break
			}
		}
		if parentIndex < 0 {
			m.Flash("parent placement changed — refresh and try again")
			return store.Placement{}, false
		}
		before := ""
		if parentIndex+1 < len(parentSiblings) {
			before = parentSiblings[parentIndex+1].ID
		}
		return store.Placement{ParentID: parent.Parent, BeforeID: before}, true
	}
	return store.Placement{}, false
}

// orderingFailureMessage translates a refused move into the sentence Ruby
// shows. Each status names a DIFFERENT recovery, which is why they are not
// collapsed: "refresh" and "try again" are different instructions.
func orderingFailureMessage(outcome application.Outcome, action string) string {
	switch outcome.Status {
	case store.MutationNotFound:
		return "task or placement anchor no longer exists — refresh and try again"
	case store.MutationStale:
		return "task changed underneath — try again"
	case store.MutationConflict:
		return "placement anchor moved underneath — try again"
	case store.MutationCycle:
		return "can't move a task into its own subtree"
	case store.MutationTooDeep:
		if action == "indent" {
			return "can't indent — maximum task depth reached"
		}
		return "move exceeds maximum task depth"
	case store.MutationInvalid:
		if len(outcome.Errors) > 0 {
			return outcome.Errors[0]
		}
		return "invalid task placement"
	}
	return outcomeMessage(outcome, "could not move the task")
}

// -- links ---------------------------------------------------------------------------

// Opener launches a URL. It is an INTERFACE, not a function call, because the
// alternative is a test suite that opens real browser windows.
type Opener interface {
	// Open launches the URL and reports whether a launcher could be spawned.
	Open(url string) bool
}

// OpenLink is `o`: open one link immediately or let the user choose among many.
func (m *Model) OpenLink() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	found := m.read.Queries().Links(*item)
	if len(found) == 0 {
		m.Flash("no links on this task")
		return
	}
	if len(found) > 1 {
		m.SetLinkPicker(NewLinkPicker(m.styler, item.ID, found))
		_ = m.SetMode(ModeLinkPicker)
		return
	}
	m.openSelectedLink(found[0])
}

func (m *Model) resolveLinkPickerOutcome(result PickerResult) {
	if result.Kind == PickerCancelled {
		m.SetLinkPicker(nil)
		return
	}
	if result.Kind != PickerAccepted || m.linkPicker == nil {
		return
	}
	link, ok := m.linkPicker.Link(result)
	if !ok {
		return
	}
	m.SetLinkPicker(nil)
	m.openSelectedLink(link)
}

func (m *Model) openSelectedLink(link links.Link) {
	if m.opener == nil || !m.opener.Open(link.URL) {
		m.Flash("no browser launcher found (set TASKS_OPENER)")
		return
	}
	m.Flash("opened " + link.System + ": " + link.URL)
}

// -- clipboard -------------------------------------------------------------------------

// YankRef is `y`: copy the stable reference the CLI accepts.
func (m *Model) YankRef() {
	m.yank(func(item store.Item) string { return ExportReference(item) })
}

// YankMarkdown is `Y`: copy the task as a markdown checkbox line with its notes.
func (m *Model) YankMarkdown() {
	m.yank(func(item store.Item) string {
		queries := m.read.Queries()
		return exportMarkdown(item, queries.Body(item), queries)
	})
}

func (m *Model) yank(render func(store.Item) string) {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if _, found := m.read.TaskFor(item.ID); !found {
		m.Flash("task no longer exists")
		return
	}
	text := render(*item)
	copied := false
	if m.copyToClipboard != nil {
		copied = m.copyToClipboard(text)
	} else if command := clipboard.Command(); command != nil {
		copied = clipboard.Copy(text, command)
	}
	if !copied {
		m.Flash("no clipboard tool found (pbcopy/wl-copy/xclip/xsel)")
		return
	}
	m.Flash("yanked: “" + item.Title + "”")
}
