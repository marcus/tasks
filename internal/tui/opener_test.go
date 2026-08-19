package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/determinism"
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
	if got, ok := opener.Command(); !ok || len(got) != 3 || got[1] != "-a" || got[2] != "Safari" {
		t.Fatalf("the override split into %v (ok=%v)", got, ok)
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

type shellSplitCase struct {
	name  string
	input string
	want  []string
	ok    bool
}

// This corpus is intentionally byte-oriented: it includes every ASCII
// whitespace byte and the places where a backslash has different meaning
// inside and outside double quotes. Ruby's Shellwords.split is the oracle.
var shellSplitCases = []shellSplitCase{
	{name: "empty input", input: "", want: []string{}, ok: true},
	{name: "plain words", input: `open -a Safari`, want: []string{"open", "-a", "Safari"}, ok: true},
	{name: "space tab newline separators", input: " open\t-a\nSafari ", want: []string{"open", "-a", "Safari"}, ok: true},
	{name: "carriage return form feed vertical tab separators", input: "\ropen\r-a\fBrowser\v", want: []string{"open", "-a", "Browser"}, ok: true},
	{name: "only ASCII whitespace", input: " \t\n\r\f\v", want: []string{}, ok: true},
	{name: "single quoted word", input: `open -a 'My Browser'`, want: []string{"open", "-a", "My Browser"}, ok: true},
	{name: "single quotes preserve backslash", input: `'a\q'`, want: []string{`a\q`}, ok: true},
	{name: "double quoted word", input: `open -a "Google Chrome"`, want: []string{"open", "-a", "Google Chrome"}, ok: true},
	{name: "empty double quoted word", input: `""`, want: []string{""}, ok: true},
	{name: "empty single quoted word", input: `''`, want: []string{""}, ok: true},
	{name: "empty quotes concatenate", input: `a""''b`, want: []string{"ab"}, ok: true},
	{name: "mixed quote adjacency", input: `pre"two words"' and more'post`, want: []string{"pretwo words and morepost"}, ok: true},
	{name: "unquoted backslash escapes ordinary byte", input: `a\q`, want: []string{"aq"}, ok: true},
	{name: "unquoted backslash escapes space", input: `a\ b`, want: []string{"a b"}, ok: true},
	{name: "unquoted backslash escapes tab", input: "a\\\tb", want: []string{"a\tb"}, ok: true},
	{name: "unquoted backslash escapes carriage return", input: "a\\\rb", want: []string{"a\rb"}, ok: true},
	{name: "unquoted backslash preserves newline pair", input: "a\\\nb", want: []string{"a\\\nb"}, ok: true},
	{name: "unquoted backslash escapes quote", input: `a\"b`, want: []string{`a"b`}, ok: true},
	{name: "unquoted backslash escapes backslash", input: `a\\b`, want: []string{`a\b`}, ok: true},
	{name: "dangling unquoted backslash", input: `open \`, want: []string{"open", `\`}, ok: true},
	{name: "lone backslash", input: `\`, want: []string{`\`}, ok: true},
	{name: "double quotes preserve backslash before q", input: `"a\q"`, want: []string{`a\q`}, ok: true},
	{name: "double quotes preserve backslash before space", input: `"a\ b"`, want: []string{`a\ b`}, ok: true},
	{name: "double quotes preserve backslash before n", input: `"a\nb"`, want: []string{`a\nb`}, ok: true},
	{name: "double quotes preserve backslash before single quote", input: `"a\'b"`, want: []string{`a\'b`}, ok: true},
	{name: "double quotes preserve backslash before carriage return", input: "\"a\\\rb\"", want: []string{"a\\\rb"}, ok: true},
	{name: "double quotes escape dollar", input: `"a\$b"`, want: []string{"a$b"}, ok: true},
	{name: "double quotes escape backtick", input: "\"a\\`b\"", want: []string{"a`b"}, ok: true},
	{name: "double quotes escape quote", input: `"a\"b"`, want: []string{`a"b`}, ok: true},
	{name: "double quotes escape backslash", input: `"a\\b"`, want: []string{`a\b`}, ok: true},
	{name: "double quotes escape newline", input: "\"a\\\nb\"", want: []string{"a\nb"}, ok: true},
	{name: "shell metacharacters are ordinary", input: `ruby app.rb | less $HOME *.txt`, want: []string{"ruby", "app.rb", "|", "less", "$HOME", "*.txt"}, ok: true},
	{name: "non ASCII whitespace is ordinary", input: "a\u00a0b", want: []string{"a\u00a0b"}, ok: true},
	{name: "unmatched double quote", input: `open -a "Chrome`, ok: false},
	{name: "unmatched single quote", input: `open 'x`, ok: false},
	{name: "escaped closing double quote remains unmatched", input: `"a\"`, ok: false},
	{name: "NUL outside quotes", input: "a\x00b", ok: false},
	{name: "NUL in single quotes", input: "'a\x00b'", ok: false},
	{name: "NUL in double quotes", input: "\"a\x00b\"", ok: false},
}

func TestOpenerShellSplit(t *testing.T) {
	for _, tc := range shellSplitCases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := shellSplit(tc.input)
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shellSplit(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// A raw non-empty override is AUTHORITATIVE. A malformed or empty-splitting one
// means there is no launcher — never a quiet fall back to the platform default,
// which would open the very browser the user configured away from.
func TestOpenerRefusesAMalformedOverrideWithoutFallingBack(t *testing.T) {
	platform := "xdg-open"
	if runtime.GOOS == "darwin" {
		platform = "open"
	}
	cases := map[string]string{
		"unmatched double quote": `open -a "Chrome`,
		"unmatched single quote": `open 'x`,
		"whitespace only":        "   ",
		"a single tab":           "\t",
		"all other ASCII space":  "\r\f\v",
		"two quotes":             `""`,
		"NUL byte":               "open\x00bad",
	}
	for name, override := range cases {
		opener := SystemOpener{Env: determinism.Env{"TASKS_OPENER": override}}
		launcher, ok := opener.Command()
		if name == "two quotes" {
			// Shellwords yields one EMPTY word: a launcher that cannot spawn.
			// Ruby reaches the same false through the failed spawn.
			if !ok || len(launcher) != 1 || launcher[0] != "" {
				t.Errorf("%s: Command gave %v (ok=%v), want one empty word", name, launcher, ok)
			}
		} else if ok {
			t.Errorf("%s: Command gave %v, want a refusal", name, launcher)
		}
		if ok && len(launcher) > 0 && launcher[0] == platform {
			t.Errorf("%s: fell back to the platform launcher %q", name, platform)
		}
		if opener.Open("https://example.test/") {
			t.Errorf("%s: Open reported success", name)
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
	want := "xdg-open"
	if runtime.GOOS == "darwin" {
		want = "open"
	}
	for name, env := range map[string]determinism.Env{
		"unset": {},
		"empty": {"TASKS_OPENER": ""},
	} {
		t.Run(name, func(t *testing.T) {
			opener := SystemOpener{Env: env}
			if got, ok := opener.Command(); !ok || len(got) != 1 || got[0] != want {
				t.Errorf("the platform default is %v (ok=%v), want %q", got, ok, want)
			}
		})
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
//
// The budget is generous on purpose. What is being waited on is the OS
// scheduling a detached /bin/sh and that script writing a file — work whose
// latency belongs to the machine, not to the code under test. A one-second
// budget failed under ordinary parallel load (`go test -count=8` alongside a
// busy CPU reproduced it at 1.2-1.5s), which reported a scheduling delay as a
// broken opener. A test that fails when the machine is busy is measuring the
// machine.
func waitFor(t *testing.T, path string) {
	t.Helper()
	for range 1000 {
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
