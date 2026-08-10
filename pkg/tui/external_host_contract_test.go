package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostFixture is the external-host shape of embeddedFixture: a host that
// chooses the exact store bytes (or leaves the store absent), adds its own
// Tasks config lines, and then builds the model through the public API only.
type hostFixture struct {
	model *Model
	dir   string
	state string
}

func newHostFixture(t *testing.T, store func(dir string), configText string, options EmbeddedOptions) hostFixture {
	t.Helper()
	dir := t.TempDir()
	if store != nil {
		store(dir)
	}
	configDir := filepath.Join(dir, "config", "tasks")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if configText != "" {
		if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(dir, "state")
	if options.SessionNamespace == "" {
		options.SessionNamespace = "contract-host"
	}
	options.Environment = map[string]string{
		"TASKS_DIR": dir, "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"HOME": dir, "PATH": filepath.Join(dir, "no-provider-bin"),
	}
	model, err := NewEmbedded(options)
	if err != nil {
		t.Fatal(err)
	}
	return hostFixture{model: model, dir: dir, state: state}
}

func validStore(t *testing.T) func(string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "valid", "small-gtd", "store", "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// -- G1: discarding a speculative model must not overwrite the live session ---

// A host builds models speculatively — a project switch, an epoch race — and
// every model in one namespace shares one session file. Closing the model it
// throws away used to write that model's stale state over the live model's.
func TestHostDiscardsSpeculativeModelWithoutOverwritingTheLiveSession(t *testing.T) {
	store := validStore(t)
	live := newHostFixture(t, store, "", EmbeddedOptions{
		SessionNamespace: "race-host", InitialView: ViewQuadrants,
	})
	live.model.Init()
	if err := live.model.Close(); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(live.state, "tasks", "hosts", "race-host", "tui.json")
	saved, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "quadrants") {
		t.Fatalf("live session did not record its view:\n%s", saved)
	}

	// The stale in-flight build lands afterwards. It shares the namespace and
	// the state directory, so Close would clobber; Discard must not.
	stale := newHostFixture(t, store, "", EmbeddedOptions{
		SessionNamespace: "race-host", InitialView: ViewAgenda,
	})
	stale.model.Init()
	// Point the stale model at the SAME state tree the live model wrote.
	stalePath := filepath.Join(stale.state, "tasks", "hosts", "race-host", "tui.json")
	if err := os.MkdirAll(filepath.Dir(stalePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stalePath, saved, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stale.model.Discard(); err != nil {
		t.Fatalf("discard: %v", err)
	}
	after, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(saved) {
		t.Fatalf("discard persisted stale session state:\nbefore %s\nafter  %s", saved, after)
	}
	if strings.Contains(string(after), "agenda") {
		t.Fatalf("discarded model overwrote the live view:\n%s", after)
	}
}

// Close and Discard share one guard: the first wins, both are idempotent, and
// a Close after a Discard must not resurrect the save.
func TestHostCloseAndDiscardAreMutuallyExclusiveAndIdempotent(t *testing.T) {
	store := validStore(t)

	discarded := newHostFixture(t, store, "", EmbeddedOptions{SessionNamespace: "guard-a"})
	discarded.model.Init()
	if err := discarded.model.Discard(); err != nil {
		t.Fatal(err)
	}
	if err := discarded.model.Discard(); err != nil {
		t.Fatal(err)
	}
	if err := discarded.model.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(discarded.state, "tasks", "hosts", "guard-a", "tui.json")); !os.IsNotExist(err) {
		t.Fatalf("Close after Discard still wrote the session: %v", err)
	}

	closed := newHostFixture(t, store, "", EmbeddedOptions{SessionNamespace: "guard-b"})
	closed.model.Init()
	if err := closed.model.Close(); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(closed.state, "tasks", "hosts", "guard-b", "tui.json")
	saved, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sessionPath); err != nil {
		t.Fatal(err)
	}
	if err := closed.model.Discard(); err != nil {
		t.Fatal(err)
	}
	if err := closed.model.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("a second Close wrote the session again (first wrote %s)", saved)
	}
}

// -- G2: a broken store must never read as an empty one ----------------------

func TestHostDistinguishesBrokenStoreFromEmptyStore(t *testing.T) {
	// A healthy store holding only its meta record: genuinely empty, no error.
	meta := func(dir string) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "valid", "small-gtd", "store", "tasks.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		first := strings.SplitN(string(raw), "\n", 2)[0]
		if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte(first+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	healthy := []struct {
		name  string
		store func(string)
	}{
		{"empty but valid", meta},
		// A brand-new install has not written the store yet. That is a first
		// run, not a fault, so it must NOT report an error.
		{"first run, no file yet", nil},
		{"zero-length file", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, testCase := range healthy {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHostFixture(t, testCase.store, "", EmbeddedOptions{})
			defer fixture.model.Discard()
			fixture.model.Init()
			if err := fixture.model.LoadError(); err != nil {
				t.Fatalf("healthy store reported %v", err)
			}
		})
	}

	broken := []struct {
		name  string
		store func(string)
	}{
		{"corrupt", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte("{{{not json\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte("{}\n"), 0o000); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "tasks.jsonl"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, testCase := range broken {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "unreadable" && os.Geteuid() == 0 {
				t.Skip("root reads a mode 0000 file")
			}
			fixture := newHostFixture(t, testCase.store, "", EmbeddedOptions{})
			defer fixture.model.Discard()
			fixture.model.Init()
			if fixture.model.LoadError() == nil {
				t.Fatalf("%s store reported no read error", testCase.name)
			}
			if frame := fixture.model.View(80, 20); !strings.Contains(frame, "cannot read the task store") {
				t.Fatalf("%s store rendered no banner:\n%s", testCase.name, frame)
			}
		})
	}
}

// A tasks directory that does not exist at all is a misconfiguration, not a
// first run: nothing will ever appear there.
func TestHostReportsAMissingTasksDirectory(t *testing.T) {
	fixture := newHostFixture(t, nil, "", EmbeddedOptions{})
	defer fixture.model.Discard()
	missing := filepath.Join(fixture.dir, "gone")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	model, err := NewEmbedded(EmbeddedOptions{
		SessionNamespace: "missing-dir",
		Environment: map[string]string{
			"TASKS_DIR": filepath.Join(missing, "nope"), "XDG_STATE_HOME": fixture.state,
			"XDG_CONFIG_HOME": filepath.Join(fixture.dir, "config"), "HOME": fixture.dir,
			"PATH": filepath.Join(fixture.dir, "no-provider-bin"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Discard()
	model.Init()
	if model.LoadError() == nil {
		t.Fatal("a missing tasks directory reported no read error")
	}
}

// -- G4: host colors overlay the user's, they do not replace them ------------

func TestHostThemeColorsOverlayTheUsersOwnColors(t *testing.T) {
	store := validStore(t)
	userConfig := "color.muted = #ff00ff\ncolor.accent = #ffaa00\n"

	overlaid := newHostFixture(t, store, userConfig, EmbeddedOptions{
		SessionNamespace: "overlay-host",
		Theme:            ThemeOptions{Colors: map[string]string{"accent": "#00ff00"}},
	})
	defer overlaid.model.Discard()
	overlaid.model.Init()
	frame := overlaid.model.View(90, 24)
	if !strings.Contains(frame, "255;0;255") {
		t.Fatalf("host colors destroyed the user's own muted color:\n%q", frame)
	}
	if strings.Contains(frame, "255;170;0") {
		t.Fatalf("host override of accent did not win:\n%q", frame)
	}

	// A host that must guarantee an exact palette opts in explicitly.
	replaced := newHostFixture(t, store, userConfig, EmbeddedOptions{
		SessionNamespace: "replace-host",
		Theme: ThemeOptions{
			Colors: map[string]string{"accent": "#00ff00"}, ReplaceColors: true,
		},
	})
	defer replaced.model.Discard()
	replaced.model.Init()
	if frame := replaced.model.View(90, 24); strings.Contains(frame, "255;0;255") {
		t.Fatalf("ReplaceColors kept the user's colors:\n%q", frame)
	}
}

func TestOverlayStringsLayersHostSlotsOverConfiguredSlots(t *testing.T) {
	base := map[string]string{"accent": "magenta", "muted": "grey"}
	merged := overlayStrings(base, map[string]string{"accent": "#00ff00", "error": "red"})
	want := map[string]string{"accent": "#00ff00", "muted": "grey", "error": "red"}
	for key, value := range want {
		if merged[key] != value {
			t.Fatalf("merged[%q] = %q, want %q", key, merged[key], value)
		}
	}
	if len(merged) != len(want) {
		t.Fatalf("merged = %#v, want %#v", merged, want)
	}
	if base["accent"] != "magenta" || len(base) != 2 {
		t.Fatalf("overlay mutated the resolved configuration: %#v", base)
	}
}
