package tui

import (
	"errors"
	"strings"

	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/termform"
)

// taskEditGroups is the field order the editor shows, grouped. It is the same
// order and the same grouping Ruby's TaskEditForm::GROUPS declares.
//
// The `placement` group and its `location` select are deliberately absent from
// BOTH implementations: nesting is done with indent/outdent in the list, not in
// the form. The store still accepts a location patch, so nothing about moving a
// task is lost — only the form surface for it.
var taskEditGroups = []struct {
	Key    string
	Label  string
	Fields []string
}{
	{"basics", "Basics", []string{"title", "priority", "deferred"}},
	{"timing", "Timing", []string{"scheduled", "deadline", "recurrence", "lead"}},
	{"organization", "Organization", []string{"contexts", "tags"}},
	{"notes", "Notes", []string{"body"}},
	{"lifecycle", "Lifecycle", []string{"state"}},
}

// The option sets the two select fields offer.
var (
	taskStateOptions = []string{"INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"}
	// recurrencePresets and leadPresets ride along as metadata for a renderer
	// that wants to offer them; they are not validation.
	recurrencePresets = []string{"daily", "weekly", "monthly", "yearly"}
	leadPresets       = []string{"3d", "1w", "2w", "1m"}
	dateSuggestions   = []string{"today", "tomorrow 9am", "fri noon", "+3 17:30"}
)

// TaskEditForm is the task-domain policy adapter around the neutral form
// engine. It owns field order, task-specific normalization and options, and the
// per-field expectations a patch is guarded by. The session owns effects.
type TaskEditForm struct {
	snapshot *EditSnapshot
	form     *termform.Form
	// expectations is the baseline each field's patch will be checked against.
	// It is NOT simply the current snapshot's: a dirty field keeps the
	// expectation it was dirtied against, which is what makes a later save
	// detect a same-field external edit instead of silently overwriting it.
	expectations map[string]string
	today        func() temporal.Date
	context      temporal.Context
	// contextOptions and tagOptions are the @contexts and tags the store
	// currently holds anywhere, offered as completions.
	contextOptions func() []string
	tagOptions     func() []string
}

// TaskEditFormOptions builds one.
type TaskEditFormOptions struct {
	Snapshot       *EditSnapshot
	Today          func() temporal.Date
	Context        temporal.Context
	ContextOptions func() []string
	TagOptions     func() []string
	Focus          string
}

// NewTaskEditForm builds the editor over one snapshot.
func NewTaskEditForm(options TaskEditFormOptions) (*TaskEditForm, error) {
	if options.Snapshot == nil || options.Snapshot.ID == "" {
		return nil, errors.New("task editor requires a stable target id")
	}
	edit := &TaskEditForm{
		snapshot:       options.Snapshot,
		expectations:   map[string]string{},
		today:          options.Today,
		context:        options.Context,
		contextOptions: options.ContextOptions,
		tagOptions:     options.TagOptions,
	}
	if edit.today == nil {
		edit.today = func() temporal.Date { return temporal.Date{} }
	}
	for _, field := range editFields {
		edit.expectations[field] = options.Snapshot.ExpectedFor(field)
	}
	form, err := termform.NewForm(edit.buildGroups(), options.Focus, nil)
	if err != nil {
		return nil, err
	}
	edit.form = form
	return edit, nil
}

// Form is the underlying engine.
func (t *TaskEditForm) Form() *termform.Form { return t.form }

// Snapshot is the read the form's baselines came from.
func (t *TaskEditForm) Snapshot() *EditSnapshot { return t.snapshot }

// TargetID is the task being edited.
func (t *TaskEditForm) TargetID() string { return t.snapshot.ID }

// ExpectedFor is the baseline a patch on one field must match.
func (t *TaskEditForm) ExpectedFor(field string) string {
	return t.expectations[normalizeEditField(field)]
}

// ReadOnly is the pair of facts the editor shows but cannot change.
func (t *TaskEditForm) ReadOnly() (id, closed string) { return t.snapshot.ID, t.snapshot.Closed }

// -- the patch value ----------------------------------------------------------------

// SemanticValue converts a form value into the exact value the store's patch
// for that field owns.
//
// Two spellings come back because the application facade carries both: a string
// for the fields whose value IS a string (with "" meaning "clear this" for the
// clearable ones), and a typed store.PatchValue for the three whose value is
// not — `deferred` is a bool, `contexts` and `tags` are ordered lists. Every
// field the editor SHOWS goes through one or the other; none refuses.
func (t *TaskEditForm) SemanticValue(field string, value any) (text string, typed *store.PatchValue) {
	switch normalizeEditField(field) {
	case "title":
		raw, _ := value.(string)
		return strings.TrimSpace(raw), nil
	case "body", "state", "priority":
		raw, _ := value.(string)
		return raw, nil
	case "recurrence":
		raw, _ := value.(string)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "", nil
		}
		result := recur.Parse(trimmed, ".+")
		if result.Error != "" || result.Canonical == "off" {
			return "", nil
		}
		return result.Canonical, nil
	case "lead":
		raw, _ := value.(string)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "", nil
		}
		// A cleared field and a typed clearing word mean the same thing: the
		// editor has one empty state, and the confirmation prompt reads "Clear
		// the lead time?" either way.
		result := lead.Parse(trimmed)
		if result.Error != "" || result.IsOff() {
			return "", nil
		}
		return result.Canonical, nil
	case "scheduled", "deadline":
		if parsed, ok := value.(*temporal.Value); ok && parsed != nil {
			// The whole temporal value, not just its date: a wall time and a
			// zone are part of what the user set, and dropping them here would
			// silently turn "Friday 5pm Berlin" into an all-day Friday.
			patch := store.TemporalValue(*parsed)
			return "", &patch
		}
		return "", nil
	case "deferred":
		flag, _ := value.(bool)
		patch := store.BoolValue(flag)
		return "", &patch
	case "contexts", "tags":
		patch := store.ListValue(toTokens(value))
		return "", &patch
	}
	return "", nil
}

func toTokens(value any) []string {
	tokens, _ := value.([]string)
	return append([]string{}, tokens...)
}

// PatchField is the store field an editor field patches.
func (t *TaskEditForm) PatchField(field string) store.PatchField {
	return patchFieldFor[normalizeEditField(field)]
}

// -- lifecycle ----------------------------------------------------------------------

// RefreshSnapshot adopts a fresh read WITHOUT replacing any dirty buffer or its
// original expectation.
func (t *TaskEditForm) RefreshSnapshot(fresh *EditSnapshot) error {
	if fresh == nil || fresh.ID != t.TargetID() {
		return errors.New("snapshot target changed")
	}
	dirty := map[string]bool{}
	for _, field := range editFields {
		if t.form.Dirty(field) {
			dirty[field] = true
		}
	}
	t.snapshot = fresh
	if _, err := t.form.Refresh(fresh.Values()); err != nil {
		return err
	}
	for _, field := range editFields {
		if !dirty[field] {
			t.expectations[field] = fresh.ExpectedFor(field)
		}
	}
	return nil
}

// AcceptCommit resolves a pending save against a fresh read.
func (t *TaskEditForm) AcceptCommit(fresh *EditSnapshot, token int) (termform.Transition, error) {
	if fresh == nil || fresh.ID != t.TargetID() {
		return termform.Transition{}, errors.New("snapshot target changed")
	}
	request := t.form.PendingCommit()
	committed := ""
	if request != nil {
		committed = request.FieldKey
	}
	dirtyOthers := map[string]bool{}
	for _, field := range editFields {
		if field != committed && t.form.Dirty(field) {
			dirtyOthers[field] = true
		}
	}
	t.snapshot = fresh
	transition, err := t.form.AcceptCommit(fresh.Values(), token)
	if err != nil {
		return termform.Transition{}, err
	}
	for _, field := range editFields {
		if !dirtyOthers[field] {
			t.expectations[field] = fresh.ExpectedFor(field)
		}
	}
	return transition, nil
}

// RejectCommit resolves a pending save with a refusal.
func (t *TaskEditForm) RejectCommit(fieldErrors map[string][]string, message string, token int) termform.Transition {
	transition, err := t.form.RejectCommit(fieldErrors, message, token)
	if err != nil {
		return termform.Transition{}
	}
	return transition
}

// ReloadField discards one local buffer and adopts the fresh persisted value.
func (t *TaskEditForm) ReloadField(field string, fresh *EditSnapshot) error {
	key := normalizeEditField(field)
	if fresh == nil || fresh.ID != t.TargetID() {
		return errors.New("snapshot target changed")
	}
	t.snapshot = fresh
	values := fresh.Values()
	t.form.SetValue(key, values[key], termform.Event{})
	if _, err := t.form.Refresh(values); err != nil {
		return err
	}
	t.expectations[key] = fresh.ExpectedFor(key)
	return nil
}

// RevertField throws away one field's unsaved edit.
func (t *TaskEditForm) RevertField(field string) {
	key := normalizeEditField(field)
	baseline := t.form.Baseline(key)
	t.form.SetValue(key, baseline, termform.Event{})
	_, _ = t.form.Refresh(map[string]any{key: baseline})
	t.expectations[key] = t.snapshot.ExpectedFor(key)
}

// -- construction ---------------------------------------------------------------------

func (t *TaskEditForm) buildGroups() []termform.Group {
	fields := map[string]termform.Field{}
	for _, field := range t.buildFields() {
		fields[field.Key()] = field
	}
	groups := make([]termform.Group, 0, len(taskEditGroups))
	for _, group := range taskEditGroups {
		members := make([]termform.Field, 0, len(group.Fields))
		for _, name := range group.Fields {
			members = append(members, fields[name])
		}
		groups = append(groups, termform.NewGroup(group.Key, group.Label, members...))
	}
	return groups
}

func (t *TaskEditForm) buildFields() []termform.Field {
	values := t.snapshot.Values()

	title := termform.NewBase("title", "Title", values["title"])
	title.RequiredFixed = true
	title.Validate = []func(any, termform.Context) string{
		func(value any, _ termform.Context) string {
			text, _ := value.(string)
			if strings.TrimSpace(text) == "" {
				return "Title is required"
			}
			return ""
		},
	}

	priority := termform.NewBase("priority", "Priority", values["priority"])
	priorityOptions := func(termform.Context) []termform.Option {
		return []termform.Option{
			{Value: nil, Label: "None"}, {Value: "A", Label: "A"},
			{Value: "B", Label: "B"}, {Value: "C", Label: "C"},
		}
	}

	deferred := termform.NewBase("deferred", "On hold", values["deferred"])

	recurrenceBase := termform.NewBase("recurrence", "Recurrence", values["recurrence"])
	recurrenceBase.Meta = map[string]any{"presets": recurrencePresets}
	recurrenceBase.Validate = []func(any, termform.Context) string{t.validateRecurrence}

	leadBase := termform.NewBase("lead", "Lead time", values["lead"])
	leadBase.Meta = map[string]any{"presets": leadPresets}
	leadBase.Validate = []func(any, termform.Context) string{t.validateLead}

	contexts := termform.NewBase("contexts", "Contexts", values["contexts"])
	tags := termform.NewBase("tags", "Tags", values["tags"])
	body := termform.NewBase("body", "Notes", values["body"])

	state := termform.NewBase("state", "State", values["state"])
	stateOptions := func(termform.Context) []termform.Option {
		names := taskStateOptions
		// A PROPOSED task is not a state the editor can move out of: approval
		// is a separate decision with its own key and its own audit trail.
		if t.snapshot.State == "PROPOSED" {
			names = []string{"PROPOSED"}
		}
		out := make([]termform.Option, 0, len(names))
		for _, name := range names {
			out = append(out, termform.NewOption(name, name))
		}
		return out
	}

	return []termform.Field{
		termform.NewInput(title),
		termform.NewSelect(priority, priorityOptions, false),
		termform.NewConfirm(deferred, "Yes", "No"),
		t.temporalField("scheduled", "Available from", values["scheduled"]),
		t.temporalField("deadline", "Deadline", values["deadline"]),
		newRecurrenceInput(recurrenceBase),
		newLeadInput(leadBase, t.context),
		termform.NewMultiSelect(contexts, t.optionsFrom(t.contextOptions, normalizeContextToken),
			true, true, normalizeContextToken),
		termform.NewMultiSelect(tags, t.optionsFrom(t.tagOptions, normalizeTagToken),
			true, true, normalizeTagToken),
		termform.NewTextArea(body),
		termform.NewSelect(state, stateOptions, false),
	}
}

func (t *TaskEditForm) optionsFrom(source func() []string, normalize func(string) string) func(termform.Context) []termform.Option {
	return func(termform.Context) []termform.Option {
		if source == nil {
			return nil
		}
		out := []termform.Option{}
		seen := map[string]bool{}
		for _, raw := range source() {
			token := normalize(raw)
			if token == "" || seen[token] {
				continue
			}
			seen[token] = true
			out = append(out, termform.NewOption(token, token))
		}
		return out
	}
}

// temporalField is the date field. It parses the SAME expression grammar the
// CLI accepts ("fri noon", "tomorrow 9am", an ISO stamp), so a user does not
// have to learn a second date language to use the TUI.
func (t *TaskEditForm) temporalField(key, label string, value any) termform.Field {
	base := termform.NewBase(key, label, value)
	hooks := termform.DateHooks{
		Parse: func(text string, today temporal.Date) (any, error) {
			return ParseTemporal(text, today, t.context)
		},
		Format: func(value any) string { return FormatTemporal(value) },
		Parsed: func(value any) bool {
			parsed, ok := value.(*temporal.Value)
			return ok && parsed != nil
		},
		DateOf: func(value any) (temporal.Date, bool) {
			if parsed, ok := value.(*temporal.Value); ok && parsed != nil {
				return parsed.Date, true
			}
			return temporal.Date{}, false
		},
		WithDate: func(date temporal.Date, current any) any {
			if parsed, ok := current.(*temporal.Value); ok && parsed != nil {
				rebuilt := *parsed
				rebuilt.Date = date
				return &rebuilt
			}
			return &temporal.Value{Date: date}
		},
	}
	return NewTemporalInput(base, hooks, t.today, dateSuggestions, t.context)
}

// ParseTemporal reads the editor's date grammar: a date expression with an
// optional wall time and an optional trailing zone / floating / fold token.
func ParseTemporal(text string, today temporal.Date, context temporal.Context) (any, error) {
	tokens := strings.Fields(strings.TrimSpace(text))
	fold := 0
	if len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		if last == "fold=earlier" || last == "fold=later" {
			if last == "fold=later" {
				fold = 1
			}
			tokens = tokens[:len(tokens)-1]
		}
	}
	mode := ""
	if len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		if last == "floating" || last == "UTC" || strings.Contains(last, "/") {
			mode = last
			tokens = tokens[:len(tokens)-1]
		}
	}
	options := temporal.ParseOptions{Today: today, Fold: fold, Floating: mode == "floating"}
	if mode != "" && mode != "floating" {
		options.Timezone = mode
	}
	if context.Timezone != nil {
		options.Context = &context
	}
	value, err := temporal.ParseExpression(strings.Join(tokens, " "), options)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// FormatTemporal is ParseTemporal's inverse: the canonical spelling the editor
// buffer holds. Round-tripping matters — the buffer is what the user edits.
func FormatTemporal(value any) string {
	parsed, ok := value.(*temporal.Value)
	if !ok || parsed == nil {
		return ""
	}
	parts := []string{parsed.Date.ISO()}
	if parsed.LocalTime != "" {
		parts = append(parts, parsed.LocalTime)
		zone := parsed.Timezone
		if zone == "" {
			zone = "floating"
		}
		parts = append(parts, zone)
		if parsed.Fold == 1 {
			parts = append(parts, "fold=later")
		}
	}
	return strings.Join(parts, " ")
}

// -- the two prose-display inputs ---------------------------------------------------

// recurrenceInput shows a committed schedule as prose ("every Mon, Wed") but
// edits the raw canonical value. The moment the field is focused or the buffer
// is dirty the display reverts to the stored spelling, so the caret never sits
// on characters the editor does not have.
type recurrenceInput struct{ *termform.Input }

func newRecurrenceInput(base termform.Base) termform.Field {
	return &recurrenceInput{Input: termform.NewInput(base)}
}

func (r *recurrenceInput) MetadataFor(value any, context termform.Context) map[string]any {
	base := r.Input.MetadataFor(value, context)
	if context.FocusedKey == r.Key() || context.Dirty(r.Key()) {
		return base
	}
	text, _ := value.(string)
	human := recur.Humanize(text)
	if human == nil || *human == text {
		return base
	}
	out := map[string]any{}
	for key, entry := range base {
		out[key] = entry
	}
	out["text"] = *human
	return out
}

// leadInput is the same idea for a lead span: "3 weeks before — opens
// 2026-10-11" when blurred, the canonical "3w" when being edited.
type leadInput struct {
	*termform.Input
	context temporal.Context
}

func newLeadInput(base termform.Base, context temporal.Context) termform.Field {
	return &leadInput{Input: termform.NewInput(base), context: context}
}

func (l *leadInput) MetadataFor(value any, context termform.Context) map[string]any {
	base := l.Input.MetadataFor(value, context)
	if context.FocusedKey == l.Key() || context.Dirty(l.Key()) {
		return base
	}
	span, _ := value.(string)
	anchor := anchorValue(context)
	if span == "" || anchor == nil {
		return base
	}
	display, ok := lead.DisplayInstant(span, *anchor, l.context)
	if !ok || display == span {
		return base
	}
	out := map[string]any{}
	for key, entry := range base {
		out[key] = entry
	}
	out["text"] = display
	return out
}

// anchorValue is the date a lead window measures back from: the deadline when
// there is one, otherwise the available-from date. Same precedence the store
// uses, so the editor's preview cannot promise a different window than the one
// that gets written.
func anchorValue(context termform.Context) *temporal.Value {
	if deadline, ok := context.Get("deadline").(*temporal.Value); ok && deadline != nil {
		return deadline
	}
	if scheduled, ok := context.Get("scheduled").(*temporal.Value); ok && scheduled != nil {
		return scheduled
	}
	return nil
}

// -- validation -----------------------------------------------------------------------

func (t *TaskEditForm) validateRecurrence(value any, context termform.Context) string {
	text, _ := value.(string)
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	// The anchor rule is checked only for a DIRTY field: a task that already
	// carries a cookie with no date is a repair case, and shouting about it
	// while the user edits an unrelated field would block every other save.
	if context.Dirty("recurrence") && anchorValue(context) == nil {
		return "Recurrence requires an Available from date or deadline"
	}
	// The engine's own rejection names the fix ("every what?", "no such day");
	// a field-level "not valid" would throw that away.
	return recur.Parse(raw, ".+").Error
}

// validateLead is the subset of the store's five lead rules this field can see
// before a write. Rules about state and about the resolved anchor range stay
// the store's — they need facts the form does not hold.
func (t *TaskEditForm) validateLead(value any, context termform.Context) string {
	text, _ := value.(string)
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	if context.Dirty("lead") {
		deadline, hasDeadline := context.Get("deadline").(*temporal.Value)
		scheduled, hasScheduled := context.Get("scheduled").(*temporal.Value)
		hasDeadline = hasDeadline && deadline != nil
		hasScheduled = hasScheduled && scheduled != nil
		if !hasDeadline && !hasScheduled {
			return "Lead time needs an Available from date or deadline to hide before"
		}
		if hasDeadline && hasScheduled {
			return "Lead time measures from the deadline — clear Available from, or the lead"
		}
	}
	return lead.Parse(raw).Error
}

// -- token normalization ------------------------------------------------------------

func normalizeContextToken(value string) string {
	token := strings.TrimSpace(value)
	if token == "" {
		return ""
	}
	if !strings.HasPrefix(token, "@") {
		token = "@" + token
	}
	return token
}

func normalizeTagToken(value string) string {
	token := strings.TrimSpace(value)
	if token == "" || strings.HasPrefix(token, "@") || token == store.DeferTag {
		return ""
	}
	return token
}

// normalizeEditField accepts the two aliases Ruby accepts, so a caller that
// says "notes" or "recur" reaches the field it means.
func normalizeEditField(field string) string {
	switch field {
	case "notes":
		return "body"
	case "recur":
		return "recurrence"
	}
	return field
}
