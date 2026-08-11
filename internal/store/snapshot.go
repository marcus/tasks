package store

import (
	"encoding/json"

	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/record"
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
	FormalLinks   []links.FormalLink
	Parent        string
	HasParent     bool
	Source        Source
}

// Snapshot is a coherent view of the task files. A caller can hold one while
// rendering and safely ask for a task's revision or its place in the tree
// without mixing fields from a later reload.
type Snapshot struct {
	items          []Item
	archiveItems   []Item
	liveRecords    []record.Record
	archiveRecords []record.Record

	archiveLoaded bool
	revisions     map[Source]map[string]string
}

// Items returns a detached copy of the live tasks in file order.
func (s *Snapshot) Items() []Item { return cloneItems(s.items) }

// ArchiveItems returns a detached copy of the archived tasks in file order.
func (s *Snapshot) ArchiveItems() []Item { return cloneItems(s.archiveItems) }

// LiveRecords returns a detached copy of the parsed live records.
func (s *Snapshot) LiveRecords() []record.Record { return cloneRecords(s.liveRecords) }

// ArchiveRecords returns a detached copy of the parsed archive records.
func (s *Snapshot) ArchiveRecords() []record.Record { return cloneRecords(s.archiveRecords) }

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

// FieldBaselines returns conflict tokens computed from this snapshot's exact
// record set. Values and baselines therefore come from one read; callers never
// pair an old display value with a fresh expectation from another store read.
func (s *Snapshot) FieldBaselines(id string, fields []PatchField) (map[PatchField]string, bool) {
	index := locateStableIndex(s.liveRecords, id)
	if index < 0 {
		return nil, false
	}
	out := make(map[PatchField]string, len(fields))
	for _, field := range fields {
		value, err := fieldBaseline(s.liveRecords, index, field)
		if err != nil {
			return nil, false
		}
		out[field] = value
	}
	return out, true
}

// ChildrenOf is the live items whose parent is `id`, in file order. The tree
// lives in the parent pointers, so no boundary is ever inferred by scanning.
func (s *Snapshot) ChildrenOf(id string) []Item {
	children := []Item{}
	for _, item := range s.items {
		if item.HasParent && item.Parent == id {
			children = append(children, cloneItem(item))
		}
	}
	return children
}

// Roots is the live items with no parent, in file order.
func (s *Snapshot) Roots() []Item {
	roots := []Item{}
	for _, item := range s.items {
		if !item.HasParent {
			roots = append(roots, cloneItem(item))
		}
	}
	return roots
}

func cloneItems(items []Item) []Item {
	if items == nil {
		return nil
	}
	out := make([]Item, len(items))
	for index, item := range items {
		out[index] = cloneItem(item)
	}
	return out
}

func cloneItem(item Item) Item {
	item.AllTags = cloneStrings(item.AllTags)
	item.Tags = cloneStrings(item.Tags)
	item.Contexts = cloneStrings(item.Contexts)
	item.FormalLinks = append([]links.FormalLink(nil), item.FormalLinks...)
	item.ScheduledTime = cloneRaw(item.ScheduledTime)
	item.DeadlineTime = cloneRaw(item.DeadlineTime)
	item.Delegation = cloneRaw(item.Delegation)
	return item
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage{}, value...)
}

func cloneRecords(records []record.Record) []record.Record {
	if records == nil {
		return nil
	}
	out := make([]record.Record, len(records))
	for recordIndex, parsed := range records {
		out[recordIndex] = record.Record{Line: parsed.Line}
		if parsed.Fields == nil {
			continue
		}
		out[recordIndex].Fields = make([]record.Field, len(parsed.Fields))
		for fieldIndex, field := range parsed.Fields {
			out[recordIndex].Fields[fieldIndex] = record.Field{
				Key:   field.Key,
				Value: cloneRaw(field.Value),
			}
		}
	}
	return out
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
		FormalLinks:   semanticLinks(parsed),
		Parent:        parent,
		HasParent:     hasParent,
		Source:        source,
	}
}

// semanticLinks is deliberately forgiving: CheckedReadSnapshot rejects a bad
// field, while ordinary snapshots still need to remain inspectable and must
// never panic because a hand edit used the wrong JSON shape.
func semanticLinks(parsed record.Record) []links.FormalLink {
	var values []json.RawMessage
	if err := json.Unmarshal(fieldRaw(parsed, "links"), &values); err != nil {
		return nil
	}
	out := make([]links.FormalLink, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		linkURL, ok := decodeString(fields["url"])
		if !ok || !links.ValidFormalURL(linkURL) || seen[linkURL] {
			continue
		}
		seen[linkURL] = true
		label, labelOK := decodeString(fields["label"])
		if fields["label"] != nil && (!labelOK || !links.ValidFormalLabel(label)) {
			label = ""
		}
		out = append(out, links.FormalLink{URL: linkURL, Label: label})
	}
	return out
}

func isoDate(raw json.RawMessage) string {
	value, ok := decodeString(raw)
	if !ok || !validISODate(value) {
		return ""
	}
	return value
}
