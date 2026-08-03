package application

import (
	"regexp"
	"strings"
	"testing"

	"tasks-go/internal/store"
)

var claimStampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

func delegateAgent(t *testing.T, h *harness, id, mode string) Outcome {
	t.Helper()
	result := h.app.DelegateTask(DelegationCommand{ID: id, Kind: "agent", Mode: mode}, nil)
	if !result.Changed() {
		t.Fatalf("delegate: status = %q errors = %v", result.Status, result.Errors)
	}
	return result
}

// test_agent_delegation_returns_the_marker_and_the_canonical_resource
func TestAgentDelegationReturnsTheMarkerAndTheCanonicalResource(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "agent", Mode: "research",
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	summary := result.Delegation
	if summary == nil {
		t.Fatal("a delegation carries a delegation summary")
	}
	if summary.Action != ActionDelegate || summary.TaskID != fixPlants {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Previous != nil {
		t.Fatalf("previous = %v, want none", summary.Previous)
	}
	if summary.Delegation["mode"] != "research" || summary.Delegation["status"] != "ready" {
		t.Fatalf("marker = %v", summary.Delegation)
	}
	if summary.State != "NEXT" {
		t.Fatalf("state = %q, want the lifecycle untouched", summary.State)
	}
	if summary.StateChanged {
		t.Fatal("agent delegation never moves lifecycle state")
	}
	if summary.Task == nil || summary.Task.ID != fixPlants {
		t.Fatalf("task = %+v", summary.Task)
	}
	if want := []string{fixPlants}; !equalStrings(result.TouchedIDs, want) {
		t.Fatalf("touched = %v, want %v", result.TouchedIDs, want)
	}
	if !revisionPattern.MatchString(result.StoreRevision) {
		t.Fatalf("revision = %q", result.StoreRevision)
	}
	h.assertChecks()
}

// test_human_delegation_moves_to_waiting_in_one_undo_step
//
// The undo-step half of the Ruby name is what this build cannot yet prove: the
// composed state write needs a coalescing patch the store does not offer. What
// IS proved is everything the composition owes — the state moved, the summary
// says so, and the persisted record agrees.
func TestHumanDelegationMovesToWaiting(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	summary := result.Delegation
	if !summary.StateChanged {
		t.Fatal("delegating to a person moves the task to WAITING")
	}
	if summary.State != "WAITING" || summary.Task.State != "WAITING" {
		t.Fatalf("state = %q / %q", summary.State, summary.Task.State)
	}
	if summary.Delegation["assignee"] != "pat@example.com" {
		t.Fatalf("marker = %v", summary.Delegation)
	}
	if summary.Delegation["status"] != "delegated" {
		t.Fatalf("status = %q, want delegated", summary.Delegation["status"])
	}
	if h.task(fixPlants).State != "WAITING" {
		t.Fatal("the persisted record must carry the WAITING the summary reports")
	}
	h.assertChecks()
}

// The composed write is a SEPARATE undo step until the store can coalesce, and
// that fact is reported rather than hidden. Ruby folds both writes into one
// journal entry via the delegation's coalesce key.
func TestComposedDelegationWriteReportsItsUndoGranularity(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q", result.Status)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "separate undo step") {
		t.Fatalf("errors = %v, want the granularity report", result.Errors)
	}
}

// With a coalescing store the composed write carries the SAME key as the
// delegation, which is what makes the pair one undo step.
func TestComposedDelegationSharesTheDelegationCoalesceKey(t *testing.T) {
	wrap, double := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil)
	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("a coalescing store reports no granularity loss: %v", result.Errors)
	}

	calls := double.log()
	if len(calls) != 1 || calls[0].verb != "patch:state" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.HasPrefix(calls[0].coalesceKey, "delegation-delegate-") {
		t.Fatalf("coalesce key = %q", calls[0].coalesceKey)
	}
	if calls[0].detail != "WAITING" {
		t.Fatalf("patched value = %q", calls[0].detail)
	}
}

// test_keep_state_opts_out_of_the_waiting_default
func TestKeepStateOptsOutOfTheWaitingDefault(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com", KeepState: true,
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if result.Delegation.StateChanged {
		t.Fatal("keep_state opts out of the WAITING default")
	}
	if result.Delegation.State != "NEXT" {
		t.Fatalf("state = %q", result.Delegation.State)
	}
	if h.task(fixPlants).State != "NEXT" {
		t.Fatal("the persisted state must be untouched")
	}
}

// test_replacing_human_with_agent_delegation_and_back
func TestReplacingHumanWithAgentDelegationAndBack(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	if result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil); !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}

	toAgent := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "agent", Mode: "refine",
	}, nil)
	if !toAgent.Changed() {
		t.Fatalf("status = %q errors = %v", toAgent.Status, toAgent.Errors)
	}
	if toAgent.Delegation.Previous["kind"] != "human" {
		t.Fatalf("previous = %v", toAgent.Delegation.Previous)
	}
	if toAgent.Delegation.Delegation["kind"] != "agent" {
		t.Fatalf("marker = %v", toAgent.Delegation.Delegation)
	}
	if toAgent.Delegation.State != "TODO" {
		t.Fatalf("state = %q — replacing the person undoes the WAITING delegating to them set",
			toAgent.Delegation.State)
	}
	if !toAgent.Delegation.StateChanged {
		t.Fatal("the state change must be reported")
	}

	back := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "sam@example.com",
	}, nil)
	if back.Delegation.Previous["kind"] != "agent" {
		t.Fatalf("previous = %v", back.Delegation.Previous)
	}
	if back.Delegation.Delegation["assignee"] != "sam@example.com" {
		t.Fatalf("marker = %v", back.Delegation.Delegation)
	}
	h.assertChecks()
}

// test_agent_delegation_only_clears_a_waiting_the_human_delegation_set
func TestAgentDelegationOnlyClearsAWaitingTheHumanDelegationSet(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	// A WAITING the owner set themselves is theirs to keep: no human marker is
	// being replaced, so the agent delegation must not touch the state.
	expected, found := h.app.Baseline(fixPlants, store.FieldState)
	if !found {
		t.Fatal("the state baseline must be readable")
	}
	if patched := h.app.PatchTask(Patch{
		ID: fixPlants, Field: store.FieldState, Value: "WAITING", Expected: expected,
	}, nil); !patched.OK() {
		t.Fatalf("state patch = %q %v", patched.Status, patched.Errors)
	}

	result := delegateAgent(t, h, fixPlants, "research")

	if result.Delegation.StateChanged {
		t.Fatal("an owner-set WAITING is not the delegation's to clear")
	}
	if result.Delegation.State != "WAITING" {
		t.Fatalf("state = %q", result.Delegation.State)
	}
}

// test_agent_delegation_can_keep_the_inherited_waiting_state
func TestAgentDelegationCanKeepTheInheritedWaitingState(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	if result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil); !result.Changed() {
		t.Fatalf("status = %q", result.Status)
	}
	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "agent", Mode: "research", KeepState: true,
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if result.Delegation.StateChanged {
		t.Fatal("keep_state opts out in both directions")
	}
	if result.Delegation.State != "WAITING" {
		t.Fatalf("state = %q", result.Delegation.State)
	}
}

// test_claim_returns_the_full_resource_and_a_lost_race_names_the_holder
func TestClaimReturnsTheFullResourceAndALostRaceNamesTheHolder(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	delegateAgent(t, h, fixPlants, "research")

	won := h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)
	if !won.Changed() {
		t.Fatalf("status = %q errors = %v", won.Status, won.Errors)
	}
	if won.Delegation.Action != ActionClaim {
		t.Fatalf("action = %q", won.Delegation.Action)
	}
	if won.Delegation.Delegation["assignee"] != worker {
		t.Fatalf("marker = %v", won.Delegation.Delegation)
	}
	if won.Delegation.Delegation["mode"] != "research" {
		t.Fatalf("a claim keeps the mode: %v", won.Delegation.Delegation)
	}
	if won.Delegation.Task.Title != "Water the plants" {
		t.Fatalf("the winner reads the whole task: %+v", won.Delegation.Task)
	}

	lost := h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: rival}, nil)
	if !lost.Conflict() {
		t.Fatalf("status = %q", lost.Status)
	}
	if lost.Delegation.Action != ActionClaim || lost.Delegation.TaskID != fixPlants {
		t.Fatalf("a refusal still names the operation: %+v", lost.Delegation)
	}
	if lost.Delegation.Holder != worker {
		t.Fatalf("holder = %q", lost.Delegation.Holder)
	}
	if !claimStampPattern.MatchString(lost.Delegation.At) {
		t.Fatalf("at = %q", lost.Delegation.At)
	}
	if lost.ExitCode() != 1 {
		t.Fatalf("exit code = %d", lost.ExitCode())
	}
	if h.markerOfRecord(fixPlants)["assignee"] != worker {
		t.Fatal("a lost race must not move the claim")
	}
	h.assertChecks()
}

// test_delegation_refuses_proposed_and_closed_tasks
func TestDelegationRefusesProposedAndClosedTasks(t *testing.T) {
	h := newHarness(t, harnessOptions{live: fixtureStore +
		`{"type":"task","id":"eeee0001","parent":"aaaa0009","state":"PROPOSED","title":"Maybe repaint"}` + "\n"})

	proposed := h.app.DelegateTask(DelegationCommand{
		ID: "eeee0001", Kind: "agent", Mode: "refine",
	}, nil)
	if proposed.Status != store.MutationInvalid {
		t.Fatalf("status = %q", proposed.Status)
	}
	if !strings.Contains(proposed.FirstError(), "PROPOSED") {
		t.Fatalf("errors = %v", proposed.Errors)
	}

	closed := h.app.DelegateTask(DelegationCommand{
		ID: fixOld, Kind: "human", Assignee: "pat@example.com",
	}, nil)
	if closed.Status != store.MutationInvalid || !strings.Contains(closed.FirstError(), "DONE") {
		t.Fatalf("closed = %q %v", closed.Status, closed.Errors)
	}

	missing := h.app.ClaimTask(DelegationCommand{ID: "ffffffff", Worker: worker}, nil)
	if missing.Status != store.MutationNotFound {
		t.Fatalf("status = %q", missing.Status)
	}
	if missing.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2", missing.ExitCode())
	}
	// Even a refusal carries the operation, so an adapter renders one shape.
	if missing.Delegation == nil || missing.Delegation.Action != ActionClaim {
		t.Fatalf("summary = %+v", missing.Delegation)
	}
}

// test_delegation_honors_an_expected_revision — adapted.
//
// The Go store has no revision guard on a delegation yet. Accepting the guard
// and not checking it is the one answer that must not ship: a caller that
// passed a revision would believe it was protected. Refusing says so.
func TestDelegationRefusesAnExpectedRevisionItCannotEnforce(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	before := h.read()

	result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "agent", Mode: "refine", ExpectedRevision: "s1.deadbeef",
	}, nil)

	if result.Status != store.MutationInvalid {
		t.Fatalf("status = %q", result.Status)
	}
	if !strings.Contains(result.FirstError(), "expected_revision") {
		t.Fatalf("errors = %v", result.Errors)
	}
	if h.read() != before {
		t.Fatal("a refused delegation must not write")
	}
}

// test_delegation_accepts_prebuilt_typed_commands — adapted. Go's typed command
// makes the "options with a prebuilt command" mixing error unrepresentable; the
// surviving invariant is the closed action vocabulary.
func TestAnUnknownDelegationActionIsRefused(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	result := h.app.runDelegation(DelegationCommand{ID: fixPlants, Action: "promote"}, nil)
	if result.Status != store.MutationInvalid {
		t.Fatalf("status = %q", result.Status)
	}
	if !strings.Contains(result.FirstError(), "unknown delegation action") {
		t.Fatalf("errors = %v", result.Errors)
	}

	blank := h.app.DelegateTask(DelegationCommand{ID: "  "}, nil)
	if blank.Status != store.MutationInvalid || blank.FirstError() != "task id is required" {
		t.Fatalf("blank id = %q %v", blank.Status, blank.Errors)
	}
}

// -- the verbs whose store half is a double ------------------------------------

// test_undelegate_clears_the_marker_and_leaves_lifecycle_alone
func TestUndelegateClearsTheMarkerAndLeavesLifecycleAlone(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})

	if result := h.app.DelegateTask(DelegationCommand{
		ID: fixPlants, Kind: "human", Assignee: "pat@example.com",
	}, nil); !result.Changed() {
		t.Fatalf("delegate = %q %v", result.Status, result.Errors)
	}

	result := h.app.UndelegateTask(DelegationCommand{ID: fixPlants}, nil)
	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if result.Delegation.Action != ActionUndelegate {
		t.Fatalf("action = %q", result.Delegation.Action)
	}
	if result.Delegation.Previous["assignee"] != "pat@example.com" {
		t.Fatalf("previous = %v", result.Delegation.Previous)
	}
	if result.Delegation.Delegation != nil {
		t.Fatalf("marker = %v, want cleared", result.Delegation.Delegation)
	}
	if result.Delegation.State != "WAITING" {
		t.Fatalf("state = %q — the owner decides when to leave WAITING", result.Delegation.State)
	}

	repeat := h.app.UndelegateTask(DelegationCommand{ID: fixPlants}, nil)
	if !repeat.OK() || repeat.Changed() {
		t.Fatalf("an idempotent repeat is a no_change: %q", repeat.Status)
	}
	if repeat.Delegation.Previous != nil {
		t.Fatalf("previous = %v", repeat.Delegation.Previous)
	}
	if repeat.Delegation.Task == nil {
		t.Fatal("an idempotent repeat still returns the resource")
	}
}

// test_release_enforces_the_worker_match_and_the_owner_can_force_it
func TestReleaseEnforcesTheWorkerMatchAndTheOwnerCanForceIt(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "research")
	if claim := h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil); !claim.Changed() {
		t.Fatalf("claim = %q %v", claim.Status, claim.Errors)
	}

	mismatch := h.app.ReleaseTask(DelegationCommand{ID: fixPlants, Worker: rival}, nil)
	if !mismatch.Conflict() {
		t.Fatalf("status = %q", mismatch.Status)
	}
	if mismatch.Delegation.Action != ActionRelease || mismatch.Delegation.Holder != worker {
		t.Fatalf("summary = %+v", mismatch.Delegation)
	}
	if h.markerOfRecord(fixPlants)["assignee"] != worker {
		t.Fatal("a mismatched release must not move the claim")
	}

	forced := h.app.ReleaseTask(DelegationCommand{ID: fixPlants, Force: true}, nil)
	if !forced.Changed() {
		t.Fatalf("status = %q errors = %v", forced.Status, forced.Errors)
	}
	if forced.Delegation.Delegation["status"] != store.DelegationReady {
		t.Fatalf("marker = %v, want the agent-ready queue", forced.Delegation.Delegation)
	}
	if forced.Delegation.NoteRequested {
		t.Fatal("a release without a note reports no note outcome")
	}
}

// test_release_note_is_appended_in_the_same_undo_step
func TestReleaseNoteIsAppendedUnderTheReleaseCoalesceKey(t *testing.T) {
	wrap, double := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "implement")
	h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)

	result := h.app.ReleaseTask(DelegationCommand{
		ID: fixPlants, Worker: worker, Note: "  blocked: need repo access  ",
	}, nil)

	if !result.Changed() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if !result.Delegation.NoteRequested || !result.Delegation.NoteApplied {
		t.Fatalf("note summary = %+v", result.Delegation)
	}
	parsed, _ := h.recordFor("Water the plants")
	if body := parsed.String("body"); body != "blocked: need repo access" {
		t.Fatalf("body = %q, want the trimmed note", body)
	}

	// The release and the note carry ONE key, which is what makes them one undo
	// step, and it is that release's own key rather than a shared one.
	calls := double.log()
	var releaseKey, noteKey string
	for _, call := range calls {
		switch call.verb {
		case "release":
			releaseKey = call.coalesceKey
		case "patch:body":
			noteKey = call.coalesceKey
		}
	}
	if releaseKey == "" || releaseKey != noteKey {
		t.Fatalf("release key %q, note key %q", releaseKey, noteKey)
	}
	if !strings.HasPrefix(releaseKey, "delegation-release-") {
		t.Fatalf("key = %q", releaseKey)
	}
}

// A note appended to a task that already has one keeps the existing body.
func TestReleaseNoteAppendsToAnExistingBody(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixTravel, "implement")
	h.app.ClaimTask(DelegationCommand{ID: fixTravel, Worker: worker}, nil)

	result := h.app.ReleaseTask(DelegationCommand{
		ID: fixTravel, Worker: worker, Note: "blocked: waiting on the desk",
	}, nil)
	if !result.Changed() || !result.Delegation.NoteApplied {
		t.Fatalf("status = %q %+v", result.Status, result.Delegation)
	}

	parsed, _ := h.recordFor("Travel desk reply")
	if want := "Some note line.\nblocked: waiting on the desk"; parsed.String("body") != want {
		t.Fatalf("body = %q, want %q", parsed.String("body"), want)
	}
}

// A note this process cannot even trim degrades to a typed "not applied" —
// never to a panic on top of a release that already succeeded.
func TestAnUnusableReleaseNoteDoesNotFailTheRelease(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "implement")
	h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)

	result := h.app.ReleaseTask(DelegationCommand{
		ID: fixPlants, Worker: worker, Note: "blocked \xff\xfe",
	}, nil)

	if !result.Changed() {
		t.Fatalf("the release itself must still succeed: %q %v", result.Status, result.Errors)
	}
	if !result.Delegation.NoteRequested || result.Delegation.NoteApplied {
		t.Fatalf("note summary = %+v", result.Delegation)
	}
	if len(result.Errors) == 0 || result.Errors[0] != NoteEncodingError {
		t.Fatalf("errors = %v", result.Errors)
	}
	if result.Delegation.Delegation["status"] != store.DelegationReady {
		t.Fatalf("the release still landed: %v", result.Delegation.Delegation)
	}
	parsed, _ := h.recordFor("Water the plants")
	if parsed.Has("body") {
		t.Fatalf("no note may reach the file: %q", parsed.String("body"))
	}
}

// An empty or whitespace-only note is not a note at all.
func TestABlankReleaseNoteIsNoNote(t *testing.T) {
	wrap, double := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "implement")
	h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)

	result := h.app.ReleaseTask(DelegationCommand{ID: fixPlants, Worker: worker, Note: "   "}, nil)
	if !result.Changed() {
		t.Fatalf("status = %q", result.Status)
	}
	for _, call := range double.log() {
		if call.verb == "patch:body" {
			t.Fatal("a blank note must not produce a write")
		}
	}
}

// test_work_ref_is_set_cleared_and_worker_matched
func TestWorkRefIsSetClearedAndWorkerMatched(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "research")
	h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)

	stale := h.app.SetWorkRef(DelegationCommand{
		ID: fixPlants, WorkRef: "https://example.com/other", Worker: rival,
	}, nil)
	if !stale.Conflict() {
		t.Fatalf("status = %q", stale.Status)
	}

	set := h.app.SetWorkRef(DelegationCommand{
		ID: fixPlants, WorkRef: "https://example.com/brief", Worker: worker,
	}, nil)
	if !set.Changed() {
		t.Fatalf("status = %q errors = %v", set.Status, set.Errors)
	}
	if set.Delegation.Delegation["work_ref"] != "https://example.com/brief" {
		t.Fatalf("marker = %v", set.Delegation.Delegation)
	}

	cleared := h.app.SetWorkRef(DelegationCommand{ID: fixPlants, WorkRef: "off"}, nil)
	if !cleared.Changed() {
		t.Fatalf("status = %q errors = %v", cleared.Status, cleared.Errors)
	}
	if _, present := cleared.Delegation.Delegation["work_ref"]; present {
		t.Fatalf("marker = %v, want the reference cleared", cleared.Delegation.Delegation)
	}
	if cleared.Delegation.Action != ActionWorkRef {
		t.Fatalf("action = %q", cleared.Delegation.Action)
	}
}

// Every surface spells "clear this reference" its own way, and they all mean
// the same thing exactly once, here.
func TestWorkRefClearWordsNormalizeAtEverySurface(t *testing.T) {
	for _, spelling := range []string{"", "off", "none", "OFF", "  None  "} {
		command := DelegationCommand{ID: fixPlants, Action: ActionWorkRef, WorkRef: spelling}
		if !command.ClearsWorkRef() {
			t.Fatalf("%q must clear the reference", spelling)
		}
	}
	for _, spelling := range []string{"https://example.com/off", "offline", "no"} {
		command := DelegationCommand{ID: fixPlants, Action: ActionWorkRef, WorkRef: spelling}
		if command.ClearsWorkRef() {
			t.Fatalf("%q is a reference, not a clear instruction", spelling)
		}
	}
}

// test_mode_update_keeps_the_work_ref_and_reports_the_previous_marker
func TestModeUpdateKeepsTheWorkRefAndReportsThePreviousMarker(t *testing.T) {
	wrap, _ := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})
	delegateAgent(t, h, fixPlants, "research")
	if set := h.app.SetWorkRef(DelegationCommand{
		ID: fixPlants, WorkRef: "https://example.com/brief",
	}, nil); !set.Changed() {
		t.Fatalf("work ref = %q %v", set.Status, set.Errors)
	}

	result := delegateAgent(t, h, fixPlants, "implement")

	if result.Delegation.Previous["mode"] != "research" {
		t.Fatalf("previous = %v", result.Delegation.Previous)
	}
	if result.Delegation.Delegation["mode"] != "implement" {
		t.Fatalf("marker = %v", result.Delegation.Delegation)
	}
	if result.Delegation.Delegation["work_ref"] != "https://example.com/brief" {
		t.Fatalf("a mode update keeps the reference: %v", result.Delegation.Delegation)
	}
	if result.Delegation.Delegation["status"] != store.DelegationReady {
		t.Fatalf("status = %q", result.Delegation.Delegation["status"])
	}
}

// test_every_delegation_mutation_is_individually_undoable — adapted to what the
// application can prove: each verb reached the store exactly once, in order,
// each with its OWN coalescing key, so no two operations can merge into one
// undo entry.
func TestEveryDelegationVerbGetsItsOwnCoalesceKey(t *testing.T) {
	wrap, double := capableFactory()
	h := newHarness(t, harnessOptions{wrap: wrap})

	delegateAgent(t, h, fixPlants, "research")
	h.app.ClaimTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)
	h.app.SetWorkRef(DelegationCommand{ID: fixPlants, WorkRef: "https://example.com/brief", Worker: worker}, nil)
	h.app.ReleaseTask(DelegationCommand{ID: fixPlants, Worker: worker}, nil)
	h.app.UndelegateTask(DelegationCommand{ID: fixPlants}, nil)

	verbs := []string{}
	keys := map[string]bool{}
	for _, call := range double.log() {
		verbs = append(verbs, call.verb)
		if keys[call.coalesceKey] {
			t.Fatalf("coalesce key %q was reused across operations", call.coalesceKey)
		}
		keys[call.coalesceKey] = true
	}
	if want := []string{"work_ref", "release", "undelegate"}; !equalStrings(verbs, want) {
		t.Fatalf("verbs = %v, want %v", verbs, want)
	}
	h.assertChecks()
}

// A capability the store does not implement REFUSES. It does not report success
// for something that never happened.
func TestUnsupportedCapabilitiesRefuseRatherThanSilentlySucceed(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	for name, run := range map[string]func() Outcome{
		"undelegate": func() Outcome { return h.app.UndelegateTask(DelegationCommand{ID: fixPlants}, nil) },
		"release":    func() Outcome { return h.app.ReleaseTask(DelegationCommand{ID: fixPlants}, nil) },
		"work_ref": func() Outcome {
			return h.app.SetWorkRef(DelegationCommand{ID: fixPlants, WorkRef: "x"}, nil)
		},
		"delete": func() Outcome { return h.app.DeleteTask(DeleteCommand{ID: fixPlants}, nil) },
		"approve": func() Outcome {
			return h.app.ApproveTask(fixPlants, "", nil)
		},
	} {
		result := run()
		if result.OK() {
			t.Fatalf("%s reported success without a store that implements it", name)
		}
		if result.Status != store.MutationUnavailable {
			t.Fatalf("%s status = %q", name, result.Status)
		}
		if !strings.Contains(result.FirstError(), "does not implement it yet") {
			t.Fatalf("%s errors = %v", name, result.Errors)
		}
	}
}
