package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/marcus/tasks/internal/check"
)

// The API is the only surface where many requests hit one store pair at once,
// so these are the tests the CLI does not have and cannot have. Run them with
// `go test -race ./internal/api/`.
//
// What each one is actually asking:
//
//   - that the server itself holds no shared mutable state (the race detector
//     answers that);
//   - that the store's lock keeps concurrent writers from interleaving into a
//     file that no longer validates; and
//   - that the If-Match precondition does its job under contention — exactly
//     one of N racing conditional writes may win.

func TestConcurrentCreatesAllLandAndTheStoreStaysValid(t *testing.T) {
	h := newHarness(t)
	const writers = 12

	var wait sync.WaitGroup
	answers := make([]answer, writers)
	start := make(chan struct{})
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			answers[index] = h.json("POST", "/api/v1/tasks",
				fmt.Sprintf(`{"title":"Concurrent %d"}`, index), nil)
		}(index)
	}
	close(start)
	wait.Wait()

	ids := map[string]bool{}
	for index, answered := range answers {
		if answered.Status != 201 {
			t.Fatalf("create %d: %d %s", index, answered.Status, answered.Body)
		}
		id, _ := answered.data()["id"].(string)
		if ids[id] {
			t.Fatalf("two concurrent creates minted the same id %q", id)
		}
		ids[id] = true
	}

	// Every accepted write is present, and the file the twelve of them produced
	// still validates.
	body := string(h.storeBytes())
	for index := 0; index < writers; index++ {
		if !strings.Contains(body, fmt.Sprintf("Concurrent %d", index)) {
			t.Errorf("create %d is missing from the store", index)
		}
	}
	if result := check.Check(h.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// Two conditional writes against the same task, from the same baseline: one
// wins with 200, the other loses with 412, and the file carries exactly the
// winner's value. The one outcome that must never happen is both succeeding.
func TestTwoConditionalWritesRaceAndExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	for attempt := 0; attempt < 8; attempt++ {
		baseline := h.etagOf(fixPR)
		titles := []string{
			fmt.Sprintf("winner-a-%d", attempt), fmt.Sprintf("winner-b-%d", attempt),
		}
		answers := make([]answer, 2)

		var wait sync.WaitGroup
		start := make(chan struct{})
		for index := 0; index < 2; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				answers[index] = h.json("PATCH", "/api/v1/tasks/"+fixPR,
					fmt.Sprintf(`{"title":%q}`, titles[index]), h.withIfMatch(baseline))
			}(index)
		}
		close(start)
		wait.Wait()

		winners := 0
		winningTitle := ""
		for index, answered := range answers {
			switch answered.Status {
			case 200:
				winners++
				winningTitle = titles[index]
			case 412:
				if answered.code() != "stale_revision" {
					t.Fatalf("loser code = %q", answered.code())
				}
				// The 412 carries the CURRENT resource and a fresh ETag, which
				// is what lets the loser decide again without a second read.
				if answered.dig("error", "details", "current") == nil {
					t.Errorf("412 carries no current resource: %s", answered.Body)
				}
				if answered.etag() == "" {
					t.Errorf("412 carries no fresh ETag: %s", answered.Body)
				}
			default:
				t.Fatalf("unexpected status %d: %s", answered.Status, answered.Body)
			}
		}
		if winners != 1 {
			t.Fatalf("attempt %d: %d writers won the same precondition", attempt, winners)
		}
		if got, _ := h.get("/api/v1/tasks/" + fixPR).data()["title"].(string); got != winningTitle {
			t.Fatalf("attempt %d: store holds %q, winner wrote %q", attempt, got, winningTitle)
		}
	}
	if result := check.Check(h.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// Concurrent writes to DIFFERENT tasks must all succeed. A precondition that
// failed here would mean the revision token was too coarse — the whole reason
// the store keeps a task's own value, its location, and its lifecycle in three
// separate components.
func TestConcurrentWritesToDifferentTasksAllSucceed(t *testing.T) {
	h := newHarness(t)
	targets := []string{fixFlight, fixPR, fixEval, fixTravel, fixPlants, fixGarden}
	tags := make([]string, len(targets))
	for index, id := range targets {
		tags[index] = h.etagOf(id)
	}

	answers := make([]answer, len(targets))
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := range targets {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			answers[index] = h.json("PATCH", "/api/v1/tasks/"+targets[index],
				fmt.Sprintf(`{"title":"retitled %d"}`, index), h.withIfMatch(tags[index]))
		}(index)
	}
	close(start)
	wait.Wait()

	for index, answered := range answers {
		if answered.Status != 200 {
			t.Errorf("%s: %d %s", targets[index], answered.Status, answered.Body)
		}
	}
	if result := check.Check(h.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// Reads racing writes must never observe a torn file. The atomic replace is the
// store's job; what this asserts is that the API never publishes a partial one
// — a read either sees the value before or the value after, and its ETag always
// matches the revision in the body it returned.
func TestReadsRacingWritesNeverObserveATornStore(t *testing.T) {
	h := newHarness(t)
	stop := make(chan struct{})
	writerDone := make(chan struct{})

	go func() {
		defer close(writerDone)
		for round := 0; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			loaded := h.get("/api/v1/tasks/" + fixPR)
			if loaded.Status != 200 {
				return
			}
			h.json("PATCH", "/api/v1/tasks/"+fixPR,
				fmt.Sprintf(`{"title":"churn %d"}`, round), h.withIfMatch(loaded.etag()))
		}
	}()

	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for round := 0; round < 40; round++ {
				answered := h.get("/api/v1/tasks")
				if answered.Status != 200 {
					t.Errorf("read during churn: %d %s", answered.Status, answered.Body)
					return
				}
				if answered.dig("meta", "store_revision") == nil {
					t.Errorf("read carries no store revision: %s", answered.Body)
					return
				}
				single := h.get("/api/v1/tasks/" + fixPR)
				if single.Status != 200 {
					t.Errorf("resource read during churn: %d %s", single.Status, single.Body)
					return
				}
				revision, _ := single.data()["revision"].(string)
				if single.etag() != `"`+revision+`"` {
					t.Errorf("etag %q disagrees with the body revision %q", single.etag(), revision)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	<-writerDone

	if result := check.Check(h.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// TWO servers over ONE store pair — the shape a second process takes, and the
// reason the API's safety cannot come from anything held in memory. If the
// serialization lived in the Server value rather than in the store's own lock,
// this is the test that would fail.
func TestTwoServersOverOneStorePairSerializeTheirWrites(t *testing.T) {
	first := newHarness(t)
	second := newHarnessSharing(t, first)

	answers := make([]answer, 12)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			server := first
			if index%2 == 1 {
				server = second
			}
			answers[index] = server.json("POST", "/api/v1/tasks",
				fmt.Sprintf(`{"title":"cross %d"}`, index), nil)
		}(index)
	}
	close(start)
	wait.Wait()

	ids := map[string]bool{}
	for index, answered := range answers {
		if answered.Status != 201 {
			t.Fatalf("cross create %d: %d %s", index, answered.Status, answered.Body)
		}
		id, _ := answered.data()["id"].(string)
		if ids[id] {
			t.Fatalf("two servers minted the same id %q", id)
		}
		ids[id] = true
	}
	body := string(first.storeBytes())
	for index := 0; index < 12; index++ {
		if !strings.Contains(body, fmt.Sprintf("cross %d", index)) {
			t.Errorf("cross create %d is missing from the shared store", index)
		}
	}
	// Both servers answer from the same bytes, so their global revisions agree.
	if first.get("/api/v1/meta").dig("meta", "store_revision") !=
		second.get("/api/v1/meta").dig("meta", "store_revision") {
		t.Error("two servers over one store pair report different store revisions")
	}
	if result := check.Check(first.org); !result.OK() {
		t.Errorf("the store no longer validates: %v", result.Errors)
	}
}

// An external writer — a CLI process, or a hand edit — changes the bytes under
// a loaded ETag. The next conditional write must lose, and the reader must see
// the external value rather than a cached one.
func TestAnExternalChangeMakesALoadedETagStale(t *testing.T) {
	h := newHarness(t)
	loaded := h.etagOf(fixPR)
	before := h.get("/api/v1/meta").dig("meta", "store_revision")

	h.writeStore(strings.Replace(fixtureOrg, "Review PR backlog", "Externally renamed", 1))

	if after := h.get("/api/v1/meta").dig("meta", "store_revision"); after == before {
		t.Error("an external change did not advance the store revision")
	}
	if got, _ := h.get("/api/v1/tasks/" + fixPR).data()["title"].(string); got != "Externally renamed" {
		t.Errorf("the read is stale: %q", got)
	}
	stale := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"API overwrite"}`, h.withIfMatch(loaded))
	assertError(t, stale, 412, "stale_revision")
	current, _ := stale.dig("error", "details", "current").(map[string]any)
	if current["title"] != "Externally renamed" {
		t.Errorf("412 details.current.title = %v", current["title"])
	}
	if !strings.Contains(string(h.storeBytes()), "Externally renamed") {
		t.Error("the refused write overwrote the external change")
	}
}

// Concurrent requests must produce one PARSEABLE log line each. This is the
// test for the interleaving the race detector found: two goroutines writing
// through one io.Writer produced half a JSON object followed by half of
// another, which no consumer can read.
func TestConcurrentRequestsProduceWholeLogLines(t *testing.T) {
	h := newHarness(t)
	var wait sync.WaitGroup
	for index := 0; index < 24; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			h.get("/healthz")
		}()
	}
	wait.Wait()

	lines := strings.Split(strings.TrimSpace(h.logs.String()), "\n")
	if len(lines) != 24 {
		t.Fatalf("got %d log lines, want 24", len(lines))
	}
	seen := map[string]bool{}
	for index, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %d is not valid JSON: %q", index, line)
		}
		requestID, _ := entry["request_id"].(string)
		if requestID == "" || seen[requestID] {
			t.Fatalf("log line %d has a missing or repeated request id: %q", index, line)
		}
		seen[requestID] = true
	}
}

// The rollback record must reach the client honestly: a failed WRITE is
// `unavailable`, a failed post-write CHECK is `store_invalid`. Collapsing both
// is what once made a failed write claim the file failed validation.
func TestRollbackStageIsSurfacedRatherThanFlattened(t *testing.T) {
	h := newHarness(t)
	// A store whose bytes are invalid refuses the write before it starts, and
	// the refusal names validation rather than availability.
	h.writeStore(strings.Replace(fixtureOrg, `"parent":"aaaa0003"`, `"parent":"ffffffff"`, 1))
	answered := h.json("POST", "/api/v1/tasks", `{"title":"must not land"}`, nil)
	assertError(t, answered, 503, "store_invalid")
	if strings.Contains(answered.Body, h.dir) {
		t.Error("the refusal leaks the store path")
	}
	if !strings.Contains(string(h.storeBytes()), `"parent":"ffffffff"`) {
		t.Error("the refused write repaired or overwrote the invalid store")
	}
	// And a read of the same store is refused the same way, without leaking a
	// path either.
	read := h.get("/api/v1/tasks")
	assertError(t, read, 503, "store_invalid")
	if strings.Contains(read.Body, h.dir) {
		t.Error("the read refusal leaks the store path")
	}
}
