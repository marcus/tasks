// Package store builds the immutable, lenient read model used by task queries.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"tasks-go/internal/record"
)

// Source identifies the file from which an Item was read.
type Source string

const (
	Live    Source = "live"
	Archive Source = "archive"
)

// Item is the defensive read projection of one task record. Fields deliberately
// retain malformed values as any: structural validation belongs to Check, and a
// read must not panic before Check can report the hand-edited defect.
type Item struct {
	State     any
	Priority  any
	Title     any
	Tags      []string
	Scheduled *time.Time
	Deadline  *time.Time
	Recur     any
	Lead      any
	LeadSkip  any
	ID        *string
	Closed    *time.Time
	Line      int
	Source    Source
}

// Snapshot is a coherent projection of live and archive records. It owns its
// item slices and indexes; callers receive copies so a held snapshot cannot be
// changed through an accessor.
type Snapshot struct {
	liveItems    []Item
	archiveItems []Item
	liveByID     map[string]Item
	archiveByID  map[string]Item
}

// NewSnapshot builds one read view from already parsed inputs. It deliberately
// selects only task records and does no structural validation.
func NewSnapshot(live, archive []record.Record) Snapshot {
	snapshot := Snapshot{
		liveItems:    buildItems(live, Live),
		archiveItems: buildItems(archive, Archive),
		liveByID:     make(map[string]Item),
		archiveByID:  make(map[string]Item),
	}
	for _, item := range snapshot.liveItems {
		if item.ID != nil {
			snapshot.liveByID[*item.ID] = copyItem(item)
		}
	}
	for _, item := range snapshot.archiveItems {
		if item.ID != nil {
			snapshot.archiveByID[*item.ID] = copyItem(item)
		}
	}
	return snapshot
}

// Items returns a copy of the live task items in record order.
func (s Snapshot) Items() []Item { return copyItems(s.liveItems) }

// ArchiveItems returns a copy of the archive task items in record order.
func (s Snapshot) ArchiveItems() []Item { return copyItems(s.archiveItems) }

// ItemByID returns the task in the requested source, if one has an id matching
// id. Duplicate ids retain Ruby Hash assignment semantics: the later record wins.
func (s Snapshot) ItemByID(source Source, id string) (Item, bool) {
	var item Item
	var ok bool
	if source == Archive {
		item, ok = s.archiveByID[id]
	} else {
		item, ok = s.liveByID[id]
	}
	return copyItem(item), ok
}

func buildItems(records []record.Record, source Source) []Item {
	items := make([]Item, 0)
	for _, rec := range records {
		fields := fieldsByName(rec)
		if stringValue(fields["type"]) != "task" {
			continue
		}
		item := Item{
			State:     value(fields["state"]),
			Priority:  value(fields["priority"]),
			Title:     value(fields["title"]),
			Tags:      stringArray(fields["tags"]),
			Scheduled: isoDate(fields["scheduled"]),
			Deadline:  isoDate(fields["deadline"]),
			Recur:     value(fields["recur"]),
			Lead:      value(fields["lead"]),
			LeadSkip:  value(fields["lead_skip"]),
			Closed:    isoDate(fields["closed"]),
			Line:      rec.Line,
			Source:    source,
		}
		if id, present := fields["id"]; present && !isNull(id) {
			text := rubyString(value(id))
			item.ID = &text
		}
		items = append(items, item)
	}
	return items
}

func fieldsByName(rec record.Record) map[string]json.RawMessage {
	fields := make(map[string]json.RawMessage, len(rec.Fields))
	for _, field := range rec.Fields {
		fields[field.Key] = field.Value
	}
	return fields
}

func value(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil
	}
	return decoded
}

func stringValue(raw json.RawMessage) string {
	text, _ := value(raw).(string)
	return text
}

func stringArray(raw json.RawMessage) []string {
	values, ok := value(raw).([]any)
	if !ok {
		return []string{}
	}
	strings := make([]string, len(values))
	for index, entry := range values {
		strings[index] = rubyString(entry)
	}
	return strings
}

func isoDate(raw json.RawMessage) *time.Time {
	text, ok := value(raw).(string)
	if !ok || text == "" {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return nil
	}
	return &parsed
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// rubyString is the small portion of Object#to_s reachable from JSON values.
func rubyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return fmt.Sprint(typed)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func copyItems(items []Item) []Item {
	copied := make([]Item, len(items))
	for index, item := range items {
		copied[index] = copyItem(item)
	}
	return copied
}

func copyItem(item Item) Item {
	item.State = copyJSONValue(item.State)
	item.Priority = copyJSONValue(item.Priority)
	item.Title = copyJSONValue(item.Title)
	item.Recur = copyJSONValue(item.Recur)
	item.Lead = copyJSONValue(item.Lead)
	item.LeadSkip = copyJSONValue(item.LeadSkip)
	tags := item.Tags
	item.Tags = make([]string, len(tags))
	copy(item.Tags, tags)
	if item.ID != nil {
		id := *item.ID
		item.ID = &id
	}
	if item.Scheduled != nil {
		date := *item.Scheduled
		item.Scheduled = &date
	}
	if item.Deadline != nil {
		date := *item.Deadline
		item.Deadline = &date
	}
	if item.Closed != nil {
		date := *item.Closed
		item.Closed = &date
	}
	return item
}

// copyJSONValue keeps malformed-but-readable JSON values private to the
// snapshot. Structural validation belongs to Check, so Item fields may hold an
// arbitrary JSON array or object even where the schema later rejects it.
func copyJSONValue(value any) any {
	switch typed := value.(type) {
	case []any:
		copied := make([]any, len(typed))
		for index, child := range typed {
			copied[index] = copyJSONValue(child)
		}
		return copied
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, child := range typed {
			copied[key] = copyJSONValue(child)
		}
		return copied
	default:
		return value
	}
}
