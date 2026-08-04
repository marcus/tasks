package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// EditSnapshot is one coherent read of a task PLUS the per-field baselines the
// store will compare a patch against.
//
// STRUCTURAL NOTE, and the one place this packet had to build rather than port.
// Ruby has `Tasks::EditSnapshot`, produced by `application.edit_snapshot(id)`,
// which carries every field, its baseline and the task's revision. The Go
// application facade publishes the values and field expectations through one
// held ReadModel. This assembles the editor shape without taking a second store
// read, so every buffer and every expectation describes the same bytes.
//
// The important property is preserved: every baseline in a snapshot comes from
// one moment, so a patch's conflict check compares against the value the user
// was actually looking at.
type EditSnapshot struct {
	ID       string
	Title    string
	Priority string
	Deferred bool
	// Scheduled and Deadline are nil when the task has no such date. A nil is
	// meaningfully different from a zero date: it means "clear this".
	Scheduled  *temporal.Value
	Deadline   *temporal.Value
	Recurrence string
	Lead       string
	Contexts   []string
	Tags       []string
	Body       string
	State      string
	Closed     string
	// SubtreeIDs is the task and its descendants, in document order, so a
	// completion confirmation can say how many tasks ride along.
	SubtreeIDs []string

	baselines map[string]string
}

// editFields is the field vocabulary the editor covers, in the order the form
// shows them.
var editFields = []string{
	"title", "priority", "deferred", "scheduled", "deadline",
	"recurrence", "lead", "contexts", "tags", "body", "state",
}

// patchFieldFor maps an editor field name onto the store's patch vocabulary.
var patchFieldFor = map[string]store.PatchField{
	"title":      store.FieldTitle,
	"priority":   store.FieldPriority,
	"deferred":   store.FieldDeferred,
	"scheduled":  store.FieldScheduled,
	"deadline":   store.FieldDeadline,
	"recurrence": store.FieldRecurrence,
	"lead":       store.FieldLead,
	"contexts":   store.FieldContexts,
	"tags":       store.FieldTags,
	"body":       store.FieldBody,
	"state":      store.FieldState,
}

// NewEditSnapshot builds a snapshot for one task from a live read model, or
// reports that the task is not there.
func NewEditSnapshot(app *application.Application, read *application.ReadModel, id string) (*EditSnapshot, bool) {
	if read == nil || id == "" {
		return nil, false
	}
	item, found := read.TaskFor(id)
	if !found {
		return nil, false
	}
	queries := read.Queries()
	snapshot := &EditSnapshot{
		ID:         item.ID,
		Title:      item.Title,
		Priority:   item.Priority,
		Deferred:   containsString(item.AllTags, store.DeferTag),
		Recurrence: item.Recur,
		Lead:       item.Lead,
		Contexts:   append([]string{}, item.Contexts...),
		Tags:       append([]string{}, item.Tags...),
		Body:       strings.Join(queries.Body(item), "\n"),
		State:      item.State,
		Closed:     item.Closed,
		SubtreeIDs: subtreeIDs(queries, item),
		baselines:  map[string]string{},
	}
	if value, present := queries.ScheduledValue(item); present {
		snapshot.Scheduled = &value
	}
	if value, present := queries.DeadlineValue(item); present {
		snapshot.Deadline = &value
	}
	fields := make([]store.PatchField, 0, len(editFields))
	for _, field := range editFields {
		fields = append(fields, patchFieldFor[field])
	}
	baselines, present := read.FieldBaselines(id, fields)
	if !present {
		return nil, false
	}
	for _, field := range editFields {
		snapshot.baselines[field] = baselines[patchFieldFor[field]]
	}
	_ = app // retained in the public constructor shape used by existing callers
	return snapshot, true
}

// ExpectedFor is the baseline a patch on one field must match.
func (s *EditSnapshot) ExpectedFor(field string) string { return s.baselines[field] }

// Value is the snapshot's value for one editor field, in the shape the matching
// form field holds.
func (s *EditSnapshot) Value(field string) any {
	switch field {
	case "title":
		return s.Title
	case "priority":
		if s.Priority == "" {
			return nil
		}
		return s.Priority
	case "deferred":
		return s.Deferred
	case "scheduled":
		if s.Scheduled == nil {
			return nil
		}
		return s.Scheduled
	case "deadline":
		if s.Deadline == nil {
			return nil
		}
		return s.Deadline
	case "recurrence":
		return s.Recurrence
	case "lead":
		return s.Lead
	case "contexts":
		return append([]string{}, s.Contexts...)
	case "tags":
		return append([]string{}, s.Tags...)
	case "body":
		return s.Body
	case "state":
		return s.State
	}
	return nil
}

// Values is every field's value.
func (s *EditSnapshot) Values() map[string]any {
	out := map[string]any{}
	for _, field := range editFields {
		out[field] = s.Value(field)
	}
	return out
}

// OpenDescendantIDs is every open task strictly beneath this one — what a
// cascade completion would also close.
func (s *EditSnapshot) OpenDescendantIDs(read *application.ReadModel) []string {
	if read == nil || len(s.SubtreeIDs) < 2 {
		return nil
	}
	wanted := map[string]bool{}
	for _, id := range s.SubtreeIDs[1:] {
		wanted[id] = true
	}
	out := []string{}
	for _, item := range read.Items() {
		if wanted[item.ID] && isOpenState(item.State) {
			out = append(out, item.ID)
		}
	}
	return out
}

func subtreeIDs(queries *taskquery.Queries, item store.Item) []string {
	node := queries.NodeFor(item)
	if node == nil {
		return []string{item.ID}
	}
	out := []string{}
	var walk func(*taskquery.Node)
	walk = func(current *taskquery.Node) {
		if current.Item != nil && current.Item.ID != "" {
			out = append(out, current.Item.ID)
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
	return out
}
