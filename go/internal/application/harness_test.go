package application

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"tasks-go/internal/check"
	"tasks-go/internal/record"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// The shared fixture is test/test_helper.rb's FIXTURE_RECORDS, id for id, so a
// behavioral assertion written against the Ruby suite reads the same here.
const (
	fixInbox  = "aaaa0001"
	fixGarden = "aaaa0002"
	fixWork   = "aaaa0003"
	fixFlight = "aaaa0004"
	fixPR     = "aaaa0005"
	fixEval   = "aaaa0006"
	fixTravel = "aaaa0007"
	fixOld    = "aaaa0008"
	fixHome   = "aaaa0009"
	fixPlants = "aaaa000a"
)

const fixtureStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"random thought about the garden"}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02"}
{"type":"task","id":"aaaa0005","parent":"aaaa0003","state":"NEXT","priority":"B","title":"Review PR backlog","tags":["@computer","important"]}
{"type":"task","id":"aaaa0006","parent":"aaaa0003","state":"TODO","priority":"A","title":"Midyear self-eval","tags":["@computer","important"],"scheduled":"2026-07-03"}
{"type":"task","id":"aaaa0007","parent":"aaaa0003","state":"WAITING","title":"Travel desk reply","tags":["@email","urgent"],"body":"Some note line."}
{"type":"task","id":"aaaa0008","parent":"aaaa0003","state":"DONE","priority":"C","title":"Old finished thing","tags":["@computer"],"closed":"2026-06-20"}
{"type":"section","id":"aaaa0009","title":"Home"}
{"type":"task","id":"aaaa000a","parent":"aaaa0009","state":"NEXT","title":"Water the plants","tags":["@home"]}
`

const archiveFixture = `{"type":"meta","version":2}
{"type":"task","id":"dead0001","state":"DONE","title":"Archived report"}
`

// The workers test_application.rb uses, verbatim.
const (
	worker = "claude-code/claude-fable-5/aaaa1111"
	rival  = "claude-code/claude-opus-5/bbbb2222"
)

// fixedDay is the day the Ruby suite measures the fixture against.
var fixedDay = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

func contextOn(day time.Time) temporal.Context {
	return temporal.Context{Now: day.UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
}

// harness is one temp store plus the application over it, and the counter that
// proves every operation received its own store.
type harness struct {
	t       *testing.T
	root    string
	org     string
	archive string
	app     *Application
	built   *atomic.Int64
	stores  []Store
}

type harnessOptions struct {
	live        string
	archive     string
	hostContext string
	now         time.Time
	// wrap turns each freshly built store into the value the application sees,
	// which is how a capability double is installed.
	wrap func(*store.Store) Store
}

func newHarness(t *testing.T, options harnessOptions) *harness {
	t.Helper()
	if options.live == "" {
		options.live = fixtureStore
	}
	if options.now.IsZero() {
		options.now = fixedDay
	}
	root := t.TempDir()
	org := filepath.Join(root, "tasks.jsonl")
	archive := filepath.Join(root, "archive.jsonl")
	if err := os.WriteFile(org, []byte(options.live), 0o644); err != nil {
		t.Fatal(err)
	}
	if options.archive != "" {
		if err := os.WriteFile(archive, []byte(options.archive), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h := &harness{t: t, root: root, org: org, archive: archive, built: &atomic.Int64{}}
	var ids atomic.Uint32
	var keys atomic.Uint32
	factory := func() Store {
		h.built.Add(1)
		built := store.NewWriter(org, archive, store.Options{
			JournalDir:    filepath.Join(root, "journal"),
			Now:           func() time.Time { return options.now },
			Device:        "fixture",
			IDSource:      func() string { return fmt.Sprintf("bbbb%04x", ids.Add(1)) },
			CoalesceScope: "pinned-scope",
			MaxDepth:      4,
		})
		if options.wrap != nil {
			return options.wrap(built)
		}
		return built
	}
	app, err := New(Options{
		Factory:             factory,
		TemporalContext:     func() temporal.Context { return contextOn(options.now) },
		HostContext:         options.hostContext,
		DelegationKeySource: func() string { return fmt.Sprintf("key%013x", keys.Add(1)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.app = app
	return h
}

func (h *harness) read() string {
	h.t.Helper()
	raw, err := os.ReadFile(h.org)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

func (h *harness) write(contents string) {
	h.t.Helper()
	if err := os.WriteFile(h.org, []byte(contents), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) assertChecks() {
	h.t.Helper()
	if result := check.Check(h.org); !result.OK() {
		h.t.Fatalf("store failed validation: %+v", result.Errors)
	}
}

func (h *harness) task(id string) store.Item {
	h.t.Helper()
	item, found, err := h.app.GetTask(id, false, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if !found {
		h.t.Fatalf("task %s not found", id)
	}
	return item
}

func idsOf(items []store.Item) []string {
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// markerOfRecord reads a delegation object straight out of the file, which is
// how a test asks what was PERSISTED rather than what a result reported.
func (h *harness) markerOfRecord(id string) map[string]string {
	h.t.Helper()
	parsed := record.Parse([]byte(h.read()))
	for _, r := range parsed.Records {
		if r.String("id") != id {
			continue
		}
		raw, ok := r.Get(store.DelegationField)
		if !ok {
			return nil
		}
		return decodeMarker(raw)
	}
	return nil
}

func recordsOf(t *testing.T, h *harness) []record.Record {
	t.Helper()
	return record.Parse([]byte(h.read())).Records
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
