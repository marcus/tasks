package clipboard

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Ruby has no test_clipboard.rb — the module is exercised only through the app.
// These tests use the same injection point Ruby offers (`cmd:`) so the copy
// path is proved without touching the real clipboard.

func TestCopyPipesTheTextToTheInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "captured")
	if !Copy("task id 7de3\nsecond line", []string{"sh", "-c", "cat > " + out}) {
		t.Fatal("Copy reported failure")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "task id 7de3\nsecond line" {
		t.Fatalf("captured %q", got)
	}
}

func TestCopyReportsFailureWithoutFailing(t *testing.T) {
	if Copy("x", nil) && Command() == nil {
		t.Fatal("Copy must be false when no clipboard command exists")
	}
	if Copy("x", []string{"definitely-not-a-real-clipboard-binary"}) {
		t.Fatal("a missing binary must report failure, not succeed")
	}
	if runtime.GOOS != "windows" && Copy("x", []string{"sh", "-c", "exit 3"}) {
		t.Fatal("a nonzero exit must report failure")
	}
	if Copy("x", []string{}) {
		t.Fatal("an empty command must report failure")
	}
}

func TestCommandsListPlatformToolsInPreferenceOrder(t *testing.T) {
	want := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	if len(Commands) != len(want) {
		t.Fatalf("%d commands, want %d", len(Commands), len(want))
	}
	for i := range want {
		if len(Commands[i]) != len(want[i]) {
			t.Fatalf("command %d = %v, want %v", i, Commands[i], want[i])
		}
		for j := range want[i] {
			if Commands[i][j] != want[i][j] {
				t.Fatalf("command %d = %v, want %v", i, Commands[i], want[i])
			}
		}
	}
}

func TestCommandDetectionIsStable(t *testing.T) {
	// The lookup is memoized, as in Ruby; two calls must agree.
	first := Command()
	second := Command()
	if len(first) != len(second) {
		t.Fatalf("detection changed between calls: %v vs %v", first, second)
	}
	if runtime.GOOS == "darwin" && (len(first) == 0 || first[0] != "pbcopy") {
		t.Fatalf("macOS should detect pbcopy, got %v", first)
	}
}
