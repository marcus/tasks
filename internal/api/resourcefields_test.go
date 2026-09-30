package api

import "testing"

// #31: two clients that loaded the same task each replace its formal links
// under the same If-Match. The first write must change the revision, so the
// second is refused rather than silently dropping the first client's link.
func TestConcurrentFormalLinkEditsWithOneETagConflict(t *testing.T) {
	h := newHarness(t)
	loaded := h.etagOf(fixPR)

	first := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"formal_links":[{"url":"https://example.test/a"}]}`, h.withIfMatch(loaded))
	assertStatus(t, first, 200)
	if first.etag() == loaded {
		t.Fatalf("a formal_links write left the revision at %s", loaded)
	}
	if reread := h.etagOf(fixPR); reread != first.etag() {
		t.Fatalf("reread etag %s, write answered %s", reread, first.etag())
	}

	second := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"formal_links":[{"url":"https://example.test/b"}]}`, h.withIfMatch(loaded))
	assertError(t, second, 412, "stale_revision")

	links, _ := h.get("/api/v1/tasks/" + fixPR).data()["formal_links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["url"] != "https://example.test/a" {
		t.Fatalf("formal_links = %v, want the first client's link kept", links)
	}
}

// #32: depth counts task ancestors only. A top-level task inside a project — a
// section nested under "Projects" — is depth 0 like a top-level Inbox task.
func TestDepthIgnoresNestedSections(t *testing.T) {
	const org = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"Inbox top"}
{"type":"section","id":"aaaa0003","title":"Projects"}
{"type":"section","id":"aaaa0004","parent":"aaaa0003","title":"Launch site"}
{"type":"task","id":"aaaa0006","parent":"aaaa0004","state":"NEXT","title":"Project top"}
{"type":"task","id":"aaaa0007","parent":"aaaa0006","state":"TODO","title":"Project child"}
{"type":"section","id":"aaaa0005","parent":"aaaa0004","title":"Phase one"}
{"type":"task","id":"aaaa0008","parent":"aaaa0005","state":"TODO","title":"Sub-project top"}
`
	h := newHarnessWith(t, org, "", "")
	for _, tc := range []struct {
		id      string
		depth   float64
		section string
		parent  any
	}{
		{"aaaa0002", 0, "aaaa0001", nil},
		{"aaaa0006", 0, "aaaa0004", nil},
		{"aaaa0007", 1, "aaaa0004", "aaaa0006"},
		{"aaaa0008", 0, "aaaa0005", nil},
	} {
		task := h.get("/api/v1/tasks/" + tc.id).data()
		if task["depth"] != tc.depth || task["section_id"] != tc.section || task["parent_id"] != tc.parent {
			t.Errorf("%s: depth %v section %v parent %v, want %v %v %v", tc.id,
				task["depth"], task["section_id"], task["parent_id"], tc.depth, tc.section, tc.parent)
		}
	}
}

// #34: created comes from the capture note, updated from the stored stamp, and
// both are null when the store holds neither.
func TestCreatedAndUpdatedDates(t *testing.T) {
	h := newHarness(t)
	untouched := h.get("/api/v1/tasks/" + fixPR).data()
	if untouched["created"] != nil || untouched["updated"] != nil {
		t.Fatalf("a record with no capture note or stamp: created %v, updated %v",
			untouched["created"], untouched["updated"])
	}

	created := h.json("POST", "/api/v1/tasks", `{"title":"Fresh capture"}`, nil)
	assertStatus(t, created, 201)
	fresh := created.data()
	if fresh["created"] != "2026-07-15" || fresh["updated"] != "2026-07-15T12:00:00Z" {
		t.Fatalf("fresh capture: created %v, updated %v", fresh["created"], fresh["updated"])
	}

	patched := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"Touched"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, patched, 200)
	if patched.data()["updated"] != "2026-07-15T12:00:00Z" {
		t.Fatalf("updated after PATCH = %v", patched.data()["updated"])
	}
	// A body edit that removes the capture note leaves the task undated rather
	// than guessing.
	id := fresh["id"].(string)
	rewritten := h.json("PATCH", "/api/v1/tasks/"+id, `{"body":["Just notes."]}`,
		h.withIfMatch(created.etag()))
	assertStatus(t, rewritten, 200)
	if rewritten.data()["created"] != nil {
		t.Fatalf("created without a capture note = %v", rewritten.data()["created"])
	}
}

// #34: an archived task reports the day its subtree was swept, including a
// descendant whose root carries the stamp; a live task reports null.
func TestArchivedOnFollowsTheSweptRoot(t *testing.T) {
	const archive = `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Archive"}
{"type":"task","id":"dddd0001","parent":"cccc0001","state":"DONE","title":"Swept root","closed":"2026-07-01","archived":"2026-07-03"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"DONE","title":"Swept child","closed":"2026-07-01"}
{"type":"task","id":"dddd0003","parent":"cccc0001","state":"DONE","title":"Unstamped","closed":"2026-07-01"}
`
	h := newHarnessWith(t, fixtureOrg, archive, "")
	for id, want := range map[string]any{"dddd0001": "2026-07-03", "dddd0002": "2026-07-03", "dddd0003": nil} {
		task := h.get("/api/v1/tasks/" + id + "?source=archive").data()
		if task["archived"] != true || task["archived_on"] != want {
			t.Errorf("%s: archived %v archived_on %v, want %v", id, task["archived"], task["archived_on"], want)
		}
	}
	if live := h.get("/api/v1/tasks/" + fixPR).data(); live["archived_on"] != nil {
		t.Errorf("live archived_on = %v", live["archived_on"])
	}
}

// #34: the resource's quadrant is the same classification `tasks quadrants`
// prints, and null where no quadrant applies.
func TestQuadrantOnTheResource(t *testing.T) {
	h := newHarness(t)
	for id, want := range map[string]any{
		fixFlight: "Q1", // priority A, urgent tag
		fixPR:     "Q2", // priority B, no deadline
		fixTravel: "Q3", // urgent tag, no priority
		fixGarden: "Q4",
		fixPlants: "Q4", // on hold: classified, just not listed by the view
		fixOld:    nil,  // DONE
	} {
		if got := h.get("/api/v1/tasks/" + id).data()["quadrant"]; got != want {
			t.Errorf("%s quadrant = %v, want %v", id, got, want)
		}
	}
}

// #34: agent_ready is the claimable-queue rule `scope=agent_ready` applies.
func TestAgentReadyMatchesTheQueue(t *testing.T) {
	h := newHarness(t)
	if h.get("/api/v1/tasks/" + fixPR).data()["agent_ready"] != false {
		t.Fatal("an undelegated task is agent_ready")
	}
	delegated := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, delegated, 200)
	if delegated.data()["agent_ready"] != true {
		t.Fatalf("agent_ready after delegation = %v", delegated.data()["agent_ready"])
	}
	assertStrings(t, h.get("/api/v1/tasks?scope=agent_ready").ids(), []string{fixPR}, "agent_ready queue")

	claimed := h.json("POST", "/api/v1/tasks/"+fixPR+"/claim", `{"worker":"w1"}`,
		h.withIfMatch(delegated.etag()))
	assertStatus(t, claimed, 200)
	if claimed.data()["agent_ready"] != false {
		t.Fatalf("a claimed task is still agent_ready")
	}
}
