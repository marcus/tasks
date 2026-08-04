package api

import (
	"regexp"
	"strings"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/timezones"
)

// schemaVersion is Format::VERSION, the one schema this build reads.
const schemaVersion = check.Version

// validateCreateBody is App#validate_create_body!.
func validateCreateBody(body *jsonObject) error {
	if !body.has("title") {
		return validationError(reason("title", "is required"))
	}
	if err := validateCommonBody(body, true); err != nil {
		return err
	}
	hasProject := body.has("project") && !body.isNull("project")
	hasParent := body.has("parent_id") && !body.isNull("parent_id")
	if hasProject && hasParent {
		return validationError(reason("location", "project and parent_id cannot both be supplied"))
	}
	return nil
}

// validatePatchBody is App#validate_patch_body!.
func validatePatchBody(body *jsonObject, queries *taskquery.Queries, current store.Item) error {
	if err := validateCommonBody(body, false); err != nil {
		return err
	}
	for _, field := range []string{"scheduled", "deadline"} {
		timeKey := field + "_time"
		if !body.has(timeKey) || body.isNull(timeKey) || body.has(field) {
			continue
		}
		stored := current.Scheduled
		if field == "deadline" {
			stored = current.Deadline
		}
		if stored == "" {
			return validationError(reason(timeKey, "requires "+field))
		}
	}
	if body.has("placement") && body.has("parent_id") {
		return validationError(
			reason("placement", "cannot be combined with parent_id"),
			reason("parent_id", "cannot be combined with placement"),
		)
	}
	if body.has("placement") {
		return validatePlacement(body)
	}
	return nil
}

// validatePlacement is App#validate_placement!.
func validatePlacement(body *jsonObject) error {
	placement, ok := body.object("placement")
	if !ok {
		return validationError(reason("placement", "must be an object"))
	}
	for _, key := range placement.keys {
		if !containsString(placementFields, key) {
			return validationError(reason("placement", "must contain only parent_id and before_id"))
		}
	}
	if !placement.has("parent_id") {
		return validationError(reason("placement.parent_id", "is required"))
	}
	parentID, isText := placement.text("parent_id")
	if !isText || !taskIDPattern.MatchString(parentID) {
		return validationError(reason("placement.parent_id", "must be a stable task or section id"))
	}
	if placement.has("before_id") && !placement.isNull("before_id") {
		beforeID, isText := placement.text("before_id")
		if !isText || !taskIDPattern.MatchString(beforeID) {
			return validationError(reason("placement.before_id", "must be a stable task id or null"))
		}
	}
	return nil
}

// validateCommonBody is App#validate_common_body!, in the same order, because
// the FIRST refusal is the one a client sees.
func validateCommonBody(body *jsonObject, create bool) error {
	if body.has("title") {
		title, isText := body.text("title")
		if !isText || strings.TrimSpace(title) == "" {
			return validationError(reason("title", "must be non-empty text"))
		}
	}
	if body.has("priority") && !body.isNull("priority") {
		priority, isText := body.text("priority")
		if !isText || !containsString(metaPriorities, priority) {
			return validationError(reason("priority", "must be A, B, C, or null"))
		}
	}
	if body.has("state") {
		state, isText := body.text("state")
		if !isText || !containsString(taskquery.StateOrder(), state) {
			return validationError(reason("state", "must be a documented task state"))
		}
	}
	booleanFields := []string{"deferred"}
	if create {
		booleanFields = append(booleanFields, "apply_host_context")
	}
	for _, field := range booleanFields {
		if body.has(field) {
			if _, isBool := body.boolean(field); !isBool {
				return validationError(reason(field, "must be true or false"))
			}
		}
	}
	for _, field := range []string{"scheduled", "deadline"} {
		timeKey := field + "_time"
		if body.has(field) {
			if err := validateISODate(body, field); err != nil {
				return err
			}
		}
		if body.has(timeKey) {
			if err := validateTimeInput(body, timeKey); err != nil {
				return err
			}
		}
		if create && body.has(timeKey) && !body.has(field) {
			return validationError(reason(timeKey, "requires "+field))
		}
		if body.has(field) && body.isNull(field) && body.has(timeKey) && !body.isNull(timeKey) {
			return validationError(reason(timeKey, "cannot be set when "+field+" is null"))
		}
	}
	for _, field := range []string{"tags", "contexts"} {
		if !body.has(field) {
			continue
		}
		if _, ok := body.stringList(field); !ok {
			return validationError(reason(field, "must be a list of text values"))
		}
	}
	if contexts, ok := body.stringList("contexts"); ok && body.has("contexts") {
		for _, tag := range contexts {
			if !strings.HasPrefix(tag, "@") || len(tag) == 1 {
				return validationError(reason("contexts", "each context must start with @"))
			}
		}
	}
	if tags, ok := body.stringList("tags"); ok && body.has("tags") {
		for _, tag := range tags {
			if tag == "" || strings.HasPrefix(tag, "@") || tag == store.DeferTag {
				return validationError(reason("tags", "must contain ordinary tags only"))
			}
		}
	}
	if body.has("parent_id") && !body.isNull("parent_id") {
		parentID, isText := body.text("parent_id")
		if !isText || !taskIDPattern.MatchString(parentID) {
			return validationError(reason("parent_id", "must be a stable task id or null"))
		}
	}
	if create && body.has("project") && !body.isNull("project") {
		if _, isText := body.text("project"); !isText {
			return validationError(reason("project", "must be text or null"))
		}
	}
	if body.has("body") {
		if body.isNull("body") {
			if !create {
				return validationError(reason("body", "null is not valid for PATCH body"))
			}
		} else if _, isText := body.text("body"); !isText {
			if _, isList := body.stringList("body"); !isList {
				return validationError(reason("body", "must be text, a list of text lines, or null"))
			}
		}
	}
	if body.has("recurrence") && !body.isNull("recurrence") {
		if _, isText := body.text("recurrence"); !isText {
			return validationError(reason("recurrence", "must be text or null"))
		}
	}
	if body.has("lead") && !body.isNull("lead") {
		if _, isText := body.text("lead"); !isText {
			return validationError(reason("lead", "must be text or null"))
		}
	}
	return nil
}

// validateISODate is App#validate_iso_date!: the spelling first, then the
// calendar. `2026-02-30` matches the pattern and is not a date.
func validateISODate(body *jsonObject, field string) error {
	if body.isNull(field) {
		return nil
	}
	value, isText := body.text(field)
	if !isText || !isoDatePattern.MatchString(value) {
		return validationError(reason(field, "must be an ISO YYYY-MM-DD date or null"))
	}
	if _, ok := temporal.ParseDate(value); !ok {
		return validationError(reason(field, "must be a real calendar date"))
	}
	return nil
}

// validateTimeInput is App#validate_time_input!: the time object accepts three
// members and no derived ones — an `instant` a client sent would be a fact it
// does not own.
func validateTimeInput(body *jsonObject, field string) error {
	if body.isNull(field) {
		return nil
	}
	value, ok := body.object(field)
	if !ok {
		return validationError(reason(field, "must be an object or null"))
	}
	unknown := []string{}
	for _, key := range value.keys {
		if key != "local" && key != "timezone" && key != "fold" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		return validationError(reason(field, "contains unknown fields: "+strings.Join(unknown, ", ")))
	}
	if !value.has("local") {
		return validationError(reason(field+".local", "is required"))
	}
	local, isText := value.text("local")
	if !isText || !temporal.ValidLocal(local) {
		return validationError(reason(field+".local", "must use HH:MM minute precision"))
	}
	if value.has("timezone") && !value.isNull("timezone") {
		zone, isText := value.text("timezone")
		if !isText {
			return validationError(reason(field+".timezone", "must be a time zone identifier"))
		}
		if _, err := timezones.Load(zone); err != nil {
			return validationError(reason(field+".timezone", err.Error()))
		}
	}
	if value.has("fold") {
		fold, isText := value.text("fold")
		_ = fold
		if isText {
			return validationError(reason(field+".fold", "must be 0 or 1"))
		}
		number, ok := foldValue(value)
		if !ok || (number != 0 && number != 1) {
			return validationError(reason(field+".fold", "must be 0 or 1"))
		}
	}
	return nil
}

func foldValue(value *jsonObject) (int, bool) {
	text := strings.TrimSpace(string(value.raw("fold")))
	switch text {
	case "0":
		return 0, true
	case "1":
		return 1, true
	}
	return 0, false
}

// normalizeRecurrence is App#normalize_recurrence: both input syntaxes through
// the one shared parser, stored in the canonical spelling, with the engine's own
// reason on a refusal.
func normalizeRecurrence(body *jsonObject, allowOff bool) (string, error) {
	value, _ := body.text("recurrence")
	result := recur.Parse(value, ".+")
	if result.Error != "" {
		return "", validationError(reason("recurrence", result.Error))
	}
	if result.Canonical == "off" {
		if allowOff {
			return "", nil
		}
		return "", validationError(reason("recurrence", `must name a schedule, not "off"`))
	}
	return result.Canonical, nil
}

// normalizeLead is App#normalize_lead.
func normalizeLead(body *jsonObject, allowOff bool) (string, error) {
	value, _ := body.text("lead")
	result := lead.Parse(value)
	if result.Error != "" {
		return "", validationError(reason("lead", result.Error))
	}
	if result.Canonical == "off" {
		if allowOff {
			return "", nil
		}
		return "", validationError(reason("lead", `must name a span, not "off"`))
	}
	return result.Canonical, nil
}

// patchChanges is App#normalize_patch_changes: the request body mapped onto the
// store's typed changeset.
func (s *Server) patchChanges(body *jsonObject, queries *taskquery.Queries,
	current store.Item) ([]store.Change, error) {
	changes := []store.Change{}
	context := s.options.TemporalContext()

	for _, key := range body.keys {
		switch key {
		case "title":
			value, _ := body.text(key)
			changes = append(changes, store.Change{Field: store.FieldTitle, Value: store.TextValue(value)})
		case "priority":
			changes = append(changes, store.Change{Field: store.FieldPriority, Value: textOrNone(body, key)})
		case "state":
			value, _ := body.text(key)
			changes = append(changes, store.Change{Field: store.FieldState, Value: store.TextValue(value)})
		case "deferred":
			value, _ := body.boolean(key)
			changes = append(changes, store.Change{Field: store.FieldDeferred, Value: store.BoolValue(value)})
		case "contexts":
			values, _ := body.stringList(key)
			changes = append(changes, store.Change{Field: store.FieldContexts, Value: store.ListValue(values)})
		case "tags":
			values, _ := body.stringList(key)
			changes = append(changes, store.Change{Field: store.FieldTags, Value: store.ListValue(values)})
		case "body":
			changes = append(changes, store.Change{
				Field: store.FieldBody, Value: store.TextValue(normalizeBody(body, key)),
			})
		case "recurrence":
			value := store.NoValue()
			if !body.isNull(key) {
				canonical, err := normalizeRecurrence(body, true)
				if err != nil {
					return nil, err
				}
				if canonical != "" {
					value = store.TextValue(canonical)
				}
			}
			changes = append(changes, store.Change{Field: store.FieldRecurrence, Value: value})
		case "lead":
			value := store.NoValue()
			if !body.isNull(key) {
				canonical, err := normalizeLead(body, true)
				if err != nil {
					return nil, err
				}
				if canonical != "" {
					value = store.TextValue(canonical)
				}
			}
			changes = append(changes, store.Change{Field: store.FieldLead, Value: value})
		case "parent_id", "placement":
			// Both spellings name the same structural destination, and the store
			// owns every rule about it — cycles, depth, the anchor's parent. What
			// this layer owes is the VALUE's shape: a null parent_id is UNNEST, a
			// named one is an append, and `placement` may also carry the anchor.
			// Flattening the anchor away would be exactly the silent half-work a
			// stated refusal exists to prevent.
			changes = append(changes, store.Change{
				Field: store.FieldLocation, Value: locationValue(body, key),
			})
		}
	}

	// The two temporal pairs are folded LAST and once each: a request may name
	// the date, the time, or both, and all three produce one field change.
	for _, field := range []string{"scheduled", "deadline"} {
		if !body.has(field) && !body.has(field+"_time") {
			continue
		}
		value, err := s.temporalPatchValue(body, field, queries, current, context)
		if err != nil {
			return nil, err
		}
		patchField := store.FieldScheduled
		if field == "deadline" {
			patchField = store.FieldDeadline
		}
		changes = append(changes, store.Change{Field: patchField, Value: value})
	}
	return changes, nil
}

func textOrNone(body *jsonObject, key string) store.PatchValue {
	if body.isNull(key) {
		return store.NoValue()
	}
	value, _ := body.text(key)
	return store.TextValue(value)
}

func locationValue(body *jsonObject, key string) store.PatchValue {
	if key == "parent_id" {
		if body.isNull(key) {
			return store.UnnestValue()
		}
		value, _ := body.text(key)
		return store.TextValue(value)
	}
	placement, ok := body.object(key)
	if !ok {
		return store.TextValue("")
	}
	parent, _ := placement.text("parent_id")
	before, _ := placement.text("before_id")
	return store.PlacementValue(parent, before)
}

// normalizeBody is App#normalize_body: a list of lines joins with newlines.
func normalizeBody(body *jsonObject, key string) string {
	if lines, ok := body.stringList(key); ok {
		return strings.Join(lines, "\n")
	}
	value, _ := body.text(key)
	return value
}

// temporalPatchValue is App#temporal_patch_value.
//
// The interesting case is the third: moving ONLY the date keeps the stored time
// metadata, which can land the preserved wall time in a DST gap. That is a
// client-input problem — a 422 — and never a server error.
func (s *Server) temporalPatchValue(body *jsonObject, field string, queries *taskquery.Queries,
	current store.Item, context temporal.Context) (store.PatchValue, error) {
	timeKey := field + "_time"
	dateKey := body.has(field)
	hasTimeKey := body.has(timeKey)

	dateText := ""
	if dateKey {
		if !body.isNull(field) {
			dateText, _ = body.text(field)
		}
	} else {
		dateText = current.Scheduled
		if field == "deadline" {
			dateText = current.Deadline
		}
	}
	if dateText == "" {
		return store.NoValue(), nil
	}
	date, ok := temporal.ParseDate(dateText)
	if !ok {
		return store.NoValue(), validationError(reason(field, "must be a real calendar date"))
	}

	if hasTimeKey {
		if body.isNull(timeKey) {
			value, _ := temporal.NewValue(date, "", "", 0, false)
			return store.TemporalValue(value), nil
		}
		return s.temporalInput(body, field, date, context)
	}

	existing, hasExisting := queries.ScheduledValue(current)
	if field == "deadline" {
		existing, hasExisting = queries.DeadlineValue(current)
	}
	if !hasExisting {
		value, _ := temporal.NewValue(date, "", "", 0, false)
		return store.TemporalValue(value), nil
	}
	moved, err := temporal.NewValue(date, existing.LocalTime, existing.Timezone, existing.Fold, true)
	if err != nil {
		return store.NoValue(), validationError(reason(field, err.Error()))
	}
	if moved.Floating() {
		if _, err := moved.Instant(context); err != nil {
			return store.NoValue(), validationError(reason(field, err.Error()))
		}
	}
	return store.TemporalValue(moved), nil
}

// temporalInput builds a value from an explicit `{ local, timezone, fold }`.
func (s *Server) temporalInput(body *jsonObject, field string, date temporal.Date,
	context temporal.Context) (store.PatchValue, error) {
	timeKey := field + "_time"
	object, ok := body.object(timeKey)
	if !ok {
		return store.NoValue(), validationError(reason(timeKey, "must be an object or null"))
	}
	local, _ := object.text("local")
	zone := ""
	if object.has("timezone") && !object.isNull("timezone") {
		zone, _ = object.text("timezone")
	}
	fold := 0
	if object.has("fold") {
		fold, _ = foldValue(object)
	}
	value, err := temporal.NewValue(date, local, zone, fold, true)
	if err != nil {
		return store.NoValue(), validationError(reason(timeKey, err.Error()))
	}
	if value.Floating() {
		if _, err := value.Instant(context); err != nil {
			return store.NoValue(), validationError(reason(timeKey, err.Error()))
		}
	}
	return store.TemporalValue(value), nil
}

// isoDatePattern is the spelling gate `validate_iso_date!` applies before the
// calendar one.
var isoDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
