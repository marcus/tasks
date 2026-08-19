package application

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/store"
)

// projectHarness installs the scripted double over the real fixture, so the
// schema gate and the duplicate-title check run against real bytes while the
// project store half is programmed.
func projectHarness(t *testing.T, script *scriptedStore, live string) *harness {
	t.Helper()
	return newHarness(t, harnessOptions{live: live, wrap: func(built *store.Store) Store {
		script.Store = built
		return script
	}})
}

const projectsFixture = `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Inbox"}
{"type":"section","id":"cccc0002","title":"Projects"}
{"type":"section","id":"cccc0003","parent":"cccc0002","title":"Site launch"}
{"type":"task","id":"cccc0004","parent":"cccc0003","state":"NEXT","title":"Pick a generator"}
`

func TestCreateProjectRefusesABlankTitleBeforeAnyStoreCall(t *testing.T) {
	script := &scriptedStore{}
	h := projectHarness(t, script, projectsFixture)

	result := h.app.CreateProject("   ", nil)

	if !result.Invalid() || result.FirstError() != "title cannot be blank" {
		t.Fatalf("result = %q %v", result.Status, result.Errors)
	}
	if len(script.calls) != 0 {
		t.Fatalf("a blank title must not reach the store: %+v", script.calls)
	}
}

// A duplicate name would make later project refs ambiguous, so it is refused
// before the write — and the comparison is case-insensitive, because a ref is.
func TestCreateProjectRefusesADuplicateProjectOrAreaName(t *testing.T) {
	script := &scriptedStore{createResult: store.MutationResult{Status: store.MutationOK}}
	h := projectHarness(t, script, projectsFixture)

	result := h.app.CreateProject("  site LAUNCH  ", nil)

	if !result.Invalid() {
		t.Fatalf("status = %q", result.Status)
	}
	if !strings.Contains(result.FirstError(), `"site LAUNCH"`) {
		t.Fatalf("error = %q", result.FirstError())
	}
	// The reason also goes under the FIELD that caused it. This pre-check runs
	// before store.CreateProject's own duplicate check and would otherwise shadow
	// its richer refusal, which is how an HTTP 422 came to carry empty details for
	// the one refusal a client can act on.
	reasons := result.FieldErrors["title"]
	if len(reasons) != 1 || reasons[0] != result.FirstError() {
		t.Fatalf("field_errors = %v, want title => [%q]", result.FieldErrors, result.FirstError())
	}
	if len(script.calls) != 0 {
		t.Fatalf("a duplicate must not reach the store: %+v", script.calls)
	}

	fresh := h.app.CreateProject("Second site", nil)
	if !fresh.OK() {
		t.Fatalf("status = %q", fresh.Status)
	}
	if len(script.calls) != 1 || script.calls[0].detail != "Second site" {
		t.Fatalf("calls = %+v", script.calls)
	}
}

// The multi-call project commands gate on the checked read FIRST, so a store at
// a schema this build must not interpret is refused before any of them runs.
func TestProjectCommandsRefuseAnUnsupportedSchemaBeforeWriting(t *testing.T) {
	script := &scriptedStore{renameFound: true, completeFound: true, archiveFound: true}
	h := projectHarness(t, script, `{"type":"meta","version":99}`+"\n")

	for name, run := range map[string]func() Outcome{
		"create":   func() Outcome { return h.app.CreateProject("Anything", nil) },
		"rename":   func() Outcome { return h.app.RenameProject("cccc0003", "Renamed", nil) },
		"complete": func() Outcome { return h.app.CompleteProject("cccc0003", nil) },
		"archive":  func() Outcome { return h.app.ArchiveProject("cccc0003", nil) },
	} {
		result := run()
		if result.Status != store.MutationUnsupportedSchema {
			t.Fatalf("%s status = %q", name, result.Status)
		}
		if len(result.Errors) == 0 {
			t.Fatalf("%s carries no diagnostic", name)
		}
	}
	if len(script.calls) != 0 {
		t.Fatalf("no project call may run behind the gate: %+v", script.calls)
	}
}

func TestRenameProjectMapsAMissingSectionToNotFound(t *testing.T) {
	script := &scriptedStore{renameFound: false}
	h := projectHarness(t, script, projectsFixture)

	if result := h.app.RenameProject("ffffffff", "Renamed", nil); !result.NotFound() {
		t.Fatalf("status = %q", result.Status)
	}
	if result := h.app.RenameProject("cccc0003", "  ", nil); !result.Invalid() {
		t.Fatalf("blank title = %q", result.Status)
	}

	script.renameFound = true
	result := h.app.RenameProject("cccc0003", "Renamed", nil)
	if !result.OK() {
		t.Fatalf("status = %q", result.Status)
	}
	if want := []string{"cccc0003"}; !equalStrings(result.TouchedIDs, want) {
		t.Fatalf("touched = %v", result.TouchedIDs)
	}
}

// A rename that failed and reverted must not read as a missing section: the
// store's recorded rollback is the only evidence, and the STAGE decides whether
// the diagnostic blames the write or validation.
func TestARolledBackProjectCommandKeepsItsStage(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		stage  store.RollbackStage
		status store.MutationStatus
	}{
		{"write", store.RollbackWrite, store.MutationUnavailable},
		{"validation", store.RollbackValidation, store.MutationStoreInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			script := &scriptedStore{
				renameFound:    false,
				rollbackReason: "could not replace the file",
				rollbackStage:  testCase.stage,
			}
			h := projectHarness(t, script, projectsFixture)

			result := h.app.RenameProject("cccc0003", "Renamed", nil)

			if result.Status != testCase.status {
				t.Fatalf("status = %q, want %q", result.Status, testCase.status)
			}
			if !result.RolledBack || result.RollbackStage != testCase.stage {
				t.Fatalf("rollback = %v %q", result.RolledBack, result.RollbackStage)
			}
			if result.FirstError() != "could not replace the file" {
				t.Fatalf("errors = %v", result.Errors)
			}
		})
	}
}

// Zero closed is a clean no-op for an already-closed project and a ROLLBACK for
// a write that reverted. Only the recorded rollback tells them apart.
func TestCompleteProjectDistinguishesACleanZeroFromARollback(t *testing.T) {
	script := &scriptedStore{completeFound: true, completeClosed: 0}
	h := projectHarness(t, script, projectsFixture)

	clean := h.app.CompleteProject("cccc0003", nil)
	if !clean.OK() {
		t.Fatalf("status = %q", clean.Status)
	}
	if clean.Project == nil || clean.Project.Closed != 0 {
		t.Fatalf("summary = %+v", clean.Project)
	}

	script.rollbackReason = "file failed validation after the edit"
	script.rollbackStage = store.RollbackValidation
	reverted := h.app.CompleteProject("cccc0003", nil)
	if reverted.Status != store.MutationStoreInvalid || !reverted.RolledBack {
		t.Fatalf("status = %q rolled back = %v", reverted.Status, reverted.RolledBack)
	}

	script.rollbackReason, script.rollbackStage = "", ""
	script.completeClosed = 3
	closed := h.app.CompleteProject("cccc0003", nil)
	if !closed.OK() || closed.Project.Closed != 3 {
		t.Fatalf("closed = %q %+v", closed.Status, closed.Project)
	}

	script.completeFound = false
	if missing := h.app.CompleteProject("ffffffff", nil); !missing.NotFound() {
		t.Fatalf("status = %q", missing.Status)
	}
}

// A project holding undecided proposals is a CONFLICT, not a failure: archiving
// a proposal without deciding it is a decision nobody made.
func TestArchiveProjectRefusesUndecidedProposals(t *testing.T) {
	script := &scriptedStore{archiveProposed: true}
	h := projectHarness(t, script, projectsFixture)

	result := h.app.ArchiveProject("cccc0003", nil)

	if !result.Conflict() {
		t.Fatalf("status = %q", result.Status)
	}
	if result.FirstError() != "decide proposed tasks before archiving the project" {
		t.Fatalf("errors = %v", result.Errors)
	}
}

func TestArchiveProjectReportsTheMovedIDsAndTheirCount(t *testing.T) {
	script := &scriptedStore{archiveFound: true, archiveMoved: []string{"cccc0003", "cccc0004"}}
	h := projectHarness(t, script, projectsFixture)

	result := h.app.ArchiveProject("cccc0003", nil)

	if !result.OK() {
		t.Fatalf("status = %q", result.Status)
	}
	if want := []string{"cccc0003", "cccc0004"}; !equalStrings(result.TouchedIDs, want) {
		t.Fatalf("touched = %v", result.TouchedIDs)
	}
	if result.Project == nil || result.Project.Archived != 2 {
		t.Fatalf("summary = %+v", result.Project)
	}

	script.archiveFound = false
	script.archiveMoved = nil
	if missing := h.app.ArchiveProject("ffffffff", nil); !missing.NotFound() {
		t.Fatalf("status = %q", missing.Status)
	}
}

// -- delete and proposals ------------------------------------------------------

func TestDeleteTaskPassesTheCommandThroughUnchanged(t *testing.T) {
	script := &scriptedStore{deleteResult: store.MutationResult{
		Status: store.MutationOK, TouchedIDs: []string{fixPlants},
	}}
	h := projectHarness(t, script, fixtureStore)

	if blank := h.app.DeleteTask(DeleteCommand{}, nil); !blank.Invalid() {
		t.Fatalf("a blank id = %q", blank.Status)
	}
	result := h.app.DeleteTask(DeleteCommand{ID: fixPlants, Cascade: true}, nil)
	if !result.OK() {
		t.Fatalf("status = %q", result.Status)
	}
	if len(script.calls) != 1 || script.calls[0].verb != "delete" || script.calls[0].detail != "cascade=true" {
		t.Fatalf("calls = %+v", script.calls)
	}
}

func TestProposalDecisionsReachTheStoreWithTheOperationsToday(t *testing.T) {
	script := &scriptedStore{proposalResult: store.MutationResult{Status: store.MutationOK}}
	h := projectHarness(t, script, fixtureStore)

	if result := h.app.ApproveTask(fixPlants, "", nil); !result.OK() {
		t.Fatalf("approve = %q", result.Status)
	}
	if result := h.app.RejectTask(fixPlants, "", []string{"not now"}, nil); !result.OK() {
		t.Fatalf("reject = %q", result.Status)
	}
	if len(script.calls) != 2 {
		t.Fatalf("calls = %+v", script.calls)
	}
	if script.calls[0].verb != "decide:approve" || script.calls[1].verb != "decide:reject" {
		t.Fatalf("calls = %+v", script.calls)
	}
	if script.calls[0].detail != "2026-07-14" {
		t.Fatalf("today = %q, want the application clock", script.calls[0].detail)
	}
}

// Approve+complete is ONE application command with ONE store call: a surface
// that composed approve and complete could leave the proposal accepted-but-open
// on a failure, and `undo` would then rewind only half of it.
func TestApproveAndCompleteIsASingleStoreCall(t *testing.T) {
	script := &scriptedStore{proposalResult: store.MutationResult{Status: store.MutationOK}}
	h := projectHarness(t, script, fixtureStore)

	if result := h.app.ApproveAndCompleteTask(fixPlants, "rev-1", nil); !result.OK() {
		t.Fatalf("approve+complete = %q", result.Status)
	}
	if len(script.calls) != 1 || script.calls[0].verb != "decide:approve_complete" {
		t.Fatalf("calls = %+v", script.calls)
	}
	if script.calls[0].detail != "2026-07-14" {
		t.Fatalf("today = %q, want the application clock", script.calls[0].detail)
	}
	blank := h.app.ApproveAndCompleteTask("  ", "", nil)
	if !blank.Invalid() || blank.FirstError() != "task id is required" {
		t.Fatalf("blank id = %q %v", blank.Status, blank.Errors)
	}
	if len(script.calls) != 1 {
		t.Fatalf("a blank id must not reach the store: %+v", script.calls)
	}
}

// Ruby raises ArgumentError from the decision constructor and rescues it into
// an invalid result. A transport-facing layer simply never raises.
func TestAMalformedProposalDecisionIsARefusalNotAPanic(t *testing.T) {
	script := &scriptedStore{proposalResult: store.MutationResult{Status: store.MutationOK}}
	h := projectHarness(t, script, fixtureStore)

	blank := h.app.DecideProposal(ProposalDecision{Action: ProposalApprove}, nil)
	if !blank.Invalid() || blank.FirstError() != "task id is required" {
		t.Fatalf("blank id = %q %v", blank.Status, blank.Errors)
	}
	unknown := h.app.DecideProposal(ProposalDecision{ID: fixPlants, Action: "promote"}, nil)
	if !unknown.Invalid() || !strings.Contains(unknown.FirstError(), "unknown proposal action") {
		t.Fatalf("unknown action = %q %v", unknown.Status, unknown.Errors)
	}
	if len(script.calls) != 0 {
		t.Fatalf("a malformed decision must not reach the store: %+v", script.calls)
	}
}
