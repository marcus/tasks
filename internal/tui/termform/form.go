package termform

import (
	"errors"
	"fmt"
)

// Form is the focus and commit lifecycle over a set of grouped fields.
//
// The single most important behavior in this file is request_focus: leaving a
// DIRTY field does not move focus, it asks the host to persist the field and
// waits. That is save-on-blur, and it is why the editor never needs an explicit
// save key — and why a rejected save leaves the cursor exactly where the user
// can fix it.
type Form struct {
	groups       []Group
	fields       []Field
	fieldByKey   map[string]Field
	groupByField map[string]Group

	values    map[string]any
	baselines map[string]any
	errors    map[string][]string
	// validationActive is Ruby's @validation_active: errors are recomputed on
	// every change only AFTER something has already validated once. A form that
	// showed "is required" before the user typed anything would be shouting.
	validationActive bool

	keyMap         *KeyMap
	commitSequence int
	pendingCommit  *CommitRequest
	focusKey       string
}

// ErrDuplicateKey and friends are the construction refusals. They are errors
// rather than panics because a form is built from data (a task snapshot) and a
// malformed one must be reportable, not fatal.
var (
	ErrDuplicateKey = errors.New("duplicate termform key")
	ErrUnknownKey   = errors.New("unknown termform field key")
	ErrNoPending    = errors.New("no commit is pending")
	ErrStaleToken   = errors.New("commit token does not match")
)

// NewForm builds a form. focus names the field to start on; "" starts on the
// first focusable field.
func NewForm(groups []Group, focus string, keyMap *KeyMap) (*Form, error) {
	if keyMap == nil {
		keyMap = NewKeyMap(nil)
	}
	form := &Form{
		groups:       groups,
		fieldByKey:   map[string]Field{},
		groupByField: map[string]Group{},
		values:       map[string]any{},
		baselines:    map[string]any{},
		errors:       map[string][]string{},
		keyMap:       keyMap,
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if seen[group.Key] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateKey, group.Key)
		}
		seen[group.Key] = true
		for _, field := range group.Fields {
			if seen[field.Key()] {
				return nil, fmt.Errorf("%w: %s", ErrDuplicateKey, field.Key())
			}
			seen[field.Key()] = true
			form.fields = append(form.fields, field)
			form.fieldByKey[field.Key()] = field
			form.groupByField[field.Key()] = group
			form.values[field.Key()] = copyValue(field.Initial())
			form.baselines[field.Key()] = copyValue(field.InitialBaseline())
		}
	}
	if focus != "" {
		if _, known := form.fieldByKey[focus]; !known {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, focus)
		}
		form.focusKey = focus
	}
	form.ensureFocus("")
	form.synchronizeFields()
	if form.focusKey != "" {
		form.notifyFocus(form.focusKey)
	}
	return form, nil
}

// Fields is every field, in declaration order.
func (f *Form) Fields() []Field { return f.fields }

// Field looks one up. A missing key is a programming error in the adapter
// above, so it returns nil rather than raising — the caller decides.
func (f *Form) Field(key string) Field { return f.fieldByKey[key] }

// FocusKey is the focused field's key.
func (f *Form) FocusKey() string { return f.focusKey }

// PendingCommit is the in-flight save, or nil.
func (f *Form) PendingCommit() *CommitRequest { return f.pendingCommit }

// Pending reports whether a save is awaiting the host's answer.
func (f *Form) Pending() bool { return f.pendingCommit != nil }

// Value is one field's current buffer.
func (f *Form) Value(key string) any { return copyValue(f.values[key]) }

// Baseline is one field's last persisted value.
func (f *Form) Baseline(key string) any { return copyValue(f.baselines[key]) }

// Values is a copy of every buffer.
func (f *Form) Values() map[string]any {
	out := map[string]any{}
	for key, value := range f.values {
		out[key] = copyValue(value)
	}
	return out
}

// Errors is the current validation state.
func (f *Form) Errors() map[string][]string {
	out := map[string][]string{}
	for key, messages := range f.errors {
		out[key] = append([]string{}, messages...)
	}
	return out
}

// Context is the read-only view fields and validators see.
func (f *Form) Context() Context {
	return Context{Values: f.values, Baselines: f.baselines, FocusedKey: f.focusKey, Errors: f.errors}
}

// Dirty reports whether one field differs from its baseline.
func (f *Form) Dirty(key string) bool { return f.Context().Dirty(key) }

// AnyDirty reports whether any field does.
func (f *Form) AnyDirty() bool { return f.Context().AnyDirty() }

// ChangedKeys is every dirty key.
func (f *Form) ChangedKeys() []string { return f.Context().ChangedKeys() }

// FocusableFields is every field a cursor may land on right now.
func (f *Form) FocusableFields() []Field {
	context := f.Context()
	out := []Field{}
	for _, field := range f.fields {
		if f.fieldFocusable(field, context) {
			out = append(out, field)
		}
	}
	return out
}

// VisibleFields is every field the renderer should paint.
func (f *Form) VisibleFields() []Field {
	context := f.Context()
	out := []Field{}
	for _, field := range f.fields {
		if f.groupByField[field.Key()].isVisible(context) && field.IsVisible(context) {
			out = append(out, field)
		}
	}
	return out
}

// -- focus ----------------------------------------------------------------------

// Focus moves to a named field, going through the save-on-blur handshake when
// the field being left is dirty.
func (f *Form) Focus(key string, event Event, direction string) Transition {
	if !f.focusableKey(key) || f.focusKey == key {
		return f.transition(Handled, event)
	}
	return f.requestFocus(key, event, direction)
}

// FocusNext and FocusPrevious walk the focusable fields, wrapping.
func (f *Form) FocusNext(event Event) Transition     { return f.moveFocus(1, event) }
func (f *Form) FocusPrevious(event Event) Transition { return f.moveFocus(-1, event) }

func (f *Form) moveFocus(offset int, event Event) Transition {
	candidates := f.FocusableFields()
	if len(candidates) == 0 {
		return f.transition(Handled, event)
	}
	index := -1
	for position, field := range candidates {
		if field.Key() == f.focusKey {
			index = position
			break
		}
	}
	target := candidates[0]
	if index >= 0 {
		target = candidates[((index+offset)%len(candidates)+len(candidates))%len(candidates)]
	}
	direction := "next"
	if offset < 0 {
		direction = "previous"
	}
	return f.Focus(target.Key(), event, direction)
}

// requestFocus is save-on-blur.
//
// Three cases, in Ruby's order:
//   - a commit is already pending on a still-dirty field: refuse to move, and
//     say so, so the user is never left with two unresolved saves;
//   - the field being left is dirty: ask the host to persist it and hold focus;
//   - otherwise: just move.
func (f *Form) requestFocus(target string, event Event, direction string) Transition {
	if f.pendingCommit != nil {
		if f.Dirty(f.pendingCommit.FieldKey) {
			return f.transitionWith(CommitPending, event, func(t *Transition) {
				t.Request = f.pendingCommit
			})
		}
		f.pendingCommit = nil
	}
	if f.focusKey != "" && f.Dirty(f.focusKey) {
		return f.RequestCommit(target, direction, f.focusKey, event)
	}
	f.applyFocus(target)
	return f.transition(FocusChanged, event)
}

func (f *Form) applyFocus(key string) {
	changed := key != f.focusKey
	f.focusKey = key
	if changed && key != "" {
		f.notifyFocus(key)
	}
}

func (f *Form) notifyFocus(key string) {
	if field := f.fieldByKey[key]; field != nil {
		field.FocusGained(f.values[key], f.Context())
	}
}

func (f *Form) focusableKey(key string) bool {
	context := f.Context()
	field := f.fieldByKey[key]
	return field != nil && f.fieldFocusable(field, context)
}

func (f *Form) fieldFocusable(field Field, context Context) bool {
	group := f.groupByField[field.Key()]
	return group.isVisible(context) && group.isEnabled(context) &&
		field.IsVisible(context) && field.IsEnabled(context)
}

// ensureFocus keeps focus on a field that still exists and is still reachable.
//
// A pending commit OWNS logical focus until the host answers: reactivity may
// hide or disable that field, and moving focus away would orphan the request.
func (f *Form) ensureFocus(after string) {
	if f.pendingCommit != nil {
		f.applyFocus(f.pendingCommit.FieldKey)
		return
	}
	candidates := f.FocusableFields()
	if f.focusKey != "" {
		for _, field := range candidates {
			if field.Key() == f.focusKey {
				return
			}
		}
	}
	if after != "" {
		position := -1
		for index, field := range f.fields {
			if field.Key() == after {
				position = index
				break
			}
		}
		if position >= 0 {
			// Ruby rotates the declaration order past the field that just
			// vanished, so focus lands on the NEXT reachable field rather than
			// snapping back to the top of the form.
			for offset := 1; offset <= len(f.fields); offset++ {
				candidate := f.fields[(position+offset)%len(f.fields)]
				for _, focusable := range candidates {
					if focusable.Key() == candidate.Key() {
						f.applyFocus(candidate.Key())
						return
					}
				}
			}
			f.applyFocus("")
			return
		}
	}
	if len(candidates) == 0 {
		f.applyFocus("")
		return
	}
	f.applyFocus(candidates[0].Key())
}

// -- values ---------------------------------------------------------------------

// SetValue writes one field's buffer.
func (f *Form) SetValue(key string, value any, event Event) Transition {
	field := f.fieldByKey[key]
	if field == nil {
		return f.transition(Unhandled, event)
	}
	normalized := copyValue(field.NormalizeValue(value))
	if equalValues(f.values[key], normalized) {
		return f.transition(Handled, event)
	}
	previousFocus := f.focusKey
	f.values[key] = normalized
	field.SyncValue(normalized)
	if f.validationActive {
		f.Validate()
	}
	f.ensureFocus(previousFocus)
	transition := f.transition(Changed, event)
	transition.ChangedKey = key
	return transition
}

// Validate recomputes the error set over the focusable fields. A hidden or
// disabled field is deliberately not validated: refusing a save because of a
// rule the user cannot see or reach is a dead end.
func (f *Form) Validate() map[string][]string {
	context := f.Context()
	errors := map[string][]string{}
	for _, field := range f.fields {
		if !f.fieldFocusable(field, context) {
			continue
		}
		if messages := field.ValidationErrors(f.values[field.Key()], context); len(messages) > 0 {
			errors[field.Key()] = messages
		}
	}
	f.errors = errors
	f.validationActive = true
	return f.Errors()
}

// Valid reports whether the whole form passes.
func (f *Form) Valid() bool { return len(f.Validate()) == 0 }

// -- the commit handshake ---------------------------------------------------------

// RequestCommit asks the host to persist the focused field.
//
// Validation runs FIRST and a failure never opens a request: a host must not be
// asked to write a value the form already knows is wrong, and the focus lands
// on the first offending field so the user is looking at the problem.
func (f *Form) RequestCommit(intendedFocus, direction, fieldKey string, event Event) Transition {
	if f.Pending() {
		return f.transitionWith(CommitPending, event, func(t *Transition) { t.Request = f.pendingCommit })
	}
	f.Validate()
	if len(f.errors) > 0 {
		for _, key := range f.orderedErrorKeys() {
			if f.focusableKey(key) {
				f.applyFocus(key)
				break
			}
		}
		return f.transitionWith(Invalid, event, func(t *Transition) { t.Errors = f.Errors() })
	}
	intended := intendedFocus
	if intended == "" {
		intended = f.focusKey
	}
	committed := fieldKey
	if committed == "" {
		committed = f.focusKey
	}
	if committed == "" {
		return f.transition(Handled, event)
	}
	f.commitSequence++
	f.pendingCommit = &CommitRequest{
		Token:            f.commitSequence,
		Values:           f.Values(),
		ChangedKeys:      f.ChangedKeys(),
		FocusKey:         f.focusKey,
		FieldKey:         committed,
		ProposedValue:    copyValue(f.values[committed]),
		ExpectedBaseline: copyValue(f.baselines[committed]),
		IntendedFocus:    intended,
		Direction:        direction,
	}
	return f.transitionWith(CommitRequested, event, func(t *Transition) { t.Request = f.pendingCommit })
}

// orderedErrorKeys walks the errors in FIELD ORDER rather than map order, so
// the field a rejected commit lands on is the same one on every run.
func (f *Form) orderedErrorKeys() []string {
	out := []string{}
	for _, field := range f.fields {
		if _, failed := f.errors[field.Key()]; failed {
			out = append(out, field.Key())
		}
	}
	return out
}

// AcceptCommit resolves a pending save with the host's fresh values.
//
// The committed field is reconciled against the expectation the request
// carried; every OTHER key in the fresh set updates its baseline, and updates
// its buffer only if that buffer was clean. That is the rule that lets an
// external edit land in the form without discarding what the user is typing.
func (f *Form) AcceptCommit(fresh map[string]any, token int) (Transition, error) {
	request, err := f.requirePending(token)
	if err != nil {
		return Transition{}, err
	}
	values := map[string]any{}
	for key, value := range fresh {
		field := f.fieldByKey[key]
		if field == nil {
			return Transition{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		values[key] = copyValue(field.NormalizeValue(value))
	}
	if committed, supplied := values[request.FieldKey]; supplied {
		delete(values, request.FieldKey)
		f.reconcileCommitted(request, committed)
	} else if equalValues(f.baselines[request.FieldKey], request.ExpectedBaseline) {
		f.reconcileCommitted(request, request.ProposedValue)
	} else if !equalValues(f.values[request.FieldKey], f.baselines[request.FieldKey]) {
		return Transition{}, errors.New("commit token is stale after refresh")
	}
	for _, key := range sortedKeys(values) {
		if equalValues(f.values[key], f.baselines[key]) {
			f.values[key] = copyValue(values[key])
		}
		f.baselines[key] = copyValue(values[key])
	}
	f.pendingCommit = nil
	f.errors = map[string][]string{}
	f.validationActive = false
	// Reconcile editing buffers BEFORE focus moves, so focus_gained sees the
	// committed value instead of being undone by a later buffer sync.
	f.synchronizeFields()
	if request.IntendedFocus != "" && f.focusableKey(request.IntendedFocus) {
		f.applyFocus(request.IntendedFocus)
	}
	f.ensureFocus("")
	return f.transitionWith(CommitAccepted, Event{}, func(t *Transition) { t.Request = request }), nil
}

func (f *Form) reconcileCommitted(request *CommitRequest, committed any) {
	current := f.values[request.FieldKey]
	if equalValues(current, request.ProposedValue) ||
		equalValues(current, f.baselines[request.FieldKey]) {
		f.values[request.FieldKey] = copyValue(committed)
	}
	f.baselines[request.FieldKey] = copyValue(committed)
}

// RejectCommit resolves a pending save with the host's refusal. The buffer is
// untouched — a rejected save must never cost the user what they typed.
func (f *Form) RejectCommit(fieldErrors map[string][]string, message string, token int) (Transition, error) {
	request, err := f.requirePending(token)
	if err != nil {
		return Transition{}, err
	}
	f.pendingCommit = nil
	supplied := map[string][]string{}
	for key, messages := range fieldErrors {
		if key != "base" && f.fieldByKey[key] == nil {
			return Transition{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		supplied[key] = append([]string{}, messages...)
	}
	if message != "" {
		supplied["base"] = []string{message}
	}
	if len(supplied) > 0 {
		f.errors = supplied
		f.validationActive = true
	}
	f.ensureFocus("")
	return f.transitionWith(CommitRejected, Event{}, func(t *Transition) {
		t.Request = request
		t.Errors = f.Errors()
	}), nil
}

// Refresh adopts fresh host values. Same rule as AcceptCommit's other keys: a
// dirty buffer keeps its text, every baseline moves.
func (f *Form) Refresh(fresh map[string]any) (Transition, error) {
	values := map[string]any{}
	for key, value := range fresh {
		field := f.fieldByKey[key]
		if field == nil {
			return Transition{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		values[key] = copyValue(field.NormalizeValue(value))
	}
	for _, key := range sortedKeys(values) {
		if equalValues(f.values[key], f.baselines[key]) {
			f.values[key] = copyValue(values[key])
		}
		f.baselines[key] = copyValue(values[key])
	}
	f.synchronizeFields()
	if f.validationActive {
		f.Validate()
	}
	f.ensureFocus("")
	return f.transition(Refreshed, Event{}), nil
}

func (f *Form) requirePending(token int) (*CommitRequest, error) {
	if f.pendingCommit == nil {
		return nil, ErrNoPending
	}
	if token != 0 && token != f.pendingCommit.Token {
		return nil, ErrStaleToken
	}
	return f.pendingCommit, nil
}

func (f *Form) synchronizeFields() {
	for _, field := range f.fields {
		field.SyncValue(f.values[field.Key()])
	}
}

// -- input ------------------------------------------------------------------------

// HandleKey routes one raw byte sequence.
func (f *Form) HandleKey(raw string) Transition { return f.Handle(f.keyMap.EventFor(raw)) }

// Handle routes one event: the focused field first, then the navigation and
// commit protocol.
func (f *Form) Handle(event Event) Transition {
	if event.Type == EventKey {
		raw := event.Raw
		if raw == "" {
			raw = event.Key
		}
		if bytes, named := KeyBytes[raw]; named {
			raw = bytes
		}
		event = f.keyMap.EventFor(raw)
	}
	if f.focusKey != "" {
		field := f.fieldByKey[f.focusKey]
		if result := field.HandleEvent(event, f.values[f.focusKey], f.Context()); result != nil {
			if result.Status == Changed {
				return f.SetValue(f.focusKey, result.Value, event)
			}
			return f.transition(Handled, event)
		}
	}
	switch event.Type {
	case EventNext:
		return f.FocusNext(event)
	case EventPrevious:
		return f.FocusPrevious(event)
	case EventFocus:
		return f.Focus(event.Key, event, event.Direction)
	case EventChange:
		return f.SetValue(event.Key, event.Value, event)
	case EventCommit:
		return f.RequestCommit(event.IntendedFocus, "", "", event)
	case EventCancel:
		return f.transition(CancelRequested, event)
	}
	return f.transition(Unhandled, event)
}

// -- rendering ----------------------------------------------------------------------

// RenderModel projects the whole form for a renderer.
func (f *Form) RenderModel() RenderModel {
	context := f.Context()
	pendingOwner := ""
	if f.pendingCommit != nil {
		pendingOwner = f.pendingCommit.FieldKey
	}
	rowIndex := 0
	groups := []RenderGroup{}
	var focused *Row
	allRows := []Row{}
	for _, group := range f.groups {
		groupVisible := group.isVisible(context)
		ownsPending := false
		for _, field := range group.Fields {
			if field.Key() == pendingOwner {
				ownsPending = true
			}
		}
		if !groupVisible && !ownsPending {
			continue
		}
		groupEnabled := groupVisible && group.isEnabled(context)
		rows := []Row{}
		for _, field := range group.Fields {
			pending := field.Key() == pendingOwner
			visible := groupVisible && field.IsVisible(context)
			if !visible && !pending {
				continue
			}
			value := f.values[field.Key()]
			row := Row{
				Key:      field.Key(),
				GroupKey: group.Key,
				Label:    field.LabelFor(context),
				Value:    value,
				Index:    rowIndex,
				Enabled:  visible && groupEnabled && field.IsEnabled(context),
				Focused:  field.Key() == f.focusKey,
				Pending:  pending,
				Dirty:    context.Dirty(field.Key()),
				Required: field.IsRequired(context),
				Errors:   append([]string{}, f.errors[field.Key()]...),
				Metadata: field.MetadataFor(value, context),
			}
			if row.Focused {
				row.Cursor = field.CursorFor(value, context)
			}
			rowIndex++
			rows = append(rows, row)
		}
		groups = append(groups, RenderGroup{
			Key: group.Key, Label: group.Label, Rows: rows,
			Enabled: groupEnabled, Metadata: group.Meta,
		})
		allRows = append(allRows, rows...)
	}
	for index := range allRows {
		if allRows[index].Focused {
			focused = &allRows[index]
			break
		}
	}
	return RenderModel{
		Groups: groups, Rows: allRows, FocusedKey: f.focusKey,
		Errors: f.Errors(), focusedRow: focused,
	}
}

func (f *Form) transition(kind TransitionType, event Event) Transition {
	return f.transitionWith(kind, event, nil)
}

func (f *Form) transitionWith(kind TransitionType, event Event, decorate func(*Transition)) Transition {
	transition := Transition{
		Type: kind, Event: event, FocusKey: f.focusKey, RenderModel: f.RenderModel(),
	}
	if decorate != nil {
		decorate(&transition)
	}
	return transition
}
