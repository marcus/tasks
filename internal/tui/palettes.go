package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// paletteMaxResults is how many rows either palette shows at once.
const paletteMaxResults = 8

// ReturnMode is where an overlay goes when it closes. A palette opened from a
// modal must return TO that modal, or dismissing it would silently close two
// things at once.
type ReturnMode string

// The two return modes an overlay can carry.
const (
	ReturnList  ReturnMode = "list"
	ReturnModal ReturnMode = "modal"
)

// ActionPalette is the searchable projection of the actions available right
// now. It owns query and selection state only; the model executes the chosen
// registry entry.
type ActionPalette struct {
	entries    []shortcuts.Entry
	returnMode ReturnMode
	// targetID is the task the palette was opened FOR. A detail-context action
	// must never survive the disappearance of that task and then act on
	// whatever the fallback selection happens to be.
	targetID string
	picker   *ChoicePicker
}

// NewActionPalette builds one over a set of available entries.
func NewActionPalette(styler Styler, entries []shortcuts.Entry, returnMode ReturnMode, targetID string) *ActionPalette {
	options := make([]PickerOption, 0, len(entries))
	for _, entry := range entries {
		options = append(options, PickerOption{
			ID:         entry.Handler,
			Label:      entry.Description + "  " + styler.Paint("muted", entry.DisplayKey),
			SearchText: []string{entry.Description, entry.DisplayKey, entry.Handler},
			Kind:       PickerChoice,
			Metadata:   entry,
		})
	}
	return &ActionPalette{
		entries: entries, returnMode: returnMode, targetID: targetID,
		picker: NewChoicePicker(ChoicePickerOptions{
			Title: "actions", Options: options, Mode: SelectSingle,
			AcceptLabel: "run", EmptyLabel: "no matching actions", MaxVisible: paletteMaxResults,
		}),
	}
}

// Picker exposes the underlying picker for rendering and hit testing.
func (a *ActionPalette) Picker() *ChoicePicker { return a.picker }

// ReturnMode and TargetID are what the model needs to close it correctly.
func (a *ActionPalette) ReturnMode() ReturnMode { return a.returnMode }
func (a *ActionPalette) TargetID() string       { return a.targetID }

// PaletteOutcome is what a palette key did.
type PaletteOutcome struct {
	Kind PickerResultKind
	// Entry is set when Kind is PickerAccepted: the action to run.
	Entry   shortcuts.Entry
	Execute bool
}

// HandleKey routes one key and resolves an acceptance back to a registry entry.
func (a *ActionPalette) HandleKey(key string) PaletteOutcome {
	return a.resolve(a.picker.HandleKey(key))
}

// Hit routes one click.
func (a *ActionPalette) Hit(rowOffset int) PaletteOutcome {
	return a.resolve(a.picker.Hit(rowOffset))
}

// Paste inserts pasted text into the query.
func (a *ActionPalette) Paste(text string) PaletteOutcome {
	return a.resolve(a.picker.Paste(text))
}

// Move steps the cursor.
func (a *ActionPalette) Move(delta int) PaletteOutcome { return a.resolve(a.picker.Move(delta)) }

// Fail attaches an error, so a failed action is reported in the palette the
// user chose it from rather than as a bare flash behind a closed overlay.
func (a *ActionPalette) Fail(message string) { a.picker.Fail(message) }

func (a *ActionPalette) resolve(result PickerResult) PaletteOutcome {
	if result.Kind != PickerAccepted || len(result.IDs) == 0 {
		return PaletteOutcome{Kind: result.Kind}
	}
	for _, entry := range a.entries {
		if entry.Handler == result.IDs[0] {
			return PaletteOutcome{Kind: PickerAccepted, Entry: entry, Execute: true}
		}
	}
	return PaletteOutcome{Kind: PickerHandled}
}

// -- the context palette ----------------------------------------------------------

// clearContextsID is the command row that empties the whole filter set.
const clearContextsID = "__clear_contexts__"

// clearContextsLabel is what that row says.
const clearContextsLabel = "Clear all contexts"

// ContextPalette is the multiple-choice adapter over the TUI's global @context
// filters. The picker owns interaction; the model owns applying the result.
type ContextPalette struct {
	currentFilters []string
	picker         *ChoicePicker
}

// NewContextPalette builds one over the contexts the store currently holds.
func NewContextPalette(contexts, current []string) *ContextPalette {
	palette := &ContextPalette{currentFilters: NormalizeContextFilters(current)}
	preferred := ""
	if len(palette.currentFilters) > 0 {
		preferred = palette.currentFilters[0]
	}
	palette.picker = NewChoicePicker(ChoicePickerOptions{
		Title:       "contexts",
		Options:     contextOptions(contexts),
		Selection:   palette.currentFilters,
		Mode:        SelectMultiple,
		AcceptLabel: "apply",
		EmptyLabel:  "no matching contexts",
		MaxVisible:  paletteMaxResults,
		PreferredID: preferred,
		// The `@` is a sigil, not a letter: a user typing "home" means @home,
		// so both sides of the match drop it.
		SearchNormalizer: func(value string) string { return strings.TrimPrefix(value, "@") },
		SelectedStyle:    "context_filter_active",
		ToggleCommand: func(option PickerOption, staged []string) ([]string, bool) {
			if option.ID == clearContextsID {
				return []string{}, true
			}
			return staged, false
		},
	})
	return palette
}

// Picker exposes the underlying picker.
func (c *ContextPalette) Picker() *ChoicePicker { return c.picker }

// CurrentFilters is the filter set the palette opened with.
func (c *ContextPalette) CurrentFilters() []string { return append([]string{}, c.currentFilters...) }

// ContextOutcome is what a context-palette key did.
type ContextOutcome struct {
	Kind PickerResultKind
	// Contexts is the filter set to apply when Kind is PickerAccepted.
	Contexts []string
	Apply    bool
}

// HandleKey routes one key.
func (c *ContextPalette) HandleKey(key string) ContextOutcome {
	cursor, queryPresent, changed := c.acceptanceContext()
	return c.resolve(c.picker.HandleKey(key), cursor, queryPresent, changed)
}

// Hit routes one click.
func (c *ContextPalette) Hit(rowOffset int) ContextOutcome {
	cursor, queryPresent, changed := c.acceptanceContext()
	return c.resolve(c.picker.Hit(rowOffset), cursor, queryPresent, changed)
}

// Paste inserts pasted text into the query.
func (c *ContextPalette) Paste(text string) ContextOutcome {
	cursor, queryPresent, changed := c.acceptanceContext()
	return c.resolve(c.picker.Paste(text), cursor, queryPresent, changed)
}

// Move steps the cursor.
func (c *ContextPalette) Move(delta int) ContextOutcome {
	cursor, queryPresent, changed := c.acceptanceContext()
	return c.resolve(c.picker.Move(delta), cursor, queryPresent, changed)
}

// acceptanceContext snapshots the three facts the "search then Return" shortcut
// depends on, BEFORE the key is applied — the key itself changes all three.
func (c *ContextPalette) acceptanceContext() (*PickerOption, bool, bool) {
	return c.picker.Current(), strings.TrimSpace(c.picker.Input()) != "", c.picker.SelectionChanged()
}

// resolve is the one behavior that makes this palette pleasant: typing a query
// and pressing Return applies JUST the context under the cursor, replacing the
// filter set, rather than adding it to whatever was already selected. Toggling
// with space first — which is an explicit multi-select gesture — keeps the
// staged set instead.
func (c *ContextPalette) resolve(result PickerResult, cursor *PickerOption,
	queryPresent, selectionChanged bool) ContextOutcome {

	if result.Kind != PickerAccepted {
		return ContextOutcome{Kind: result.Kind}
	}
	if queryPresent && !selectionChanged && cursor != nil && cursor.Kind == PickerChoice {
		return ContextOutcome{Kind: PickerAccepted, Contexts: []string{cursor.ID}, Apply: true}
	}
	return ContextOutcome{Kind: PickerAccepted, Contexts: result.IDs, Apply: true}
}

// RefreshOptions adopts a fresh context list without losing what is staged.
func (c *ContextPalette) RefreshOptions(contexts, current []string) {
	if current != nil {
		c.currentFilters = NormalizeContextFilters(current)
	}
	c.picker.RefreshOptions(contextOptions(contexts), c.picker.staged)
}

func contextOptions(contexts []string) []PickerOption {
	options := []PickerOption{{ID: clearContextsID, Label: clearContextsLabel, Kind: PickerCommand}}
	for _, context := range NormalizeContextFilters(contexts) {
		options = append(options, PickerOption{ID: context, Label: context, Kind: PickerChoice})
	}
	return options
}
