package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/tui/termform"
)

// FieldModal is the TUI's reusable MULTI-field popup.
//
// QuickForm is the single-field popup: one prompt, one value, one error line.
// A prompt that needs three different KINDS of answer — a typed name, a choice
// from a vocabulary the user configured, and a paragraph of instructions —
// cannot be spelled as one text field without inventing a word grammar, and a
// word grammar is exactly the thing that made the old delegate prompt dangerous.
//
// What this owns, and what it deliberately does not:
//
//   - It owns focus, per-field validation, the geometry, and the HIT MAP. The
//     hit map is recorded by the paint, not re-derived from the field list, so
//     a pointer cannot reach a cell the box is not currently painting — on
//     either axis. That is the same rule the palettes follow. What the cell
//     MEANT is a separate question, since the vocabulary behind it is a runtime
//     func that can change between the paint and the click; fieldmodalinput.go
//     says how that is resolved.
//   - It does not own the vocabulary. A choice field takes a func, called on
//     every render, so a list of modes that comes from user config is read at
//     the moment it is painted rather than baked in when the modal was built.
//   - It does not own the mutation. Submit returns a message on refusal and ""
//     on success, so a caller reports its own refusal in its own words.
//
// The editing machinery is termform's: Input and TextArea are the same buffers
// the task editor uses, including their grapheme handling, their scroll offsets
// and now their click-to-caret inverse. The choice field is local, because
// termform.Select spends Return on opening and picking, and in a modal Return
// belongs to submit.
//
// GEOMETRY IS FIXED AT CONSTRUCTION. Every field reserves its rows — value rows,
// option rows, and its own hint row — whether or not it currently has anything
// to say. That is what makes an inline validation error cost zero rows: the
// error is painted in the hint's slot. Width is measured once, from the whole
// content, and cached. Neither the box nor its centered position moves while it
// is being used, which is the Modal invariant restated for a form.
type FieldModal struct {
	kind       FieldModalKind
	title      string
	targetID   string
	returnMode ReturnMode
	minWidth   int

	fields  []*modalField
	byKey   map[string]*modalField
	actions []FieldModalAction

	submitLabel string
	cancelLabel string
	submit      func(values map[string]string) string

	focus int
	// err is the host's refusal — a rejected write — which outranks the fields'
	// own validation because it is the newer news.
	err string
	// guard is the unsaved-changes latch. It arms on the first cancel of a
	// dirty modal and disarms on any other input, mirroring the task-draft quit
	// confirmation: a destructive discard always takes two deliberate gestures.
	guard bool
	// variant is the box's border look. Neutral keeps every existing modal
	// unpainted; a host asks for accent/warning/danger when the border itself
	// carries news.
	variant BoxVariant
	// armedAction is the extra affordance standing behind its first press,
	// painted in the armed danger look until the second press acts or anything
	// clears it. It mirrors the confirmation-message arming the delegate modal
	// already does through SetError, so the button and the status line tell
	// the same story.
	armedAction string

	width int
	// layout and renderWidth are the last paint's hit map and its painted width,
	// which together bound both axes of a click.
	layout      []fieldModalLine
	renderWidth int

	// scrollDrag is an in-flight scrollbar drag: which surface owns the thumb,
	// where inside the thumb the pointer grabbed, and where the track sits in
	// box rows. The offset is recomputed from the GRAB each motion via
	// OffsetAtRow — never accumulated per event — so a dropped or coalesced
	// motion event cannot make the thumb drift away from the pointer.
	scrollDrag *fieldModalScrollDrag

	// Success is the effect to run after a successful submit, set by the submit
	// callback itself, exactly as QuickForm does it.
	Success func()
	// OnAction runs an extra affordance and says what happens next, so a caller
	// can answer "release" with a confirmation of its own, or with an immediate
	// Cancelled that closes the modal. A nil handler makes the actions inert.
	OnAction func(id string) FieldModalOutcome
}

// FieldModalKind names which multi-field modal is open, so a test and a hit
// test can both say which popup they mean.
type FieldModalKind string

// The multi-field modals this build has. The delegate modal belongs to its own
// packet; the fixture kind is the general-purpose one the tests drive.
const (
	FieldModalDelegate FieldModalKind = "delegate"
	FieldModalGeneric  FieldModalKind = "generic"
)

// FieldKind is which control a field paints.
type FieldKind string

// The three field kinds.
const (
	// FieldText is a single-line editor.
	FieldText FieldKind = "text"
	// FieldChoice is a list over a RUNTIME vocabulary.
	FieldChoice FieldKind = "choice"
	// FieldTextArea is a wrapped multi-line editor.
	FieldTextArea FieldKind = "text_area"
)

// FieldOption is one entry in a choice field's vocabulary.
type FieldOption struct {
	Value string
	Label string
}

// FieldSpec declares one field.
type FieldSpec struct {
	Key   string
	Label string
	Kind  FieldKind
	// Hint is the steady guidance shown under the field. An inline validation
	// error replaces it in place, so neither ever changes the row count.
	Hint    string
	Initial string
	// Options is the runtime vocabulary, re-read on every render. Nil is an
	// empty vocabulary, which a choice field renders as "(no options available)"
	// rather than as a missing control.
	Options func() []FieldOption
	// FreeText lets a choice field keep typed text that matches no option,
	// which is how "pick a known assignee OR type an address" is one field
	// instead of two.
	FreeText bool
	// Rows is a text area's height; VisibleOptions is a choice list's. Both
	// have defaults and both are fixed for the life of the modal.
	Rows           int
	VisibleOptions int
	// Validate is the field's own rule, returning "" when the value is fine.
	Validate func(value string) string
}

// FieldModalAction is an extra affordance — release, undelegate — with both a
// key and a button. A caller that needs a confirmation runs it on the outcome;
// the component does not decide what is destructive enough to confirm.
type FieldModalAction struct {
	ID    string
	Label string
	// Key is the raw sequence that invokes it, e.g. "\x12" for ctrl-r.
	Key string
	// KeyLabel is how that key is spelled on the button.
	KeyLabel string
}

// FieldModalOptions builds a FieldModal.
type FieldModalOptions struct {
	Kind        FieldModalKind
	Title       string
	Fields      []FieldSpec
	Actions     []FieldModalAction
	SubmitLabel string
	CancelLabel string
	MinWidth    int
	TargetID    string
	ReturnMode  ReturnMode
	// Submit returns "" on success and a user-visible message on refusal.
	Submit func(values map[string]string) string
}

// FieldModalResult is what one gesture did.
type FieldModalResult string

// The outcomes. Guarded is the armed unsaved-changes latch: nothing has been
// discarded yet, and the caller should keep the modal open.
const (
	FieldModalHandled   FieldModalResult = "handled"
	FieldModalChanged   FieldModalResult = "changed"
	FieldModalGuarded   FieldModalResult = "guarded"
	FieldModalCancelled FieldModalResult = "cancelled"
	FieldModalSubmitted FieldModalResult = "submitted"
	FieldModalError     FieldModalResult = "error"
	FieldModalActioned  FieldModalResult = "action"
)

// FieldModalOutcome is a result plus, for an action, which one.
type FieldModalOutcome struct {
	Result   FieldModalResult
	ActionID string
}

func fieldModalHandled() FieldModalOutcome { return FieldModalOutcome{Result: FieldModalHandled} }
func fieldModalChanged() FieldModalOutcome { return FieldModalOutcome{Result: FieldModalChanged} }

// fieldModalScrollDrag is one scrollbar drag's captured state.
type fieldModalScrollDrag struct {
	// key is the owning field; area distinguishes a note's editor from a
	// choice list when both could carry a thumb.
	key  string
	area bool
	// grab is how far inside the thumb the pointer pressed. A drag asks for
	// "thumb top at row minus grab", so the thumb moves with the pointer
	// without snapping its top under it.
	grab int
	// trackTop is the box row of track row zero at press time, so a motion at
	// any box row can be turned back into a track row.
	trackTop int
}

// modalField is one field's live state.
type modalField struct {
	spec    FieldSpec
	initial string
	// input and area are the termform buffers; exactly one is non-nil for a
	// text-ish field, and a free-text choice field also carries an input.
	input *termform.Input
	area  *termform.TextArea
	// selected is a choice field's value; query is its live filter.
	selected string
	query    *termform.Input
	// highlight is the position in the FILTERED list; offset is the list's own
	// scroll, kept separate so a wheel tick can scroll without selecting.
	highlight int
	offset    int
	err       string
	// valueWidth and areaWidth are what the last paint gave the buffers, which
	// is what a click has to measure against.
	valueWidth int
}

// NewFieldModal builds one. A spec with no key or an unknown kind is dropped
// rather than painted as a control nothing can focus.
func NewFieldModal(options FieldModalOptions) *FieldModal {
	modal := &FieldModal{
		kind: options.Kind, title: options.Title, targetID: options.TargetID,
		returnMode: options.ReturnMode, minWidth: options.MinWidth,
		actions: append([]FieldModalAction{}, options.Actions...),
		submit:  options.Submit, byKey: map[string]*modalField{},
		submitLabel: options.SubmitLabel, cancelLabel: options.CancelLabel,
	}
	if modal.returnMode == "" {
		modal.returnMode = ReturnList
	}
	if modal.submitLabel == "" {
		modal.submitLabel = "Save"
	}
	if modal.cancelLabel == "" {
		modal.cancelLabel = "Cancel"
	}
	for _, spec := range options.Fields {
		if spec.Key == "" || modal.byKey[spec.Key] != nil {
			continue
		}
		field := newModalField(spec)
		if field == nil {
			continue
		}
		modal.fields = append(modal.fields, field)
		modal.byKey[spec.Key] = field
	}
	return modal
}

func newModalField(spec FieldSpec) *modalField {
	if spec.Rows <= 0 {
		spec.Rows = 3
	}
	if spec.VisibleOptions <= 0 {
		spec.VisibleOptions = 5
	}
	field := &modalField{spec: spec, initial: spec.Initial}
	switch spec.Kind {
	case FieldText:
		field.input = termform.NewInput(termform.NewBase(spec.Key, spec.Label, spec.Initial))
	case FieldTextArea:
		field.area = termform.NewTextArea(termform.NewBase(spec.Key, spec.Label, spec.Initial))
	case FieldChoice:
		field.selected = spec.Initial
		if spec.FreeText {
			// A free-text choice has ONE buffer, not two: what is typed is both
			// the filter and the value, which is what makes "pick a known
			// assignee or type a new address" a single field.
			field.input = termform.NewInput(termform.NewBase(spec.Key, spec.Label, spec.Initial))
			field.query = field.input
		} else {
			field.query = termform.NewInput(termform.NewBase(spec.Key+"_query", "", ""))
		}
	default:
		return nil
	}
	return field
}

// -- reading -------------------------------------------------------------------------

// Kind, Title, TargetID and ReturnMode are the read accessors a host needs.
func (f *FieldModal) Kind() FieldModalKind { return f.kind }
func (f *FieldModal) Title() string        { return f.title }
func (f *FieldModal) TargetID() string     { return f.targetID }
func (f *FieldModal) ReturnMode() ReturnMode {
	return f.returnMode
}

// Keys is the field order.
func (f *FieldModal) Keys() []string {
	out := make([]string, 0, len(f.fields))
	for _, field := range f.fields {
		out = append(out, field.spec.Key)
	}
	return out
}

// FocusKey is the focused field's key, or "" for an empty modal.
func (f *FieldModal) FocusKey() string {
	if field := f.focused(); field != nil {
		return field.spec.Key
	}
	return ""
}

// SetFocus moves focus to a named field, reporting whether it exists.
func (f *FieldModal) SetFocus(key string) bool {
	for index, field := range f.fields {
		if field.spec.Key == key {
			changed := f.focus != index
			f.focus = index
			if changed {
				field.focusGained()
			}
			return true
		}
	}
	return false
}

// Value is one field's current value.
func (f *FieldModal) Value(key string) string {
	field := f.byKey[key]
	if field == nil {
		return ""
	}
	return field.value()
}

// Values is every field's value.
func (f *FieldModal) Values() map[string]string {
	out := map[string]string{}
	for _, field := range f.fields {
		out[field.spec.Key] = field.value()
	}
	return out
}

// SetValue writes a field from the host.
func (f *FieldModal) SetValue(key, value string) bool {
	field := f.byKey[key]
	if field == nil {
		return false
	}
	field.setValue(value)
	return true
}

// Dirty reports whether anything differs from what the modal opened with.
func (f *FieldModal) Dirty() bool {
	for _, field := range f.fields {
		if field.value() != field.initial {
			return true
		}
	}
	return false
}

// Error is the host's refusal message.
func (f *FieldModal) Error() string { return f.err }

// SetError posts a host refusal without closing the modal. Posting any refusal
// disarms an armed action: the armed button and the confirmation message it
// armed are ONE story, and a refusal that overwrote the message while the
// button stayed inverted would be an armed look with nothing behind it. A caller
// that is arming — posting the confirmation — calls SetArmedAction after.
func (f *FieldModal) SetError(message string) {
	f.err = message
	f.armedAction = ""
}

// FieldError is one field's inline validation message.
func (f *FieldModal) FieldError(key string) string {
	if field := f.byKey[key]; field != nil {
		return field.err
	}
	return ""
}

// SetFieldError posts a host refusal against one field, so a rejection about the
// mode is painted under the mode rather than at the bottom of the box.
func (f *FieldModal) SetFieldError(key, message string) bool {
	field := f.byKey[key]
	if field == nil {
		return false
	}
	field.err = message
	return true
}

// PendingCancel reports the armed unsaved-changes latch.
func (f *FieldModal) PendingCancel() bool { return f.guard }

// SetArmedAction marks one of the modal's actions as standing behind its first
// press: its button inverts to the armed look and, while armed, the box border
// reads as warning — the same news in two places. Arming without a matching
// action id reports false and changes nothing.
func (f *FieldModal) SetArmedAction(id string) bool {
	for _, action := range f.actions {
		if action.ID == id {
			f.armedAction = id
			return true
		}
	}
	return false
}

// ArmedAction reports which action is behind its first press, or "".
func (f *FieldModal) ArmedAction() string { return f.armedAction }

// DisarmCancel drops the armed latch, for a host that interrupted the modal
// with something the user has since answered. An interruption is input like any
// other: the arm must not survive it, or the escape AFTER the interruption
// discards without the confirmation the latch exists to require.
func (f *FieldModal) DisarmCancel() { f.guard = false }

// Options is a choice field's vocabulary as of right now.
func (f *FieldModal) Options(key string) []FieldOption {
	if field := f.byKey[key]; field != nil {
		return field.options()
	}
	return nil
}

func (f *FieldModal) focused() *modalField {
	if f.focus < 0 || f.focus >= len(f.fields) {
		return nil
	}
	return f.fields[f.focus]
}

// -- field state ------------------------------------------------------------------

func (m *modalField) value() string {
	switch {
	case m.spec.Kind == FieldChoice:
		return m.selected
	case m.area != nil:
		return m.area.Text()
	case m.input != nil:
		return m.input.Text()
	}
	return ""
}

func (m *modalField) setValue(value string) {
	switch {
	case m.spec.Kind == FieldChoice:
		m.selected = value
		if m.input != nil {
			m.input.SyncValue(value)
		}
	case m.area != nil:
		m.area.SyncValue(value)
	case m.input != nil:
		m.input.SyncValue(value)
	}
}

func (m *modalField) options() []FieldOption {
	if m.spec.Options == nil {
		return nil
	}
	return m.spec.Options()
}

// filtered narrows the vocabulary by the live query. Prefix matches sort ahead
// of substring matches, so typing "wo" lands on "work" rather than on the first
// option that merely contains those letters.
func (m *modalField) filtered() []FieldOption {
	options := m.options()
	query := ""
	if m.query != nil {
		query = strings.ToLower(strings.TrimSpace(m.query.Text()))
	}
	if query == "" {
		return options
	}
	// A free-text field's buffer starts out holding the value it was prefilled
	// with. Filtering by it would offer only the option already chosen, so a
	// buffer that still IS the selection shows the whole vocabulary; narrowing
	// begins with the first keystroke that changes it.
	if m.spec.FreeText && m.query.Text() == m.selected {
		for _, option := range options {
			if option.Value == m.selected {
				return options
			}
		}
	}
	prefix, contains := []FieldOption{}, []FieldOption{}
	for _, option := range options {
		label := strings.ToLower(option.Label)
		value := strings.ToLower(option.Value)
		switch {
		case strings.HasPrefix(label, query) || strings.HasPrefix(value, query):
			prefix = append(prefix, option)
		case strings.Contains(label, query) || strings.Contains(value, query):
			contains = append(contains, option)
		}
	}
	return append(prefix, contains...)
}

// selectedLabel is what the value row shows: the vocabulary's label for the
// current value, or the value itself when it came from typing.
func (m *modalField) selectedLabel() string {
	for _, option := range m.options() {
		if option.Value == m.selected {
			return option.Label
		}
	}
	return m.selected
}

func (m *modalField) validationError() string {
	if m.spec.Validate == nil {
		return ""
	}
	return m.spec.Validate(m.value())
}

// rows is the field's FIXED row count, its bordered boxes' borders included
// and the hint row included. It must agree exactly with what fieldRows paints,
// because Height is what bounds the viewport — undercounting here scrolls the
// buttons out of the box.
func (m *modalField) rows() int {
	switch m.spec.Kind {
	case FieldTextArea:
		return 1 + m.spec.Rows + 2 + 1 // label, bordered editor, hint
	case FieldChoice:
		return 1 + 3 + m.spec.VisibleOptions + 2 + 1 // label, value box, option list, hint
	default:
		return 1 + 3 + 1 // label, bordered editor, hint
	}
}

// focusGained lets a field position itself for entry: a note opens at the TOP
// rather than at the end of whatever was prefilled, and a choice puts its cursor
// on what is currently selected.
func (m *modalField) focusGained() {
	switch {
	case m.area != nil:
		m.area.FocusGained(nil, termform.Context{})
	case m.spec.Kind == FieldChoice:
		m.revealSelection()
	}
}

// clampList keeps the highlight and the window inside the vocabulary.
//
// It deliberately does NOT drag the window back to the highlight: a wheel tick
// scrolls the offered options without changing the selection, and a repaint that
// re-centered on the selection would undo the tick before it was ever seen.
// Moving the window to follow the highlight is revealHighlight's job, called by
// the gestures that move the highlight.
func (m *modalField) clampList(count int) {
	if count <= 0 {
		m.highlight, m.offset = 0, 0
		return
	}
	m.highlight = clamp(m.highlight, 0, count-1)
	m.offset = clamp(m.offset, 0, max(count-m.spec.VisibleOptions, 0))
}

func (m *modalField) revealHighlight(count int) {
	m.clampList(count)
	if m.highlight < m.offset {
		m.offset = m.highlight
	}
	if m.highlight >= m.offset+m.spec.VisibleOptions {
		m.offset = m.highlight - m.spec.VisibleOptions + 1
	}
}

// revealSelection puts the cursor and the window on the selected option, which
// is what focusing a choice field should show.
func (m *modalField) revealSelection() {
	options := m.filtered()
	m.highlight = 0
	for index, option := range options {
		if option.Value == m.selected {
			m.highlight = index
			break
		}
	}
	m.revealHighlight(len(options))
}
