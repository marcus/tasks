package application

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/store"
)

const activateFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"Held, past start","tags":["defer"],"scheduled":"2026-07-01"}
{"type":"task","id":"aaaa0003","parent":"aaaa0001","state":"TODO","title":"Future start","scheduled":"2026-08-01"}
{"type":"task","id":"aaaa0004","parent":"aaaa0001","state":"TODO","title":"Lead window","deadline":"2026-08-01","lead":"1w"}
{"type":"task","id":"aaaa0005","parent":"aaaa0001","state":"TODO","title":"Parent"}
{"type":"task","id":"aaaa0006","parent":"aaaa0005","state":"TODO","title":"Child"}
{"type":"task","id":"aaaa0007","parent":"aaaa0001","state":"NEXT","title":"Weekly","scheduled":"2026-07-07","recur":".+1w"}
`

// ActivateTask is the store's `activate` field and nothing else: the hold goes,
// a past start date stays, a future one goes, and a lead task records a
// one-occurrence release instead of losing its anchor.
func TestActivateTaskKeepsAPastStartAndReleasesALeadOccurrence(t *testing.T) {
	h := newHarness(t, harnessOptions{live: activateFixture})
	for _, id := range []string{"aaaa0002", "aaaa0003", "aaaa0004"} {
		revision, _ := h.app.store().(Placer).TaskRevision(id)
		outcome := h.app.ActivateTask(ActivateCommand{ID: id, ExpectedRevision: revision}, nil)
		if !outcome.Changed() {
			t.Fatalf("%s: %q %v", id, outcome.Status, outcome.Errors)
		}
	}
	stored := h.read()
	for _, want := range []string{
		`"state":"TODO","title":"Held, past start","scheduled":"2026-07-01",`,
		`"state":"TODO","title":"Future start","updated"`,
		`"deadline":"2026-08-01","lead":"1w","lead_skip":"2026-08-01"`,
	} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %s in\n%s", want, stored)
		}
	}
	h.assertChecks()
}

func TestActivateTaskRefusesAStaleRevisionAndAMissingID(t *testing.T) {
	h := newHarness(t, harnessOptions{live: activateFixture})
	revision, _ := h.app.store().(Placer).TaskRevision("aaaa0002")
	h.app.PatchTask(Patch{ID: "aaaa0002", Field: store.FieldTitle, Value: "Renamed", Expected: "Held, past start"}, nil)
	if outcome := h.app.ActivateTask(ActivateCommand{ID: "aaaa0002", ExpectedRevision: revision}, nil); !outcome.Stale() {
		t.Errorf("stale revision: %q", outcome.Status)
	}
	if outcome := h.app.ActivateTask(ActivateCommand{ID: "deadbeef"}, nil); !outcome.NotFound() {
		t.Errorf("missing id: %q", outcome.Status)
	}
	if outcome := h.app.ActivateTask(ActivateCommand{}, nil); !outcome.Invalid() {
		t.Errorf("blank id: %q", outcome.Status)
	}
}

// Effects come from the store's own report: a roll names its anchor move, a
// cascade names every task it closed, and an ordinary edit has none.
func TestEffectsReportRollsAndCascadesFromTheStoreResult(t *testing.T) {
	h := newHarness(t, harnessOptions{live: activateFixture})

	cascade := h.app.PatchTask(Patch{ID: "aaaa0005", Field: store.FieldState, Value: "DONE",
		Expected: mustBaseline(t, h, "aaaa0005", store.FieldState)}, nil)
	effects := cascade.EffectsFor("aaaa0005")
	if effects.Rolled != nil || !equalStrings(effects.TouchedIDs, []string{"aaaa0005", "aaaa0006"}) {
		t.Errorf("cascade effects = %+v", effects)
	}

	rolled := h.app.PatchTask(Patch{ID: "aaaa0007", Field: store.FieldState, Value: "DONE",
		Expected: mustBaseline(t, h, "aaaa0007", store.FieldState)}, nil)
	effects = rolled.EffectsFor("aaaa0007")
	if effects.Rolled == nil || effects.Rolled.From != "2026-07-07" || effects.Rolled.To != "2026-07-21" {
		t.Errorf("roll effects = %+v (rolled %+v)", effects, effects.Rolled)
	}
	if len(effects.TouchedIDs) != 0 {
		t.Errorf("a roll touches only its own task: %v", effects.TouchedIDs)
	}

	plain := h.app.PatchTask(Patch{ID: "aaaa0003", Field: store.FieldTitle, Value: "Retitled",
		Expected: "Future start"}, nil)
	if plain.EffectsFor("aaaa0003").Any() {
		t.Errorf("a title edit reported effects: %+v", plain.EffectsFor("aaaa0003"))
	}
	if (Outcome{}).EffectsFor("aaaa0003").Any() {
		t.Error("a refusal reported effects")
	}
}

func mustBaseline(t *testing.T, h *harness, id string, field store.PatchField) string {
	t.Helper()
	value, found, err := h.app.Baseline(id, field)
	if err != nil || !found {
		t.Fatalf("baseline %s/%s: %v %v", id, field, found, err)
	}
	return value
}
