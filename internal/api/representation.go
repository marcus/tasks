package api

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// This file is lib/tasks/api/representation.rb: the wire shapes of a task, a
// section, a project, and the success/error envelopes.
//
// Everything is written through jsonout rather than encoding/json for one
// reason that decides the whole design: the responses are compared byte for
// byte against the Ruby server, and encoding/json sorts object keys while
// Ruby's Hash preserves insertion order. A struct-with-tags shape would have
// been shorter and would have produced a different document.

// detailWriter writes the members of an error's `details` object. It is handed
// an already-open object, so it writes keys and nothing else.
type detailWriter func(w *jsonout.Writer)

// fieldDetails is the `{ "fields": { … } }` shape every 422 carries.
func fieldDetails(fields []fieldError) detailWriter {
	return func(w *jsonout.Writer) {
		w.Key("fields")
		w.BeginObject()
		for _, field := range fields {
			w.Key(field.Field)
			w.BeginArray()
			for _, text := range field.Reasons {
				w.Str(text)
			}
			w.EndArray()
		}
		w.EndObject()
	}
}

// keyValueDetails is the flat `{ key: value }` details a placement or delete
// refusal carries. Values are written as strings or ints, in the given order.
type detailPair struct {
	Key   string
	Value any
}

func pairDetails(pairs ...detailPair) detailWriter {
	return func(w *jsonout.Writer) {
		for _, pair := range pairs {
			w.Key(pair.Key)
			switch value := pair.Value.(type) {
			case nil:
				w.Null()
			case string:
				w.Str(value)
			case int:
				w.Int(value)
			case bool:
				w.Bool(value)
			case detailWriter:
				w.BeginObject()
				value(w)
				w.EndObject()
			case func(*jsonout.Writer):
				value(w)
			default:
				w.Null()
			}
		}
	}
}

// treeIndex answers the structural questions a task resource asks — section,
// ancestors, children, descendants — over ONE source of one snapshot.
//
// It is built from the records rather than from taskquery.Node because the
// resource names ids, and a Node carries a title and a line but no id.
type treeIndex struct {
	byID     map[string]record.Record
	children map[string][]record.Record
}

func newTreeIndex(records []record.Record) *treeIndex {
	index := &treeIndex{byID: map[string]record.Record{}, children: map[string][]record.Record{}}
	for _, parsed := range records {
		id := parsed.String("id")
		if id != "" {
			index.byID[id] = parsed
		}
		if parent := parsed.String("parent"); parent != "" {
			index.children[parent] = append(index.children[parent], parsed)
		}
	}
	return index
}

// section is `section_for`: climb through task ancestors until a section, or
// nothing.
func (t *treeIndex) section(id string) (record.Record, bool) {
	current, found := t.byID[id]
	if !found {
		return record.Record{}, false
	}
	for found && current.String("type") == "task" {
		current, found = t.byID[current.String("parent")]
	}
	if !found || current.String("type") != "section" {
		return record.Record{}, false
	}
	return current, true
}

// ancestorIDs is `ancestor_ids`: every id above this record, outermost first.
func (t *treeIndex) ancestorIDs(id string) []string {
	ancestors := []string{}
	current, found := t.byID[id]
	if !found {
		return ancestors
	}
	current, found = t.byID[current.String("parent")]
	for found {
		if value := current.String("id"); value != "" {
			ancestors = append(ancestors, value)
		}
		current, found = t.byID[current.String("parent")]
	}
	for left, right := 0, len(ancestors)-1; left < right; left, right = left+1, right-1 {
		ancestors[left], ancestors[right] = ancestors[right], ancestors[left]
	}
	return ancestors
}

// childCount is `child_ids_for(...).length`: direct TASK children only.
func (t *treeIndex) childCount(id string) int {
	count := 0
	for _, child := range t.children[id] {
		if child.String("type") == "task" {
			count++
		}
	}
	return count
}

// descendantCount is the whole task subtree beneath an id.
func (t *treeIndex) descendantCount(id string) int {
	if id == "" {
		return 0
	}
	total := 0
	for _, child := range t.children[id] {
		if child.String("type") != "task" {
			continue
		}
		total += 1 + t.descendantCount(child.String("id"))
	}
	return total
}

// resourceContext is everything one task resource needs beyond the item: the
// read model it derives from and the structural index of its own source.
type resourceContext struct {
	queries *taskquery.Queries
	live    *treeIndex
	archive *treeIndex
}

func newResourceContext(queries *taskquery.Queries) *resourceContext {
	snapshot := queries.Snapshot()
	return &resourceContext{
		queries: queries,
		live:    newTreeIndex(snapshot.LiveRecords()),
		archive: newTreeIndex(snapshot.ArchiveRecords()),
	}
}

func (c *resourceContext) indexFor(source store.Source) *treeIndex {
	if source == store.SourceArchive {
		return c.archive
	}
	return c.live
}

// revisionFor is the opaque token the ETag carries.
func (c *resourceContext) revisionFor(item store.Item) string {
	return c.queries.Snapshot().RevisionFor(item)
}

// writeTask is Representation.task. The member order is Ruby's Hash literal
// order and is part of the contract.
func (c *resourceContext) writeTask(w *jsonout.Writer, item store.Item) {
	index := c.indexFor(item.Source)
	sectionID, sectionTitle := "", ""
	if section, found := index.section(item.ID); found {
		sectionID, sectionTitle = section.String("id"), section.String("title")
	}
	parentID := ""
	if item.HasParent && item.Parent != sectionID {
		parentID = item.Parent
	}
	depth := 0
	for _, ancestor := range index.ancestorIDs(item.ID) {
		if ancestor != sectionID {
			depth++
		}
	}

	scheduled, hasScheduled := c.queries.ScheduledValue(item)
	deadline, hasDeadline := c.queries.DeadlineValue(item)
	availability := c.queries.AvailabilityFor(item)

	w.BeginObject()
	w.KeyStrOrNull("id", item.ID)
	w.KeyStrOrNull("revision", c.revisionFor(item))
	w.KeyStr("source", string(item.Source))
	w.KeyStrOrNull("parent_id", parentID)
	w.KeyStrOrNull("section_id", sectionID)
	w.KeyInt("depth", depth)
	w.KeyStr("state", item.State)
	w.KeyStrOrNull("priority", item.Priority)
	w.KeyStr("title", item.Title)
	w.Key("contexts")
	w.Strings(item.Contexts)
	w.Key("tags")
	w.Strings(ordinaryTags(item.Tags))
	w.KeyBool("deferred", c.queries.Deferred(item))
	w.KeyStrOrNull("scheduled", item.Scheduled)
	w.Key("scheduled_time")
	c.writeAPITime(w, scheduled, hasScheduled)
	w.KeyStrOrNull("deadline", item.Deadline)
	w.Key("deadline_time")
	c.writeAPITime(w, deadline, hasDeadline)
	w.KeyBool("available", availability.Available())
	w.KeyStr("availability_reason", availability.Reason)
	w.KeyStrOrNull("availability_blocker_id", availability.BlockerID)
	w.Key("available_at")
	if availability.AvailableAt.IsZero() {
		w.Null()
	} else {
		w.Str(availability.AvailableAt.UTC().Format(instantLayout))
	}
	w.KeyStrOrNull("recurrence", item.Recur)
	w.Key("recurrence_human")
	if human := recur.Humanize(item.Recur); item.Recur != "" && human != nil {
		w.Str(*human)
	} else {
		w.Null()
	}
	w.KeyStrOrNull("lead", item.Lead)
	w.Key("lead_human")
	if human, ok := lead.Humanize(item.Lead); ok && lead.Span(item.Lead) {
		w.Str(human)
	} else {
		w.Null()
	}
	w.Key("lead_opens")
	if opens, ok := c.queries.LeadOpens(item); ok {
		w.Str(opens.ISO())
	} else {
		w.Null()
	}
	w.Key("lead_opens_at")
	if opensAt, ok := c.queries.LeadOpensAt(item); ok {
		w.Str(opensAt.UTC().Format(instantLayout))
	} else {
		w.Null()
	}
	w.Key("body")
	w.Strings(c.queries.Body(item))
	w.KeyStrOrNull("closed", item.Closed)
	w.KeyBool("archived", item.Source == store.SourceArchive)
	w.Key("project")
	if project, ok := c.queries.Project(item); ok {
		w.Str(project)
	} else if sectionTitle != "" {
		w.Str(sectionTitle)
	} else {
		w.Null()
	}
	w.KeyInt("child_count", index.childCount(item.ID))
	w.KeyInt("descendant_count", index.descendantCount(item.ID))
	w.Key("formal_links")
	w.BeginArray()
	for _, link := range item.FormalLinks {
		w.BeginObject()
		w.KeyStr("url", link.URL)
		if link.Label != "" {
			w.KeyStr("label", link.Label)
		}
		w.EndObject()
	}
	w.EndArray()
	w.Key("links")
	w.BeginArray()
	for _, link := range c.queries.Links(item) {
		w.BeginObject()
		w.KeyStr("system", link.System)
		w.KeyStr("url", link.URL)
		if link.Label != nil {
			w.KeyStr("label", *link.Label)
		} else {
			w.KeyNull("label")
		}
		w.KeyStr("source", string(link.Source))
		w.EndObject()
	}
	w.EndArray()
	w.Key("delegation")
	writeDelegation(w, item.Delegation)
	w.EndObject()
}

// instantLayout is Time#iso8601 for a UTC instant.
const instantLayout = "2006-01-02T15:04:05Z"

// ordinaryTags drops contexts and the defer marker, which are exposed as their
// own members rather than as tags.
func ordinaryTags(tags []string) []string {
	kept := []string{}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "@") || tag == store.DeferTag {
			continue
		}
		kept = append(kept, tag)
	}
	return kept
}

func (c *resourceContext) writeAPITime(w *jsonout.Writer, value temporal.Value, present bool) {
	api, ok := c.queries.APITimeFor(value, present)
	if !ok {
		w.Null()
		return
	}
	writeAPITimeValue(w, api)
}

func writeAPITimeValue(w *jsonout.Writer, api taskquery.APITime) {
	w.BeginObject()
	w.KeyStr("local", api.Local)
	w.KeyStrOrNull("timezone", api.Timezone)
	w.KeyInt("fold", api.Fold)
	w.KeyStr("effective_timezone", api.EffectiveTimezone)
	w.KeyStr("instant", api.Instant)
	w.EndObject()
}

// writeDelegation emits the stored marker verbatim in canonical key order, with
// absent keys omitted — the same rendering `tasks show --json` produces, so one
// delegation is spelled identically on disk, on the CLI, and over HTTP.
func writeDelegation(w *jsonout.Writer, raw json.RawMessage) {
	if len(raw) == 0 {
		w.Null()
		return
	}
	fields, err := record.Fields(raw)
	if err != nil || len(fields) == 0 {
		w.Null()
		return
	}
	byKey := map[string]json.RawMessage{}
	sourceOrder := []string{}
	for _, field := range fields {
		byKey[field.Key] = field.Value
		sourceOrder = append(sourceOrder, field.Key)
	}
	known := map[string]bool{}
	for _, key := range record.DelegationKeyOrder {
		known[key] = true
	}
	emit := func(key string) {
		value, present := byKey[key]
		if !present || absentDelegationValue(value) {
			return
		}
		w.KeyStr(key, delegationText(value))
	}
	w.BeginObject()
	for _, key := range record.DelegationKeyOrder {
		emit(key)
	}
	for _, key := range sourceOrder {
		if !known[key] {
			emit(key)
		}
	}
	w.EndObject()
}

func absentDelegationValue(raw json.RawMessage) bool {
	text := string(raw)
	return text == "null" || text == `""` || text == "[]" || text == "{}"
}

// delegationText is Object#to_s over the JSON scalars a marker member can hold.
// Ruby's TaskView deep-copies the object through to_s, so a newer writer's
// numeric member arrives at a consumer as a string; preserving the JSON type
// would be a nicer document and a different one.
func delegationText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case nil:
		return ""
	}
	return string(raw)
}

// writeSection is Representation.section.
func writeSection(w *jsonout.Writer, parsed record.Record) {
	w.BeginObject()
	w.KeyStrOrNull("id", parsed.String("id"))
	w.KeyStr("title", parsed.String("title"))
	w.KeyStrOrNull("parent_id", parsed.String("parent"))
	w.EndObject()
}

// writeProject is Representation.project: every field present with an explicit
// null so the schema can be strict, and the physical line never exposed.
func writeProject(w *jsonout.Writer, view taskquery.ProjectView) {
	w.BeginObject()
	w.KeyStr("id", view.ID)
	w.KeyStr("title", view.Title)
	if view.HasParentID {
		w.KeyStr("parent_id", view.ParentID)
	} else {
		w.KeyNull("parent_id")
	}
	w.KeyStr("kind", view.Kind)
	w.KeyInt("open_count", view.OpenCount)
	w.KeyInt("next_count", view.NextCount)
	w.Key("next_date")
	if view.HasNextDate {
		w.Str(view.NextDate.ISO())
	} else {
		w.Null()
	}
	w.Key("next_time")
	if view.HasNextTime {
		writeAPITimeValue(w, view.NextTime)
	} else {
		w.Null()
	}
	w.Key("next_at")
	if !view.NextAt.IsZero() {
		w.Str(view.NextAt.UTC().Format(instantLayout))
	} else {
		w.Null()
	}
	w.KeyBool("stuck", view.Stuck)
	w.KeyInt("held_count", view.HeldCount)
	w.Key("body")
	if view.HasBody {
		w.Str(view.Body)
	} else {
		w.Null()
	}
	w.Key("task_ids")
	w.Strings(view.TaskIDs)
	w.EndObject()
}

// writeSuccess is Representation.success: `{ data:, meta: { store_revision: } }`.
func writeSuccess(w *jsonout.Writer, data func(*jsonout.Writer), revision string) {
	w.BeginObject()
	w.Key("data")
	data(w)
	w.Key("meta")
	w.BeginObject()
	w.KeyStrOrNull("store_revision", revision)
	w.EndObject()
	w.EndObject()
}

// writeError is Representation.error.
func writeError(w *jsonout.Writer, code, text, requestID string, details detailWriter) {
	w.BeginObject()
	w.Key("error")
	w.BeginObject()
	w.KeyStr("code", code)
	w.KeyStr("message", text)
	w.Key("details")
	w.BeginObject()
	if details != nil {
		details(w)
	}
	w.EndObject()
	w.KeyStr("request_id", requestID)
	w.EndObject()
	w.EndObject()
}
