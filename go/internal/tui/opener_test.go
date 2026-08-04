package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tasks-go/internal/determinism"
)

// The launcher these tests run is a temp shell script that writes its arguments
// to a file. No browser is ever opened, and the override that makes that
// possible is the same one a user sets to choose their own browser.

// fakeLauncher writes a script that records its argv, and returns its path and
// the file it records into.
func fakeLauncher(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "opened.txt")
	script := filepath.Join(dir, "launcher")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + record + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, record
}

func TestOpenerHonoursTheOverrideAndPassesTheURL(t *testing.T) {
	script, record := fakeLauncher(t)
	opener := SystemOpener{Env: determinism.Env{"TASKS_OPENER": script}}
	if !opener.Open("https://example.test/issue/1") {
		t.Fatal("the launcher could not be spawned")
	}
	waitFor(t, record)
	if got := readFile(t, record); !strings.Contains(got, "https://example.test/issue/1") {
		t.Errorf("the launcher received %q", got)
	}
}

// The override is SHELL-SPLIT, so `TASKS_OPENER="open -a Safari"` works.
func TestOpenerShellSplitsTheOverride(t *testing.T) {
	script, record := fakeLauncher(t)
	opener := SystemOpener{Env: determinism.Env{"TASKS_OPENER": script + " -a Safari"}}
	if got := opener.Command(); len(got) != 3 || got[1] != "-a" || got[2] != "Safari" {
		t.Fatalf("the override split into %v", got)
	}
	if !opener.Open("https://example.test/") {
		t.Fatal("the launcher could not be spawned")
	}
	waitFor(t, record)
	got := readFile(t, record)
	for _, want := range []string{"-a", "Safari", "https://example.test/"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launcher did not receive %q: %q", want, got)
		}
	}
}

func TestOpenerShellSplitHandlesQuotesAndEscapes(t *testing.T) {
	cases := map[string][]string{
		`open`:                    {"open"},
		`open -a "Google Chrome"`: {"open", "-a", "Google Chrome"},
		`open -a 'My Browser'`:    {"open", "-a", "My Browser"},
		`my\ browser --new`:       {"my browser", "--new"},
		`  spaced   out  `:        {"spaced", "out"},
		`""`:                      {""},
	}
	for override, want := range cases {
		got := shellSplit(override)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q split into %v, want %v", override, got, want)
		}
	}
}

// A bad override must not take the interface down on a keystroke.
func TestOpenerReturnsFalseWhenTheLauncherCannotBeSpawned(t *testing.T) {
	opener := SystemOpener{Env: determinism.Env{
		"TASKS_OPENER": filepath.Join(t.TempDir(), "definitely-not-here"),
	}}
	if opener.Open("https://example.test/") {
		t.Error("a missing launcher reported success")
	}
}

func TestOpenerFallsBackToThePlatformLauncher(t *testing.T) {
	opener := SystemOpener{Env: determinism.Env{}}
	want := "xdg-open"
	if runtime.GOOS == "darwin" {
		want = "open"
	}
	if got := opener.Command(); len(got) != 1 || got[0] != want {
		t.Errorf("the platform default is %v, want %q", got, want)
	}
}

// The wired-up path: the model's `o` key reaches the real opener.
func TestTheModelOpensALinkThroughTheSystemOpener(t *testing.T) {
	script, record := fakeLauncher(t)
	harness := newModelHarness(t, harnessOptions{
		live:   linkedFixture,
		opener: SystemOpener{Env: determinism.Env{"TASKS_OPENER": script}},
	})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("o")
	waitFor(t, record)
	if got := readFile(t, record); !strings.Contains(got, "example.com") {
		t.Errorf("the launcher received %q", got)
	}
	if !strings.Contains(harness.model.FlashMessage(), "opened") {
		t.Errorf("opening said %q", harness.model.FlashMessage())
	}
}

// waitFor polls for the detached launcher's output. The open is deliberately
// asynchronous, so a test cannot assert on it synchronously.
func waitFor(t *testing.T, path string) {
	t.Helper()
	for range 200 {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the launcher never wrote %s", path)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
