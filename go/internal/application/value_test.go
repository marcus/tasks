package application

import (
	"sync"
	"testing"

	"tasks-go/internal/store"
)

// test_task_placement.rb's value-object half: a destination is two stable ids,
// and no anchor means "append as the parent's last child".
func TestPlacementIsTwoStableIDsAndNothingElse(t *testing.T) {
	appended := Placement{ParentID: fixWork}
	if !appended.Appends() {
		t.Fatal("no anchor means append")
	}
	anchored := Placement{ParentID: fixWork, BeforeID: fixFlight}
	if anchored.Appends() {
		t.Fatal("an anchor is not an append")
	}
	if anchored.ParentID != fixWork || anchored.BeforeID != fixFlight {
		t.Fatalf("placement = %+v", anchored)
	}
}

func TestDelegationCommandKindPredicates(t *testing.T) {
	human := DelegationCommand{ID: fixPlants, Action: ActionDelegate, Kind: "human"}
	agent := DelegationCommand{ID: fixPlants, Action: ActionDelegate, Kind: "agent"}
	neither := DelegationCommand{ID: fixPlants, Action: ActionDelegate}

	if !human.Human() || human.Agent() {
		t.Fatal("human predicates")
	}
	if !agent.Agent() || agent.Human() {
		t.Fatal("agent predicates")
	}
	if neither.Human() || neither.Agent() {
		t.Fatal("an unset kind is neither")
	}
}

// test_checked_result_owns_an_immutable_copy_of_plain_payloads — adapted.
//
// Ruby deep-freezes the payload because a Hash handed to a result is the same
// Hash the caller still holds. The Go answer is that a command's slices are
// COPIED when the application accepts one, which is the same guarantee at the
// only boundary that matters.
func TestAnAcceptedCommandDoesNotAliasTheCallersSlices(t *testing.T) {
	tags := []string{"@work"}
	notes := []string{"one"}
	command := CreateCommand{Title: "Draft", Tags: tags, Notes: notes}

	accepted := command.clone()
	tags[0] = "mutated"
	notes[0] = "mutated"

	if accepted.Tags[0] != "@work" || accepted.Notes[0] != "one" {
		t.Fatalf("accepted = %+v", accepted)
	}

	// And the reverse: preparing a command must not write through to the
	// caller's slice either.
	app, err := New(Options{Factory: func() Store { return nil }, HostContext: "@home"})
	if err != nil {
		t.Fatal(err)
	}
	prepared := app.PrepareCreateTask(CreateCommand{Title: "Draft", Tags: tags})
	if len(tags) != 1 || tags[0] != "mutated" {
		t.Fatalf("caller slice = %v", tags)
	}
	if len(prepared.Tags) != 2 || prepared.Tags[0] != "@home" {
		t.Fatalf("prepared = %v", prepared.Tags)
	}
}

// An unknown nested key is not an error and is simply not part of the flat
// marker the application reasons about.
func TestDecodeMarkerKeepsStringsAndDropsTheRest(t *testing.T) {
	marker := decodeMarker(mustJSON(t, map[string]any{
		"kind": "agent", "status": "ready", "count": 3,
		"nested": map[string]any{"a": "b"},
	}))
	if marker["kind"] != "agent" || marker["status"] != "ready" {
		t.Fatalf("marker = %v", marker)
	}
	if _, present := marker["count"]; present {
		t.Fatalf("marker = %v", marker)
	}
	if _, present := marker["nested"]; present {
		t.Fatalf("marker = %v", marker)
	}
	if decodeMarker(nil) != nil {
		t.Fatal("no marker decodes to nil")
	}
	if decodeMarker([]byte("not json")) != nil {
		t.Fatal("unparseable bytes decode to nil rather than panicking")
	}
}

// The read vocabulary maps name for name, and an unrecognized store status
// degrades to `unavailable` rather than to `ok`.
func TestReadStatusMapsTheStoreVocabulary(t *testing.T) {
	cases := map[store.Status]ReadStatus{
		store.StatusOK:                ReadOK,
		store.StatusUnsupportedSchema: ReadUnsupportedSchema,
		store.StatusStoreInvalid:      ReadStoreInvalid,
		store.StatusUnavailable:       ReadUnavailable,
		store.Status("invented"):      ReadUnavailable,
	}
	for from, want := range cases {
		if got := readStatusOf(from); got != want {
			t.Fatalf("%q -> %q, want %q", from, got, want)
		}
	}
}

// TaskResultFromMutation refuses a result it cannot answer coherently rather
// than racing a second read to fill the gap.
func TestTaskResultFromMutationRequiresACoherentSnapshot(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	if _, err := h.app.TaskResultFromMutation(Outcome{}, fixPlants, nil); err == nil {
		t.Fatal("a zero outcome carries no snapshot")
	}
	refusal := Outcome{MutationResult: store.MutationResult{Status: store.MutationInvalid}}
	if _, err := h.app.TaskResultFromMutation(refusal, fixPlants, nil); err == nil {
		t.Fatal("a refusal carries no snapshot")
	}

	created := h.app.CreateTask(CreateCommand{Title: "Coherent", Project: "Home"}, nil)
	result, err := h.app.TaskResultFromMutation(created, created.TouchedIDs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() || result.Data.Title != "Coherent" {
		t.Fatalf("result = %q %+v", result.Status, result.Data)
	}
	if result.StoreRevision != created.StoreRevision {
		t.Fatal("the response revision is the write's own")
	}

	// An id that is not in the write's snapshot is not_found, not an error.
	missing, err := h.app.TaskResultFromMutation(created, "ffffffff", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.NotFound() {
		t.Fatalf("status = %q", missing.Status)
	}
}

// The application will be shared by a concurrent API surface, so it must hold
// no mutable state of its own: every operation builds its own store and its own
// snapshot, and nothing crosses between them.
func TestConcurrentOperationsShareNoApplicationState(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	var wait sync.WaitGroup
	errors := make(chan string, 64)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for round := 0; round < 4; round++ {
				if _, err := h.app.ListTasks(openScope(t), nil); err != nil {
					errors <- err.Error()
					return
				}
				if result := h.app.ListTasksResult(allScope(t), nil); !result.OK() {
					errors <- string(result.Status)
					return
				}
				if _, err := h.app.ReadTasks(nil); err != nil {
					errors <- err.Error()
					return
				}
				if _, found, err := h.app.GetTask(fixFlight, false, nil); err != nil || !found {
					errors <- "lookup failed"
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	close(errors)
	for message := range errors {
		t.Fatalf("concurrent read failed: %s", message)
	}
}

// Concurrent WRITES through the application serialize on the store's lock, and
// every one of them either lands or refuses cleanly — none corrupts the file.
func TestConcurrentCapturesAllLandOrRefuseCleanly(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	var wait sync.WaitGroup
	statuses := make(chan store.MutationStatus, 16)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			result := h.app.CreateTask(CreateCommand{
				Title: "concurrent capture", Project: "Home",
			}, nil)
			statuses <- result.Status
		}(worker)
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != store.MutationOK {
			t.Fatalf("capture status = %q", status)
		}
	}
	h.assertChecks()

	captured := 0
	for _, parsed := range recordsOf(t, h) {
		if parsed.String("title") == "concurrent capture" {
			captured++
		}
	}
	if captured != 8 {
		t.Fatalf("captured %d of 8", captured)
	}
}
