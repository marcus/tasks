package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Filing a proposal is a LOCATION change, not a decision about it. These tests
// hold `move` to that at the level a caller sees: argv in, stdout/exit out, and
// the resulting file bytes read back — including the invariant that matters most
// here, which is that nothing in the write accepted the proposal.

// proposalMoveFixture is an Inbox holding two proposals and one accepted task,
// plus a "Projects" heading with one project section — the shape the issue
// describes (a proposal captured into the Inbox that belongs in a project).
const proposalMoveFixture = `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Inbox"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"PROPOSED","title":"Fit a new altimeter"}
{"type":"task","id":"eeee0003","parent":"eeee0001","state":"PROPOSED","title":"Book the checkride"}
{"type":"task","id":"eeee0004","parent":"eeee0001","state":"INBOX","title":"Unfiled capture"}
{"type":"section","id":"eeee0005","title":"Projects"}
{"type":"section","id":"eeee0006","parent":"eeee0005","title":"Aviator"}
{"type":"task","id":"eeee0007","parent":"eeee0006","state":"TODO","title":"Renew the medical"}
`

func TestCLIMoveFilesAProposalIntoAProjectAndKeepsItProposed(t *testing.T) {
	dir := seedStore(t, proposalMoveFixture)

	// The preview keeps its existing shape and writes nothing.
	preview := runUnchanged(t, dir, "move", "eeee0002", "Aviator", "--dry-run")
	if !strings.HasPrefix(preview.stdout, `would move to "Aviator": `) {
		t.Errorf("stdout = %q", preview.stdout)
	}

	if result := runCLI(t, dir, "move", "eeee0002", "Aviator"); result.status != 0 {
		t.Fatalf("move: exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "eeee0002")
	if row["parent"] != "eeee0006" {
		t.Errorf("parent = %v, want the Aviator section", row["parent"])
	}
	if row["state"] != "PROPOSED" {
		t.Errorf("state = %v — filing a proposal is not an approval", row["state"])
	}

	// `show` is where a caller reads the new home back.
	shown := runCLI(t, dir, "show", "eeee0002", "--json")
	if shown.status != 0 {
		t.Fatalf("show: exit %d, stderr %q", shown.status, shown.stderr)
	}
	var resource map[string]any
	if err := json.Unmarshal([]byte(shown.stdout), &resource); err != nil {
		t.Fatalf("parse show: %v (%s)", err, shown.stdout)
	}
	if resource["project"] != "Aviator" || resource["state"] != "PROPOSED" {
		t.Errorf("show = project %v / state %v", resource["project"], resource["state"])
	}

	// The move is an ordinary journal step, so the previous location comes back.
	if undone := runCLI(t, dir, "undo"); undone.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undone.status, undone.stderr)
	}
	restored := recordFor(t, dir, "eeee0002")
	if restored["parent"] != "eeee0001" || restored["state"] != "PROPOSED" {
		t.Errorf("after undo = parent %v / state %v, want the Inbox and PROPOSED",
			restored["parent"], restored["state"])
	}
}

// A destination that does not exist refuses identically whichever kind of task
// named it: widening the scope must not have widened what counts as a section.
func TestCLIMoveProposalMissingSectionRefusesLikeAnOpenTask(t *testing.T) {
	dir := seedStore(t, proposalMoveFixture)
	before := storeBytes(t, dir)

	proposal := runCLI(t, dir, "move", "eeee0002", "Hangar")
	accepted := runCLI(t, dir, "move", "eeee0004", "Hangar")
	if proposal.status != 1 || proposal.stderr != accepted.stderr {
		t.Errorf("proposal = exit %d %q, accepted = exit %d %q",
			proposal.status, proposal.stderr, accepted.status, accepted.stderr)
	}
	if !strings.Contains(proposal.stderr, `could not move (no "Hangar" section?)`) {
		t.Errorf("stderr = %q", proposal.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused move wrote to the store")
	}
}

// `--under` and `--top` carry the same scope as the positional section form. A
// proposal may nest under another proposal — the store settles proposal trees
// leaves-first — while accepted work still may not, and learns that from ref
// resolution rather than from a store refusal.
func TestCLIMoveProposalNestsUnderAProposalAndUnnests(t *testing.T) {
	dir := seedStore(t, proposalMoveFixture)

	if result := runCLI(t, dir, "move", "eeee0003", "--under", "eeee0002"); result.status != 0 {
		t.Fatalf("nest: exit %d, stderr %q", result.status, result.stderr)
	}
	nested := recordFor(t, dir, "eeee0003")
	if nested["parent"] != "eeee0002" || nested["state"] != "PROPOSED" {
		t.Errorf("nested = parent %v / state %v", nested["parent"], nested["state"])
	}

	if result := runCLI(t, dir, "move", "eeee0003", "--top"); result.status != 0 {
		t.Fatalf("unnest: exit %d, stderr %q", result.status, result.stderr)
	}
	if unnested := recordFor(t, dir, "eeee0003"); unnested["parent"] != "eeee0001" {
		t.Errorf("unnested parent = %v, want the Inbox section", unnested["parent"])
	}

	before := storeBytes(t, dir)
	refused := runCLI(t, dir, "move", "eeee0004", "--under", "eeee0002")
	if refused.status != refExit || !strings.Contains(refused.stderr, "expected an open task") {
		t.Errorf("accepted work under a proposal = exit %d, stderr %q", refused.status, refused.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused nest wrote to the store")
	}
}

// The anchored form keeps its JSON shape, and its anchor may be a proposed
// sibling when the moving subtree is a proposal too.
func TestCLIMoveProposalBeforeAProposedSibling(t *testing.T) {
	dir := seedStore(t, proposalMoveFixture)
	result := runCLI(t, dir, "move", "eeee0003", "--before", "eeee0002", "--json")
	if result.status != 0 {
		t.Fatalf("place: exit %d, stderr %q", result.status, result.stderr)
	}
	var payload struct {
		Touched   []map[string]any `json:"touched"`
		Placement struct {
			ParentID   string `json:"parent_id"`
			ParentType string `json:"parent_type"`
			BeforeID   string `json:"before_id"`
		} `json:"placement"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if payload.Placement.ParentID != "eeee0001" || payload.Placement.ParentType != "section" ||
		payload.Placement.BeforeID != "eeee0002" {
		t.Errorf("placement = %+v", payload.Placement)
	}
	if len(payload.Touched) != 1 || payload.Touched[0]["state"] != "PROPOSED" {
		t.Errorf("touched = %v", payload.Touched)
	}
	// File order is the placement's whole point.
	lines := strings.Split(strings.TrimSpace(storeBytes(t, dir)), "\n")
	moved, anchor := -1, -1
	for index, line := range lines {
		switch {
		case strings.Contains(line, `"id":"eeee0003"`):
			moved = index
		case strings.Contains(line, `"id":"eeee0002"`):
			anchor = index
		}
	}
	if moved == -1 || anchor == -1 || moved > anchor {
		t.Errorf("moved at %d, anchor at %d", moved, anchor)
	}
}
