package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/tui"
)

// One task, one user, three surfaces.
//
// Capture it on the CLI, delegate all three parts of it in the TUI's `D` modal,
// then read it back with `show` and with the queue an agent actually polls. The
// briefing is the part worth proving end to end: a note that reached the marker
// but not `list --agent-ready --json` would leave the receiving agent fetching
// the task again to learn what it was asked to do.
func TestJourneyCaptureDelegateInTheModalThenReadOnTheCLI(t *testing.T) {
	dir := t.TempDir()
	if result := runCLI(t, dir, "capture", "Ship the thing"); result.status != 0 {
		t.Fatalf("capture exited %d: %s", result.status, result.stderr)
	}

	model := journeyModel(t, dir)
	// A captured task lands in the inbox, which is view 6.
	journeyKeys(model, "6")
	if !journeySelect(t, model, "Ship the thing") {
		t.Fatalf("the captured task is not on screen: rows=%d err=%v", len(model.Rows()), model.ReadError())
	}
	journeyKeys(model, "D")
	if model.Mode() != tui.ModeFieldModal {
		t.Fatalf("D produced mode %s", model.Mode())
	}
	modal := model.FieldModal()
	modal.SetValue("assignee", "agent")
	modal.SetValue("mode", "implement")
	modal.SetValue("note", "Keep the diff small.")
	// Ctrl-s, the note field's submit — Return is text inside a briefing.
	journeyKeys(model, "\x13")
	if model.Mode() == tui.ModeFieldModal {
		t.Fatalf("the modal refused the delegation: %q", modal.Error())
	}

	shown := runCLI(t, dir, "show", "Ship the thing")
	if !strings.Contains(shown.stdout, "implement") ||
		!strings.Contains(shown.stdout, "Keep the diff small.") {
		t.Fatalf("show does not carry the delegation:\n%s", shown.stdout)
	}

	queue := runCLI(t, dir, "list", "--agent-ready", "--json")
	if queue.status != 0 {
		t.Fatalf("list exited %d: %s", queue.status, queue.stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(queue.stdout), &rows); err != nil {
		t.Fatalf("not JSON (%v): %s", err, queue.stdout)
	}
	if len(rows) != 1 {
		t.Fatalf("the claimable queue is %v", rows)
	}
	marker, _ := rows[0]["delegation"].(map[string]any)
	if marker["mode"] != "implement" || marker["note"] != "Keep the diff small." {
		t.Fatalf("the queue row the agent reads is %v", marker)
	}
}

// journeyModel builds the shipping TUI over the very files the CLI just wrote,
// through the same constructor the executable uses.
func journeyModel(t *testing.T, dir string) *tui.Model {
	t.Helper()
	journeyEnv := determinism.Env{
		"TASKS_FILE":      filepath.Join(dir, "tasks.jsonl"),
		"TASKS_ARCHIVE":   filepath.Join(dir, "archive.jsonl"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "cfg"),
		"TASKS_PIN_NOW":   "2026-07-20T12:00:00Z",
		"TZ":              "UTC",
	}
	paths := config.Resolve("", journeyEnv, nil)
	model, err := tui.NewRuntime(tui.RuntimeOptions{Paths: paths, Env: journeyEnv})
	if err != nil {
		t.Fatalf("building the TUI: %v", err)
	}
	model.Init()
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return model
}

// journeySelect walks the list with `j`, the way a user does: the selection is
// the model's own and there is no test-only setter to short-circuit it.
func journeySelect(t *testing.T, model *tui.Model, title string) bool {
	t.Helper()
	for step := 0; step < len(model.Rows())+1; step++ {
		if item := model.CurrentItem(); item != nil && strings.Contains(item.Title, title) {
			return true
		}
		journeyKeys(model, "j")
	}
	return false
}

func journeyKeys(model *tui.Model, sequences ...string) {
	for _, sequence := range sequences {
		if sequence == "\x13" {
			model.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			continue
		}
		runes := []rune(sequence)
		model.Update(tea.KeyPressMsg{Code: runes[0], Text: sequence})
	}
}
