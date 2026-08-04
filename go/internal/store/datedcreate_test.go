package store

import (
	"strings"
	"testing"

	"tasks-go/internal/temporal"
)

// The dated half of Store#normalize_create_task: the two dates, the recurrence
// cookie, the lead span, and the four rules that relate them.
//
// Every refusal here is one the FILE never sees. A create that would need an
// immediate repair — a schedule nothing can reach, a lead outside the storable
// years — is a create that should have been refused, because the alternative is
// a record whose own store would reject it on the next write.

func isoValue(t *testing.T, iso string) temporal.Value {
	t.Helper()
	date, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("not a date: %q", iso)
	}
	return temporal.Value{Date: date}
}

func createdLine(t *testing.T, target *Store, result MutationResult) string {
	t.Helper()
	if len(result.TouchedIDs) == 0 {
		t.Fatal("the create reported no id")
	}
	return line(t, target, result.TouchedIDs[0])
}

// A dated create writes the date and lands as TODO rather than INBOX: a task
// that is already scheduled has been processed, whatever else is true of it.
func TestADatedCreateWritesTheDateAndDefaultsToTODO(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command CreateCommand
		want    string
	}{
		{"a deadline", CreateCommand{Title: "Dated", Deadline: isoValue(t, "2026-09-09"),
			HasDeadline: true}, `"deadline":"2026-09-09"`},
		{"an available-from date", CreateCommand{Title: "Dated",
			Scheduled: isoValue(t, "2026-09-01"), HasScheduled: true}, `"scheduled":"2026-09-01"`},
	} {
		target, _ := writerFixture(t, fixtureStore)
		result := target.CreateTask(testCase.command, "2026-06-10")
		if result.Status != MutationOK {
			t.Fatalf("%s: status = %q, errors = %v", testCase.name, result.Status, result.Errors)
		}
		written := createdLine(t, target, result)
		if !strings.Contains(written, testCase.want) {
			t.Errorf("%s: %s does not contain %s", testCase.name, written, testCase.want)
		}
		if !strings.Contains(written, `"state":"TODO"`) {
			t.Errorf("%s: a dated create did not default to TODO: %s", testCase.name, written)
		}
		assertChecked(t, target)
	}
}

// An explicit state still wins. The dated default is a default, not a rule.
func TestAnExplicitStateBeatsTheDatedDefault(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{
		Title: "Dated but next", State: "NEXT",
		Deadline: isoValue(t, "2026-09-09"), HasDeadline: true,
	}, "2026-06-10")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !strings.Contains(createdLine(t, target, result), `"state":"NEXT"`) {
		t.Error("the explicit state was overwritten")
	}
}

// A timed date carries its wall time, zone and fold into the record, through
// the same emitter a patch writes.
func TestATimedCreateWritesTheWholeTemporalValue(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	value, err := temporal.ParseExpression("2026-11-01 01:30", temporal.ParseOptions{
		Today: isoValue(t, "2026-06-10").Date, Timezone: "America/Los_Angeles", Fold: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := target.CreateTask(CreateCommand{
		Title: "Timed", Deadline: value, HasDeadline: true,
	}, "2026-06-10")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	written := createdLine(t, target, result)
	for _, want := range []string{`"deadline":"2026-11-01"`, `"local":"01:30"`,
		`"timezone":"America/Los_Angeles"`, `"fold":1`} {
		if !strings.Contains(written, want) {
			t.Errorf("%s does not contain %s", written, want)
		}
	}
	assertChecked(t, target)
}

// Capturing with a recurrence and no date has always meant "start repeating
// now": the store seeds today rather than refusing.
func TestARecurringCreateWithoutADateSeedsToday(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{Title: "Weekly", Recurrence: ".+1w"}, "2026-06-10")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	written := createdLine(t, target, result)
	if !strings.Contains(written, `"scheduled":"2026-06-10"`) ||
		!strings.Contains(written, `"recur":".+1w"`) {
		t.Errorf("record = %s", written)
	}
	assertChecked(t, target)
}

// With a LEAD that reading is wrong: today's anchor puts the window in the
// past, so the task would appear immediately and the schedule's own first
// occurrence would never be used. A lead therefore seeds the FIRST OCCURRENCE,
// which is what makes `--recur y:06-01 --lead 17d` mean "invisible until May
// 15" with no further arguments.
func TestARecurringCreateWithALeadSeedsTheFirstOccurrence(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{
		Title: "Annual", Recurrence: "y:06-01", Lead: "17d",
	}, "2026-06-10")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	written := createdLine(t, target, result)
	// The next 1 June after 2026-06-10 is 2027-06-01, and 17 days before that
	// is 2027-05-15 — not today.
	if !strings.Contains(written, `"scheduled":"2027-06-01"`) {
		t.Errorf("record = %s, want the first occurrence rather than today", written)
	}
	if !strings.Contains(written, `"lead":"17d"`) {
		t.Errorf("record = %s, want the lead span", written)
	}
	assertChecked(t, target)
}

// The four refusals, each stated against the values this create was about to
// write rather than against a record that does not exist yet.
func TestTheDatedCreateRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command CreateCommand
		want    string
	}{
		{"a recurrence on a closed state",
			CreateCommand{Title: "x", State: "DONE", Recurrence: ".+1w"},
			"can't set recurrence on a DONE task"},
		{"a recurrence on a proposal",
			CreateCommand{Title: "x", State: "PROPOSED", Recurrence: ".+1w"},
			"can't set recurrence on a PROPOSED task"},
		{"a lead with no date to hide before",
			CreateCommand{Title: "x", Lead: "2d"},
			"a lead time needs a date to hide before — add a deadline or an available-from date first"},
		{"an invalid cookie",
			CreateCommand{Title: "x", Recurrence: "not a schedule"},
			"invalid recurrence cookie"},
		{"an invalid span",
			CreateCommand{Title: "x", Lead: "soonish",
				Deadline: isoValue(t, "2026-09-09"), HasDeadline: true},
			"invalid lead time (expected a span like 3w, 2d, 1m, 1y)"},
	} {
		target, _ := writerFixture(t, fixtureStore)
		before := readStore(t, target)
		result := target.CreateTask(testCase.command, "2026-06-10")
		if result.Status != MutationInvalid {
			t.Errorf("%s: status = %q, want invalid", testCase.name, result.Status)
			continue
		}
		if !containsMessage(result.Errors, testCase.want) {
			t.Errorf("%s: errors = %v, want %q", testCase.name, result.Errors, testCase.want)
		}
		if readStore(t, target) != before {
			t.Errorf("%s: a refused create wrote bytes", testCase.name)
		}
	}
}

// Rule 3: a lead measures from the deadline, so carrying an available-from date
// beside it would leave a second, ignored gate.
func TestALeadWithBothDatesIsRefusedAtCreate(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{
		Title: "Both", Lead: "2d",
		Deadline: isoValue(t, "2026-09-09"), HasDeadline: true,
		Scheduled: isoValue(t, "2026-09-01"), HasScheduled: true,
	}, "2026-06-10")
	if result.Status != MutationInvalid {
		t.Fatalf("status = %q, want invalid", result.Status)
	}
	if !strings.Contains(result.FirstError(), "second, ignored gate") {
		t.Errorf("error = %q", result.FirstError())
	}
}

// Rule 5: the derived gate has to stay a storable date. A lead measured back
// past year 0 is not one.
func TestALeadOutsideTheStorableYearsIsRefusedAtCreate(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{
		Title: "Ancient", Lead: "9999y",
		Deadline: isoValue(t, "2026-09-09"), HasDeadline: true,
	}, "2026-06-10")
	if result.Status != MutationInvalid {
		t.Fatalf("status = %q, want invalid", result.Status)
	}
	if !strings.Contains(result.FirstError(), "outside the four-digit years") {
		t.Errorf("error = %q", result.FirstError())
	}
}

// A create under an explicit parent still lands in that parent's subtree, and
// the two destinations remain mutually exclusive.
func TestCreateUnderAParentAndTheProjectConflict(t *testing.T) {
	target, _ := writerFixture(t, fixtureStore)
	result := target.CreateTask(CreateCommand{Title: "Child", ParentID: "1a2b3c02"}, "2026-06-10")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !strings.Contains(createdLine(t, target, result), `"parent":"1a2b3c02"`) {
		t.Error("the task did not land under its parent")
	}

	conflict := target.CreateTask(CreateCommand{
		Title: "Ambiguous", ParentID: "1a2b3c02", Project: "Inbox",
	}, "2026-06-10")
	if conflict.Status != MutationInvalid ||
		!containsMessage(conflict.Errors, "project and parent_id cannot both be supplied") {
		t.Errorf("conflict = %q %v", conflict.Status, conflict.Errors)
	}
}

func containsMessage(messages []string, want string) bool {
	for _, message := range messages {
		if message == want {
			return true
		}
	}
	return false
}
