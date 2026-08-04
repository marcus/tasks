// Package termform is the persistence-neutral form engine the TUI's editor and
// popups are built from — the Go port of Ruby's lib/term_form/.
//
// It owns four things and nothing else: the event vocabulary, the field
// protocol, the focus/commit lifecycle, and a renderer-neutral render model. It
// knows nothing about tasks, terminals, colors, or files. That separation is
// what makes SAVE ON BLUR testable: the whole "leaving a dirty field asks the
// host to persist it, and the host answers accept or reject" handshake is a
// pure state machine here, and the task-domain adapter above it only supplies
// values and answers.
//
// The one structural difference from Ruby: Ruby's values are untyped objects
// and every field asserts the shape it accepts. Go has no such thing, so values
// are `any` and each field normalizes into its OWN shape (string, bool,
// []string, *temporal.Value). Equality — which decides dirty, which decides
// whether a commit is even requested — therefore goes through equalValues
// rather than `==`, because two []string values that are `==` in Ruby are two
// different slice headers here.
package termform

import (
	"reflect"
	"sort"

	"tasks-go/internal/tui/term/input"
)

// KeyBytes is the byte sequence a named key sends, Ruby's Event::KEY_BYTES.
// It is re-exported from the input primitive rather than re-declared, so the
// editor and the form engine can never disagree about what "up" means.
var KeyBytes = input.KeyBytes

// Event is one normalized input. Ruby carries an arbitrary payload Hash; the
// three payload keys any field actually reads are named fields here, so a typo
// is a compile error rather than a silently absent value.
type Event struct {
	Type EventType
	// Key is the semantic key name for a :key event ("up", "return"), or the
	// raw bytes when the host had no name for it.
	Key string
	// Text is the typed or pasted text for :input and :paste events.
	Text string
	// Raw is the transport bytes, retained for diagnostics and as the fallback
	// a field reads when a default binding collapsed an arrow into navigation.
	Raw string
	// IntendedFocus is the field a :commit event wants focus to land on.
	IntendedFocus string
	// Direction rides along with a :focus event.
	Direction string
	// Value rides along with a :change event.
	Value any
}

// EventType is the event vocabulary.
type EventType string

// The event types, in the order lib/term_form/event.rb uses them.
const (
	EventKey      EventType = "key"
	EventInput    EventType = "input"
	EventPaste    EventType = "paste"
	EventNext     EventType = "next"
	EventPrevious EventType = "previous"
	EventFocus    EventType = "focus"
	EventChange   EventType = "change"
	EventCommit   EventType = "commit"
	EventCancel   EventType = "cancel"
)

// KeyEvent builds the event for one named key or raw sequence.
func KeyEvent(key string) Event {
	raw := key
	if bytes, named := KeyBytes[key]; named {
		raw = bytes
	}
	return Event{Type: EventKey, Key: key, Raw: raw}
}

// PasteEvent builds a paste.
func PasteEvent(text string) Event { return Event{Type: EventPaste, Text: text, Raw: text} }

// DecodedKey is Fields::DecodedEvent.key: the byte sequence a field should act
// on, following the DECODED semantics rather than the transport bytes.
//
// The distinction matters at exactly one place and it is load-bearing: the
// default key map folds ↑/↓ into :previous/:next navigation events, and a text
// area still has to be able to read them as vertical cursor motion. So a
// navigation event exposes its raw bytes and nothing else.
func DecodedKey(event Event) string {
	var candidate string
	switch event.Type {
	case EventKey:
		candidate = event.Key
		if candidate == "" {
			candidate = event.Raw
		}
	case EventInput:
		candidate = event.Text
		if candidate == "" {
			candidate = event.Raw
		}
	case EventNext, EventPrevious:
		candidate = event.Raw
	}
	if candidate == "" {
		return ""
	}
	if bytes, named := KeyBytes[candidate]; named {
		return bytes
	}
	return candidate
}

// Command reports Ruby's DecodedEvent.command?: the event IS the semantic
// command, or it is a key event carrying one of the raw sequences that mean it.
func Command(event Event, kind EventType, keys ...string) bool {
	if event.Type == kind {
		return true
	}
	if event.Type != EventKey {
		return false
	}
	decoded := DecodedKey(event)
	for _, key := range keys {
		if decoded == key {
			return true
		}
	}
	return false
}

// -- transitions --------------------------------------------------------------

// TransitionType is what one handled event did.
type TransitionType string

// The transition types, in the order lib/term_form/event.rb declares them.
const (
	Unhandled       TransitionType = "unhandled"
	Handled         TransitionType = "handled"
	Changed         TransitionType = "changed"
	FocusChanged    TransitionType = "focus_changed"
	Invalid         TransitionType = "invalid"
	CommitRequested TransitionType = "commit_requested"
	CommitPending   TransitionType = "commit_pending"
	CommitAccepted  TransitionType = "commit_accepted"
	CommitRejected  TransitionType = "commit_rejected"
	CancelRequested TransitionType = "cancel_requested"
	Refreshed       TransitionType = "refreshed"
)

// Transition is the answer to one event.
type Transition struct {
	Type        TransitionType
	Event       Event
	FocusKey    string
	RenderModel RenderModel
	Request     *CommitRequest
	Errors      map[string][]string
	ChangedKey  string
}

// IsChanged, IsFocusChanged and friends read as Ruby's predicate methods do.
func (t Transition) IsChanged() bool      { return t.Type == Changed }
func (t Transition) IsFocusChanged() bool { return t.Type == FocusChanged }
func (t Transition) IsInvalid() bool      { return t.Type == Invalid }
func (t Transition) IsUnhandled() bool    { return t.Type == Unhandled }

// -- the key map ---------------------------------------------------------------

// KeyMap turns raw bytes into semantic events.
type KeyMap struct{ bindings map[string]Event }

// DefaultBindings is Ruby's KeyMap::DEFAULT_BINDINGS. Tab and ↓ both mean
// "next": a form is a vertical list, and a user who presses ↓ in it expects to
// leave the field — which is what makes save-on-blur reachable without tab.
var DefaultBindings = map[string]EventType{
	"\t":       EventNext,
	"\x1b[B":   EventNext,
	"\x1b[Z":   EventPrevious,
	"\x1b[A":   EventPrevious,
	"\r":       EventCommit,
	"\n":       EventCommit,
	"\x1b":     EventCancel,
	"\x1b\x1b": EventCancel,
}

// NewKeyMap builds a key map over the defaults.
func NewKeyMap(overrides map[string]EventType) *KeyMap {
	bindings := map[string]Event{}
	for raw, kind := range DefaultBindings {
		bindings[raw] = Event{Type: kind}
	}
	for raw, kind := range overrides {
		bindings[raw] = Event{Type: kind}
	}
	return &KeyMap{bindings: bindings}
}

// EventFor maps raw bytes onto a semantic event. Unbound bytes are typed text.
func (k *KeyMap) EventFor(raw string) Event {
	if bound, found := k.bindings[raw]; found {
		return Event{Type: bound.Type, Raw: raw}
	}
	return Event{Type: EventInput, Text: raw, Raw: raw}
}

// -- value equality -------------------------------------------------------------

// equalValues is the ONE comparison the whole engine uses for dirty state,
// commit necessity and value replacement.
//
// It exists because Go's `==` panics on slices and compares pointers on
// pointers, and both shapes are real field values here: a MultiSelect holds
// []string and a date field holds *temporal.Value. Two structurally identical
// values must read as equal or every refresh would look like an edit.
func equalValues(left, right any) bool {
	if left == nil || right == nil {
		return isBlankNil(left) && isBlankNil(right)
	}
	return reflect.DeepEqual(deref(left), deref(right))
}

func isBlankNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map:
		return reflected.IsNil()
	}
	return false
}

// deref compares a pointer by what it points AT. A field that hands back a
// freshly allocated value on every read would otherwise be permanently dirty.
func deref(value any) any {
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Ptr && !reflected.IsNil() {
		return reflected.Elem().Interface()
	}
	return value
}

// copyValue is Support.copy: a defensive copy of the mutable shapes, so a value
// handed to a host and a value held in the form cannot alias.
func copyValue(value any) any {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case map[string]any:
		out := map[string]any{}
		for key, entry := range typed {
			out[key] = copyValue(entry)
		}
		return out
	}
	return value
}

func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
