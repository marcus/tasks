package application

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// recordFor is test_helper.rb's record_for: the persisted record with a title.
func (h *harness) recordFor(title string) (record.Record, bool) {
	h.t.Helper()
	for _, parsed := range record.Parse([]byte(h.read())).Records {
		if parsed.String("title") == title {
			return parsed, true
		}
	}
	return record.Record{}, false
}

func tagsOf(parsed record.Record) []string {
	return record.DecodeStrings(mustGet(parsed, "tags"))
}

func mustGet(parsed record.Record, key string) []byte {
	raw, _ := parsed.Get(key)
	return raw
}

// test_application_create_accepts_own_indefinite_hold_atomically
func TestApplicationCreateAcceptsOwnIndefiniteHoldAtomically(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.CreateTask(CreateCommand{
		Title: "Held from creation", Project: "Work", Deferred: true,
	}, nil)

	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	parsed, found := h.recordFor("Held from creation")
	if !found {
		t.Fatal("the task was not written")
	}
	if !contains(tagsOf(parsed), store.DeferTag) {
		t.Fatalf("tags = %v, want the defer tag", tagsOf(parsed))
	}

	// The hold is visible through the SAME snapshot the write produced, which
	// is the property that makes a mutation response coherent.
	read, err := h.app.TaskResultFromMutation(result, result.TouchedIDs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !read.OK() {
		t.Fatalf("post-write read = %q", read.Status)
	}
	queries, err := h.app.Queries(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	availability := queries.AvailabilityFor(read.Data)
	if availability.Available() || availability.Reason != "on_hold" {
		t.Fatalf("availability = %+v, want on_hold", availability)
	}
	h.assertChecks()
}

// test_application_adds_host_context_alongside_explicit_contexts
func TestApplicationAddsHostContextAlongsideExplicitContexts(t *testing.T) {
	h := newHarness(t, harnessOptions{hostContext: "@home"})

	result := h.app.CreateTask(CreateCommand{
		Title: "Call from laptop", Tags: []string{"@computer", "follow-up"},
	}, nil)
	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}

	parsed, _ := h.recordFor("Call from laptop")
	if want := []string{"@home", "@computer", "follow-up"}; !equalStrings(tagsOf(parsed), want) {
		t.Fatalf("tags = %v, want %v", tagsOf(parsed), want)
	}
	h.assertChecks()
}

// test_application_deduplicates_or_explicitly_suppresses_host_context
func TestApplicationDeduplicatesOrExplicitlySuppressesHostContext(t *testing.T) {
	h := newHarness(t, harnessOptions{hostContext: "@home"})

	if result := h.app.CreateTask(CreateCommand{
		Title: "Already home", Tags: []string{"@home", "@computer"},
	}, nil); !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if result := h.app.CreateTask(CreateCommand{
		Title: "Work only", Tags: []string{"@work"}, SkipHostContext: true,
	}, nil); !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}

	already, _ := h.recordFor("Already home")
	if want := []string{"@home", "@computer"}; !equalStrings(tagsOf(already), want) {
		t.Fatalf("tags = %v, want %v", tagsOf(already), want)
	}
	only, _ := h.recordFor("Work only")
	if want := []string{"@work"}; !equalStrings(tagsOf(only), want) {
		t.Fatalf("tags = %v, want %v", tagsOf(only), want)
	}
	h.assertChecks()
}

// The dry-run preview and the real create share one preparation, so a preview
// can never promise tags the write would not produce.
func TestPrepareCreateTaskIsWhatTheWriteWouldPersist(t *testing.T) {
	h := newHarness(t, harnessOptions{hostContext: "@home"})

	command := CreateCommand{Title: "Preview", Tags: []string{"@computer", "later"}}
	prepared := h.app.PrepareCreateTask(command)
	if want := []string{"@home", "@computer", "later"}; !equalStrings(prepared.Tags, want) {
		t.Fatalf("prepared = %v, want %v", prepared.Tags, want)
	}
	if !equalStrings(command.Tags, []string{"@computer", "later"}) {
		t.Fatal("preparing a command must not mutate the caller's command")
	}

	if result := h.app.CreateTask(command, nil); !result.OK() {
		t.Fatalf("status = %q", result.Status)
	}
	written, _ := h.recordFor("Preview")
	if !equalStrings(tagsOf(written), prepared.Tags) {
		t.Fatalf("written %v, previewed %v", tagsOf(written), prepared.Tags)
	}
}

// test_application_accepts_attributes_or_a_typed_command_and_validates_context
func TestApplicationCreateCarriesTheRevisionOfItsOwnWrite(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	first := h.app.CreateTask(CreateCommand{Title: "Via attributes", Project: "Home"}, nil)
	second := h.app.CreateTask(CreateCommand{Title: "Via parent", ParentID: fixFlight}, nil)

	if !first.OK() || !second.OK() {
		t.Fatalf("statuses = %q %q", first.Status, second.Status)
	}
	if !revisionPattern.MatchString(first.StoreRevision) || !revisionPattern.MatchString(second.StoreRevision) {
		t.Fatalf("revisions = %q %q", first.StoreRevision, second.StoreRevision)
	}
	// The second write's revision is the store's CURRENT revision: a mutation
	// result and a fresh checked read describe the same bytes.
	if status := h.app.ReadStatusResult(nil); status.StoreRevision != second.StoreRevision {
		t.Fatalf("status revision %q != write revision %q", status.StoreRevision, second.StoreRevision)
	}

	attributes, _ := h.recordFor("Via attributes")
	if attributes.String("parent") != fixHome {
		t.Fatalf("project capture landed under %q", attributes.String("parent"))
	}
	parented, _ := h.recordFor("Via parent")
	if parented.String("parent") != fixFlight {
		t.Fatalf("parent capture landed under %q", parented.String("parent"))
	}
}

// test_create_task_is_immutable_and_copies_mutable_inputs
func TestCreateCommandCopiesMutableInputs(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	tags := []string{"@work", "important"}
	notes := []string{"first", "second"}
	command := CreateCommand{Title: "Draft proposal", Tags: tags, Notes: notes}

	result := h.app.CreateTask(command, nil)
	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	// A caller reusing its own slices cannot reach back into the accepted
	// command — Ruby buys this with freeze, this build with a copy.
	tags[0] = "mutated"
	notes[0] = "mutated"

	parsed, _ := h.recordFor("Draft proposal")
	if want := []string{"@work", "important"}; !equalStrings(tagsOf(parsed), want) {
		t.Fatalf("tags = %v, want %v", tagsOf(parsed), want)
	}
	if body := parsed.String("body"); !strings.Contains(body, "first") || strings.Contains(body, "mutated") {
		t.Fatalf("body = %q", body)
	}
}

// test_create_rejects_invalid_recurrence_or_ambiguous_initial_body_without_writing
func TestCreateRefusesBodyAndNotesTogetherWithoutWriting(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	before := h.read()

	result := h.app.CreateTask(CreateCommand{
		Title: "Draft proposal", Body: "body", Notes: []string{"note"},
	}, nil)

	if result.Status != store.MutationInvalid {
		t.Fatalf("status = %q", result.Status)
	}
	if result.FirstError() != "body and notes cannot both be supplied" {
		t.Fatalf("errors = %v", result.Errors)
	}
	if h.read() != before {
		t.Fatal("a refused create must not touch the file")
	}
}

// The four dated fields now REACH the file. They used to be refused here
// because the store could not write them — refusing was the honest answer while
// that was true, and silently dropping a deadline the caller asked for was the
// failure mode being engineered against.
//
// The rules about them stay in the store. What this layer owes is only that the
// value arrives: a task created with a deadline has one.
func TestCreateWritesTheDatedFields(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command CreateCommand
		want    string
	}{
		{"scheduled", CreateCommand{Title: "T", Scheduled: "2026-08-01"}, `"scheduled":"2026-08-01"`},
		{"deadline", CreateCommand{Title: "T", Deadline: "2026-08-08"}, `"deadline":"2026-08-08"`},
		{"recur", CreateCommand{Title: "T", Recurrence: ".+1w"}, `"recur":".+1w"`},
		{"lead", CreateCommand{Title: "T", Lead: "3d", Deadline: "2026-08-08"}, `"lead":"3d"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			result := h.app.CreateTask(testCase.command, nil)
			if result.Status != store.MutationOK {
				t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
			}
			if !strings.Contains(h.read(), testCase.want) {
				t.Fatalf("the store does not contain %s", testCase.want)
			}
		})
	}
}

// A caller that has already parsed a COMPLETE temporal value hands it over
// whole, and every part of it reaches the file: the local time, the zone, and the
// fold that disambiguates a repeated wall clock hour.
//
// The string fields cannot express any of that, so before this seam existed an
// HTTP client asking for a 17:00 deadline could only be given an all-day one or a
// refusal.
func TestCreateWritesACompleteTemporalValue(t *testing.T) {
	london, err := temporal.NewValue(mustDate(t, "2026-08-08"), "17:00", "Europe/London", 0, true)
	if err != nil {
		t.Fatalf("build value: %v", err)
	}
	ambiguous, err := temporal.NewValue(mustDate(t, "2026-11-01"), "01:30", "America/Los_Angeles", 1, true)
	if err != nil {
		t.Fatalf("build value: %v", err)
	}

	for _, testCase := range []struct {
		name    string
		command CreateCommand
		want    []string
	}{
		{
			name:    "a zoned deadline",
			command: CreateCommand{Title: "T", Deadline: "2026-08-08", DeadlineValue: &london},
			want:    []string{`"deadline":"2026-08-08"`, `"local":"17:00"`, `"timezone":"Europe/London"`},
		},
		{
			name:    "a zoned schedule",
			command: CreateCommand{Title: "T", Scheduled: "2026-08-08", ScheduledValue: &london},
			want:    []string{`"scheduled":"2026-08-08"`, `"local":"17:00"`, `"timezone":"Europe/London"`},
		},
		{
			name:    "an ambiguous wall time keeps its fold",
			command: CreateCommand{Title: "T", Deadline: "2026-11-01", DeadlineValue: &ambiguous},
			want:    []string{`"local":"01:30"`, `"timezone":"America/Los_Angeles"`, `"fold":1`},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			result := h.app.CreateTask(testCase.command, nil)
			if result.Status != store.MutationOK {
				t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
			}
			for _, want := range testCase.want {
				if !strings.Contains(h.read(), want) {
					t.Fatalf("the store does not contain %s:\n%s", want, h.read())
				}
			}
		})
	}
}

// The value WINS over the string for its own field, because it is strictly more
// information about the same thing. The string stays the primary spelling for
// every caller that has only text.
func TestCreateValuePrecedenceAndStringCallersAreUnchanged(t *testing.T) {
	value, err := temporal.NewValue(mustDate(t, "2026-09-09"), "08:15", "Europe/London", 0, true)
	if err != nil {
		t.Fatalf("build value: %v", err)
	}
	h := newHarness(t, harnessOptions{})
	// The two disagree on the date on purpose: the complete value decides.
	result := h.app.CreateTask(CreateCommand{
		Title: "Precedence", Deadline: "2026-08-08", DeadlineValue: &value,
	}, nil)
	if result.Status != store.MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !strings.Contains(h.read(), `"deadline":"2026-09-09"`) {
		t.Fatalf("the value did not win over the string:\n%s", h.read())
	}

	// And a text-only caller is untouched: no time metadata appears at all.
	plain := newHarness(t, harnessOptions{})
	if result := plain.app.CreateTask(CreateCommand{Title: "Plain", Deadline: "2026-08-08"}, nil); !result.OK() {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if strings.Contains(plain.read(), "deadline_time") {
		t.Fatalf("a text-only create invented time metadata:\n%s", plain.read())
	}
}

// The pointee is COPIED when the command is accepted, so a caller that reuses
// its value cannot change what was submitted — the same guarantee Tags and Notes
// already carry, and the reason clone() exists at all.
func TestCreateCommandCopiesAnAcceptedTemporalValue(t *testing.T) {
	value, err := temporal.NewValue(mustDate(t, "2026-08-08"), "17:00", "Europe/London", 0, true)
	if err != nil {
		t.Fatalf("build value: %v", err)
	}
	h := newHarness(t, harnessOptions{})
	command := CreateCommand{Title: "Copied", Deadline: "2026-08-08", DeadlineValue: &value}

	result := h.app.CreateTask(command, nil)
	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	// Reach back into the value the command still points at.
	value.LocalTime = "23:59"
	value.Timezone = "America/New_York"

	if !strings.Contains(h.read(), `"local":"17:00"`) ||
		strings.Contains(h.read(), "23:59") || strings.Contains(h.read(), "New_York") {
		t.Fatalf("the accepted command saw a caller's later mutation:\n%s", h.read())
	}
}

// An invalid value is refused at this boundary rather than persisted, exactly as
// Ruby's TemporalValue validates on construction. A zone with no local time
// cannot be stored, and neither can a wall time that names no instant.
func TestCreateRefusesAnInvalidTemporalValueWithoutWriting(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value temporal.Value
	}{
		{"a zone with no local time", temporal.Value{Date: mustDate(t, "2026-08-08"), Timezone: "Europe/London"}},
		{"a local time that is not HH:MM", temporal.Value{Date: mustDate(t, "2026-08-08"), LocalTime: "5pm"}},
		{"a zone that names no region",
			temporal.Value{Date: mustDate(t, "2026-08-08"), LocalTime: "09:00", Timezone: "PST"}},
		{"a wall time inside a DST gap",
			temporal.Value{Date: mustDate(t, "2026-03-08"), LocalTime: "02:30", Timezone: "America/Los_Angeles"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			before := h.read()
			value := testCase.value
			result := h.app.CreateTask(CreateCommand{
				Title: "T", Deadline: "2026-08-08", DeadlineValue: &value,
			}, nil)
			if result.Status != store.MutationInvalid {
				t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
			}
			if !strings.Contains(result.FirstError(), "deadline_time") {
				t.Fatalf("error = %q, want it to name the field", result.FirstError())
			}
			if h.read() != before {
				t.Fatal("a refused create must not touch the file")
			}
		})
	}
}

func mustDate(t *testing.T, iso string) temporal.Date {
	t.Helper()
	date, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("parse date %q", iso)
	}
	return date
}

// A date that is not a date is refused BEFORE the transaction, naming the
// argument rather than reaching the store as a generic invalid.
func TestCreateRefusesAnUnparseableDate(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	before := h.read()

	result := h.app.CreateTask(CreateCommand{Title: "T", Deadline: "next tuesday"}, nil)
	if result.Status != store.MutationInvalid {
		t.Fatalf("status = %q", result.Status)
	}
	if !strings.Contains(result.FirstError(), "deadline") {
		t.Fatalf("error = %q, want it to name the field", result.FirstError())
	}
	if h.read() != before {
		t.Fatal("a refused create must not touch the file")
	}
}

// The store's own refusals reach the caller unchanged: the application does not
// re-implement them, and must not swallow them either.
func TestStoreRefusalsReachTheCallerUnchanged(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	blank := h.app.CreateTask(CreateCommand{Title: "   "}, nil)
	if blank.Status != store.MutationInvalid || blank.FirstError() != "title cannot be blank" {
		t.Fatalf("blank title = %q %v", blank.Status, blank.Errors)
	}
	missing := h.app.CreateTask(CreateCommand{Title: "Orphan", ParentID: "ffffffff"}, nil)
	if missing.Status != store.MutationNotFound {
		t.Fatalf("missing parent = %q", missing.Status)
	}
	unknown := h.app.CreateTask(CreateCommand{Title: "Nowhere", Project: "Nonexistent"}, nil)
	if unknown.Status != store.MutationInvalid || unknown.FirstError() != "capture project does not exist" {
		t.Fatalf("unknown project = %q %v", unknown.Status, unknown.Errors)
	}
}

// -- construction -------------------------------------------------------------

func TestNewRefusesAMissingStoreFactory(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("an application without a store factory must not be constructible")
	}
}

func TestHostContextMustBeAnAtContext(t *testing.T) {
	factory := func() Store { return nil }
	for _, bad := range []string{"home", "@", "@two words", " @home"} {
		if _, err := New(Options{Factory: factory, HostContext: bad}); err == nil {
			t.Fatalf("host context %q must be refused", bad)
		}
	}
	if _, err := New(Options{Factory: factory, HostContext: "@home"}); err != nil {
		t.Fatalf("@home must be accepted: %v", err)
	}
	if _, err := New(Options{Factory: factory}); err != nil {
		t.Fatalf("no host context must be accepted: %v", err)
	}
}

func TestOperationContextIsValidatedAtConstruction(t *testing.T) {
	if _, err := NewOperationContext("", SourceCLI, ""); err == nil {
		t.Fatal("a blank operation id must be refused")
	}
	if _, err := NewOperationContext("capture-1", OperationSource("shell"), ""); err == nil {
		t.Fatal("an unknown source must be refused")
	}
	operation, err := NewOperationContext("  capture-1  ", SourceCLI, "  marcus  ")
	if err != nil {
		t.Fatal(err)
	}
	if operation.OperationID() != "capture-1" || operation.Actor() != "marcus" {
		t.Fatalf("context = %+v, want trimmed values", operation)
	}
	if operation.Source() != SourceCLI {
		t.Fatalf("source = %q", operation.Source())
	}
	// A nil context is the "none supplied" case every entry point accepts.
	var absent *OperationContext
	if absent.OperationID() != "" || absent.Source() != "" {
		t.Fatal("a nil context must read as empty rather than panic")
	}
	if _, ok := absent.TemporalContext(); ok {
		t.Fatal("a nil context pins no clock")
	}
}

// An operation may pin its own clock, and that pin beats the application's.
func TestOperationContextPinsTheClockForOneOperation(t *testing.T) {
	const records = `{"type":"meta","version":2}
{"type":"section","id":"dd000001","title":"Work"}
{"type":"task","id":"dd000002","parent":"dd000001","state":"NEXT","title":"Tomorrow","scheduled":"2026-07-15"}
`
	h := newHarness(t, harnessOptions{live: records, now: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)})

	if items, _ := h.app.ListTasks(openScope(t), nil); len(items) != 0 {
		t.Fatalf("the application clock lists %v", idsOf(items))
	}

	operation, err := NewOperationContext("api-1", SourceAPI, "")
	if err != nil {
		t.Fatal(err)
	}
	later := operation.WithTemporalContext(temporal.Context{
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Timezone: time.UTC, TimezoneID: "Etc/UTC",
	})
	items, err := h.app.ListTasks(openScope(t), later)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dd000002"}; !equalStrings(idsOf(items), want) {
		t.Fatalf("pinned clock lists %v, want %v", idsOf(items), want)
	}
	// The pin is per-operation: the original context is unchanged.
	if _, ok := operation.TemporalContext(); ok {
		t.Fatal("WithTemporalContext must not mutate the context it copies")
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
