package store

import (
	"encoding/json"

	"tasks-go/internal/record"
)

// Item is a task as the read surfaces see it: coerced defensively so a
// hand-edited or malformed record can never crash a reader before Check gets
// to report it. The coercion only keeps readers alive; it never repairs the
// underlying breakage.
type Item struct {
	Line     int
	ID       string
	HasID    bool
	State    string
	Priority string
	Title    string
	// AllTags is the tag list exactly as the record stored it, contexts and
	// ordinary tags interleaved. Tags and Contexts are the same list split for
	// rendering; AllTags is what the headline and every tag filter read, because
	// stored order is part of the headline's bytes.
	AllTags  []string
	Tags     []string
	Contexts []string
	// Scheduled, Deadline and Closed are normalized ISO dates, or "" when the
	// stored value is missing or unparseable.
	Scheduled     string
	Deadline      string
	Closed        string
	Archived      string
	ScheduledTime json.RawMessage
	DeadlineTime  json.RawMessage
	Recur         string
	Lead          string
	LeadSkip      string
	Delegation    json.RawMessage
	Parent        string
	HasParent     bool
	Source        Source
}

// Snapshot is a coherent, immutable view of the task files. A caller can hold
// one while rendering and safely ask for a task's revision or its place in the
// tree without mixing fields from a later reload.
type Snapshot struct {
	Items          []Item
	ArchiveItems   []Item
	LiveRecords    []record.Record
	ArchiveRecords []record.Record

	archiveLoaded bool
	revisions     map[Source]map[string]string
}

// ArchiveLoaded reports whether the archive half was captured.
func (s *Snapshot) ArchiveLoaded() bool { return s.archiveLoaded }

// RevisionFor is the store-produced token for an item. There is deliberately
// no line-number fallback: an id-less legacy item has no API-safe revision.
func (s *Snapshot) RevisionFor(item Item) string {
	if !item.HasID {
		return ""
	}
	return s.revisions[item.Source][string(jsonString(item.ID))]
}

// ChildrenOf is the live items whose parent is `id`, in file order. The tree
// lives in the parent pointers, so no boundary is ever inferred by scanning.
func (s *Snapshot) ChildrenOf(id string) []Item {
	children := []Item{}
	for _, item := range s.Items {
		if item.HasParent && item.Parent == id {
			children = append(children, item)
		}
	}
	return children
}

// Roots is the live items with no parent, in file order.
func (s *Snapshot) Roots() []Item {
	roots := []Item{}
	for _, item := range s.Items {
		if !item.HasParent {
			roots = append(roots, item)
		}
	}
	return roots
}

func buildItems(records []record.Record, source Source) []Item {
	items := []Item{}
	for _, parsed := range records {
		if stringField(parsed, "type") != "task" {
			continue
		}
		items = append(items, buildItem(parsed, source))
	}
	return items
}

func buildItem(parsed record.Record, source Source) Item {
	tags := semanticTags(parsed)
	if tags == nil {
		tags = []string{}
	}
	contexts := []string{}
	ordinary := []string{}
	for _, tag := range tags {
		if len(tag) > 0 && tag[0] == '@' {
			contexts = append(contexts, tag)
			continue
		}
		ordinary = append(ordinary, tag)
	}
	id, hasID := decodeString(fieldRaw(parsed, "id"))
	parent, hasParent := decodeString(fieldRaw(parsed, "parent"))
	return Item{
		Line:          parsed.Line,
		ID:            id,
		HasID:         hasID,
		State:         stringField(parsed, "state"),
		Priority:      stringField(parsed, "priority"),
		Title:         stringField(parsed, "title"),
		AllTags:       tags,
		Tags:          ordinary,
		Contexts:      contexts,
		Scheduled:     isoDate(fieldRaw(parsed, "scheduled")),
		Deadline:      isoDate(fieldRaw(parsed, "deadline")),
		Closed:        isoDate(fieldRaw(parsed, "closed")),
		Archived:      isoDate(fieldRaw(parsed, "archived")),
		ScheduledTime: fieldRaw(parsed, "scheduled_time"),
		DeadlineTime:  fieldRaw(parsed, "deadline_time"),
		Recur:         stringField(parsed, "recur"),
		Lead:          stringField(parsed, "lead"),
		LeadSkip:      stringField(parsed, "lead_skip"),
		Delegation:    fieldRaw(parsed, record.DelegationField),
		Parent:        parent,
		HasParent:     hasParent,
		Source:        source,
	}
}

func isoDate(raw json.RawMessage) string {
	value, ok := decodeString(raw)
	if !ok || !validISODate(value) {
		return ""
	}
	return value
}
