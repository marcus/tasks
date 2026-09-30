package api

import (
	"strings"
	"testing"
)

// groupSummary flattens `groups` into "block|key=id,id" terms, which is what the
// view assertions compare.
func groupSummary(answered answer) string {
	list, _ := answered.dig("data", "groups").([]any)
	terms := []string{}
	for _, element := range list {
		group, _ := element.(map[string]any)
		block, _ := group["block"].(string)
		key, _ := group["key"].(string)
		terms = append(terms, block+"|"+key+"="+strings.Join(stringsOf(group["task_ids"]), ","))
	}
	return strings.Join(terms, " ")
}

func taskIDsOf(answered answer) []string {
	list, _ := answered.dig("data", "tasks").([]any)
	values := []string{}
	for _, element := range list {
		task, _ := element.(map[string]any)
		id, _ := task["id"].(string)
		values = append(values, id)
	}
	return values
}

func TestViewsReturnFullTasksAndGroups(t *testing.T) {
	h := newHarness(t)
	for _, test := range []struct {
		path   string
		tasks  []string
		groups string
	}{
		{"/api/v1/views/agenda", []string{fixFlight, fixEval},
			"|overdue=" + fixFlight + " |today=" + fixEval + " |tomorrow= |later="},
		{"/api/v1/views/next", []string{fixFlight, fixPR}, "|@computer=" + fixFlight + "," + fixPR},
		{"/api/v1/views/next?include_unavailable=true", []string{fixFlight, fixPR, fixPlants},
			"|@computer=" + fixFlight + "," + fixPR + " |@home=" + fixPlants},
		{"/api/v1/views/quadrants",
			[]string{fixGarden, fixFlight, fixPR, fixChild, fixGrand, fixEval, fixTravel},
			"|Q1=" + fixFlight + " |Q2=" + fixPR + "," + fixEval + " |Q3=" + fixTravel +
				" |Q4=" + fixGarden + "," + fixChild + "," + fixGrand},
		{"/api/v1/views/inbox", []string{fixGarden}, "inbox|Inbox=" + fixGarden},
	} {
		answered := h.get(test.path)
		assertStatus(t, answered, 200)
		assertStrings(t, taskIDsOf(answered), test.tasks, test.path+" tasks")
		if got := groupSummary(answered); got != test.groups {
			t.Errorf("%s groups = %q, want %q", test.path, got, test.groups)
		}
		revision, _ := answered.dig("meta", "store_revision").(string)
		if answered.etag() != `"`+revision+`"` {
			t.Errorf("%s etag = %q, store_revision = %q", test.path, answered.etag(), revision)
		}
		if answered.dig("data", "today") != "2026-07-15" {
			t.Errorf("%s today = %v", test.path, answered.dig("data", "today"))
		}
	}

	// Each row is the full Task resource, revision included — the same bytes a
	// GET of that task returns.
	agenda := h.get("/api/v1/views/agenda")
	first, _ := agenda.dig("data", "tasks").([]any)[0].(map[string]any)
	single := h.get("/api/v1/tasks/" + fixFlight)
	if first["revision"] == nil || first["revision"] != single.data()["revision"] {
		t.Errorf("row revision = %v, resource revision = %v", first["revision"], single.data()["revision"])
	}
	if first["deadline"] != "2026-07-02" || first["project"] != "Work" {
		t.Errorf("row is not the full resource: %v", first)
	}

	quadrants := h.get("/api/v1/views/quadrants")
	groups, _ := quadrants.dig("data", "groups").([]any)
	label, _ := groups[0].(map[string]any)["label"].(string)
	if !strings.HasPrefix(label, "Q1 · Important + Urgent") {
		t.Errorf("Q1 label = %q", label)
	}
}

// The inbox view is the intake tab: Approvals in triage order, then the
// accepted inbox, each grouped by project in the Projects view's order with
// the unfiled bucket last.
func TestInboxViewReturnsBothIntakeBlocks(t *testing.T) {
	h := newHarnessWith(t, `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"loose capture"}
{"type":"task","id":"aaaa0003","parent":"aaaa0001","state":"PROPOSED","title":"unfiled proposal","priority":"A"}
{"type":"section","id":"aaaa0004","title":"Projects"}
{"type":"section","id":"aaaa0005","parent":"aaaa0004","title":"Launch"}
{"type":"task","id":"aaaa0006","parent":"aaaa0005","state":"PROPOSED","title":"low proposal","priority":"C"}
{"type":"task","id":"aaaa0007","parent":"aaaa0005","state":"PROPOSED","title":"high proposal","priority":"B"}
{"type":"task","id":"aaaa0008","parent":"aaaa0005","state":"INBOX","title":"filed capture"}
{"type":"task","id":"aaaa0009","parent":"aaaa0005","state":"INBOX","title":"held capture","tags":["defer"]}
`, "", "")
	answered := h.get("/api/v1/views/inbox")
	assertStatus(t, answered, 200)
	assertStrings(t, taskIDsOf(answered),
		[]string{"aaaa0003", "aaaa0007", "aaaa0006", "aaaa0002", "aaaa0008"}, "inbox tasks")
	want := "approvals|Launch=aaaa0007,aaaa0006 approvals|Inbox=aaaa0003 " +
		"inbox|Launch=aaaa0008 inbox|Inbox=aaaa0002"
	if got := groupSummary(answered); got != want {
		t.Errorf("groups = %q, want %q", got, want)
	}
	// Z reaches the accepted block and never the decision queue.
	revealed := h.get("/api/v1/views/inbox?include_unavailable=true")
	if got := groupSummary(revealed); !strings.Contains(got, "inbox|Launch=aaaa0008,aaaa0009") {
		t.Errorf("revealed groups = %q", got)
	}
}

func TestViewRefusals(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"projects", "someday", "AGENDA"} {
		answered := h.get("/api/v1/views/" + name)
		assertError(t, answered, 404, "not_found")
		if answered.message() != "No view with that name." {
			t.Errorf("%s message = %q", name, answered.message())
		}
		assertStrings(t, stringsOf(answered.dig("error", "details", "views")),
			[]string{"agenda", "next", "quadrants", "inbox", "outline"}, "advertised views")
	}
	assertError(t, h.get("/api/v1/views/agenda?include_unavailable=yes"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/views/agenda?include_closed=true"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/views/outline?include_unavailable=true"), 422, "validation_failed")
	posted := h.json("POST", "/api/v1/views/agenda", "{}", nil)
	assertError(t, posted, 404, "not_found")

	capabilities, _ := h.get("/api/v1/meta").dig("data", "capabilities").(map[string]any)
	if capabilities["views"] != true {
		t.Errorf("capabilities.views = %v", capabilities["views"])
	}
}

// A missing endpoint and a missing task share a code, not a sentence.
func TestUnknownRoutesSayTheEndpointIsMissing(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v1/nope", "/api/v1/views", "/api/v1/tasks/aaaa0004/approve"} {
		answered := h.get(path)
		assertError(t, answered, 404, "not_found")
		if strings.Contains(answered.message(), "task") {
			t.Errorf("GET %s message = %q", path, answered.message())
		}
	}
	missing := h.get("/api/v1/tasks/ffffffff")
	if missing.message() != "No task with that id." {
		t.Errorf("missing task message = %q", missing.message())
	}
	logged := h.logs.String()
	if !strings.Contains(logged, `"route":"unmatched"`) {
		t.Errorf("unknown routes are not logged as unmatched: %s", logged)
	}
}

func TestViewRouteIsLoggedAsATemplate(t *testing.T) {
	h := newHarness(t)
	h.get("/api/v1/views/next")
	if !strings.Contains(h.logs.String(), `"route":"/api/v1/views/{name}"`) {
		t.Errorf("log = %s", h.logs.String())
	}
}

func TestOutlineView(t *testing.T) {
	h := newHarness(t)
	answered := h.get("/api/v1/views/outline")
	assertStatus(t, answered, 200)
	nodes, _ := answered.dig("data", "nodes").([]any)
	terms := []string{}
	for _, element := range nodes {
		node, _ := element.(map[string]any)
		kind, _ := node["kind"].(string)
		id, _ := node["id"].(string)
		parent, _ := node["parent_id"].(string)
		depth, _ := node["depth"].(float64)
		terms = append(terms, kind[:1]+":"+id+"^"+parent+"@"+string(rune('0'+int(depth))))
		switch kind {
		case "section":
			if node["task"] != nil {
				t.Errorf("section node carries a task: %v", node)
			}
		case "task":
			task, _ := node["task"].(map[string]any)
			if node["section"] != nil || task["id"] != id || task["revision"] == nil {
				t.Errorf("task node = %v", node)
			}
		}
	}
	// The DONE task is hidden; everything else is in file order, sections and
	// tasks interleaved.
	want := "s:" + fixInbox + "^@0 t:" + fixGarden + "^" + fixInbox + "@1 " +
		"s:" + fixWork + "^@0 t:" + fixFlight + "^" + fixWork + "@1 t:" + fixPR + "^" + fixWork + "@1 " +
		"t:" + fixChild + "^" + fixPR + "@2 t:" + fixGrand + "^" + fixChild + "@3 " +
		"t:" + fixEval + "^" + fixWork + "@1 t:" + fixTravel + "^" + fixWork + "@1 " +
		"s:" + fixHome + "^@0 t:" + fixPlants + "^" + fixHome + "@1"
	if got := strings.Join(terms, " "); got != want {
		t.Errorf("outline =\n  %s\nwant\n  %s", got, want)
	}

	work, _ := nodes[2].(map[string]any)["section"].(map[string]any)
	for key, value := range map[string]any{
		"id": fixWork, "title": "Work", "parent_id": nil, "kind": "area", "body": nil,
		"state": nil, "closed": nil, "task_count": float64(6), "open_task_count": float64(6),
		"closed_task_count": float64(1), "hidden_closed_count": float64(1),
	} {
		if work[key] != value {
			t.Errorf("Work section %s = %v, want %v", key, work[key], value)
		}
	}
	inbox, _ := nodes[0].(map[string]any)["section"].(map[string]any)
	if inbox["kind"] != "inbox" {
		t.Errorf("Inbox kind = %v", inbox["kind"])
	}

	closed := h.get("/api/v1/views/outline?include_closed=true")
	assertStatus(t, closed, 200)
	if closed.dig("data", "include_closed") != true {
		t.Errorf("include_closed = %v", closed.dig("data", "include_closed"))
	}
	if !strings.Contains(closed.Body, `"id":"`+fixOld+`"`) {
		t.Error("include_closed did not reveal the DONE task")
	}
}

func TestSectionsCarryRoleNoteAndLifecycle(t *testing.T) {
	h := newProjectHarness(t)
	sections := h.get("/api/v1/sections")
	assertStatus(t, sections, 200)
	kinds := map[string]string{}
	for _, row := range sections.rows() {
		id, _ := row["id"].(string)
		kind, _ := row["kind"].(string)
		kinds[id] = kind
		for _, key := range []string{"id", "title", "parent_id", "kind", "body", "state", "closed"} {
			if _, present := row[key]; !present {
				t.Errorf("section %s has no %s: %v", id, key, row)
			}
		}
		if id == pfxSite && row["body"] != "Goal: ship the personal site." {
			t.Errorf("Site launch body = %v", row["body"])
		}
	}
	for id, want := range map[string]string{
		pfxInbox: "inbox", pfxProjects: "projects_root", pfxSite: "project", pfxSiteSub: "subsection",
		pfxTasks: "area", pfxDonepile: "area",
	} {
		if kinds[id] != want {
			t.Errorf("section %s kind = %q, want %q", id, kinds[id], want)
		}
	}
}
