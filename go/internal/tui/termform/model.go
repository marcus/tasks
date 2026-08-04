package termform

// Context is the read-only view of the form a field sees while deciding
// anything: every value, every baseline, who has focus, and the current errors.
//
// It is passed BY VALUE and never mutated, which is what lets a field's
// validator read a sibling field — "a recurrence needs one of the two date
// fields" is a rule about the form, not about the field.
type Context struct {
	Values     map[string]any
	Baselines  map[string]any
	FocusedKey string
	Errors     map[string][]string
}

// Get is Ruby's `context[:key]`.
func (c Context) Get(key string) any { return c.Values[key] }

// Baseline is the last persisted value for a key.
func (c Context) Baseline(key string) any { return c.Baselines[key] }

// Dirty reports whether one field's buffer differs from its baseline.
func (c Context) Dirty(key string) bool {
	return !equalValues(c.Values[key], c.Baselines[key])
}

// AnyDirty reports whether ANY field differs from its baseline.
func (c Context) AnyDirty() bool { return len(c.ChangedKeys()) > 0 }

// ChangedKeys is every dirty key, in a stable order. Ruby iterates a Hash whose
// order is insertion order; a Go map's is randomized, so this sorts — otherwise
// two runs over identical data would produce different commit requests.
func (c Context) ChangedKeys() []string {
	out := []string{}
	for _, key := range sortedKeys(c.Values) {
		if c.Dirty(key) {
			out = append(out, key)
		}
	}
	return out
}

// Result is what a field's HandleEvent returns: nil means "not mine".
type Result struct {
	// Status is Handled or Changed.
	Status TransitionType
	Value  any
}

// HandledResult and ChangedResult build the two shapes.
func HandledResult(value any) *Result { return &Result{Status: Handled, Value: value} }
func ChangedResult(value any) *Result { return &Result{Status: Changed, Value: value} }

// Field is one editable value plus its behavior.
//
// Ruby subclasses a Field class; Go composes. Every field embeds Base, which
// supplies the whole default protocol, and overrides only what it owns. The
// interface is what Form talks to.
type Field interface {
	Key() string
	LabelFor(context Context) string
	IsVisible(context Context) bool
	IsEnabled(context Context) bool
	IsRequired(context Context) bool
	Initial() any
	InitialBaseline() any
	Metadata() map[string]any
	MetadataFor(value any, context Context) map[string]any
	CursorFor(value any, context Context) *int
	// HandleEvent consumes one event, or returns nil to leave it to Form's
	// navigation and commit protocol.
	HandleEvent(event Event, value any, context Context) *Result
	NormalizeValue(value any) any
	// SyncValue reconciles a stateful field's private editing buffer after the
	// form's value changed underneath it.
	SyncValue(value any)
	// FocusGained lets a field position its private cursor for entry. It must
	// tolerate repeats — initial focus and commit round-trips both re-apply it.
	FocusGained(value any, context Context)
	ValidationErrors(value any, context Context) []string
}

// Base is the default Field implementation every concrete field embeds.
type Base struct {
	FieldKey string
	Label    string
	Value    any
	// Baseline defaults to Value when BaselineSet is false, which is Ruby's
	// `baseline: UNSET` sentinel: a field with no separate baseline starts clean.
	Baseline    any
	BaselineSet bool
	// Visible, Enabled and Required are the reactive properties. A nil func is
	// the constant in VisibleFixed/EnabledFixed/RequiredFixed.
	Visible       func(Context) bool
	VisibleFixed  bool
	Enabled       func(Context) bool
	EnabledFixed  bool
	Required      func(Context) bool
	RequiredFixed bool
	// Validate is the field's own rules. Each returns "" for "no problem".
	Validate []func(value any, context Context) string
	Meta     map[string]any
}

// NewBase fills the defaults a caller almost always wants: visible, enabled,
// not required.
func NewBase(key, label string, value any) Base {
	if label == "" {
		label = key
	}
	return Base{FieldKey: key, Label: label, Value: value, VisibleFixed: true, EnabledFixed: true}
}

func (b *Base) Key() string { return b.FieldKey }

func (b *Base) LabelFor(Context) string { return b.Label }

func (b *Base) IsVisible(context Context) bool {
	if b.Visible != nil {
		return b.Visible(context)
	}
	return b.VisibleFixed
}

func (b *Base) IsEnabled(context Context) bool {
	if b.Enabled != nil {
		return b.Enabled(context)
	}
	return b.EnabledFixed
}

func (b *Base) IsRequired(context Context) bool {
	if b.Required != nil {
		return b.Required(context)
	}
	return b.RequiredFixed
}

func (b *Base) Initial() any { return b.Value }

func (b *Base) InitialBaseline() any {
	if b.BaselineSet {
		return b.Baseline
	}
	return b.Value
}

func (b *Base) Metadata() map[string]any {
	if b.Meta == nil {
		return map[string]any{}
	}
	return b.Meta
}

func (b *Base) MetadataFor(any, Context) map[string]any { return b.Metadata() }

func (b *Base) CursorFor(any, Context) *int { return nil }

func (b *Base) HandleEvent(Event, any, Context) *Result { return nil }

func (b *Base) NormalizeValue(value any) any { return value }

func (b *Base) SyncValue(any) {}

func (b *Base) FocusGained(any, Context) {}

// ValidationErrors is the required check plus the field's own validators, in
// Ruby's order — "is required" first, so an empty required field says the
// obvious thing rather than a parse complaint about "".
func (b *Base) ValidationErrors(value any, context Context) []string {
	errors := []string{}
	if b.IsRequired(context) && blankValue(value) {
		errors = append(errors, "is required")
	}
	for _, validator := range b.Validate {
		if message := validator(value, context); message != "" {
			errors = append(errors, message)
		}
	}
	return errors
}

func blankValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []string:
		return len(typed) == 0
	}
	return isBlankNil(value)
}

// -- groups ---------------------------------------------------------------------

// Group is a named run of fields. It exists so a whole section can be hidden or
// disabled together, and so the renderer has a heading to paint.
type Group struct {
	Key          string
	Label        string
	Fields       []Field
	Visible      func(Context) bool
	Enabled      func(Context) bool
	VisibleFixed bool
	EnabledFixed bool
	Meta         map[string]any
}

// NewGroup builds a visible, enabled group.
func NewGroup(key, label string, fields ...Field) Group {
	return Group{Key: key, Label: label, Fields: fields, VisibleFixed: true, EnabledFixed: true}
}

func (g Group) isVisible(context Context) bool {
	if g.Visible != nil {
		return g.Visible(context)
	}
	return g.VisibleFixed
}

func (g Group) isEnabled(context Context) bool {
	if g.Enabled != nil {
		return g.Enabled(context)
	}
	return g.EnabledFixed
}

// -- the render model -------------------------------------------------------------

// Row is one field as the renderer sees it. Every decision the renderer makes
// is answered here, so the renderer never asks the form a question.
type Row struct {
	Key      string
	GroupKey string
	Label    string
	Value    any
	Index    int
	Enabled  bool
	Focused  bool
	// Pending marks the field whose commit the host has not answered yet. It
	// keeps rendering even if reactivity hid it, because a request the user
	// cannot see is a request they cannot resolve.
	Pending  bool
	Dirty    bool
	Required bool
	Errors   []string
	Cursor   *int
	Metadata map[string]any
}

// Error is the first error, or "".
func (r Row) Error() string {
	if len(r.Errors) == 0 {
		return ""
	}
	return r.Errors[0]
}

// RenderGroup is one group's rows.
type RenderGroup struct {
	Key      string
	Label    string
	Rows     []Row
	Enabled  bool
	Metadata map[string]any
}

// RenderModel is the whole form, renderer-neutral.
type RenderModel struct {
	Groups     []RenderGroup
	Rows       []Row
	FocusedKey string
	Errors     map[string][]string
	focusedRow *Row
}

// FocusedRow is the row with focus, or nil.
func (m RenderModel) FocusedRow() *Row { return m.focusedRow }

// FocusedRowIndex is the focused row's index, or -1.
func (m RenderModel) FocusedRowIndex() int {
	if m.focusedRow == nil {
		return -1
	}
	return m.focusedRow.Index
}

// CommitRequest is one save-on-blur handshake in flight.
//
// It carries the EXPECTED BASELINE as well as the proposed value, because the
// host's answer arrives later and the field may have been refreshed in between.
// Without the expectation, accepting a stale commit would silently overwrite an
// external edit — the exact failure save-on-blur exists to prevent.
type CommitRequest struct {
	Token            int
	Values           map[string]any
	ChangedKeys      []string
	FocusKey         string
	FieldKey         string
	ProposedValue    any
	ExpectedBaseline any
	IntendedFocus    string
	Direction        string
}
