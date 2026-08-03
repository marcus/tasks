package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"tasks-go/internal/jsonout"
	"tasks-go/internal/lead"
	"tasks-go/internal/record"
	"tasks-go/internal/recur"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
)

// emitItemsJSON writes the standard task array every read command answers
// `--json` with. One shape, one insertion order: adding or renaming a field
// cannot split the JSON between a read command and the report a mutation
// prints, because both come through here.
func (s *surfaceContext) emitItemsJSON(queries *taskquery.Queries, items []store.Item) int {
	w := jsonout.New()
	w.BeginArray()
	for _, item := range items {
		writeItemJSON(w, queries, item)
	}
	w.EndArray()
	if err := w.Err(); err != nil {
		return abort(err.Error())
	}
	out(w.String())
	return 0
}

func writeItemJSON(w *jsonout.Writer, queries *taskquery.Queries, item store.Item) {
	writeItemJSONWith(w, queries, item, nil)
}

// writeItemJSONWith is the same row plus whatever the view adds to it —
// `quadrants` contributes its classification, `show` its closed date, notes and
// links. The rider writes INSIDE the object and after every standard member, so
// a view can extend the shape without forking it: Ruby merges its extras onto
// the same hash for exactly this reason.
func writeItemJSONWith(w *jsonout.Writer, queries *taskquery.Queries, item store.Item,
	extra func(*jsonout.Writer)) {
	w.BeginObject()
	w.KeyStrOrNull("id", item.ID)
	w.KeyStr("state", item.State)
	w.KeyStrOrNull("priority", item.Priority)
	w.KeyStr("title", item.Title)
	w.Key("tags")
	w.Strings(item.Tags)
	w.Key("contexts")
	w.Strings(item.Contexts)
	w.KeyBool("deferred", queries.Deferred(item))

	scheduled, hasScheduled := queries.ScheduledValue(item)
	deadline, hasDeadline := queries.DeadlineValue(item)
	w.KeyStrOrNull("scheduled", item.Scheduled)
	w.Key("scheduled_time")
	writeAPITime(w, queries, scheduled, hasScheduled)
	w.KeyStrOrNull("deadline", item.Deadline)
	w.Key("deadline_time")
	writeAPITime(w, queries, deadline, hasDeadline)

	w.KeyStrOrNull("recur", item.Recur)
	w.Key("recur_human")
	if item.Recur == "" {
		w.Null()
	} else if human := recur.Humanize(item.Recur); human != nil {
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
	w.KeyInt("line", item.Line)
	w.KeyStr("source", string(item.Source))
	w.KeyStr("headline", taskquery.Headline(item))

	// Everything below comes from the read model rather than the record: it is
	// what the task MEANS for this reader, not what the file says.
	opens, hasOpens := queries.LeadOpens(item)
	w.Key("lead_opens")
	if hasOpens {
		w.Str(opens.ISO())
	} else {
		w.Null()
	}
	opensAt, hasOpensAt := queries.LeadOpensAt(item)
	w.Key("lead_opens_at")
	if hasOpensAt {
		w.Str(opensAt.Format("2006-01-02T15:04:05Z"))
	} else {
		w.Null()
	}
	availability := queries.AvailabilityFor(item)
	w.KeyBool("available", availability.Available())
	w.KeyStr("availability_reason", availability.Reason)
	w.KeyStrOrNull("availability_blocker_id", availability.BlockerID)
	w.Key("available_at")
	if availability.AvailableAt.IsZero() {
		w.Null()
	} else {
		w.Str(availability.AvailableAt.Format("2006-01-02T15:04:05Z"))
	}
	project, hasProject := queries.Project(item)
	w.Key("project")
	if hasProject {
		w.Str(project)
	} else {
		w.Null()
	}
	w.Key("delegation")
	writeDelegation(w, item.Delegation)
	if extra != nil {
		extra(w)
	}
	w.EndObject()
}

// writeAPITime renders a stamp's time half: the stored spelling AND what it
// resolves to for this reader. Both halves are needed — the stored zone is what
// the user wrote down, the effective zone and instant are what it means now. An
// all-day stamp has no wall time and reports null.
func writeAPITime(w *jsonout.Writer, queries *taskquery.Queries, value temporal.Value, present bool) {
	api, ok := queries.APITimeFor(value, present)
	if !ok {
		w.Null()
		return
	}
	writeAPITimeValue(w, api)
}

// writeAPITimeValue is the object itself, shared with the project resource so a
// stamp reads the same wherever it appears.
func writeAPITimeValue(w *jsonout.Writer, api taskquery.APITime) {
	w.BeginObject()
	w.KeyStr("local", api.Local)
	w.KeyStrOrNull("timezone", api.Timezone)
	w.KeyInt("fold", api.Fold)
	w.KeyStr("effective_timezone", api.EffectiveTimezone)
	w.KeyStr("instant", api.Instant)
	w.EndObject()
}

// writeDelegation emits the marker in canonical key order, every value spelled
// as a string. That last part is Ruby's, not an invention: TaskView deep-copies
// the object through `to_s`, so a newer writer's numeric field arrives at a
// consumer as "25000". Preserving the type here would be a nicer JSON document
// and a different one.
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
		if !present || delegationAbsent(value) {
			return
		}
		w.KeyStr(key, rubyToS(value))
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

func delegationAbsent(raw json.RawMessage) bool {
	trimmed := string(raw)
	return trimmed == "null" || trimmed == `""` || trimmed == "[]" || trimmed == "{}"
}

// rubyToS is Object#to_s for the JSON values a delegation member can hold.
func rubyToS(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func parseInt(value string) (int, error) { return strconv.Atoi(value) }

// delegationFields flattens a marker to its string members, which is all the
// headline renderings need. A value that is not an object is not a marker.
func delegationFields(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	fields, err := record.Fields(raw)
	if err != nil || len(fields) == 0 {
		return nil
	}
	flat := map[string]string{}
	for _, field := range fields {
		flat[field.Key] = rubyToS(field.Value)
	}
	return flat
}
