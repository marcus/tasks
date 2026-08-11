package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

func sessionEnv(t *testing.T) determinism.Env {
	t.Helper()
	root := t.TempDir()
	return determinism.Env{"XDG_STATE_HOME": filepath.Join(root, "state"), "HOME": root}
}

func TestSessionRoundTrips(t *testing.T) {
	env := sessionEnv(t)
	state := SessionState{
		View:           ViewNext,
		Collapsed:      []string{"aaaa0001"},
		PanelMode:      "wide",
		PanelOffset:    4,
		ContextFilters: []string{"@home"},
	}
	if err := SaveSession(state, env); err != nil {
		t.Fatal(err)
	}
	if got := LoadSession(env); !reflect.DeepEqual(got.View, state.View) ||
		!reflect.DeepEqual(got.Collapsed, state.Collapsed) ||
		got.PanelMode != state.PanelMode || got.PanelOffset != state.PanelOffset ||
		!reflect.DeepEqual(got.ContextFilters, state.ContextFilters) {
		t.Fatalf("round trip lost state: %+v", got)
	}
}

func TestSessionLandsUnderXDGStateHome(t *testing.T) {
	env := sessionEnv(t)
	if err := SaveSession(SessionState{View: ViewInbox}, env); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(env["XDG_STATE_HOME"], "tasks", "tui.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("session did not land at %s: %v", want, err)
	}
}

func TestSessionReadsToleratesEveryBrokenFile(t *testing.T) {
	// Session state must NEVER be able to keep the TUI from starting, so every
	// unusable shape has to load as "no saved state" rather than as an error.
	cases := map[string]string{
		"not json":         "{{{{",
		"not an object":    `[1,2,3]`,
		"foreign version":  `{"version":99,"view":"next"}`,
		"missing version":  `{"view":"next"}`,
		"empty":            ``,
		"wrong value type": `{"version":1,"collapsed":"nope"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			env := sessionEnv(t)
			path := SessionPath(env)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := LoadSession(env); got.View != "" && got.View != ViewNext {
				t.Fatalf("broken file produced %+v", got)
			}
		})
	}
}

func TestSessionSaveNeverWritesADuplicateVersionKey(t *testing.T) {
	env := sessionEnv(t)
	if err := SaveSession(SessionState{View: ViewNext}, env); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SessionPath(env))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(raw), `"version"`); count != 1 {
		t.Fatalf("wrote %d version keys:\n%s", count, raw)
	}
}

func TestNormalizeContextFilters(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"adds the sigil", []string{"home"}, []string{"@home"}},
		{"keeps an existing sigil", []string{"@home"}, []string{"@home"}},
		{"trims", []string{"  @home  "}, []string{"@home"}},
		{"drops empties", []string{"", "   "}, []string{}},
		{"drops a bare sigil", []string{"@"}, []string{}},
		{"deduplicates", []string{"@home", "home"}, []string{"@home"}},
		{"sorts", []string{"@work", "@home"}, []string{"@home", "@work"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := NormalizeContextFilters(testCase.input); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestModelRestoresASavedView(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewQuadrants)
	harness.model.Save()

	restored := New(Options{
		App: harness.model.app, Paths: harness.model.paths, Env: harness.env,
		Session: LoadSession(harness.env),
	})
	if restored.view != ViewQuadrants {
		t.Fatalf("restored view %q", restored.view)
	}
}

func TestModelRedirectsTheRetiredApprovalsView(t *testing.T) {
	// "approvals" was its own tab before it merged into Inbox. A session saved
	// by that build must land on the tab that absorbed it, not on a view that
	// no longer exists.
	if got := restoreView("approvals"); got != ViewInbox {
		t.Fatalf("restored %q", got)
	}
}

func TestModelIgnoresAnUnknownSavedView(t *testing.T) {
	if got := restoreView("telescope"); got != ViewAgenda {
		t.Fatalf("restored %q", got)
	}
}

func TestSavedStateDropsCollapsedIDsTheStoreNoLongerHolds(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.collapsed["deadbeef"] = true
	harness.model.collapsed[fixWork] = true
	harness.model.collapsed[fixFlight] = true

	state := harness.model.SessionState()
	for _, id := range state.Collapsed {
		if id == "deadbeef" {
			t.Fatalf("a collapsed id for a record the store never had survived: %v", state.Collapsed)
		}
	}
	// Sections fold too, so a live section id (fixWork) survives beside a live
	// task id (fixFlight); only the id the store never held is dropped.
	if len(state.Collapsed) != 2 || state.Collapsed[0] != fixWork || state.Collapsed[1] != fixFlight {
		t.Fatalf("collapsed set %v; live task AND section ids belong there", state.Collapsed)
	}
}

func TestSavedStateDropsContextFiltersNoLiveTaskCarries(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.contextFilters = []string{"@home", "@atlantis"}
	state := harness.model.SessionState()
	if !reflect.DeepEqual(state.ContextFilters, []string{"@home"}) {
		t.Fatalf("context filters %v", state.ContextFilters)
	}
}
