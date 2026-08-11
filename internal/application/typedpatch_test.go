package application

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/store"
)

// The typed patch capability exists for fields whose value a
// string cannot carry. These tests are about that boundary, not about the
// store's own field rules, which store_test already owns.

func TestTypedPatchWritesNonStringFieldShapes(t *testing.T) {
	cases := []struct {
		name   string
		field  store.PatchField
		value  store.PatchValue
		expect func(line string) bool
		want   string
	}{
		{
			name: "deferred is a bool", field: store.FieldDeferred,
			value: store.BoolValue(true),
			expect: func(line string) bool {
				return strings.Contains(line, `"`+store.DeferTag+`"`)
			},
			want: "the defer marker in the tag list",
		},
		{
			name: "contexts is an ordered list", field: store.FieldContexts,
			value: store.ListValue([]string{"@home", "@errand"}),
			expect: func(line string) bool {
				return strings.Contains(line, `"@home"`) && strings.Contains(line, `"@errand"`)
			},
			want: "both contexts",
		},
		{
			name: "tags is an ordered list", field: store.FieldTags,
			value: store.ListValue([]string{"important", "billing"}),
			expect: func(line string) bool {
				return strings.Contains(line, `"billing"`)
			},
			want: "the new tag",
		},
		{
			name: "formal links are ordered objects", field: store.FieldLinks,
			value: store.LinksValue([]links.FormalLink{{URL: "https://example.com", Label: "Example"}}),
			expect: func(line string) bool {
				return strings.Contains(line, `"links":[{"url":"https://example.com","label":"Example"}]`)
			},
			want: "the formal link",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			expected, present := h.app.Baseline(fixFlight, testCase.field)
			if !present {
				t.Fatalf("no baseline published for %s", testCase.field)
			}
			outcome := h.app.PatchTask(TypedPatch(fixFlight, testCase.field, testCase.value,
				expected, "edit "+string(testCase.field), "one-edit"), nil)
			if !outcome.OK() {
				t.Fatalf("typed patch refused: %s %v", outcome.Status, outcome.Errors)
			}
			line := taskLine(t, h.read(), fixFlight)
			if !testCase.expect(line) {
				t.Errorf("the stored record does not carry %s: %s", testCase.want, line)
			}
		})
	}
}

// The narrow conflict check has to work for a typed value exactly as it does
// for a string one, or the editor's save-on-blur would overwrite a concurrent
// edit rather than refusing it.
func TestTypedPatchStillHonoursTheFieldOwnedBaseline(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	stale, _ := h.app.Baseline(fixFlight, store.FieldContexts)

	first := h.app.PatchTask(TypedPatch(fixFlight, store.FieldContexts,
		store.ListValue([]string{"@office"}), stale, "edit contexts", ""), nil)
	if !first.OK() {
		t.Fatalf("the first write refused: %s", first.Status)
	}

	second := h.app.PatchTask(TypedPatch(fixFlight, store.FieldContexts,
		store.ListValue([]string{"@home"}), stale, "edit contexts", ""), nil)
	if !second.Conflict() {
		t.Fatalf("a write against a stale baseline produced %s, want conflict", second.Status)
	}
	if line := taskLine(t, h.read(), fixFlight); !strings.Contains(line, `"@office"`) {
		t.Errorf("the refused write still changed the record: %s", line)
	}
}

// One editing session is one undo step. The coalesce key is what buys that, and
// it has to survive the typed path as well as the string one.
func TestTypedPatchCarriesTheCoalesceKeyToTheStore(t *testing.T) {
	wrap, double := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	expected, _ := h.app.Baseline(fixFlight, store.FieldTags)
	outcome := h.app.PatchTask(TypedPatch(fixFlight, store.FieldTags,
		store.ListValue([]string{"important", "urgent", "billing"}),
		expected, "edit tags", "session-key"), nil)
	if !outcome.OK() {
		t.Fatalf("typed patch refused: %s %v", outcome.Status, outcome.Errors)
	}
	found := false
	for _, call := range double.log() {
		if call.verb == "typed_patch:tags" {
			found = true
			if call.coalesceKey != "session-key" {
				t.Errorf("coalesce key reached the store as %q", call.coalesceKey)
			}
		}
	}
	if !found {
		t.Errorf("the typed capability was not used; calls were %v", double.log())
	}
}

// A store without the capability must REFUSE by name rather than fall back to
// the string spelling, which would reach the store's own confusing complaint
// about a value the caller never sent as text.
func TestTypedPatchRefusesAStoreThatCannotCarryIt(t *testing.T) {
	h := newHarness(t, harnessOptions{
		wrap: func(built *store.Store) Store { return stringOnlyStore{inner: built} },
	})
	outcome := h.app.PatchTask(TypedPatch(fixFlight, store.FieldContexts,
		store.ListValue([]string{"@home"}), "", "edit contexts", ""), nil)
	if outcome.Status != store.MutationInvalid && !strings.Contains(
		strings.Join(outcome.Errors, " "), "typed value") {
		t.Fatalf("a capability-less store produced %s %v, want a named refusal",
			outcome.Status, outcome.Errors)
	}
}

// stringOnlyStore is the Store seam with the typed capability deliberately
// ABSENT, which is the state every non-*store.Store adapter starts in.
//
// It forwards the interface method-for-method rather than embedding the store,
// because embedding would promote `Patch` and the capability probe would find
// the very method this double exists to hide.
type stringOnlyStore struct{ inner *store.Store }

func (s stringOnlyStore) Org() string     { return s.inner.Org() }
func (s stringOnlyStore) Archive() string { return s.inner.Archive() }

func (s stringOnlyStore) ReadSnapshot(includeArchive bool) (*store.Snapshot, error) {
	return s.inner.ReadSnapshot(includeArchive)
}

func (s stringOnlyStore) CheckedReadSnapshot() (store.CheckedRead, error) {
	return s.inner.CheckedReadSnapshot()
}

func (s stringOnlyStore) CreateTask(command store.CreateCommand, today string) store.MutationResult {
	return s.inner.CreateTask(command, today)
}

func (s stringOnlyStore) PatchTask(id string, field store.PatchField,
	value, expected, label, today string) store.MutationResult {

	return s.inner.PatchTask(id, field, value, expected, label, today)
}

func (s stringOnlyStore) ExpectedFor(id string, field store.PatchField) (string, bool) {
	return s.inner.ExpectedFor(id, field)
}

func (s stringOnlyStore) Delegate(id, kind, mode, assignee, coalesceKey string) store.MutationResult {
	return s.inner.Delegate(id, kind, mode, assignee, coalesceKey)
}

func (s stringOnlyStore) Claim(id, worker, coalesceKey string) store.MutationResult {
	return s.inner.Claim(id, worker, coalesceKey)
}

func (s stringOnlyStore) PatchesField(field store.PatchField) bool {
	return s.inner.PatchesField(field)
}

// taskLine finds one task's stored line, so an assertion is about bytes.
func taskLine(t *testing.T, content, id string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, `"id":"`+id+`"`) {
			return line
		}
	}
	t.Fatalf("no line carries id %s", id)
	return ""
}
