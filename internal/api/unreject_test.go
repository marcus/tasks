package api

import (
	"strings"
	"testing"
)

// The HTTP half of the decline/restore loop, kept in step with `tasks list
// --rejected` and `tasks unreject`: one scope to find the decline, one intent
// route to undo it, and the same refusals the CLI gives.
func TestRejectedScopeAndUnrejectRoute(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Declined by mistake","state":"PROPOSED"}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)

	if containsString(h.get("/api/v1/tasks?scope=rejected").ids(), id) {
		t.Error("an undecided proposal is not a decline")
	}

	rejected := h.json("POST", "/api/v1/tasks/"+id+"/reject", "", h.withIfMatch(created.etag()))
	assertStatus(t, rejected, 200)
	if rejected.data()["rejected"] != "2026-07-15" {
		t.Errorf("rejected marker = %v, want the pinned day", rejected.data()["rejected"])
	}

	// Found by the review scope, and by no default one.
	assertStrings(t, h.get("/api/v1/tasks?scope=rejected").ids(), []string{id}, "rejected ids")
	if containsString(h.get("/api/v1/tasks").ids(), id) {
		t.Error("a decline appeared in the default scope")
	}
	if containsString(h.get("/api/v1/tasks?scope=proposed").ids(), id) {
		t.Error("a decline appeared in the proposed scope")
	}

	// The precondition is required, exactly as on approve/reject.
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/unreject", "", nil), 428, "missing_precondition")
	// And there is no body to send.
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/unreject", `{"notes":"x"}`,
		h.withIfMatch(rejected.etag())), 400, "malformed_request")

	restored := h.json("POST", "/api/v1/tasks/"+id+"/unreject", "", h.withIfMatch(rejected.etag()))
	assertStatus(t, restored, 200)
	if restored.data()["state"] != "PROPOSED" {
		t.Errorf("state = %v, want PROPOSED", restored.data()["state"])
	}
	if restored.data()["id"] != id {
		t.Errorf("id = %v, want the same id back", restored.data()["id"])
	}
	if restored.data()["rejected"] != nil || restored.data()["closed"] != nil {
		t.Errorf("rejected = %v, closed = %v — both belong to the undone decision",
			restored.data()["rejected"], restored.data()["closed"])
	}
	if len(h.get("/api/v1/tasks?scope=rejected").ids()) != 0 {
		t.Error("the restored proposal is no longer a decline")
	}

	// Restoring a proposal is not a thing, and the refusal names the state.
	again := h.json("POST", "/api/v1/tasks/"+id+"/unreject", "", h.withIfMatch(restored.etag()))
	assertError(t, again, 422, "validation_failed")
	if !strings.Contains(again.message(), "not a rejected proposal") {
		t.Errorf("the refusal does not name the reason: %q", again.message())
	}
}
