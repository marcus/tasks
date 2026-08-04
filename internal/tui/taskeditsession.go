package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/termform"
)

// The two editor control keys. Ctrl-S saves the focused field in place;
// Ctrl-O finishes editing, saving the focused buffer first if it is dirty.
const (
	editorCtrlS = "\x13"
	editorCtrlO = "\x0f"
)

// EditorStatus is the outcome vocabulary one editor interaction produces.
type EditorStatus string

// The editor outcomes.
const (
	EditorUnhandled        EditorStatus = "unhandled"
	EditorHandled          EditorStatus = "handled"
	EditorChanged          EditorStatus = "changed"
	EditorFocusChanged     EditorStatus = "focus_changed"
	EditorInvalid          EditorStatus = "invalid"
	EditorConfirmation     EditorStatus = "confirmation"
	EditorConfirmCancelled EditorStatus = "confirmation_cancelled"
	EditorConflicted       EditorStatus = "conflict"
	EditorConflictReloaded EditorStatus = "conflict_reloaded"
	EditorMissing          EditorStatus = "missing"
	EditorFinished         EditorStatus = "finished"
	EditorRefreshed        EditorStatus = "refreshed"
	EditorRevertPending    EditorStatus = "revert_pending"
	EditorReverted         EditorStatus = "reverted"
	EditorSaved            EditorStatus = "saved"
	EditorNoChange         EditorStatus = "no_change"
	EditorQuitReady        EditorStatus = "quit_ready"
	EditorQuitConfirmation EditorStatus = "quit_confirmation"
	EditorQuitConfirmed    EditorStatus = "quit_confirmed"
	EditorQuitCancelled    EditorStatus = "quit_cancelled"
	EditorSuspended        EditorStatus = "suspended"
	EditorCopyKept         EditorStatus = "copy_kept"
)

// EditorOutcome is one interaction's answer.
type EditorOutcome struct {
	Status     EditorStatus
	Transition termform.Transition
	Message    string
	// Field is the field an outcome is about, when it is about one.
	Field string
	// Wrote reports that this outcome CHANGED the store. It is separate from
	// the status because a finish and a save both may or may not have written,
	// and the list has to be rebuilt for exactly the ones that did.
	Wrote bool
}

// Handled reports that the editor consumed the input.
func (o EditorOutcome) Handled() bool { return o.Status != EditorUnhandled }

// EditorConfirmationPrompt is a consequence the user must accept before a save
// lands. It is not a nicety: `state → DONE` on a parent cascades to its open
// descendants, and clearing the last date retires the recurrence with it.
type EditorConfirmationPrompt struct {
	Token   string
	Field   string
	Value   any
	Message string
	Request *termform.CommitRequest
	Finish  bool
}

// EditorConflict is a same-field external edit caught at save time.
type EditorConflict struct {
	Field      string
	LocalValue any
	FreshValue any
}

// TaskEditorSession coordinates one durable task-edit pass: it translates form
// transitions into application-owned patches, and holds every piece of
// recoverable local state while it does.
//
// The design rule throughout is that NOTHING the user typed is ever lost by a
// refusal. A rejected save keeps the buffer; a conflict keeps the buffer AND
// offers to copy it; escape asks twice before discarding a field.
type TaskEditorSession struct {
	app  *application.Application
	read func() *application.ReadModel
	// operation mints the per-write operation context, so the editor's writes
	// carry the TUI's identity like every other read and write it makes.
	operation func() *application.OperationContext
	today     func() temporal.Date

	targetID    string
	coalesceKey string
	editForm    *TaskEditForm
	snapshot    *EditSnapshot
	missing     bool

	pendingConfirmation *EditorConfirmationPrompt
	pendingRevert       string
	pendingQuit         bool
	conflict            *EditorConflict
	keptCopy            any
	lastResult          *application.Outcome
}

// TaskEditorOptions builds a session.
type TaskEditorOptions struct {
	App       *application.Application
	Read      func() *application.ReadModel
	Operation func() *application.OperationContext
	Today     func() temporal.Date
	Context   temporal.Context
	TargetID  string
	Focus     string
	// ContextOptions and TagOptions are the completions the two token fields
	// offer.
	ContextOptions func() []string
	TagOptions     func() []string
}

// NewTaskEditorSession opens an editor over one task.
func NewTaskEditorSession(options TaskEditorOptions) (*TaskEditorSession, error) {
	if strings.TrimSpace(options.TargetID) == "" {
		return nil, fmt.Errorf("task editor requires a stable target id")
	}
	session := &TaskEditorSession{
		app: options.App, read: options.Read, operation: options.Operation,
		today: options.Today, targetID: options.TargetID, coalesceKey: mintCoalesceKey(),
	}
	snapshot, found := NewEditSnapshot(options.App, options.Read(), options.TargetID)
	if !found {
		session.missing = true
		return session, nil
	}
	session.snapshot = snapshot
	form, err := NewTaskEditForm(TaskEditFormOptions{
		Snapshot: snapshot, Today: options.Today, Context: options.Context,
		ContextOptions: options.ContextOptions, TagOptions: options.TagOptions,
		Focus: options.Focus,
	})
	if err != nil {
		return nil, err
	}
	session.editForm = form
	return session, nil
}

// mintCoalesceKey groups a burst of field saves into ONE undo step. Without it
// a user who edited four fields would have to press undo four times to get back
// to where they started, which is not what "undo my edit" means.
func mintCoalesceKey() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "tui-edit"
	}
	return "tui-edit-" + hex.EncodeToString(buffer)
}

// TargetID, EditForm, Snapshot and the predicates are the read surface.
func (s *TaskEditorSession) TargetID() string        { return s.targetID }
func (s *TaskEditorSession) EditForm() *TaskEditForm { return s.editForm }
func (s *TaskEditorSession) Snapshot() *EditSnapshot { return s.snapshot }
func (s *TaskEditorSession) Missing() bool           { return s.missing }

// Form is the underlying engine, or nil for a missing task.
func (s *TaskEditorSession) Form() *termform.Form {
	if s.editForm == nil {
		return nil
	}
	return s.editForm.Form()
}

// FocusedKey is the field the caret is in.
func (s *TaskEditorSession) FocusedKey() string {
	if form := s.Form(); form != nil {
		return form.FocusKey()
	}
	return ""
}

// Dirty reports unsaved edits, in the whole form or in one field.
func (s *TaskEditorSession) Dirty(field string) bool {
	form := s.Form()
	if form == nil {
		return false
	}
	if field == "" {
		return form.AnyDirty()
	}
	return form.Dirty(field)
}

// PendingConfirmation and Conflict expose the two blocking states.
func (s *TaskEditorSession) PendingConfirmation() *EditorConfirmationPrompt {
	return s.pendingConfirmation
}
func (s *TaskEditorSession) Conflict() *EditorConflict { return s.conflict }

// PendingRevert is the field an Escape has armed for discard.
func (s *TaskEditorSession) PendingRevert() string { return s.pendingRevert }

// PendingQuit reports an armed quit confirmation.
func (s *TaskEditorSession) PendingQuit() bool { return s.pendingQuit }

// CopyValue is the value a missing-task editor still holds for the user to
// copy — the whole reason a vanished task does not simply close the editor.
func (s *TaskEditorSession) CopyValue() any {
	if s.keptCopy != nil {
		return s.keptCopy
	}
	if form := s.Form(); form != nil && form.FocusKey() != "" {
		return form.Value(form.FocusKey())
	}
	return nil
}

// RenderModel is what the panel paints.
func (s *TaskEditorSession) RenderModel() termform.RenderModel {
	if form := s.Form(); form != nil {
		return form.RenderModel()
	}
	return termform.RenderModel{}
}

// -- input -----------------------------------------------------------------------

// HandleKey routes one raw key through the editor.
func (s *TaskEditorSession) HandleKey(key string) EditorOutcome {
	if s.missing {
		return EditorOutcome{Status: EditorMissing, Message: "Task no longer exists"}
	}
	switch key {
	case editorCtrlS:
		return s.Save()
	case editorCtrlO:
		return s.Finish()
	}
	if s.pendingConfirmation != nil {
		return s.handleConfirmationKey(key)
	}

	escape := key == "\x1b"
	secondRevert := s.pendingRevert != "" && escape && s.pendingRevert == s.FocusedKey()
	if !escape {
		s.pendingRevert = ""
	}
	if secondRevert && !s.pickerOpen() {
		return s.revertDirtyField()
	}

	transition := s.Form().HandleKey(key)
	switch transition.Type {
	case termform.CommitRequested:
		return s.commitRequest(transition.Request, false)
	case termform.Invalid:
		return EditorOutcome{Status: EditorInvalid, Transition: transition,
			Message: firstError(transition.Errors)}
	case termform.CancelRequested:
		return s.handleCancel(transition)
	case termform.Unhandled:
		return EditorOutcome{Status: EditorUnhandled, Transition: transition}
	}
	if transition.IsChanged() || transition.IsFocusChanged() {
		s.pendingRevert = ""
	}
	return EditorOutcome{Status: EditorStatus(transition.Type), Transition: transition}
}

// Paste inserts pasted text into the focused field.
func (s *TaskEditorSession) Paste(text string) EditorOutcome {
	if s.missing || s.Form() == nil {
		return EditorOutcome{Status: EditorMissing, Message: "Task no longer exists"}
	}
	transition := s.Form().Handle(termform.PasteEvent(text))
	if transition.Type == termform.CommitRequested {
		return s.commitRequest(transition.Request, false)
	}
	return EditorOutcome{Status: EditorStatus(transition.Type), Transition: transition}
}

// Save persists the focused field in place, keeping focus on it.
func (s *TaskEditorSession) Save() EditorOutcome {
	if s.missing {
		return EditorOutcome{Status: EditorMissing, Message: "Task no longer exists"}
	}
	if s.pendingConfirmation != nil {
		return EditorOutcome{Status: EditorConfirmation, Message: s.pendingConfirmation.Message}
	}
	transition := s.Form().RequestCommit(s.FocusedKey(), "", "", termform.Event{})
	if transition.IsInvalid() {
		return EditorOutcome{Status: EditorInvalid, Transition: transition,
			Message: firstError(transition.Errors)}
	}
	if transition.Type != termform.CommitRequested {
		return EditorOutcome{Status: EditorHandled, Transition: transition}
	}
	return s.commitRequest(transition.Request, false)
}

// Finish leaves the editor: immediately when the focused field is clean, and
// only after that field is accepted when it is not. A refusal always leaves the
// editor open and recoverable.
func (s *TaskEditorSession) Finish() EditorOutcome {
	if s.missing {
		return EditorOutcome{Status: EditorMissing, Message: "Task no longer exists"}
	}
	if s.pendingConfirmation != nil {
		return EditorOutcome{Status: EditorConfirmation, Message: s.pendingConfirmation.Message}
	}
	if !s.Dirty(s.FocusedKey()) {
		return EditorOutcome{Status: EditorFinished}
	}
	transition := s.Form().RequestCommit(s.FocusedKey(), "", "", termform.Event{})
	if transition.IsInvalid() {
		return EditorOutcome{Status: EditorInvalid, Transition: transition,
			Message: firstError(transition.Errors)}
	}
	if transition.Type != termform.CommitRequested {
		return EditorOutcome{Status: EditorHandled, Transition: transition}
	}
	return s.commitRequest(transition.Request, true)
}

// Refresh adopts a fresh read. Dirty buffers keep both their text and the
// expectation their eventual patch will be compared against.
func (s *TaskEditorSession) Refresh() EditorOutcome {
	if s.pendingConfirmation != nil {
		return EditorOutcome{Status: EditorConfirmation, Message: s.pendingConfirmation.Message}
	}
	fresh, found := NewEditSnapshot(s.app, s.read(), s.targetID)
	if !found {
		return s.becomeMissing()
	}
	s.snapshot = fresh
	if err := s.editForm.RefreshSnapshot(fresh); err != nil {
		return EditorOutcome{Status: EditorHandled}
	}
	return EditorOutcome{Status: EditorRefreshed}
}

// -- quitting --------------------------------------------------------------------

// RequestQuit asks whether an unsaved draft may be discarded. The DRAFT owns
// this protection so the same guard follows the editor whether it is visible or
// suspended by a resize.
func (s *TaskEditorSession) RequestQuit() EditorOutcome {
	if !s.Dirty("") {
		return EditorOutcome{Status: EditorQuitReady}
	}
	s.pendingQuit = true
	return EditorOutcome{Status: EditorQuitConfirmation,
		Message: "Unsaved task draft will be discarded"}
}

// HandleQuitConfirmation answers the quit prompt.
func (s *TaskEditorSession) HandleQuitConfirmation(key string) EditorOutcome {
	if !s.pendingQuit {
		return EditorOutcome{Status: EditorUnhandled}
	}
	switch key {
	case "y", "Y", "\r", "\n":
		s.pendingQuit = false
		return EditorOutcome{Status: EditorQuitConfirmed, Message: "Unsaved task draft discarded"}
	case "n", "N", "\x1b":
		s.pendingQuit = false
		return EditorOutcome{Status: EditorQuitCancelled,
			Message: "Quit cancelled; unsaved task draft retained"}
	}
	return EditorOutcome{Status: EditorQuitConfirmation,
		Message: "Unsaved task draft will be discarded"}
}

// Suspend hides the editor without losing it. Every buffer and durable baseline
// survives; the transient one-key prompts are DISARMED, because a hidden editor
// must never retain a destructive action a read-mode key could accept.
func (s *TaskEditorSession) Suspend() EditorOutcome {
	switch {
	case s.missing:
		return EditorOutcome{Status: EditorSuspended,
			Message: "Task no longer exists; local field retained for copy or discard"}
	case s.pendingConfirmation != nil:
		s.CancelConfirmation()
		return EditorOutcome{Status: EditorSuspended,
			Message: "Confirmation cancelled while editing paused; local value retained"}
	case s.pendingRevert != "":
		s.pendingRevert = ""
		return EditorOutcome{Status: EditorSuspended,
			Message: "Discard prompt cancelled while editing paused; local value retained"}
	case s.pendingQuit:
		s.pendingQuit = false
		return EditorOutcome{Status: EditorSuspended,
			Message: "Quit cancelled while editing paused; local value retained"}
	case s.conflict != nil:
		return EditorOutcome{Status: EditorSuspended,
			Message: "Edit conflict — field changed externally; local value retained"}
	}
	return EditorOutcome{Status: EditorSuspended, Message: "Editing paused; local value retained"}
}

// -- confirmations ------------------------------------------------------------------

// Confirm accepts a pending consequence and performs the write.
func (s *TaskEditorSession) Confirm() EditorOutcome {
	confirmation := s.pendingConfirmation
	if confirmation == nil {
		return EditorOutcome{Status: EditorHandled}
	}
	outcome := s.persist(confirmation.Request, confirmation.Field, confirmation.Value,
		confirmation.Finish, true)
	if outcome.Status != EditorConflicted {
		s.pendingConfirmation = nil
	}
	return outcome
}

// CancelConfirmation declines a pending consequence, keeping the local value.
func (s *TaskEditorSession) CancelConfirmation() EditorOutcome {
	confirmation := s.pendingConfirmation
	if confirmation == nil {
		return EditorOutcome{Status: EditorHandled}
	}
	s.pendingConfirmation = nil
	transition := s.editForm.RejectCommit(nil, "", confirmation.Request.Token)
	return EditorOutcome{Status: EditorConfirmCancelled, Transition: transition,
		Message: "Change cancelled; local value retained"}
}

func (s *TaskEditorSession) handleConfirmationKey(key string) EditorOutcome {
	switch key {
	case "y", "Y", "\r", "\n":
		return s.Confirm()
	case "n", "N", "\x1b":
		return s.CancelConfirmation()
	}
	return EditorOutcome{Status: EditorConfirmation, Message: s.pendingConfirmation.Message}
}

// -- conflicts ------------------------------------------------------------------------

// ReloadConflict discards the conflicting local buffer and adopts the value
// that actually landed.
func (s *TaskEditorSession) ReloadConflict() EditorOutcome {
	if s.conflict == nil {
		return EditorOutcome{Status: EditorHandled}
	}
	field := s.conflict.Field
	fresh, found := NewEditSnapshot(s.app, s.read(), s.targetID)
	if !found {
		return s.becomeMissing()
	}
	if s.pendingConfirmation != nil {
		s.editForm.RejectCommit(nil, "", s.pendingConfirmation.Request.Token)
		s.pendingConfirmation = nil
	}
	s.snapshot = fresh
	_ = s.editForm.ReloadField(field, fresh)
	_ = s.editForm.RefreshSnapshot(fresh)
	s.conflict = nil
	s.keptCopy = nil
	return EditorOutcome{Status: EditorConflictReloaded, Field: field}
}

// KeepForCopy preserves the local value as an explicit copy payload. The
// conflict stays active until a reload, so this can never become an overwrite.
func (s *TaskEditorSession) KeepForCopy() EditorOutcome {
	if s.conflict == nil {
		return EditorOutcome{Status: EditorHandled}
	}
	s.keptCopy = s.conflict.LocalValue
	return EditorOutcome{Status: EditorCopyKept, Message: "Local value retained for copy"}
}

// -- the write path ----------------------------------------------------------------------

func (s *TaskEditorSession) commitRequest(request *termform.CommitRequest, finish bool) EditorOutcome {
	if request == nil {
		return EditorOutcome{Status: EditorHandled}
	}
	field := request.FieldKey
	if confirmation := s.consequenceFor(request, finish); confirmation != nil {
		s.pendingConfirmation = confirmation
		return EditorOutcome{Status: EditorConfirmation, Message: confirmation.Message, Field: field}
	}
	return s.persist(request, field, request.ProposedValue, finish, false)
}

// persist is the ONE place a task edit reaches the store, and it goes through
// the application facade like every other write in this build.
func (s *TaskEditorSession) persist(request *termform.CommitRequest, field string, value any,
	finish, retainPendingOnConflict bool) EditorOutcome {

	text, typed := s.editForm.SemanticValue(field, value)
	result := s.app.PatchTask(application.Patch{
		ID:    s.targetID,
		Field: s.editForm.PatchField(field),
		Value: text,
		Typed: typed,
		// The expectation is the baseline THIS buffer was dirtied against, not
		// the newest read. That is the narrow conflict check: only a change to
		// the same field refuses, so an unrelated concurrent edit does not cost
		// the user their work.
		Expected:     s.editForm.ExpectedFor(field),
		HistoryLabel: "edit " + normalizeEditField(field) + ": " + s.snapshot.Title,
		// One session is one undo step, however many fields it saved.
		CoalesceKey: s.coalesceKey,
	}, s.operation())
	s.lastResult = &result

	if result.OK() || result.NoChange() {
		fresh, found := NewEditSnapshot(s.app, s.read(), s.targetID)
		if !found {
			return s.becomeMissing()
		}
		s.snapshot = fresh
		s.conflict = nil
		transition, err := s.editForm.AcceptCommit(fresh, request.Token)
		if err != nil {
			return EditorOutcome{Status: EditorHandled, Field: field}
		}
		if finish {
			return EditorOutcome{Status: EditorFinished, Transition: transition,
				Field: field, Wrote: result.OK()}
		}
		status := EditorSaved
		if result.NoChange() {
			status = EditorNoChange
		}
		return EditorOutcome{Status: status, Transition: transition,
			Field: field, Wrote: result.OK()}
	}

	switch {
	case result.Conflict():
		var transition termform.Transition
		if !retainPendingOnConflict {
			transition = s.editForm.RejectCommit(map[string][]string{
				field: {"Changed externally; reload, revert, or keep for copy"},
			}, "", request.Token)
		}
		local := s.Form().Value(field)
		fresh, found := NewEditSnapshot(s.app, s.read(), s.targetID)
		var freshValue any
		if found {
			freshValue = fresh.Value(field)
			s.snapshot = fresh
			_ = s.editForm.RefreshSnapshot(fresh)
		}
		s.conflict = &EditorConflict{Field: field, LocalValue: local, FreshValue: freshValue}
		return EditorOutcome{Status: EditorConflicted, Transition: transition, Field: field,
			Message: "Changed externally; reload, revert, or keep for copy"}
	case result.NotFound():
		s.editForm.RejectCommit(nil, "Task no longer exists", request.Token)
		return s.becomeMissing()
	}

	errors := result.FieldErrors
	if len(errors) == 0 {
		messages := result.Errors
		if len(messages) == 0 {
			messages = []string{"could not save " + normalizeEditField(field)}
		}
		errors = map[string][]string{field: messages}
	}
	transition := s.editForm.RejectCommit(errors, "", request.Token)
	return EditorOutcome{Status: EditorInvalid, Transition: transition, Field: field,
		Message: firstError(errors)}
}

// consequenceFor is the confirmation gate. It exists for the writes whose
// EFFECT is bigger than the field: completing a parent closes its descendants,
// clearing the last date retires the recurrence, adding one promotes an INBOX
// task. Each of those is a surprise if it happens silently.
func (s *TaskEditorSession) consequenceFor(request *termform.CommitRequest, finish bool) *EditorConfirmationPrompt {
	field := request.FieldKey
	value := request.ProposedValue
	old := s.snapshot.Value(field)
	message := ""

	switch field {
	case "state":
		next, _ := value.(string)
		previous, _ := old.(string)
		if next == previous {
			return nil
		}
		switch {
		case next == "DONE" && s.snapshot.Recurrence != "":
			message = "Completing this recurring task advances its recurrence. Continue?"
		case next == "DONE":
			descendants := s.snapshot.OpenDescendantIDs(s.read())
			if len(descendants) == 0 {
				message = "Mark this task done?"
			} else {
				message = fmt.Sprintf("Mark this task done and cascade to %d descendant(s)?",
					len(descendants))
			}
		case next == "CANCELLED":
			message = "Cancel this task?"
		default:
			message = fmt.Sprintf("Change state from %s to %s?", previous, next)
		}
	case "recurrence":
		next, _ := value.(string)
		previous, _ := old.(string)
		if next == previous {
			return nil
		}
		// The prompt asks about MEANING, not spelling: the gloss leads and the
		// canonical value follows, since that is what lands in the record.
		if strings.TrimSpace(next) == "" {
			message = "Clear recurrence?"
		} else {
			canonical := recur.Parse(next, ".+").Canonical
			gloss := canonical
			if humanized := recur.Humanize(canonical); humanized != nil {
				gloss = *humanized
			}
			message = fmt.Sprintf("Set recurrence to %s (%s)?", gloss, canonical)
		}
	case "lead":
		next, _ := value.(string)
		previous, _ := old.(string)
		if next == previous {
			return nil
		}
		if strings.TrimSpace(next) == "" {
			message = "Clear the lead time?"
		} else {
			canonical, ok := lead.Canonical(next)
			if !ok {
				return nil
			}
			anchor := "Available from date"
			if s.snapshot.Deadline != nil {
				anchor = "deadline"
			}
			described := canonical
			if text, described_ok := lead.Describe(canonical); described_ok {
				described = text
			}
			message = fmt.Sprintf("Hide this task until %s its %s (%s)?", described, anchor, canonical)
		}
	case "scheduled", "deadline":
		other := s.snapshot.Deadline
		if field == "deadline" {
			other = s.snapshot.Scheduled
		}
		next, hasNext := value.(*temporal.Value)
		hasNext = hasNext && next != nil
		switch {
		case !hasNext && old != nil && other == nil && s.snapshot.Recurrence != "":
			message = "Clearing the final date also clears recurrence. Continue?"
		case hasNext && s.snapshot.State == "INBOX":
			message = "Adding this date promotes the task from INBOX to TODO. Continue?"
		}
	}
	if message == "" {
		return nil
	}
	return &EditorConfirmationPrompt{
		Token: mintConfirmationToken(), Field: field, Value: value,
		Message: message, Request: request, Finish: finish,
	}
}

func mintConfirmationToken() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "confirm"
	}
	return hex.EncodeToString(buffer)
}

// -- escape and revert ------------------------------------------------------------------

// handleCancel is Escape on a field. A clean field finishes editing; a dirty
// one ARMS a discard and says so, so a reflex Escape cannot throw away work.
func (s *TaskEditorSession) handleCancel(transition termform.Transition) EditorOutcome {
	if s.Dirty(s.FocusedKey()) {
		s.pendingRevert = s.FocusedKey()
		return EditorOutcome{Status: EditorRevertPending, Transition: transition,
			Message: "Press Escape again to discard this field"}
	}
	return EditorOutcome{Status: EditorFinished, Transition: transition}
}

func (s *TaskEditorSession) revertDirtyField() EditorOutcome {
	field := s.FocusedKey()
	s.editForm.RevertField(field)
	s.pendingRevert = ""
	if s.conflict != nil && s.conflict.Field == field {
		s.conflict = nil
	}
	return EditorOutcome{Status: EditorReverted, Field: field,
		Message: "Discarded unsaved " + normalizeEditField(field)}
}

// pickerOpen reports that the focused field owns Escape right now — a date
// field with its calendar open, or a choice field with its list open. Escape
// must close that overlay rather than arm a field discard.
func (s *TaskEditorSession) pickerOpen() bool {
	form := s.Form()
	if form == nil || form.FocusKey() == "" {
		return false
	}
	switch field := form.Field(form.FocusKey()).(type) {
	case *TemporalInput:
		return field.PickerOpen()
	case *termform.DateInput:
		return field.PickerOpen()
	case *termform.Select:
		return field.Open()
	case *termform.MultiSelect:
		return field.Open()
	}
	return false
}

func (s *TaskEditorSession) becomeMissing() EditorOutcome {
	s.missing = true
	s.pendingConfirmation = nil
	return EditorOutcome{Status: EditorMissing, Message: "Task no longer exists"}
}

func firstError(errors map[string][]string) string {
	for _, key := range []string{"base"} {
		if messages := errors[key]; len(messages) > 0 {
			return messages[0]
		}
	}
	// Field order, not map order: two runs over identical data must report the
	// same message.
	for _, field := range editFields {
		if messages := errors[field]; len(messages) > 0 {
			return messages[0]
		}
	}
	for _, messages := range errors {
		if len(messages) > 0 {
			return messages[0]
		}
	}
	return ""
}

var _ = store.FieldTitle
