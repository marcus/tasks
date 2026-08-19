package tui

import (
	"github.com/marcus/tasks/internal/tui/termform"
)

// QuickFormKind names which quick form is open, so a test and a hit test can
// both say which popup they mean.
type QuickFormKind string

// The quick forms this build has. The agent prompt is deliberately absent —
// it belongs to the agent packet.
const (
	QuickFormDate           QuickFormKind = "date"
	QuickFormRecurrence     QuickFormKind = "recurrence"
	QuickFormDeferUntil     QuickFormKind = "defer_until"
	QuickFormProjectRename  QuickFormKind = "project_rename"
	QuickFormProjectCapture QuickFormKind = "project_capture"
	QuickFormSectionDates   QuickFormKind = "section_dates"
	// Delegation is deliberately absent: `D` is a multi-field modal
	// (FieldModalDelegate), because a delegation needs three different KINDS of
	// answer and one text field could only carry them as a word grammar.
	QuickFormWorkRef QuickFormKind = "work_ref"
)

// QuickFormResult is what one key did to a quick form.
type QuickFormResult string

// The four outcomes.
const (
	QuickFormHandled   QuickFormResult = "handled"
	QuickFormChanged   QuickFormResult = "changed"
	QuickFormCancelled QuickFormResult = "cancelled"
	QuickFormSubmitted QuickFormResult = "submitted"
	QuickFormError     QuickFormResult = "error"
)

// QuickForm is the reusable lifecycle for the TUI's small single-field popups.
//
// The caller supplies domain validation and the mutation; this owns input
// editing, paste, the error line, rendering and the return mode. The submit
// callback returns a MESSAGE on failure and "" on success, which is what lets
// every popup report its own refusal in its own words without the form knowing
// anything about tasks.
type QuickForm struct {
	Kind QuickFormKind
	// TargetID is the row the form was opened for. A form outliving its target
	// must not act on whatever the selection became.
	TargetID   string
	ReturnMode ReturnMode

	title    string
	prompt   string
	suffix   string
	minWidth int
	// hint may be a live preview of what the typed value means — the recurrence
	// popup renders its schedule explanation this way — so it is a function of
	// the current text and the cells it may occupy.
	hint   func(text string, width int) string
	submit func(text string) string

	field  *termform.Input
	engine *termform.Form
	err    string
	// Success is the effect to run after a successful submit, set by the submit
	// callback itself. Ruby stores it on the UI state; keeping it on the form
	// means closing the form cannot leave a stale callback behind.
	Success func()
}

// QuickFormOptions builds a QuickForm.
type QuickFormOptions struct {
	Kind       QuickFormKind
	Title      string
	Prompt     string
	Hint       string
	HintFunc   func(text string, width int) string
	MinWidth   int
	ReturnMode ReturnMode
	Initial    string
	// Suffix is the fixed gloss shown after the value — the recurrence popup
	// uses it to say what the task repeats on right now.
	Suffix   string
	TargetID string
	// Submit returns "" on success and a user-visible message on refusal.
	Submit func(text string) string
}

// NewQuickForm builds one.
func NewQuickForm(options QuickFormOptions) *QuickForm {
	base := termform.NewBase("value", options.Prompt, options.Initial)
	field := termform.NewInput(base)
	engine, err := termform.NewForm(
		[]termform.Group{termform.NewGroup("quick", "", field)}, "value", nil)
	if err != nil {
		// The group is a literal above, so this is unreachable; a form that
		// could not be built must still not be a nil pointer downstream.
		return nil
	}
	hint := options.HintFunc
	if hint == nil {
		fixed := options.Hint
		hint = func(string, int) string { return fixed }
	}
	if options.ReturnMode == "" {
		options.ReturnMode = ReturnList
	}
	return &QuickForm{
		Kind: options.Kind, TargetID: options.TargetID, ReturnMode: options.ReturnMode,
		title: options.Title, prompt: options.Prompt, suffix: options.Suffix,
		minWidth: options.MinWidth, hint: hint, submit: options.Submit,
		field: field, engine: engine,
	}
}

// Text is the current input.
func (q *QuickForm) Text() string { return q.field.Text() }

// Cursor is the caret offset.
func (q *QuickForm) Cursor() int { return q.field.Cursor() }

// Error is the last refusal message.
func (q *QuickForm) Error() string { return q.err }

// Title is the popup's title.
func (q *QuickForm) Title() string { return q.title }

// SetText replaces the input through the engine, so a host-driven replacement
// participates in dirty-state rendering instead of mutating a second buffer.
func (q *QuickForm) SetText(text string) {
	q.engine.SetValue("value", text, termform.Event{})
}

// HandleKey routes one raw key.
func (q *QuickForm) HandleKey(key string) QuickFormResult {
	switch key {
	case "\x1b":
		return QuickFormCancelled
	case "\r", "\n":
		return q.Submit()
	}
	transition := q.engine.HandleKey(key)
	if transition.IsChanged() {
		// A keystroke means the user is fixing the thing the error complained
		// about; keeping the old message on screen would be noise.
		q.err = ""
		return QuickFormChanged
	}
	return QuickFormHandled
}

// Paste inserts pasted text.
func (q *QuickForm) Paste(text string) QuickFormResult {
	if q.engine.Handle(termform.PasteEvent(text)).IsChanged() {
		q.err = ""
		return QuickFormChanged
	}
	return QuickFormHandled
}

// Submit runs the caller's mutation.
func (q *QuickForm) Submit() QuickFormResult {
	if q.submit == nil {
		return QuickFormSubmitted
	}
	if message := q.submit(q.Text()); message != "" {
		q.err = message
		return QuickFormError
	}
	return QuickFormSubmitted
}

// Hint resolves the hint against the current input and the cells it may use.
func (q *QuickForm) Hint(width int) string { return q.hint(q.Text(), q.hintBudget(width)) }

// hintBudget is the cells a hint's own text can use: the box takes two columns
// of border and the renderer prefixes every cue with two more.
func (q *QuickForm) hintBudget(width int) int { return max(width-4, 0) }

// Popup renders the form at the given budget.
func (q *QuickForm) Popup(styler Styler, maxWidth, maxHeight int) FormRender {
	natural := max(styler.Width(q.prompt+" "+q.Text()+" "+q.suffix)+10, q.minWidth)
	width := natural
	if maxWidth > 0 {
		width = min(width, maxWidth)
	}
	height := 4
	if maxHeight > 0 {
		height = min(height, maxHeight)
	}
	return RenderForm(styler, FormRenderRequest{
		Model: q.engine.RenderModel(), Width: max(width, 0), Height: max(height, 0),
		Title: q.title, Hint: q.Hint(width), Error: q.err, Suffix: q.suffix,
	})
}
