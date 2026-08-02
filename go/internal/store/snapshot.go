// Package store builds the immutable, lenient read model used by task queries.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
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
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return []string{}
	}
	strings := make([]string, len(values))
	for index, entry := range values {
		strings[index] = rubyStringJSON(entry)
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
		return rubyNumberString(typed.String())
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

// rubyNumberString mirrors JSON.parse's Integer/Float materialization before
// Object#to_s. Keeping the JSON token verbatim would incorrectly retain
// exponent spellings, redundant zeroes, and negative integer zero.
func rubyNumberString(raw string) string {
	if !strings.ContainsAny(raw, ".eE") {
		integer, ok := new(big.Int).SetString(raw, 10)
		if ok {
			return integer.String()
		}
		return raw
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil && value == 0 {
		return raw
	}
	if value > 0 && value > 1.7976931348623157e+308 {
		return "Infinity"
	}
	if value < 0 && value < -1.7976931348623157e+308 {
		return "-Infinity"
	}

	abs := value
	if abs < 0 {
		abs = -abs
	}
	if abs != 0 && (abs < 1e-4 || abs >= 1e15) {
		return rubyScientificFloat(value)
	}
	return rubyFixedFloat(value)
}

func rubyFixedFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		return text + ".0"
	}
	return text
}

func rubyScientificFloat(value float64) string {
	text := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponent, found := strings.Cut(text, "e")
	if !found {
		return rubyFixedFloat(value)
	}
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	return mantissa + "e" + exponent
}

// rubyStringJSON preserves an object's JSON member order while spelling arrays
// and hashes as Ruby Object#to_s does. Tags are coercively rendered by
// tags.map(&:to_s), so JSON encoding (with ':' separators) is observably wrong
// for a malformed structured tag.
func rubyStringJSON(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeRubyValue(decoder)
	if err != nil {
		return ""
	}
	if scalar, ok := value.(rubyScalar); ok {
		return rubyString(scalar.value)
	}
	return value.string()
}

type rubyValue interface{ string() string }

type rubyScalar struct{ value any }

func (value rubyScalar) string() string {
	switch typed := value.value.(type) {
	case nil:
		return "nil"
	case json.Number:
		return rubyNumberString(typed.String())
	case string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return typed
		}
		return string(encoded)
	default:
		return rubyString(typed)
	}
}

type rubyArray []rubyValue

func (values rubyArray) string() string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = value.string()
	}
	return "[" + joinRuby(parts) + "]"
}

type rubyObjectField struct {
	key   string
	value rubyValue
}

type rubyObject []rubyObjectField

func (fields *rubyObject) set(key string, value rubyValue) {
	for index := range *fields {
		if (*fields)[index].key == key {
			(*fields)[index].value = value
			return
		}
	}
	*fields = append(*fields, rubyObjectField{key: key, value: value})
}

func (fields rubyObject) string() string {
	parts := make([]string, len(fields))
	for index, field := range fields {
		key, err := json.Marshal(field.key)
		if err != nil {
			key = []byte(`""`)
		}
		parts[index] = string(key) + " => " + field.value.string()
	}
	return "{" + joinRuby(parts) + "}"
}

func joinRuby(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	joined := parts[0]
	for _, part := range parts[1:] {
		joined += ", " + part
	}
	return joined
}

func decodeRubyValue(decoder *json.Decoder) (rubyValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '[':
			var values rubyArray
			for decoder.More() {
				value, err := decodeRubyValue(decoder)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			_, err := decoder.Token()
			return values, err
		case '{':
			var fields rubyObject
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeRubyValue(decoder)
				if err != nil {
					return nil, err
				}
				fields.set(key.(string), value)
			}
			_, err := decoder.Token()
			return fields, err
		}
	}
	return rubyScalar{value: token}, nil
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
