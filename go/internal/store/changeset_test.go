package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tasks-go/internal/temporal"
)

func applyChanges(t *testing.T, store *Store, id string, changes ...Change) MutationResult {
	t.Helper()
	revision, _ := store.TaskRevision(id)
	return store.ApplyChangeset(Changeset{
		ID: id, Changes: changes, ExpectedRevision: revision, Today: "2026-06-10",
	})
}

// A changeset is ONE write and ONE undo step, whatever it touches.
func TestChangesetIsOneCheckedWriteAndOneHistoryStep(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	result := mustOK(t, applyChanges(t, store, "aa000010",
		Change{FieldTitle, TextValue("Renamed")},
		Change{FieldPriority, TextValue("A")},
		Change{FieldBody, TextValue("new note")},
	))
	if len(result.TouchedIDs) != 1 || result.TouchedIDs[0] != "aa000010" {
		t.Errorf("touched = %v", result.TouchedIDs)
	}
	stored := line(t, store, "aa000010")
	for _, want := range []string{`"title":"Renamed"`, `"priority":"A"`, `"body":"new note"`} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %s in %s", want, stored)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"label"`) != 1 {
		t.Errorf("more than one history step:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"label": "edit title, priority, body: Plain task"`) {
		t.Errorf("label: %s", raw)
	}
}

// Fields are applied in FIELD_ORDER, not in the caller's order, so two callers
// who ask for the same thing differently get the same bytes.
func TestChangesetAppliesInFieldOrderNotCallerOrder(t *testing.T) {
	first, _ := writerFixture(t, patchFixture)
	second, _ := writerFixture(t, patchFixture)
	mustOK(t, applyChanges(t, first, "aa000010",
		Change{FieldState, TextValue("NEXT")},
		Change{FieldTitle, TextValue("Renamed")},
	))
	mustOK(t, applyChanges(t, second, "aa000010",
		Change{FieldTitle, TextValue("Renamed")},
		Change{FieldState, TextValue("NEXT")},
	))
	if line(t, first, "aa000010") != line(t, second, "aa000010") {
		t.Errorf("caller order changed the bytes\n %s\n %s",
			line(t, first, "aa000010"), line(t, second, "aa000010"))
	}
}

// An invalid LATER field cannot leak the earlier ones into the file: the whole
// changeset is applied to a detached copy first.
func TestChangesetIsAtomicAcrossAnInvalidLaterField(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	before := readStore(t, store)
	result := applyChanges(t, store, "aa000010",
		Change{FieldTitle, TextValue("Renamed")},
		Change{FieldState, TextValue("BOGUS")},
	)
	if result.Status != MutationInvalid {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if got := readStore(t, store); got != before {
		t.Error("a partial changeset reached the file")
	}
	if _, err := os.Stat(filepath.Join(root, "journal", "index.json")); err == nil {
		t.Error("a refused changeset recorded history")
	}
}

// A changeset that MOVES the anchor passes through a momentary dateless state,
// and must not lose the recurrence it was relocating.
func TestChangesetMovingTheAnchorKeepsTheRecurrence(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	date := mustDate(t, "2026-07-10")
	mustOK(t, applyChanges(t, store, "aa000013",
		Change{FieldScheduled, NoValue()},
		Change{FieldDeadline, TemporalValue(date)},
	))
	stored := line(t, store, "aa000013")
	if !strings.Contains(stored, `"recur":".+1w"`) {
		t.Errorf("relocating the anchor dropped the cookie: %s", stored)
	}
	if !strings.Contains(stored, `"deadline":"2026-07-10"`) || strings.Contains(stored, `"scheduled"`) {
		t.Errorf("anchor move: %s", stored)
	}
}

// Clearing the LAST date in one changeset does retire the intents.
func TestChangesetClearingEveryDateRetiresTheIntents(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Both","scheduled":"2026-06-15","deadline":"2026-06-20","recur":"m:15"}
`)
	mustOK(t, applyChanges(t, store, "aa000010",
		Change{FieldScheduled, NoValue()},
		Change{FieldDeadline, NoValue()},
	))
	stored := line(t, store, "aa000010")
	if strings.Contains(stored, `"recur"`) || strings.Contains(stored, `"lead"`) {
		t.Errorf("a dateless task kept an intent: %s", stored)
	}
}

// The validator refuses the combinations whose fields own overlapping state,
// before the file is read.
func TestChangesetRefusesOverlappingCombinations(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	for _, testCase := range []struct {
		name    string
		changes []Change
		want    string
	}{
		{"tag_delta with a slice",
			[]Change{{FieldTagDelta, TagDeltaValue([]string{"x"}, nil)}, {FieldTags, ListValue([]string{"y"})}},
			"tag_delta cannot be combined with tag slice changes"},
		{"date_clear with a date",
			[]Change{{FieldDateClear, NoValue()}, {FieldScheduled, NoValue()}},
			"date_clear cannot be combined with scheduled or deadline"},
		{"activate with deferred",
			[]Change{{FieldActivate, BoolValue(true)}, {FieldDeferred, BoolValue(false)}},
			"activate cannot be combined with deferred or scheduled"},
		{"recur and recurrence are one field",
			[]Change{{"recur", TextValue("m:15")}, {FieldRecurrence, TextValue("m:15")}},
			`changes repeat "recurrence"`},
		{"unknown field",
			[]Change{{"nonsense", TextValue("x")}},
			`unknown editable field "nonsense"`},
		{"no changes", nil, "changes must be a non-empty mapping"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := applyChanges(t, store, "aa000010", testCase.changes...)
			if result.Status != MutationInvalid {
				t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
			}
			if !contains(result.Errors, testCase.want) {
				t.Errorf("errors = %v, want %q", result.Errors, testCase.want)
			}
		})
	}
}

// The revision compares only the components the changeset's fields can
// invalidate: a title edit survives a sibling change, a MOVE would not, and a
// state change fails on a descendant's lifecycle.
func TestChangesetRevisionNarrowsToTheFieldsItChanges(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	revision, ok := store.TaskRevision("aa000017")
	if !ok {
		t.Fatal("no revision")
	}
	// An unrelated field on a descendant does not touch `own`.
	mustOK(t, patch(t, store, "aa000018", FieldBody, TextValue("note")))
	if result := (store.ApplyChangeset(Changeset{
		ID: "aa000017", Changes: []Change{{FieldTitle, TextValue("Renamed")}},
		ExpectedRevision: revision, Today: "2026-06-10",
	})); result.Status != MutationOK {
		t.Errorf("title changeset = %q, want ok", result.Status)
	}

	fresh, _ := store.TaskRevision("aa000017")
	mustOK(t, patch(t, store, "aa000018", FieldState, TextValue("WAITING")))
	if result := (store.ApplyChangeset(Changeset{
		ID: "aa000017", Changes: []Change{{FieldState, TextValue("DONE")}},
		ExpectedRevision: fresh, Today: "2026-06-10",
	})); result.Status != MutationStale {
		t.Errorf("state changeset = %q, want stale", result.Status)
	}
}

func TestChangesetRefusesAMalformedRevision(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	result := store.ApplyChangeset(Changeset{
		ID: "aa000010", Changes: []Change{{FieldTitle, TextValue("Renamed")}},
		ExpectedRevision: "not-a-revision", Today: "2026-06-10",
	})
	if result.Status != MutationInvalid || result.FirstError() != "malformed expected_revision" {
		t.Errorf("status = %q, errors = %v", result.Status, result.Errors)
	}
}

func mustDate(t *testing.T, iso string) temporal.Value {
	t.Helper()
	date, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("bad date %q", iso)
	}
	value, err := temporal.NewValue(date, "", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
