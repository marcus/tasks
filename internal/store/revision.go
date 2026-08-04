package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/marcus/tasks/internal/record"
)

// deferTag is the tag whose presence is a semantic value of its own: it hides
// a task, so it belongs in the revision alongside the dates.
const deferTag = "defer"

// revisionOwnFields is EditSnapshot::FIELDS minus :location, in that order.
// Location is not an own field — it has its own fingerprint — but the ORDER of
// the rest is load-bearing: the digest is taken over a JSON array of
// [name, value] pairs, so a reordering changes every token in the store.
var revisionOwnFields = []string{
	"title", "priority", "deferred", "scheduled", "deadline", "recurrence",
	"lead", "contexts", "tags", "body", "state",
}

var (
	jsonNull  = json.RawMessage("null")
	jsonTrue  = json.RawMessage("true")
	jsonFalse = json.RawMessage("false")
)

// StoreRevisionForContents is the content-derived global revision: a digest of
// the exact bytes of both files, length-prefixed so that no pair of contents
// can be re-split into another pair with the same digest. An absent file
// contributes the length -1, which no present file can spell.
//
// It is produced for invalid bytes too, and that is the honest answer: the
// token is a digest of bytes, not an assertion that they parse.
func StoreRevisionForContents(live, archive []byte) string {
	digest := sha256.New()
	for _, content := range [][]byte{live, archive} {
		var header [8]byte
		if content == nil {
			for index := range header {
				header[index] = 0xff
			}
			digest.Write(header[:])
			continue
		}
		size := uint64(len(content))
		for index := 0; index < 8; index++ {
			header[index] = byte(size >> (8 * (7 - index)))
		}
		digest.Write(header[:])
		digest.Write(content)
	}
	return "s1." + hex.EncodeToString(digest.Sum(nil))
}

// taskRevisions computes every task's revision in one pass. The sibling index
// is built once for the whole pass: the per-task inline sibling scan made
// every snapshot build quadratic in list size, and it must yield the exact id
// list that scan produced or one task would carry different revisions
// depending on which path built it.
func taskRevisions(records []record.Record) (map[string]string, error) {
	siblings := siblingIDsByParent(records)
	revisions := map[string]string{}
	for index, parsed := range records {
		if stringField(parsed, "type") != "task" {
			continue
		}
		id := fieldRaw(parsed, "id")
		if !truthy(id) {
			continue
		}
		revision, err := taskRevision(records, index, siblings)
		if err != nil {
			return nil, err
		}
		key, err := canonical(id)
		if err != nil {
			return nil, err
		}
		revisions[string(key)] = revision
	}
	return revisions, nil
}

// taskRevision keeps the three semantic components separate so a title-only
// update can ignore a sibling-list change while a move or a cascade still
// invalidates. Dates are normalized before hashing so equivalent snapshots
// never depend on serialization details.
func taskRevision(records []record.Record, index int, siblings map[string][]json.RawMessage) (string, error) {
	parsed := records[index]
	values, err := editValues(parsed)
	if err != nil {
		return "", err
	}
	pairs := make([]json.RawMessage, 0, len(revisionOwnFields)+3)
	for _, field := range revisionOwnFields {
		pairs = append(pairs, jsonArray(jsonString(field), values[field]))
	}
	// Time metadata is part of the task's own semantic value: a stale zone or
	// time edit must fail exactly like a stale date edit. Stored objects only —
	// never derived instants, so a tzdata update cannot invalidate revisions.
	for _, field := range []string{"scheduled_time", "deadline_time", record.DelegationField} {
		value, err := revisionValue(fieldRaw(parsed, field))
		if err != nil {
			return "", err
		}
		pairs = append(pairs, jsonArray(jsonString(field), value))
	}
	own := semanticDigest(jsonArray(pairs...))

	location, err := locationFingerprint(records, index, siblings)
	if err != nil {
		return "", err
	}
	lifecycle, err := lifecycleFingerprint(records, index)
	if err != nil {
		return "", err
	}
	return "v1." + own + "." + location + "." + lifecycle, nil
}

// editValues is the field-owned semantic view a revision digests. Every value
// is already in its revision spelling, so the caller only assembles them.
func editValues(parsed record.Record) (map[string]json.RawMessage, error) {
	tags := semanticTags(parsed)
	contexts := []string{}
	ordinary := []string{}
	deferred := false
	for _, tag := range tags {
		switch {
		case len(tag) > 0 && tag[0] == '@':
			contexts = append(contexts, tag)
		case tag == deferTag:
			deferred = true
		default:
			ordinary = append(ordinary, tag)
		}
	}

	values := map[string]json.RawMessage{}
	for _, field := range []struct{ name, key string }{
		{"title", "title"}, {"priority", "priority"}, {"recurrence", "recur"},
		{"lead", "lead"}, {"state", "state"},
	} {
		value, err := revisionValue(fieldRaw(parsed, field.key))
		if err != nil {
			return nil, err
		}
		values[field.name] = value
	}
	values["deferred"] = jsonFalse
	if deferred {
		values["deferred"] = jsonTrue
	}
	values["scheduled"] = dateValue(fieldRaw(parsed, "scheduled"))
	values["deadline"] = dateValue(fieldRaw(parsed, "deadline"))
	values["contexts"] = stringArray(contexts)
	values["tags"] = stringArray(ordinary)
	body, _ := decodeString(fieldRaw(parsed, "body"))
	values["body"] = jsonString(body)
	return values, nil
}

// locationFingerprint answers "where does this task sit", which is what an
// If-Match on a move has to guard: the parent, the parent's full sibling list,
// and the shape of the subtree underneath.
func locationFingerprint(records []record.Record, index int, siblings map[string][]json.RawMessage) (string, error) {
	parsed := records[index]
	end := subtreeEnd(records, index)
	structural := make([]json.RawMessage, 0, end-index)
	for _, child := range records[index:end] {
		triple, err := canonicalAll(fieldRaw(child, "type"), fieldRaw(child, "id"), fieldRaw(child, "parent"))
		if err != nil {
			return "", err
		}
		structural = append(structural, jsonArray(triple...))
	}
	parent, err := canonical(fieldRaw(parsed, "parent"))
	if err != nil {
		return "", err
	}
	return semanticDigest(jsonArray(parent, jsonArray(siblings[string(parent)]...), jsonArray(structural...))), nil
}

// lifecycleFingerprint answers "what state is this subtree in", which is what
// a cascading close has to guard.
func lifecycleFingerprint(records []record.Record, index int) (string, error) {
	end := subtreeEnd(records, index)
	owned := make([]json.RawMessage, 0, end-index)
	for _, child := range records[index:end] {
		if stringField(child, "type") != "task" {
			continue
		}
		fields, err := canonicalAll(
			fieldRaw(child, "id"), fieldRaw(child, "parent"), fieldRaw(child, "state"),
			fieldRaw(child, "closed"), fieldRaw(child, "scheduled"), fieldRaw(child, "scheduled_time"),
			fieldRaw(child, "deadline"), fieldRaw(child, "deadline_time"), fieldRaw(child, "recur"),
		)
		if err != nil {
			return "", err
		}
		deferred := jsonFalse
		for _, tag := range semanticTags(child) {
			if tag == deferTag {
				deferred = jsonTrue
				break
			}
		}
		owned = append(owned, jsonArray(append(fields, deferred)...))
	}
	return semanticDigest(jsonArray(owned...)), nil
}

// subtreeEnd is the index just past the subtree rooted at records[index]. The
// DFS pre-order invariant guarantees a subtree is contiguous, so extending
// while the next record's parent is inside the subtree finds it in one scan.
func subtreeEnd(records []record.Record, index int) int {
	ids := map[string]bool{}
	if key, err := canonical(fieldRaw(records[index], "id")); err == nil {
		ids[string(key)] = true
	}
	end := index + 1
	for end < len(records) {
		parent := fieldRaw(records[end], "parent")
		if !truthy(parent) {
			break
		}
		key, err := canonical(parent)
		if err != nil || !ids[string(key)] {
			break
		}
		if id, err := canonical(fieldRaw(records[end], "id")); err == nil {
			ids[string(id)] = true
		}
		end++
	}
	return end
}

func siblingIDsByParent(records []record.Record) map[string][]json.RawMessage {
	index := map[string][]json.RawMessage{}
	for _, parsed := range records {
		id := fieldRaw(parsed, "id")
		if id == nil || string(id) == "null" {
			continue
		}
		parent, err := canonical(fieldRaw(parsed, "parent"))
		if err != nil {
			continue
		}
		value, err := canonical(id)
		if err != nil {
			continue
		}
		index[string(parent)] = append(index[string(parent)], value)
	}
	return index
}

// revisionValue normalizes a value before it is hashed: an object becomes a
// key-sorted list of pairs, so two equivalent snapshots can never depend on
// the order a writer happened to serialize its members in.
func revisionValue(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return jsonNull, nil
	}
	switch trimmed[0] {
	case '{':
		fields, err := record.Fields(trimmed)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(fields, func(left, right int) bool { return fields[left].Key < fields[right].Key })
		pairs := make([]json.RawMessage, 0, len(fields))
		for _, field := range fields {
			value, err := revisionValue(field.Value)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, jsonArray(jsonString(field.Key), value))
		}
		return jsonArray(pairs...), nil
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal(trimmed, &elements); err != nil {
			return nil, err
		}
		values := make([]json.RawMessage, 0, len(elements))
		for _, element := range elements {
			value, err := revisionValue(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return jsonArray(values...), nil
	default:
		return canonical(trimmed)
	}
}

// dateValue is `revision_value(to_date(...))`: a parseable ISO date in its
// normalized spelling, and null for anything else. A malformed date never
// reaches here on a valid store — Check refuses the store first — so the
// tolerance is only what keeps a reader from crashing on one.
func dateValue(raw json.RawMessage) json.RawMessage {
	value, ok := decodeString(raw)
	if !ok || !validISODate(value) {
		return jsonNull
	}
	return jsonString(value)
}

func validISODate(value string) bool {
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	digits := func(text string) (int, bool) {
		number := 0
		for index := 0; index < len(text); index++ {
			if text[index] < '0' || text[index] > '9' {
				return 0, false
			}
			number = number*10 + int(text[index]-'0')
		}
		return number, true
	}
	year, yearOK := digits(value[0:4])
	month, monthOK := digits(value[5:7])
	day, dayOK := digits(value[8:10])
	if !yearOK || !monthOK || !dayOK || month < 1 || month > 12 || day < 1 {
		return false
	}
	lengths := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	limit := lengths[month-1]
	if month == 2 && (year%4 == 0 && (year%100 != 0 || year%400 == 0)) {
		limit = 29
	}
	return day <= limit
}

// semanticTags is the tag list as the domain reads it: an array of strings,
// with anything else dropped rather than crashing a reader Check has yet to
// warn about.
func semanticTags(parsed record.Record) []string {
	var elements []json.RawMessage
	if json.Unmarshal(fieldRaw(parsed, "tags"), &elements) != nil {
		return nil
	}
	tags := make([]string, 0, len(elements))
	for _, element := range elements {
		if value, ok := decodeString(element); ok {
			tags = append(tags, value)
		}
	}
	return tags
}

func semanticDigest(value json.RawMessage) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// canonical re-emits one raw JSON value in Ruby's JSON.generate spelling. An
// absent value is null, which is what a missing Hash key reads as.
func canonical(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return jsonNull, nil
	}
	var out bytes.Buffer
	if err := record.EncodeJSON(&out, trimmed); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func canonicalAll(values ...json.RawMessage) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		canonicalized, err := canonical(value)
		if err != nil {
			return nil, err
		}
		out = append(out, canonicalized)
	}
	return out, nil
}

func jsonArray(elements ...json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('[')
	for index, element := range elements {
		if index > 0 {
			out.WriteByte(',')
		}
		out.Write(element)
	}
	out.WriteByte(']')
	return out.Bytes()
}

func jsonString(value string) json.RawMessage {
	var out bytes.Buffer
	record.EncodeString(&out, value)
	return out.Bytes()
}

func stringArray(values []string) json.RawMessage {
	elements := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		elements = append(elements, jsonString(value))
	}
	return jsonArray(elements...)
}

// fieldRaw reads a record member. The LAST occurrence wins, because a Ruby
// Hash built from a duplicated JSON key holds the last value.
func fieldRaw(parsed record.Record, key string) json.RawMessage {
	var found json.RawMessage
	for _, field := range parsed.Fields {
		if field.Key == key {
			found = field.Value
		}
	}
	return found
}

func stringField(parsed record.Record, key string) string {
	value, _ := decodeString(fieldRaw(parsed, key))
	return value
}

func decodeString(raw json.RawMessage) (string, bool) {
	var value string
	if raw == nil || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

// truthy is Ruby's truthiness over a JSON value: everything but nil and false.
func truthy(raw json.RawMessage) bool {
	trimmed := string(bytes.TrimSpace(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "false"
}
