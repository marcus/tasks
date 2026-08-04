package tui

import (
	"fmt"
	"strings"

	"tasks-go/internal/application"
	"tasks-go/internal/recur"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/term/shortcuts"
)

// priorityLadder is the order K and J walk: A highest, no cookie lowest.
var priorityLadder = []string{"A", "B", "C", ""}

// handlers is the name-to-method table the shortcut registry dispatches
// through. The registry names a handler as a string exactly as Ruby names it as
// a symbol, and this is where a name becomes behavior.
//
// A name with NO entry here is a capability this build does not have. It is
// refused out loud by name rather than silently ignored — see refuseUnbuilt.
func (m *Model) handlers() map[string]func(key string) {
	return map[string]func(string){
		// Navigation and folding — the shell packet's, reached through the
		// registry so the palette can run them too.
		"select_prev":          func(string) { m.move(-1) },
		"select_next":          func(string) { m.move(1) },
		"prev_view":            func(string) { m.CycleView(-1) },
		"next_view":            func(string) { m.CycleView(1) },
		"jump_view":            m.jumpView,
		"collapse_selected":    func(string) { m.CollapseSelected() },
		"expand_selected":      func(string) { m.ExpandSelected() },
		"collapse_all":         func(string) { m.CollapseAll() },
		"expand_all":           func(string) { m.ExpandAll() },
		"open_detail":          func(string) { m.OpenDetail() },
		"toggle_deferred_view": func(string) { m.ToggleDeferred() },
		"start_filter":         func(string) { m.startFilter() },
		"panel_half_up":        func(string) { m.scrollPanel(-1, true) },
		"panel_half_down":      func(string) { m.scrollPanel(1, true) },
		"panel_page_up":        func(string) { m.scrollPanel(-1, false) },
		"panel_page_down":      func(string) { m.scrollPanel(1, false) },
		"grow_task_panel":      func(string) { m.ResizePanel(2) },
		"shrink_task_panel":    func(string) { m.ResizePanel(-2) },
		"dismiss_or_cancel":    func(string) { m.dismiss() },
		"quit":                 func(string) { m.requestQuit() },

		// This packet's own.
		"open_help":                    func(string) { m.OpenHelp() },
		"open_action_palette":          func(string) { m.OpenActionPalette() },
		"open_context_palette":         func(string) { m.OpenContextPalette() },
		"complete_selected":            func(string) { m.CompleteSelected() },
		"approve_proposal":             func(string) { m.DecideProposal(application.ProposalApprove) },
		"reject_proposal":              func(string) { m.DecideProposal(application.ProposalReject) },
		"raise_priority":               func(string) { m.BumpPriority(-1) },
		"lower_priority":               func(string) { m.BumpPriority(1) },
		"open_date_popup":              func(string) { m.OpenDatePopup() },
		"open_recur_popup":             func(string) { m.OpenRecurPopup() },
		"rename_project":               func(string) { m.RenameProject() },
		"capture_into_project":         func(string) { m.CaptureIntoProject() },
		"archive_sweep":                func(string) { m.ArchiveSweep() },
		"undo_last":                    func(string) { m.UndoLast() },
		"redo_last":                    func(string) { m.RedoLast() },
		"move_subtree_up":              func(string) { m.ReorderSelected("up") },
		"move_subtree_down":            func(string) { m.ReorderSelected("down") },
		"indent_subtree":               func(string) { m.ReorderSelected("indent") },
		"outdent_subtree":              func(string) { m.ReorderSelected("outdent") },
		"open_link":                    func(string) { m.OpenLink() },
		"defer_selected":               func(string) { m.DeferSelected() },
		"delegate_selected":            func(string) { m.DelegateSelected() },
		"set_work_ref_selected":        func(string) { m.SetWorkRefSelected() },
		"focus_prompt":                 func(string) { m.FocusPrompt() },
		"paste_ref":                    func(string) { m.PasteRef() },
		"toggle_model":                 func(string) { m.ToggleModel() },
		"open_agent_activity":          func(string) { m.OpenAgentActivity() },
		"cancel_queued_agent_requests": func(string) { m.CancelQueuedAgentRequests() },
		"resp_up":                      func(string) { m.ScrollResponse(-5) },
		"resp_down":                    func(string) { m.ScrollResponse(5) },
		"start_task_edit":              func(string) { m.StartTaskEdit("title") },
		"start_task_edit_last":         func(string) { m.StartTaskEdit(editFields[len(editFields)-1]) },
		"yank_ref":                     func(string) { m.YankRef() },
		"yank_markdown":                func(string) { m.YankMarkdown() },

		// Modal navigation.
		"modal_up":           func(string) { m.modalMove(-1) },
		"modal_down":         func(string) { m.modalMove(1) },
		"modal_half_up":      func(string) { m.modalScroll(-1, "half") },
		"modal_half_down":    func(string) { m.modalScroll(1, "half") },
		"modal_page_up":      func(string) { m.modalScroll(-1, "page") },
		"modal_page_down":    func(string) { m.modalScroll(1, "page") },
		"modal_start_filter": func(string) { m.ModalStartFilter() },
		"close_modal":        func(string) { m.CloseModal() },

		// The editor owns its own bytes; the registry routes them back to it.
		"task_edit_input": m.taskEditKey,
	}
}

// unbuiltHandlers names every registry handler this build cannot perform, and
// says WHY. A refusal that names the missing capability is a bug report the
// user can act on; a key that does nothing is not.
// Every registry handler now has an implementation. The map is retained as an
// empty, named seam: a later packet that lands a bound key ahead of its
// capability has somewhere honest to say so, and
// TestNoBoundKeyStillRefusesAsUnimplemented fails the moment anything is added
// without also being listed as known.
var unbuiltHandlers = map[string]string{}

// availability resolves the registry's predicate names. A bound but unavailable
// key is still CONSUMED, so dispatch can never leak into a lower-priority
// context and run something else.
func (m *Model) availability(name string) bool {
	switch name {
	case shortcuts.DefaultAvailability, "":
		return true
	case "selected_action_available?":
		return m.CurrentItem() != nil
	case "project_selected?":
		return m.CurrentProject() != nil
	case "modal_filter_available?":
		return m.modal != nil && m.modal.Filterable()
	case "panel_scroll_available?":
		return m.panel != nil && m.panel.Kind == PanelDetail
	case "proposal_action_available?":
		item := m.CurrentItem()
		return item != nil && isProposedState(item.State)
	case "recurrence_action_available?":
		item := m.CurrentItem()
		return item != nil && !isProposedState(item.State) &&
			(item.Scheduled != "" || item.Deadline != "")
	case "link_action_available?":
		item := m.CurrentItem()
		return item != nil && m.read != nil && len(m.read.Queries().Links(*item)) > 0
	case "delegation_action_available?":
		item := m.CurrentItem()
		return item != nil && isOpenState(item.State)
	case "ordering_action_available?":
		return m.view == ViewOutline && m.activeFilter() == "" &&
			len(m.contextFilters) == 0 && m.CurrentItem() != nil
	case "agent_activity_available?":
		return m.queue != nil && len(m.queue.Requests()) > 0
	case "pending_agent_requests_available?":
		return m.pendingCount() > 0
	}
	return false
}

// -- help and the palettes ----------------------------------------------------------

// OpenHelp shows the generated shortcut overlay.
func (m *Model) OpenHelp() { m.OpenModal(HelpContent(m.styler), ModalHelp, true) }

// OpenModal opens an overlay and enters modal mode.
func (m *Model) OpenModal(content ModalContent, kind ModalKind, filterable bool) {
	m.SetModal(NewModal(ModalOptions{
		Title: content.Title, Lines: content.Lines, Kind: kind,
		Filterable: filterable, FilterGroups: content.FilterGroups,
	}))
	m.modalFilterEditor().Clear()
	_ = m.SetMode(ModeModal)
}

// CloseModal dismisses the overlay.
func (m *Model) CloseModal() {
	m.pendingProject = nil
	m.archivePreview = nil
	m.archiveContext = temporal.Context{}
	m.modalFilterEditor().Clear()
	m.SetModal(nil)
	m.mode = ModeList
}

// OpenActionPalette shows every action available right now.
func (m *Model) OpenActionPalette() {
	entries := shortcuts.PaletteEntries(shortcuts.List, m.availability)
	if m.panel != nil && m.panel.Kind == PanelDetail {
		seen := map[string]bool{}
		for _, entry := range entries {
			seen[entry.Handler] = true
		}
		for _, entry := range shortcuts.PaletteEntries(shortcuts.Detail, m.availability) {
			if !seen[entry.Handler] {
				entries = append(entries, entry)
				seen[entry.Handler] = true
			}
		}
	}
	targetID := ""
	if item := m.CurrentItem(); item != nil {
		targetID = item.ID
	}
	m.SetActionPalette(NewActionPalette(m.styler, entries, ReturnList, targetID))
	_ = m.SetMode(ModePalette)
}

// CloseActionPalette dismisses it, returning where it came from.
func (m *Model) CloseActionPalette() {
	if m.actionPalette == nil {
		return
	}
	destination := m.actionPalette.ReturnMode()
	if destination == ReturnModal && m.modal == nil {
		destination = ReturnList
	}
	m.actionPalette = nil
	if destination == ReturnModal {
		m.mode = ModeModal
		return
	}
	m.mode = ModeList
}

// OpenContextPalette shows the @context filter picker.
func (m *Model) OpenContextPalette() {
	contexts := []string{}
	if m.read != nil {
		for _, item := range m.read.Items() {
			contexts = append(contexts, item.Contexts...)
		}
	}
	m.SetContextPalette(NewContextPalette(contexts, m.contextFilters))
	_ = m.SetMode(ModeContextPalette)
}

// CloseContextPalette dismisses it.
func (m *Model) CloseContextPalette() {
	m.contextPalette = nil
	m.mode = ModeList
}

// ApplyContextFilter adopts an accepted context set and rebuilds the rows.
func (m *Model) ApplyContextFilter(contexts []string) {
	previous := m.contextFilters
	m.contextFilters = NormalizeContextFilters(contexts)
	if len(m.contextFilters) == 0 {
		if len(previous) == 0 {
			m.Flash("no context filter")
		} else {
			m.Flash("context filter cleared")
		}
	} else {
		m.Flash("contexts: " + strings.Join(m.contextFilters, " + "))
	}
	m.RefreshRows()
}

// -- the modal filter -------------------------------------------------------------

// ModalStartFilter opens the live line filter inside a filterable modal.
func (m *Model) ModalStartFilter() {
	if m.modal == nil || !m.modal.Filterable() {
		return
	}
	m.modalFilterEditor().Replace(m.modal.Filter())
	_ = m.SetMode(ModeModalFilter)
}

func (m *Model) modalMove(delta int) {
	if m.modal != nil {
		m.modal.ScrollLine(delta, m.modalBodyHeight())
	}
}

func (m *Model) modalScroll(direction int, span string) {
	if m.modal == nil {
		return
	}
	if span == "half" {
		m.modal.ScrollHalf(direction, m.modalBodyHeight())
		return
	}
	m.modal.ScrollPage(direction, m.modalBodyHeight())
}

// modalBodyHeight is the rows a modal may occupy. It is derived from the same
// layout the frame paints with, so the scroll arithmetic and the drawn box
// cannot disagree about how much fits.
func (m *Model) modalBodyHeight() int { return max(m.layout().BodyHeight, 1) }

// -- writes -------------------------------------------------------------------------

// patchSelected is the thin quick-action write path: resolve the selected row
// to a stable id, read the field-owned baseline from the SAME facade the store
// compares against, and patch.
//
// It is deliberately separate from TaskEditorSession. The editor owns the rich
// save-on-blur workflow; these keyboard actions keep their own confirmations,
// messages and undo labels, and a shared abstraction would blur both.
func (m *Model) patchSelected(id string, field store.PatchField, value, label string) (application.Outcome, bool) {
	expected, present := m.app.Baseline(id, field)
	if !present {
		return application.Outcome{}, false
	}
	return m.app.PatchTask(application.Patch{
		ID: id, Field: field, Value: value, Expected: expected, HistoryLabel: label,
	}, m.operation()), true
}

// patchSelectedTemporal is patchSelected for a value a string cannot carry.
func (m *Model) patchSelectedTemporal(id string, field store.PatchField,
	value temporal.Value, label string) (application.Outcome, bool) {

	expected, present := m.app.Baseline(id, field)
	if !present {
		return application.Outcome{}, false
	}
	return m.app.PatchTask(application.TypedPatch(id, field, store.TemporalValue(value),
		expected, label, ""), m.operation()), true
}

// needsTask is Ruby's message for a key that only applies to a task while a
// project header is selected. The key is still consumed, never falling through.
func (m *Model) needsTask() { m.Flash("select a task for that") }

// CompleteSelected is `c`: close the selected task, or confirm closing a whole
// project when a project header is selected.
func (m *Model) CompleteSelected() {
	if project := m.CurrentProject(); project != nil {
		m.ConfirmCompleteProject(project)
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if !isOpenState(item.State) {
		m.Flash("already " + item.State)
		return
	}
	recurring := recur.Cookie(item.Recur)
	title := item.Title
	id := item.ID
	result, found := m.patchSelected(id, store.FieldState, "DONE", "complete: "+title)
	if !found || !(result.OK() || result.NoChange()) {
		m.Refresh()
		m.Flash("file changed underneath — try again")
		return
	}
	m.Refresh()
	if recurring {
		// A recurring task rolled forward and is still in the view — follow it,
		// and say where it landed, because "done" that leaves the task on
		// screen is otherwise indistinguishable from a failure.
		note := ""
		if fresh, present := m.read.TaskFor(id); present {
			stamp := fresh.Deadline
			if stamp == "" {
				stamp = fresh.Scheduled
			}
			if date, ok := temporal.ParseDate(stamp); ok {
				note = " → " + date.ISO() + " (" + date.Weekday().String()[:3] + ")"
			}
		}
		m.Flash("↻ " + title + note)
		m.reselect(id)
		return
	}
	extra := len(result.TouchedIDs) - 1
	subs := ""
	if extra > 0 {
		plural := "s"
		if extra == 1 {
			plural = ""
		}
		subs = fmt.Sprintf(" (+%d subtask%s)", extra, plural)
	}
	m.Flash("✓ DONE: " + title + subs + " — x to archive")
	m.RefreshRows()
}

// DecideProposal is `a` / `r` on a proposed task.
func (m *Model) DecideProposal(action application.ProposalAction) {
	item := m.CurrentItem()
	if item == nil || !isProposedState(item.State) {
		m.Flash("select a task pending approval")
		return
	}
	// Review order: after deciding this one, land on the NEXT proposal rather
	// than wherever the list happens to reflow to, so a review pass is a
	// sequence of one keystroke each.
	next := m.nextProposalID(item.ID)
	title := item.Title
	result := m.app.DecideProposal(application.ProposalDecision{
		ID: item.ID, Action: action,
		ExpectedRevision: m.read.Snapshot().RevisionFor(*item),
	}, m.operation())
	if !result.OK() {
		m.Refresh()
		message := "proposal changed underneath — try again"
		if len(result.Errors) > 0 {
			message = result.Errors[0]
		}
		m.Flash(message)
		return
	}
	m.selectedID = next
	if m.selectedID == "" && action == application.ProposalApprove {
		m.selectedID = item.ID
	}
	m.Refresh()
	target := "INBOX"
	verb := "approved"
	if action == application.ProposalReject {
		target, verb = "CANCELLED", "rejected"
	}
	m.Flash(verb + " → " + target + ": " + title)
}

func (m *Model) nextProposalID(current string) string {
	ids := []string{}
	for _, row := range m.rows {
		if row.Item != nil && isProposedState(row.Item.State) {
			ids = append(ids, row.Item.ID)
		}
	}
	index := -1
	for position, id := range ids {
		if id == current {
			index = position
			break
		}
	}
	for offset := 1; offset <= len(ids); offset++ {
		candidate := ids[((index+offset)%len(ids)+len(ids))%len(ids)]
		if candidate != current {
			return candidate
		}
	}
	return ""
}

// BumpPriority is `K` / `J`: one step along the ladder, never past its end.
func (m *Model) BumpPriority(delta int) {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	index := len(priorityLadder) - 1
	for position, value := range priorityLadder {
		if value == item.Priority {
			index = position
			break
		}
	}
	next := priorityLadder[clamp(index+delta, 0, len(priorityLadder)-1)]
	if next == item.Priority {
		return
	}
	label := "clear priority: " + item.Title
	if next != "" {
		label = "priority [#" + next + "]: " + item.Title
	}
	result, found := m.patchSelected(item.ID, store.FieldPriority, next, label)
	if !found || !(result.OK() || result.NoChange()) {
		m.Refresh()
		m.Flash("file changed underneath — try again")
		return
	}
	if next != "" {
		m.Flash("priority: [#" + next + "] " + item.Title)
	} else {
		m.Flash("priority cleared: " + item.Title)
	}
	id := item.ID
	m.Refresh()
	m.reselect(id)
}

// -- quick forms --------------------------------------------------------------------

// OpenDatePopup is `d`: reschedule the task's own date — the deadline when it
// has one, otherwise the available-from date, and a new deadline when it has
// neither.
func (m *Model) OpenDatePopup() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	target, field := "Deadline (new)", store.FieldDeadline
	if item.Deadline != "" {
		target = "Deadline"
	} else if item.Scheduled != "" {
		target, field = "Available from", store.FieldScheduled
	}
	id, title, state := item.ID, item.Title, item.State
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormDate, Title: "edit date", Prompt: "new " + target,
		Hint: "fri · tomorrow 9am · date time Zone · esc cancels", MinWidth: 50,
		ReturnMode: ReturnList, TargetID: id,
		Submit: func(raw string) string {
			value, err := ParseTemporal(raw, m.currentDate(), m.temporalContext())
			if err != nil {
				return "can't parse \u201c" + raw + "\u201d"
			}
			parsed := value.(*temporal.Value)
			label := FormatTemporal(parsed)
			result, found := m.patchSelectedTemporal(id, field, *parsed,
				"reschedule \u2192 "+label+": "+title)
			if !found {
				return "task no longer exists"
			}
			if !(result.OK() || result.NoChange()) {
				return outcomeMessage(result, "file changed underneath \u2014 reopen")
			}
			m.formSuccess(func() {
				promoted := ""
				if state == "INBOX" {
					promoted = " \u00b7 INBOX \u2192 TODO"
				}
				m.Flash("\u2192 " + title + ": " + label + promoted)
				m.Refresh()
				m.reselect(id)
			})
			return ""
		},
	}))
	_ = m.SetMode(ModeForm)
}

// OpenRecurPopup is `r`: set or clear a recurrence, with a LIVE gloss of what
// the typed cookie means as the hint.
//
// A recurrence rides a date stamp, so a task with no date cannot repeat. That
// is refused BEFORE the popup opens rather than after it is filled in: a form
// whose every submission must fail is worse than no form.
func (m *Model) OpenRecurPopup() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if item.Scheduled == "" && item.Deadline == "" {
		m.Flash("add an Available from date or deadline first \u2014 recurrence needs a date")
		return
	}
	current := "not repeating"
	if item.Recur != "" {
		gloss := item.Recur
		if human := recur.Humanize(item.Recur); human != nil {
			gloss = *human
		}
		current = "now " + gloss
	}
	id, title := item.ID, item.Title
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormRecurrence, Title: "recur", Prompt: "repeat",
		MinWidth: 76, ReturnMode: ReturnList, TargetID: id, Initial: item.Recur,
		Suffix: "(" + current + ")",
		HintFunc: func(text string, width int) string {
			anchor := item.Deadline
			if anchor == "" {
				anchor = item.Scheduled
			}
			return m.recurPreview(text, anchor, width)
		},
		Submit: func(raw string) string {
			result := recur.Parse(raw, ".+")
			if result.Error != "" {
				return result.Error
			}
			value := result.Canonical
			label := "recur " + value + ": " + title
			if value == "off" {
				value, label = "", "recur off: "+title
			}
			outcome, found := m.patchSelected(id, store.FieldRecurrence, value, label)
			if !found {
				return "task no longer exists"
			}
			if !(outcome.OK() || outcome.NoChange()) {
				return outcomeMessage(outcome, "file changed underneath \u2014 reopen")
			}
			m.formSuccess(func() {
				if value == "" {
					m.Flash("\u21bb off: " + title)
				} else {
					gloss := value
					if human := recur.Humanize(value); human != nil {
						gloss = *human
					}
					m.Flash("\u21bb " + gloss + ": " + title)
				}
				m.Refresh()
				m.reselect(id)
			})
			return ""
		},
	}))
	_ = m.SetMode(ModeForm)
}

// RenameProject is `e` on a project header.
func (m *Model) RenameProject() {
	project := m.CurrentProject()
	if project == nil {
		m.Flash("select a project")
		return
	}
	id := project.ID
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormProjectRename, Title: "rename project", Prompt: "title",
		Hint: "esc cancels", MinWidth: 40, ReturnMode: ReturnList,
		Initial: project.Title, TargetID: id,
		Submit: func(raw string) string {
			result := m.app.RenameProject(id, raw, m.operation())
			if result.Invalid() {
				return "title cannot be blank"
			}
			if !(result.OK() || result.NoChange()) {
				return "project no longer exists"
			}
			m.formSuccess(func() {
				m.Flash("renamed: " + strings.TrimSpace(raw))
				m.Refresh()
				m.reselect(id)
			})
			return ""
		},
	}))
	_ = m.SetMode(ModeForm)
}

// CaptureIntoProject is `a` on a project header: append a new TODO to it.
func (m *Model) CaptureIntoProject() {
	project := m.CurrentProject()
	if project == nil {
		m.Flash("select a project")
		return
	}
	id, projectTitle := project.ID, project.Title
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormProjectCapture, Title: "capture into “" + projectTitle + "”",
		Prompt: "task", Hint: "esc cancels", MinWidth: 44, ReturnMode: ReturnList, TargetID: id,
		Submit: func(raw string) string {
			title := strings.TrimSpace(raw)
			if title == "" {
				return "task title cannot be blank"
			}
			result := m.app.CreateTask(application.CreateCommand{
				Title: title, State: "TODO", ParentID: id,
			}, m.operation())
			if !result.OK() {
				return outcomeMessage(result, "could not capture the task")
			}
			newID := id
			if len(result.TouchedIDs) > 0 {
				newID = result.TouchedIDs[0]
			}
			m.formSuccess(func() {
				m.Flash("+ " + title)
				m.Refresh()
				m.reselect(newID)
			})
			return ""
		},
	}))
	_ = m.SetMode(ModeForm)
}

// formSuccess registers the effect to run once the form closes cleanly. It runs
// AFTER the close so a refresh cannot repaint a form that is on its way out.
func (m *Model) formSuccess(effect func()) {
	if m.form != nil {
		m.form.Success = effect
	}
}

// CloseForm dismisses the quick form, running its success effect on a
// successful submit.
func (m *Model) CloseForm(success bool) {
	if m.form == nil {
		return
	}
	destination := m.form.ReturnMode
	if destination == ReturnModal && m.modal == nil {
		destination = ReturnList
	}
	effect := m.form.Success
	m.form = nil
	if destination == ReturnModal {
		m.mode = ModeModal
	} else {
		m.mode = ModeList
	}
	if success && effect != nil {
		effect()
	}
}

// -- project confirmations -----------------------------------------------------------

// ConfirmCompleteProject asks before closing every open task in a section.
func (m *Model) ConfirmCompleteProject(project *taskquery.ProjectView) {
	if project == nil {
		m.Flash("select a project")
		return
	}
	held := *project
	m.pendingProject = &held
	plural := "s"
	if project.OpenCount == 1 {
		plural = ""
	}
	m.OpenModal(ModalContent{
		Title: "Complete project",
		Lines: []string{
			fmt.Sprintf("Complete %d open task%s in %s?", project.OpenCount, plural, project.Title),
			"",
			m.styler.Paint("muted", "Press y to complete · n / esc cancels"),
		},
	}, ModalProjectCompleteConfirm, false)
}

// ConfirmArchiveProject asks before archiving a whole section subtree,
// surfacing any open work it would take with it.
func (m *Model) ConfirmArchiveProject(project *taskquery.ProjectView) {
	if project == nil {
		m.Flash("select a project")
		return
	}
	held := *project
	m.pendingProject = &held
	note := ""
	if project.OpenCount > 0 {
		plural := "s"
		if project.OpenCount == 1 {
			plural = ""
		}
		note = fmt.Sprintf(" with %d open task%s", project.OpenCount, plural)
	}
	m.OpenModal(ModalContent{
		Title: "Archive project",
		Lines: []string{
			"Archive " + project.Title + note + "?",
			"",
			m.styler.Paint("muted", "Press y to archive · n / esc cancels"),
		},
	}, ModalProjectArchiveConfirm, false)
}

func (m *Model) projectCompleteConfirmKey(key string) {
	switch key {
	case "y", "Y", "\r", "\n":
		project := m.pendingProject
		m.CloseModal()
		if project == nil {
			return
		}
		result := m.app.CompleteProject(project.ID, m.operation())
		if !(result.OK() || result.NoChange()) {
			m.Refresh()
			m.Flash("project no longer exists")
			return
		}
		closed := 0
		if result.Project != nil {
			closed = result.Project.Closed
		}
		m.Flash(fmt.Sprintf("✓ closed %d in %s", closed, project.Title))
		m.Refresh()
		m.reselect(project.ID)
	case "n", "N", "\x1b", "q":
		m.CloseModal()
		m.Flash("complete cancelled")
	}
}

func (m *Model) projectArchiveConfirmKey(key string) {
	switch key {
	case "y", "Y", "\r", "\n":
		project := m.pendingProject
		m.CloseModal()
		if project == nil {
			return
		}
		result := m.app.ArchiveProject(project.ID, m.operation())
		if !(result.OK() || result.NoChange()) {
			m.Refresh()
			m.Flash("project no longer exists")
			return
		}
		moved := len(result.TouchedIDs)
		if result.Project != nil && result.Project.Archived > 0 {
			moved = result.Project.Archived
		}
		if m.panel != nil && m.panel.Kind == PanelProjectDetail && m.panel.Identity == project.ID {
			m.panel = nil
		}
		m.Flash(fmt.Sprintf("⤓ archived %s (%d)", project.Title, moved))
		m.Refresh()
	case "n", "N", "\x1b", "q":
		m.CloseModal()
		m.Flash("archive cancelled")
	}
}

// -- the task editor ------------------------------------------------------------------

// StartTaskEdit is `e` in the detail panel: open the durable editor on the
// selected task, focused on one field.
func (m *Model) StartTaskEdit(focus string) {
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	editLayout := m.layoutForEditing(true)
	if !editLayout.EditablePanel() {
		m.Flash(fmt.Sprintf("task editing needs at least %d×%d terminal cells",
			MinimumEditTerminalWidth(), MinimumEditTerminalHeight(len(editLayout.Footer))))
		return
	}
	if m.suspendedTaskEditor != nil && m.suspendedTaskEditor.TargetID() != item.ID &&
		m.suspendedTaskEditor.Dirty("") {
		if m.suspendedTaskEditor.Missing() {
			m.Flash("deleted task draft remains — y copies the field · esc discards it")
		} else {
			m.Flash("unsaved task draft belongs to another row — reselect it to resume")
		}
		return
	}
	if m.suspendedTaskEditor != nil && m.suspendedTaskEditor.TargetID() == item.ID {
		outcome := m.suspendedTaskEditor.Refresh()
		if outcome.Status == EditorMissing {
			m.showSuspendedEditorPanel()
			m.Flash("Task no longer exists; local field retained for copy or discard")
			return
		}
		m.taskEditor = m.suspendedTaskEditor
		m.suspendedTaskEditor = nil
		m.panel = m.suspendedTaskPanel
		m.suspendedTaskPanel = nil
		message := m.editorMessage
		if outcome.Message != "" {
			message = outcome.Message
		}
		m.editorMessage = message
		m.mode = ModeTaskEdit
		if message != "" {
			m.Flash(message)
		}
		return
	}
	m.suspendedTaskEditor = nil
	m.suspendedTaskPanel = nil
	session, err := NewTaskEditorSession(TaskEditorOptions{
		App:       m.app,
		Read:      func() *application.ReadModel { return m.read },
		Operation: m.operation,
		Today:     m.currentDate,
		Context:   m.temporalContext(),
		TargetID:  item.ID,
		Focus:     focus,
		ContextOptions: func() []string {
			return m.tokensFromStore(func(item store.Item) []string { return item.Contexts })
		},
		TagOptions: func() []string {
			return m.tokensFromStore(func(item store.Item) []string { return item.Tags })
		},
	})
	if err != nil {
		m.Flash("cannot edit: " + err.Error())
		return
	}
	if session.Missing() {
		m.Flash("task no longer exists")
		return
	}
	m.SetTaskEditor(session)
	if err := m.SetMode(ModeTaskEdit); err != nil {
		m.SetTaskEditor(nil)
		m.Flash(err.Error())
	}
}

// CloseTaskEdit leaves the editor.
func (m *Model) CloseTaskEdit(message string) {
	targetID := ""
	if m.taskEditor != nil {
		targetID = m.taskEditor.TargetID()
	}
	m.taskEditor = nil
	m.suspendedTaskEditor = nil
	m.suspendedTaskPanel = nil
	m.editorMessage = ""
	m.mode = ModeList
	if message != "" {
		m.Flash(message)
	}
	m.Refresh()
	if targetID != "" && m.selectedTarget(targetID) {
		m.showDetail()
	} else {
		m.panel = nil
	}
}

// reconcileEditorLayout suspends an editor that can no longer fit. The
// session survives intact and can be resumed with e when its target is visible
// again; hidden destructive prompts are disarmed by Suspend.
func (m *Model) reconcileEditorLayout() {
	layout := m.layout()
	if m.taskEditor == nil || layout.EditablePanel() {
		return
	}
	editor := m.taskEditor
	outcome := editor.Suspend()
	m.suspendedTaskPanel = m.panel
	m.suspendedTaskEditor = editor
	m.taskEditor = nil
	m.mode = ModeList
	m.editorMessage = outcome.Message
	m.showDetail()
	m.Flash(fmt.Sprintf("editing paused — resize to at least %d×%d; e resumes · %s",
		MinimumEditTerminalWidth(), MinimumEditTerminalHeight(len(layout.Footer)), outcome.Message))
}

func (m *Model) showSuspendedEditorPanel() {
	editor := m.suspendedTaskEditor
	if editor == nil {
		return
	}
	title := "task draft · target not visible"
	explanation := "Task exists but is hidden from the canonical views."
	canonicalView := m.suspendedTargetCanonicalView()
	if editor.Missing() {
		title = "task draft · target deleted"
		explanation = "Task no longer exists; local field retained."
	} else if canonicalView != "" {
		explanation = fmt.Sprintf("Task left %s; switch to %s to resume.", m.view, canonicalView)
	}
	guidance := "y copies field · esc discards draft"
	if canonicalView != "" {
		guidance = "switch view + e resumes · y copies · esc discards"
	}
	if editor.Missing() {
		guidance = "y copies field · esc discards draft"
	}
	lines := []string{explanation, "Draft: " + valueText(editor.CopyValue()), guidance}
	m.panel = NewRightPanel(title, PanelSuspendedTaskEdit, editor.TargetID(), lines)
	if canonicalView != "" {
		m.Flash(fmt.Sprintf("paused task draft: switch to %s to resume · y copies · esc discards", canonicalView))
	} else {
		m.Flash("paused task draft: target is not selectable · y copies · esc discards")
	}
}

func (m *Model) suspendedTargetCanonicalView() string {
	if m.suspendedTaskEditor == nil || m.suspendedTaskEditor.Missing() || m.read == nil {
		return ""
	}
	targetID := m.suspendedTaskEditor.TargetID()
	queries := m.read.Queries()
	for _, tab := range Tabs {
		request := BuildRequest{
			View:         tab.Key,
			Styler:       m.styler,
			Queries:      queries,
			Items:        m.read.Items(),
			Tree:         queries.Tree().Roots,
			UseTree:      true,
			Collapsed:    map[string]bool{},
			ShowDeferred: m.showDeferred,
			UrgentDays:   m.paths.UrgentDays,
		}
		if tab.Key == ViewProjects {
			request.Projects = queries.Projects()
		}
		for _, row := range BuildRows(request) {
			if row.Item != nil && row.Item.ID == targetID {
				return tab.Key
			}
		}
	}
	return ""
}

func (m *Model) suspendedTargetVisible() bool {
	if m.suspendedTaskEditor == nil {
		return false
	}
	for _, row := range m.rows {
		if row.Item != nil && row.Item.ID == m.suspendedTaskEditor.TargetID() {
			return true
		}
	}
	return false
}

func (m *Model) reconcileSuspendedAfterNavigation() {
	if m.suspendedTaskEditor == nil {
		return
	}
	if m.suspendedTargetVisible() && m.selectedTarget(m.suspendedTaskEditor.TargetID()) {
		m.showDetail()
		m.Flash("paused task draft selected — e resumes")
		return
	}
	m.showSuspendedEditorPanel()
}

func (m *Model) tokensFromStore(pick func(store.Item) []string) []string {
	if m.read == nil {
		return nil
	}
	out := []string{}
	for _, item := range m.read.Items() {
		out = append(out, pick(item)...)
	}
	return out
}

// -- shared helpers ----------------------------------------------------------------------

func (m *Model) jumpView(key string) {
	if len(key) == 1 && key[0] >= '1' && key[0] <= '6' {
		m.SwitchView(Tabs[key[0]-'1'].Key)
	}
}

func (m *Model) startFilter() {
	m.filterInput = m.filter
	_ = m.SetMode(ModeFilter)
}

// temporalContext is the session clock: the model's injected `now` read in the
// configured zone. It is the ONE place the TUI turns an instant into a context,
// so every read, every write and every rendered date agree about the moment.
func (m *Model) temporalContext() temporal.Context {
	context, err := temporal.NewContext(m.now(), m.paths.Timezone, m.paths.TimeFormat)
	if err != nil {
		return temporal.Context{}
	}
	return context
}

// outcomeMessage is the user-visible sentence for a refused mutation: the
// store's own words when it gave any, and the caller's fallback otherwise.
func outcomeMessage(outcome application.Outcome, fallback string) string {
	if len(outcome.Errors) > 0 {
		return outcome.Errors[0]
	}
	return fallback
}

func (m *Model) requestQuit() {
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	if editor != nil && editor.Dirty("") {
		outcome := editor.RequestQuit()
		m.quitReturnModal, m.quitReturnMode = m.modal, m.mode
		m.quitReturnMessage = m.editorMessage
		lines := []string{outcome.Message}
		if m.queueHasWork() {
			lines = append(lines, "Quitting also cancels/discards "+m.agentWorkSummary()+".")
		}
		lines = append(lines,
			"Press y or Return to discard the draft and quit.",
			"Press n or Escape to keep the draft and continue.",
			"Ctrl-C and q do not confirm this prompt.")
		m.modal = NewModal(ModalOptions{Title: "Discard unsaved task draft?",
			Lines: lines, Kind: ModalTaskDraftQuitConfirm})
		m.mode = ModeModal
		m.Flash("unsaved task draft — y/return discards and quits · n/esc keeps editing")
		return
	}
	if m.queueHasWork() {
		m.agentQuitPending = true
		m.quitReturnModal, m.quitReturnMode = m.modal, m.mode
		m.quitReturnMessage = m.editorMessage
		m.modal = NewModal(ModalOptions{Title: "Quit with agent work pending?", Lines: []string{
			"Quitting cancels/discards " + m.agentWorkSummary() + ".",
			"Press y or Return to quit.",
			"Press n or Escape to keep the queue running.",
			"Ctrl-C and q do not confirm this prompt.",
		}, Kind: ModalAgentQuitConfirm})
		m.mode = ModeModal
		m.Flash("agent work pending — y/return quits · n/esc keeps running")
		return
	}
	m.Save()
	m.quitting = true
}

func (m *Model) agentWorkSummary() string {
	parts := []string{}
	if m.queue != nil && m.queue.Active() {
		parts = append(parts, "the active request")
	}
	if pending := m.pendingCount(); pending > 0 {
		noun := "requests"
		if pending == 1 {
			noun = "request"
		}
		parts = append(parts, fmt.Sprintf("%d queued %s", pending, noun))
	}
	return strings.Join(parts, " and ")
}
