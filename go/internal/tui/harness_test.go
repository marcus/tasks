package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// The fixture is test/test_helper.rb's FIXTURE_RECORDS, id for id, so an
// assertion written against the Ruby TUI suite reads the same here.
const (
	fixInbox  = "aaaa0001"
	fixGarden = "aaaa0002"
	fixWork   = "aaaa0003"
	fixFlight = "aaaa0004"
	fixPR     = "aaaa0005"
	fixEval   = "aaaa0006"
	fixTravel = "aaaa0007"
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

// fixedDay is the day the Ruby suite measures the fixture against.
var fixedDay = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

type modelHarness struct {
	t     *testing.T
	model *Model
	org   string
	root  string
	env   determinism.Env
}

type harnessOptions struct {
	live string
	now  time.Time
	// paths overrides the resolved configuration for this model.
	paths func(*config.Paths)
	// opener is the link launcher. Tests inject a fake; nothing here may ever
	// reach a real browser.
	opener Opener
	// entries and queue are the agent surface. Tests inject scripted adapters;
	// nothing here may ever reach a real provider.
	entries []AgentEntry
	queue   *agentQueue
}

// blockedArchiveFixture has a closed root with open work still inside it, which
// is the state a sweep must refuse rather than confirm.
const blockedArchiveFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"DONE","title":"Closed parent","closed":"2026-06-20"}
{"type":"task","id":"aaaa0005","parent":"aaaa0004","state":"NEXT","title":"Still open inside"}
`

// linkedFixture carries a URL in a task body, for the link action.
const linkedFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Book flight in Concur","body":"See https://example.com/itinerary for details."}
`

// newModelHarness builds one temp store and a model over it.
//
// It NEVER touches real task data: everything lives under t.TempDir(), and the
// environment the model reads is a map, not the process environment, so a saved
// session cannot land in the developer's own XDG state directory.
func newModelHarness(t *testing.T, options harnessOptions) *modelHarness {
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

	context := func() temporal.Context {
		return temporal.Context{Now: options.now.UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
	}
	app, err := application.New(application.Options{
		Factory: func() application.Store {
			return store.NewWriter(org, archive, store.Options{
				JournalDir: filepath.Join(root, "journal"),
				Now:        func() time.Time { return options.now },
				Device:     "fixture",
				MaxDepth:   4,
				UndoLimit:  50,
				// Pinned like every other harness: the editor's one-undo-step
				// contract depends on the scope matching across the fresh store
				// each application operation builds.
				CoalesceScope: "pinned-scope",
			})
		},
		TemporalContext: context,
	})
	if err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{
		Org:        org,
		Archive:    archive,
		UrgentDays: config.DefaultUrgentDays,
		MaxDepth:   config.DefaultMaxDepth,
		Timezone:   "Etc/UTC",
		TimeFormat: 24,
	}
	if options.paths != nil {
		options.paths(&paths)
	}
	env := determinism.Env{"XDG_STATE_HOME": filepath.Join(root, "state"), "HOME": root}

	model := New(Options{
		App:     app,
		Paths:   paths,
		Env:     env,
		Now:     func() time.Time { return options.now },
		Opener:  options.opener,
		Entries: options.entries,
		Queue:   options.queue,
	})
	model.width, model.height = 100, 30
	model.Refresh()
	return &modelHarness{t: t, model: model, org: org, root: root, env: env}
}

// rewrite replaces the store's bytes, as an external writer would.
func (h *modelHarness) rewrite(content string) {
	h.t.Helper()
	if err := os.WriteFile(h.org, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	// Stale() compares modtime and size; a same-size rewrite inside one
	// filesystem timestamp tick would otherwise look unchanged.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(h.org, future, future); err != nil {
		h.t.Fatal(err)
	}
}

// press drives one rune key through Update, as a user would.
func (h *modelHarness) press(key rune) {
	h.t.Helper()
	h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
}

// pressType drives one non-rune key.
func (h *modelHarness) pressType(keyType tea.KeyType) {
	h.t.Helper()
	h.model.Update(tea.KeyMsg{Type: keyType})
}

// pressTypeEnter is the enter key.
func (h *modelHarness) pressTypeEnter() {
	h.t.Helper()
	h.pressType(tea.KeyEnter)
}

// pressTypeEsc is the escape key, spelled out because it appears often.
func (h *modelHarness) pressTypeEsc() {
	h.t.Helper()
	h.pressType(tea.KeyEsc)
}

// tick drives one file-watch tick.
func (h *modelHarness) tick() {
	h.t.Helper()
	h.model.Update(tickMsg(time.Now()))
}

// titles is the visible row text, for readable failure output.
func (h *modelHarness) titles() []string {
	out := []string{}
	for _, row := range h.model.Rows() {
		out = append(out, row.Text)
	}
	return out
}

// selectRowByID moves the cursor onto the row carrying an id.
func (h *modelHarness) selectRowByID(id string) {
	h.t.Helper()
	for index, row := range h.model.Rows() {
		if row.ID() == id {
			h.model.selectRow(index)
			return
		}
	}
	h.t.Fatalf("no row carries id %s; rows are %v", id, h.titles())
}
