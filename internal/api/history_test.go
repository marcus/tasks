package api

import (
	"strings"
	"testing"
	"time"
)

// -- helpers --------------------------------------------------------------------

func (h *harness) storeRevision() string {
	h.t.Helper()
	meta := h.get("/api/v1/meta")
	assertStatus(h.t, meta, 200)
	revision, _ := meta.dig("meta", "store_revision").(string)
	return revision
}

// patchPriority is one ordinary journaled write through the API.
func (h *harness) patchPriority(id, priority string) {
	h.t.Helper()
	answered := h.json("PATCH", "/api/v1/tasks/"+id, `{"priority":"`+priority+`"}`, h.withIfMatch(h.etagOf(id)))
	assertStatus(h.t, answered, 200)
}

func (h *harness) historyStep(verb, revision string) answer {
	h.t.Helper()
	return h.json("POST", "/api/v1/history/"+verb, `{"store_revision":`+jsonString(revision)+`}`, nil)
}

func detailsReason(a answer) string {
	value, _ := a.dig("error", "details", "reason").(string)
	return value
}

// -- history --------------------------------------------------------------------

func TestHistoryPeekUndoRedoRoundTrip(t *testing.T) {
	h := newHarness(t)
	original := string(h.storeBytes())

	peek := h.get("/api/v1/history")
	assertStatus(t, peek, 200)
	if peek.dig("data", "undo") != nil || peek.dig("data", "redo") != nil {
		t.Fatalf("fresh store peeks labels: %s", peek.Body)
	}
	if got := peek.dig("data", "store_revision"); got != h.storeRevision() || got != peek.dig("meta", "store_revision") {
		t.Fatalf("peek revision %v disagrees with /meta %q", got, h.storeRevision())
	}

	h.patchPriority(fixFlight, "C")
	peek = h.get("/api/v1/history")
	label, _ := peek.dig("data", "undo").(string)
	if label == "" || peek.dig("data", "redo") != nil {
		t.Fatalf("after a write: %s", peek.Body)
	}
	revision, _ := peek.dig("data", "store_revision").(string)

	undone := h.historyStep("undo", revision)
	assertStatus(t, undone, 200)
	if undone.dig("data", "label") != label {
		t.Fatalf("undo label = %v, want %q", undone.dig("data", "label"), label)
	}
	if string(h.storeBytes()) != original {
		t.Fatal("undo did not restore the original bytes")
	}
	after := h.storeRevision()
	if undone.dig("data", "store_revision") != after || undone.dig("meta", "store_revision") != after {
		t.Fatalf("undo reports %v, store is now %q", undone.dig("data", "store_revision"), after)
	}

	peek = h.get("/api/v1/history")
	if peek.dig("data", "undo") != nil || peek.dig("data", "redo") != label {
		t.Fatalf("after undo: %s", peek.Body)
	}
	redone := h.historyStep("redo", after)
	assertStatus(t, redone, 200)
	if redone.dig("data", "label") != label || string(h.storeBytes()) == original {
		t.Fatalf("redo = %s", redone.Body)
	}
}

// The precondition is the whole point of the route: a client that peeked, then
// lost a race to another surface, must not undo THAT surface's write.
func TestHistoryStepRefusesAStaleRevisionFromAnotherSurface(t *testing.T) {
	h := newHarness(t)
	other := newHarnessSharing(t, h)
	h.patchPriority(fixFlight, "C")
	seen := h.storeRevision()

	other.patchPriority(fixEval, "B")
	before := string(h.storeBytes())
	stale := h.historyStep("undo", seen)
	assertError(t, stale, 409, "conflict")
	if detailsReason(stale) != "stale_store_revision" {
		t.Fatalf("reason = %q", detailsReason(stale))
	}
	if stale.dig("error", "details", "store_revision") != h.storeRevision() {
		t.Fatalf("stale refusal should carry the current revision: %s", stale.Body)
	}
	if string(h.storeBytes()) != before {
		t.Fatal("a stale undo wrote")
	}
}

func TestHistoryStepEmptyAndJournalConflict(t *testing.T) {
	h := newHarness(t)
	empty := h.historyStep("undo", h.storeRevision())
	assertError(t, empty, 409, "conflict")
	if detailsReason(empty) != "empty" || empty.message() != "Nothing to undo." {
		t.Fatalf("empty = %s", empty.Body)
	}
	emptyRedo := h.historyStep("redo", h.storeRevision())
	if detailsReason(emptyRedo) != "empty" || emptyRedo.message() != "Nothing to redo." {
		t.Fatalf("empty redo = %s", emptyRedo.Body)
	}

	h.patchPriority(fixFlight, "C")
	label, _ := h.get("/api/v1/history").dig("data", "undo").(string)
	// An out-of-band edit after the journal's tip: the revision is current,
	// so the precondition passes, and the journal itself refuses.
	h.writeStore(strings.Replace(string(h.storeBytes()), "Water the plants", "Water the ferns", 1))
	conflict := h.historyStep("undo", h.storeRevision())
	assertError(t, conflict, 409, "conflict")
	if detailsReason(conflict) != "journal_conflict" || conflict.dig("error", "details", "label") != label {
		t.Fatalf("conflict = %s", conflict.Body)
	}
}

func TestHistoryStepValidatesItsBody(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{}`, `{"store_revision":7}`, `{"store_revision":""}`, `{"store_revision":"x","extra":1}`} {
		answered := h.json("POST", "/api/v1/history/undo", body, nil)
		assertError(t, answered, 422, "validation_failed")
	}
	wrongType := h.do(request{method: "POST", path: "/api/v1/history/undo", body: `{"store_revision":"x"}`, contentType: "text/plain"})
	assertError(t, wrongType, 415, "unsupported_media_type")
	foreign := h.json("POST", "/api/v1/history/redo", `{"store_revision":"x"}`,
		map[string]string{"Origin": "http://evil.example"})
	assertError(t, foreign, 403, "forbidden_origin")
	assertError(t, h.get("/api/v1/history?x=1"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/history/undo"), 404, "not_found")
}

func TestHistoryRefusesAnUnsupportedSchema(t *testing.T) {
	h := newHarness(t)
	h.writeStore(strings.Replace(fixtureOrg, `"version":2`, `"version":3`, 1))
	assertError(t, h.get("/api/v1/history"), 503, "unsupported_schema_version")
	assertError(t, h.historyStep("undo", h.storeRevisionFromFiles()), 503, "unsupported_schema_version")
}

// storeRevisionFromFiles is the revision when /meta cannot answer it.
func (h *harness) storeRevisionFromFiles() string {
	h.t.Helper()
	revision, err := h.server.options.App.StoreRevision()
	if err != nil {
		h.t.Fatal(err)
	}
	return revision
}

// -- archive --------------------------------------------------------------------

func TestArchivePreviewThenSweep(t *testing.T) {
	h := newHarness(t)
	preview := h.get("/api/v1/archive-preview")
	assertStatus(t, preview, 200)
	data := preview.data()
	if data["roots"] != float64(1) || data["descendants"] != float64(0) || data["records"] != float64(1) ||
		data["open_descendants"] != float64(0) {
		t.Fatalf("preview = %s", preview.Body)
	}
	assertStrings(t, stringsOf(data["candidate_ids"]), []string{fixOld}, "candidate_ids")
	if blocked, _ := data["blocked"].([]any); blocked == nil || len(blocked) != 0 {
		t.Fatalf("blocked = %v", data["blocked"])
	}
	fingerprint, _ := data["fingerprint"].(string)
	if fingerprint == "" || preview.dig("meta", "store_revision") != h.storeRevision() {
		t.Fatalf("preview = %s", preview.Body)
	}

	// A write that does not touch the moved set leaves the fingerprint valid.
	h.patchPriority(fixEval, "C")
	swept := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertStatus(t, swept, 200)
	if swept.data()["roots"] != float64(1) || swept.data()["records"] != float64(1) {
		t.Fatalf("sweep = %s", swept.Body)
	}
	assertStrings(t, stringsOf(swept.data()["moved_ids"]), []string{fixOld}, "moved_ids")
	if swept.dig("meta", "store_revision") != h.storeRevision() {
		t.Fatalf("sweep revision %v, store %q", swept.dig("meta", "store_revision"), h.storeRevision())
	}
	if strings.Contains(string(h.storeBytes()), fixOld) || !strings.Contains(h.archiveBytes(), fixOld) {
		t.Fatal("the swept task did not move to the archive")
	}

	// One journal step, reachable from the history routes.
	peek := h.get("/api/v1/history")
	if peek.dig("data", "undo") != "archive sweep" {
		t.Fatalf("peek after sweep = %s", peek.Body)
	}
	assertStatus(t, h.historyStep("undo", h.storeRevision()), 200)
	if !strings.Contains(string(h.storeBytes()), fixOld) {
		t.Fatal("undo did not restore the swept task")
	}
}

func TestArchiveSweepRefusesAChangedPreview(t *testing.T) {
	h := newHarness(t)
	fingerprint, _ := h.get("/api/v1/archive-preview").data()["fingerprint"].(string)
	// Closing another task changes the set the sweep would move.
	done := h.json("PATCH", "/api/v1/tasks/"+fixEval, `{"state":"DONE"}`, h.withIfMatch(h.etagOf(fixEval)))
	assertStatus(t, done, 200)
	before, archiveBefore := string(h.storeBytes()), h.archiveBytes()

	changed := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertError(t, changed, 409, "conflict")
	current, _ := h.get("/api/v1/archive-preview").data()["fingerprint"].(string)
	if detailsReason(changed) != "preview_changed" || changed.dig("error", "details", "fingerprint") != current {
		t.Fatalf("changed = %s", changed.Body)
	}
	if string(h.storeBytes()) != before || h.archiveBytes() != archiveBefore {
		t.Fatal("a refused sweep wrote")
	}
}

// The day a moved record is stamped with is part of the fingerprint, so a
// preview taken before local midnight does not authorize a sweep after it.
func TestArchiveSweepRefusesAPreviewFromAnotherDay(t *testing.T) {
	h := newHarness(t)
	fingerprint, _ := h.get("/api/v1/archive-preview").data()["fingerprint"].(string)
	h.now = h.now.Add(24 * time.Hour)
	changed := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertError(t, changed, 409, "conflict")
	if detailsReason(changed) != "preview_changed" {
		t.Fatalf("reason = %q", detailsReason(changed))
	}
}

func TestArchivePreviewListsBlockedRootsAndTheSweepRefuses(t *testing.T) {
	blocked := fixtureOrg + `{"type":"task","id":"ffff0001","parent":"aaaa0009","state":"DONE","title":"Closed parent","closed":"2026-07-01"}
{"type":"task","id":"ffff0002","parent":"ffff0001","state":"TODO","title":"Still open"}
`
	h := newHarnessWith(t, blocked, fixtureArchive, "")
	preview := h.get("/api/v1/archive-preview")
	assertStatus(t, preview, 200)
	if preview.data()["open_descendants"] != float64(1) {
		t.Fatalf("preview = %s", preview.Body)
	}
	rows, _ := preview.data()["blocked"].([]any)
	if len(rows) != 1 {
		t.Fatalf("blocked = %v", preview.data()["blocked"])
	}
	row, _ := rows[0].(map[string]any)
	if row["root_id"] != "ffff0001" || row["root_title"] != "Closed parent" {
		t.Fatalf("blocked row = %v", row)
	}
	assertStrings(t, stringsOf(row["open_ids"]), []string{"ffff0002"}, "open_ids")
	assertStrings(t, stringsOf(row["open_titles"]), []string{"Still open"}, "open_titles")

	fingerprint, _ := preview.data()["fingerprint"].(string)
	refused := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertError(t, refused, 409, "conflict")
	if detailsReason(refused) != "open_descendants" || refused.dig("error", "details", "open_descendants") != float64(1) {
		t.Fatalf("refused = %s", refused.Body)
	}
	if detailRows, _ := refused.dig("error", "details", "blocked").([]any); len(detailRows) != 1 {
		t.Fatalf("refusal blocked = %s", refused.Body)
	}
}

func TestArchiveSweepRefusesAConflictingArchiveCopy(t *testing.T) {
	archive := fixtureArchive + `{"type":"task","id":"aaaa0008","parent":"cccc0001","state":"DONE","title":"A different copy","closed":"2026-06-20"}
`
	h := newHarnessWith(t, fixtureOrg, archive, "")
	fingerprint, _ := h.get("/api/v1/archive-preview").data()["fingerprint"].(string)
	refused := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertError(t, refused, 409, "conflict")
	if detailsReason(refused) != "archive_conflict" {
		t.Fatalf("refused = %s", refused.Body)
	}
	assertStrings(t, stringsOf(refused.dig("error", "details", "conflicting_ids")), []string{fixOld}, "conflicting_ids")
}

func TestArchiveSweepWithNothingToMove(t *testing.T) {
	openOnly := strings.Replace(fixtureOrg, `"state":"DONE","priority":"C","title":"Old finished thing","tags":["@computer"],"closed":"2026-06-20"`,
		`"state":"TODO","priority":"C","title":"Old finished thing","tags":["@computer"]`, 1)
	h := newHarnessWith(t, openOnly, fixtureArchive, "")
	preview := h.get("/api/v1/archive-preview")
	if preview.data()["roots"] != float64(0) || len(stringsOf(preview.data()["candidate_ids"])) != 0 {
		t.Fatalf("preview = %s", preview.Body)
	}
	fingerprint, _ := preview.data()["fingerprint"].(string)
	swept := h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":`+jsonString(fingerprint)+`}`, nil)
	assertStatus(t, swept, 200)
	if swept.Body != `{"data":{"roots":0,"records":0,"moved_ids":[]},"meta":{"store_revision":"`+h.storeRevision()+`"}}` {
		t.Fatalf("empty sweep = %s", swept.Body)
	}
}

func TestArchiveSweepValidatesItsBodyAndTheStore(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{}`, `{"fingerprint":1}`, `{"fingerprint":""}`, `{"fingerprint":"x","roots":1}`} {
		assertError(t, h.json("POST", "/api/v1/archive-sweeps", body, nil), 422, "validation_failed")
	}
	h.writeStore("{not-json\n")
	assertError(t, h.get("/api/v1/archive-preview"), 503, "store_invalid")
	assertError(t, h.json("POST", "/api/v1/archive-sweeps", `{"fingerprint":"x"}`, nil), 503, "store_invalid")
}
