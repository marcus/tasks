package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// undoOnce steps the journal back one entry through a second store over the
// same files, which is how a test proves a write was ONE undo step: the file
// returns to exactly what it was.
func (h *harness) undoOnce() string {
	h.t.Helper()
	writer := store.NewWriter(h.org, h.archive, store.Options{
		JournalDir: filepath.Join(h.dir, "journal"), Device: "pinned",
	})
	outcome, label := writer.HistoryStep(-1)
	if outcome != store.HistoryOK {
		h.t.Fatalf("undo: %q", outcome)
	}
	return label
}

// -- POST /tasks/{id}/activate ---------------------------------------------

func TestActivateReleasesTheHoldInOneUndoStep(t *testing.T) {
	h := newHarness(t)
	before := string(h.storeBytes())
	answered := h.json("POST", "/api/v1/tasks/"+fixPlants+"/activate", "", h.withIfMatch(h.etagOf(fixPlants)))
	assertStatus(t, answered, 200)
	if answered.data()["deferred"] != false || answered.data()["available"] != true {
		t.Errorf("activated task = %v", answered.data())
	}
	if answered.etag() == "" || answered.etag() != h.etagOf(fixPlants) {
		t.Errorf("etag %q is not the task's current revision", answered.etag())
	}
	if label := h.undoOnce(); label != "activate: Water the plants" {
		t.Errorf("undo label = %q, want the CLI's", label)
	}
	if string(h.storeBytes()) != before {
		t.Error("one undo did not restore the pre-activate file")
	}
}

// The three things PATCH cannot express: a past start date stays, a future one
// goes, and a lead task gets a one-occurrence release instead of losing dates.
func TestActivateKeepsAPastStartAndReleasesALeadOccurrence(t *testing.T) {
	h := newHarness(t)

	// Past start date plus a hold: the hold goes, the date is history and stays.
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixEval, `{"deferred":true}`,
		h.withIfMatch(h.etagOf(fixEval))), 200)
	past := h.json("POST", "/api/v1/tasks/"+fixEval+"/activate", "", h.withIfMatch(h.etagOf(fixEval)))
	assertStatus(t, past, 200)
	if past.data()["scheduled"] != "2026-07-03" || past.data()["deferred"] != false {
		t.Errorf("past start: scheduled %v deferred %v", past.data()["scheduled"], past.data()["deferred"])
	}

	// A future start date is the gate itself, so it goes.
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixEval, `{"scheduled":"2026-08-01"}`,
		h.withIfMatch(h.etagOf(fixEval))), 200)
	future := h.json("POST", "/api/v1/tasks/"+fixEval+"/activate", "", h.withIfMatch(h.etagOf(fixEval)))
	assertStatus(t, future, 200)
	if future.data()["scheduled"] != nil || future.data()["available"] != true {
		t.Errorf("future start: scheduled %v available %v", future.data()["scheduled"], future.data()["available"])
	}

	// A lead window keeps its anchor and releases this occurrence only.
	created := h.json("POST", "/api/v1/tasks", `{"title":"Renew passport","deadline":"2026-08-01","lead":"1w"}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)
	if created.data()["available"] != false {
		t.Fatalf("the lead window should hide the task until 2026-07-25: %v", created.data())
	}
	released := h.json("POST", "/api/v1/tasks/"+id+"/activate", "", h.withIfMatch(created.etag()))
	assertStatus(t, released, 200)
	if released.data()["deadline"] != "2026-08-01" || released.data()["lead"] != "1w" || released.data()["available"] != true {
		t.Errorf("lead release = %v", released.data())
	}
	if !strings.Contains(string(h.storeBytes()), `"lead_skip":"2026-08-01"`) {
		t.Errorf("no one-occurrence release in the store:\n%s", h.storeBytes())
	}
}

// PARITY: the route writes the bytes `tasks activate` writes — the store's
// activate changeset guarded by the task's revision, with the CLI's label.
func TestActivateWritesTheSameBytesAsTheCLIChangeset(t *testing.T) {
	viaHTTP := newHarness(t)
	assertStatus(t, viaHTTP.json("POST", "/api/v1/tasks/"+fixPlants+"/activate", "",
		viaHTTP.withIfMatch(viaHTTP.etagOf(fixPlants))), 200)

	viaCLI := newHarness(t)
	writer := store.NewWriter(viaCLI.org, viaCLI.archive, store.Options{
		JournalDir: filepath.Join(viaCLI.dir, "journal"), Device: "pinned",
		Now: func() time.Time { return viaCLI.now }, MaxDepth: 4,
	})
	context, err := temporal.NewContext(viaCLI.now, "Etc/UTC", 12)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := writer.TaskRevision(fixPlants)
	result := writer.ApplyChangeset(store.Changeset{
		ID: fixPlants, ExpectedRevision: revision, HistoryLabel: "activate: Water the plants",
		Changes: []store.Change{{Field: store.FieldActivate, Value: store.BoolValue(true)}},
		Today:   context.LocalDate().ISO(), Context: context,
	})
	if !result.Changed() {
		t.Fatalf("CLI changeset: %q %v", result.Status, result.Errors)
	}
	if string(viaHTTP.storeBytes()) != string(viaCLI.storeBytes()) {
		t.Errorf("surfaces disagree\nHTTP:\n%s\nCLI:\n%s", viaHTTP.storeBytes(), viaCLI.storeBytes())
	}
}

func TestActivateRefusals(t *testing.T) {
	h := newHarness(t)
	path := "/api/v1/tasks/" + fixPlants + "/activate"
	before := string(h.storeBytes())
	stale := h.etagOf(fixPlants)

	assertError(t, h.json("POST", path, "", nil), 428, "missing_precondition")
	// `*` is a PATCH-delta precondition only; here it is as good as absent.
	wildcard := h.json("POST", path, "", h.withIfMatch("*"))
	assertError(t, wildcard, 428, "missing_precondition")
	if !strings.Contains(wildcard.message(), "delta") {
		t.Errorf("wildcard refusal %q does not say what * is for", wildcard.message())
	}
	assertError(t, h.json("POST", path, `{"now":true}`, h.withIfMatch(stale)), 400, "malformed_request")
	assertError(t, h.json("POST", path+"?force=true", "", h.withIfMatch(stale)), 422, "validation_failed")
	assertError(t, h.json("POST", "/api/v1/tasks/deadbeef/activate", "", h.withIfMatch(stale)), 404, "not_found")
	// An archived-only id is not a live task.
	assertError(t, h.json("POST", "/api/v1/tasks/dddd0001/activate", "", h.withIfMatch(stale)), 404, "not_found")
	if string(h.storeBytes()) != before {
		t.Fatal("a refused activate wrote")
	}

	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixPlants, `{"title":"Water the ferns"}`,
		h.withIfMatch(stale)), 200)
	refused := h.json("POST", path, "", h.withIfMatch(stale))
	assertError(t, refused, 412, "stale_revision")
	if refused.dig("error", "details", "current") == nil {
		t.Errorf("412 carries no current resource: %s", refused.Body)
	}
}

// -- PATCH delta members ----------------------------------------------------

// The four tag lists are the CLI's `tasks tag +travel -urgent @phone -@computer`
// in one write, applied to the stored sequence rather than a client's copy.
func TestPatchTagDeltasEditTheStoredSequence(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight,
		`{"add_tags":["travel"],"remove_tags":["urgent"],"add_contexts":["@phone"],"remove_contexts":["@computer"]}`,
		h.withIfMatch(h.etagOf(fixFlight)))
	assertStatus(t, answered, 200)
	assertStrings(t, stringsOf(answered.data()["tags"]), []string{"important", "travel"}, "tags")
	assertStrings(t, stringsOf(answered.data()["contexts"]), []string{"@phone"}, "contexts")
	// The undo step names the operation the way `tasks tag` does.
	if label := h.undoOnce(); label != "tags: Book flight in Concur" {
		t.Errorf("undo label = %q, want the CLI's", label)
	}
}

// A delta PATCH is labelled like the CLI verb it duplicates, and a mixed one
// names the operations, never the store's internal field names.
func TestDeltaPatchUndoLabelsMatchTheCLI(t *testing.T) {
	h := newHarness(t)
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"Called back."}`,
		h.withIfMatch("*")), 200)
	if label := h.undoOnce(); label != "note: Travel desk reply" {
		t.Errorf("append label = %q, want `tasks note`'s", label)
	}
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"x","add_tags":["travel"]}`,
		h.withIfMatch("*")), 200)
	history := h.get("/api/v1/history")
	if label, _ := history.dig("data", "undo").(string); strings.Contains(label, "_") ||
		label != "edit note, tags: Travel desk reply" {
		t.Errorf("mixed delta label = %q (history %s)", label, history.Body)
	}
}

func TestPatchAppendBodyAddsALine(t *testing.T) {
	h := newHarness(t)
	text := h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"Called back."}`,
		h.withIfMatch(h.etagOf(fixTravel)))
	assertStatus(t, text, 200)
	assertStrings(t, stringsOf(text.data()["body"]), []string{"Some note line.", "Called back."}, "body")

	lines := h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":["Two","lines"]}`,
		h.withIfMatch(h.etagOf(fixTravel)))
	assertStatus(t, lines, 200)
	assertStrings(t, stringsOf(lines.data()["body"]),
		[]string{"Some note line.", "Called back.", "Two", "lines"}, "body")

	// A first note is the whole body.
	first := h.json("PATCH", "/api/v1/tasks/"+fixGarden, `{"append_body":"First."}`,
		h.withIfMatch(h.etagOf(fixGarden)))
	assertStrings(t, stringsOf(first.data()["body"]), []string{"First."}, "first body")
}

func TestPatchDeltaRefusals(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixFlight)
	before := string(h.storeBytes())
	for _, testCase := range []struct{ body, field string }{
		{`{"add_tags":["x"],"tags":["y"]}`, "add_tags"},
		{`{"remove_contexts":["@computer"],"contexts":["@desk"]}`, "remove_contexts"},
		// A tag delta owns the whole sequence, the hold marker included.
		{`{"add_tags":["x"],"contexts":["@desk"]}`, "add_tags"},
		{`{"add_contexts":["@desk"],"deferred":true}`, "add_contexts"},
		{`{"append_body":"x","body":"y"}`, "append_body"},
		{`{"add_tags":["x"],"remove_tags":["x"]}`, "remove_tags"},
		{`{"add_tags":"x"}`, "add_tags"},
		{`{"add_tags":null}`, "add_tags"},
		{`{"add_tags":["@x"]}`, "add_tags"},
		{`{"add_tags":["defer"]}`, "add_tags"},
		{`{"remove_contexts":["computer"]}`, "remove_contexts"},
		{`{"append_body":""}`, "append_body"},
		// Whitespace-only is blank, as `tasks note` treats it.
		{`{"append_body":"   "}`, "append_body"},
		{`{"append_body":"\n\t"}`, "append_body"},
		{`{"append_body":["", "  "]}`, "append_body"},
		{`{"append_body":[]}`, "append_body"},
		{`{"append_body":null}`, "append_body"},
		{`{"append_body":7}`, "append_body"},
	} {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, testCase.body, h.withIfMatch(current))
		assertError(t, answered, 422, "validation_failed")
		fields, _ := answered.dig("error", "details", "fields").(map[string]any)
		if _, present := fields[testCase.field]; !present {
			t.Errorf("%s: details.fields = %v, want %q", testCase.body, fields, testCase.field)
		}
	}
	if string(h.storeBytes()) != before {
		t.Fatal("a refused delta wrote")
	}
}

// `If-Match: *` is the delta-only precondition, and nothing else may use it.
func TestPatchWildcardPreconditionIsForDeltasOnly(t *testing.T) {
	h := newHarness(t)
	wildcard := h.withIfMatch("*")
	for _, body := range []string{
		`{"title":"x"}`,
		`{"title":"x","append_body":"y"}`,
		`{"deadline":null}`,
	} {
		refused := h.json("PATCH", "/api/v1/tasks/"+fixTravel, body, wildcard)
		assertError(t, refused, 428, "missing_precondition")
		if !strings.Contains(refused.message(), "delta") {
			t.Errorf("%s: message %q does not say what * is for", body, refused.message())
		}
	}
	// Absent is still 428 on a delta-only PATCH: the header is mandatory.
	assertError(t, h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"x"}`, nil),
		428, "missing_precondition")
	// A wildcard names no task that is not there.
	assertError(t, h.json("PATCH", "/api/v1/tasks/deadbeef", `{"append_body":"x"}`, wildcard),
		404, "not_found")

	// Every other write refuses the wildcard with the same 428, never a 422.
	before := string(h.storeBytes())
	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixGarden, "", wildcard), 428, "missing_precondition")
	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixPR+"?cascade=true", "", wildcard), 428, "missing_precondition")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixPlants+"/activate", "", wildcard), 428, "missing_precondition")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixFlight+"/delegate", `{"kind":"agent","mode":"research"}`,
		wildcard), 428, "missing_precondition")
	if string(h.storeBytes()) != before {
		t.Fatal("a wildcard-refused write wrote")
	}

	accepted := h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"x","add_tags":["travel"]}`, wildcard)
	assertStatus(t, accepted, 200)
	if accepted.etag() == "" {
		t.Error("a wildcard write still answers with the new ETag")
	}
}

// The case the wildcard exists for: a web form appends a note while an agent
// edits the same task's priority. With the form's stale baseline the append
// is a 412 that must be retried; with `*` it lands, and so does the edit.
func TestADeltaAppendLandsBesideAConcurrentEdit(t *testing.T) {
	h := newHarness(t)
	baseline := h.etagOf(fixTravel)

	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"priority":"A"}`,
		h.withIfMatch(baseline)), 200)

	// A real revision keeps its whole-task meaning even on a delta.
	assertError(t, h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"From the form."}`,
		h.withIfMatch(baseline)), 412, "stale_revision")

	landed := h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"append_body":"From the form."}`, h.withIfMatch("*"))
	assertStatus(t, landed, 200)
	if landed.data()["priority"] != "A" {
		t.Errorf("the append overwrote the concurrent edit: %v", landed.data()["priority"])
	}
	assertStrings(t, stringsOf(landed.data()["body"]), []string{"Some note line.", "From the form."}, "body")
}

// Under real contention every wildcard append lands, none is lost, and the
// conditional edit racing them still behaves as a precondition.
func TestConcurrentWildcardAppendsAllLand(t *testing.T) {
	h := newHarness(t)
	const writers = 10
	baseline := h.etagOf(fixTravel)

	var wait sync.WaitGroup
	start := make(chan struct{})
	answers := make([]answer, writers)
	var edit answer
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			answers[index] = h.json("PATCH", "/api/v1/tasks/"+fixTravel,
				fmt.Sprintf(`{"append_body":"note %d"}`, index), h.withIfMatch("*"))
		}(index)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		edit = h.json("PATCH", "/api/v1/tasks/"+fixTravel, `{"priority":"C"}`, h.withIfMatch(baseline))
	}()
	close(start)
	wait.Wait()

	for index, answered := range answers {
		if answered.Status != 200 {
			t.Fatalf("append %d: %d %s", index, answered.Status, answered.Body)
		}
	}
	// The conditional edit wins only if it ran before every append.
	if edit.Status != 200 && edit.Status != 412 {
		t.Fatalf("conditional edit: %d %s", edit.Status, edit.Body)
	}
	body := stringsOf(h.get("/api/v1/tasks/" + fixTravel).data()["body"])
	if len(body) != writers+1 {
		t.Fatalf("body has %d lines, want %d: %v", len(body), writers+1, body)
	}
	for index := 0; index < writers; index++ {
		if !containsString(body, fmt.Sprintf("note %d", index)) {
			t.Errorf("note %d was lost", index)
		}
	}
	if result := check.Check(h.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// -- date clearing through `undate` ------------------------------------------

func TestPatchDateNullTakesTheUndateOperation(t *testing.T) {
	h := newHarness(t)
	cleared := h.json("PATCH", "/api/v1/tasks/"+fixFlight, `{"deadline":null,"deadline_time":null}`,
		h.withIfMatch(h.etagOf(fixFlight)))
	assertStatus(t, cleared, 200)
	// The cookie anchored on the deadline retires with it, as `tasks undate` does.
	if cleared.data()["deadline"] != nil || cleared.data()["recurrence"] != nil {
		t.Errorf("deadline %v recurrence %v", cleared.data()["deadline"], cleared.data()["recurrence"])
	}
	if label := h.undoOnce(); label != "remove deadline: Book flight in Concur" {
		t.Errorf("undo label = %q, want `tasks undate --kind deadline`'s", label)
	}

	// Nulling a date the task does not carry stays a declarative no-op rather
	// than the operation's "no matching date stamp" refusal.
	before := string(h.storeBytes())
	absent := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"deadline":null,"scheduled":null}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, absent, 200)
	if string(h.storeBytes()) != before {
		t.Error("nulling absent dates wrote")
	}

	// Clearing one date while setting the other keeps the per-field path.
	moved := h.json("PATCH", "/api/v1/tasks/"+fixEval, `{"scheduled":null,"deadline":"2026-09-01"}`,
		h.withIfMatch(h.etagOf(fixEval)))
	assertStatus(t, moved, 200)
	if moved.data()["scheduled"] != nil || moved.data()["deadline"] != "2026-09-01" {
		t.Errorf("scheduled %v deadline %v", moved.data()["scheduled"], moved.data()["deadline"])
	}
}

// -- meta.effects --------------------------------------------------------------

func TestCompletingARecurringTaskReportsTheRoll(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, `{"state":"DONE"}`,
		h.withIfMatch(h.etagOf(fixFlight)))
	assertStatus(t, answered, 200)
	if answered.data()["state"] != "NEXT" {
		t.Errorf("a rolled task stays open: state %v", answered.data()["state"])
	}
	rolled, _ := answered.dig("meta", "effects", "rolled").(map[string]any)
	if rolled["from"] != "2026-07-02" || rolled["to"] != answered.data()["deadline"] || rolled["to"] == rolled["from"] {
		t.Errorf("rolled = %v, deadline now %v", rolled, answered.data()["deadline"])
	}
	if answered.dig("meta", "effects", "touched_ids") != nil {
		t.Errorf("a roll touches only its own task: %s", answered.Body)
	}
}

func TestCompletingAParentReportsTheCascade(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"state":"DONE"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)
	assertStrings(t, stringsOf(answered.dig("meta", "effects", "touched_ids")),
		[]string{fixPR, fixChild, fixGrand}, "touched_ids")
	if answered.dig("meta", "effects", "rolled") != nil {
		t.Errorf("no roll: %s", answered.Body)
	}
}

// A move carries the subtree with it, and every descendant it relocated is a
// task this write changed, so touched_ids names them as well.
func TestMovingAParentReportsTheDescendantsItCarried(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"parent_id":"`+fixHome+`"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)
	if answered.data()["section_id"] != fixHome {
		t.Fatalf("section_id = %v", answered.data()["section_id"])
	}
	assertStrings(t, stringsOf(answered.dig("meta", "effects", "touched_ids")),
		[]string{fixPR, fixChild, fixGrand}, "touched_ids")
	// A leaf move changes only the returned task, so there is nothing to add.
	leaf := h.json("PATCH", "/api/v1/tasks/"+fixGarden, `{"parent_id":"`+fixHome+`"}`,
		h.withIfMatch(h.etagOf(fixGarden)))
	assertStatus(t, leaf, 200)
	if leaf.dig("meta", "effects") != nil {
		t.Errorf("a leaf move reports effects: %s", leaf.Body)
	}
}

func TestAnOrdinaryWriteCarriesNoEffects(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{"title":"Retitled"}`, `{"title":"Retitled"}`} {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, body, h.withIfMatch(h.etagOf(fixPR)))
		assertStatus(t, answered, 200)
		meta, _ := answered.dig("meta").(map[string]any)
		if _, present := meta["effects"]; present || len(meta) != 1 {
			t.Errorf("%s: meta = %v", body, meta)
		}
	}
}

// cascade=true answers with what it removed even when that is one leaf, so the
// status follows the request rather than the subtree's shape.
func TestCascadeDeleteOfALeafStillListsIt(t *testing.T) {
	h := newHarness(t)
	answered := h.json("DELETE", "/api/v1/tasks/"+fixGarden+"?cascade=true", "",
		h.withIfMatch(h.etagOf(fixGarden)))
	assertStatus(t, answered, 200)
	deleted, _ := answered.dig("data", "deleted").([]any)
	if len(deleted) != 1 {
		t.Fatalf("deleted = %v", deleted)
	}
	if first, _ := deleted[0].(map[string]any); first["id"] != fixGarden {
		t.Errorf("deleted[0] = %v", first)
	}
}
