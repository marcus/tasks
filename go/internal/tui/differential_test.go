package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// The Go half of the Ruby-vs-Go interaction differential.
//
// It is a TEST rather than a command because the driver has to construct the
// root model exactly as the shipping entry point does, and because a test
// cannot be shipped to a user by accident. `porting/compare/tui-interaction-diff`
// invokes it with TUI_DIFF_SCRIPT set; without that variable it skips, so it
// costs an ordinary `go test` run nothing.
//
// What it emits is deliberately the SAME shape the Ruby driver emits: one JSON
// object per keystroke carrying the four things a user can actually observe —
// the interaction mode, which task the cursor is on, what the status line says,
// and what the overlay is. The final store bytes are compared by the harness
// itself, from the sandbox directory, so a trace that agrees while the file
// diverges still fails.

type diffScript struct {
	// Name is the scenario, for the failure report.
	Name string `json:"name"`
	// View is the tab to switch to before the keys are sent.
	View string `json:"view"`
	// Select is the stable id to put the cursor on first.
	Select string `json:"select"`
	// Keys are raw byte sequences, exactly as a terminal sends them.
	Keys []string `json:"keys"`
}

type diffStep struct {
	Key      string `json:"key"`
	Mode     string `json:"mode"`
	Selected string `json:"selected"`
	Flash    string `json:"flash"`
	Overlay  string `json:"overlay"`
}

func TestDifferentialDriver(t *testing.T) {
	scriptPath := os.Getenv("TUI_DIFF_SCRIPT")
	if scriptPath == "" {
		t.Skip("set TUI_DIFF_SCRIPT to run the interaction differential driver")
	}
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var script diffScript
	if err := json.Unmarshal(raw, &script); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("TUI_DIFF_DIR")
	if dir == "" {
		t.Fatal("TUI_DIFF_DIR is required")
	}

	model := diffModel(t, dir)
	if script.View != "" {
		model.SwitchView(script.View)
	}
	if script.Select != "" {
		for index, row := range model.Rows() {
			if row.ID() == script.Select {
				model.selectRow(index)
				break
			}
		}
	}

	steps := []diffStep{diffObserve(model, "")}
	for _, key := range script.Keys {
		model.Update(keyMessage(key))
		steps = append(steps, diffObserve(model, key))
	}

	encoded, err := json.MarshalIndent(steps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("TUI_DIFF_OUT")
	if out == "" {
		t.Fatal("TUI_DIFF_OUT is required")
	}
	if err := os.WriteFile(out, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffObserve is the observable state after one key: nothing about internal
// structure, only what a person looking at the screen could report.
func diffObserve(model *Model, key string) diffStep {
	step := diffStep{Key: key, Mode: string(model.Mode()), Flash: model.FlashMessage()}
	if item := model.CurrentItem(); item != nil {
		step.Selected = item.ID
	} else if project := model.CurrentProject(); project != nil {
		step.Selected = project.ID
	}
	switch model.Mode() {
	case ModeModal, ModeModalFilter:
		if model.Modal() != nil {
			step.Overlay = string(model.Modal().Kind())
		}
	case ModeForm:
		if model.Form() != nil {
			step.Overlay = string(model.Form().Kind) + ":" + model.Form().Text()
			if message := model.Form().Error(); message != "" {
				step.Overlay += " !" + message
			}
		}
	case ModePalette:
		if model.ActionPalette() != nil {
			step.Overlay = "actions:" + model.ActionPalette().Picker().Input()
		}
	case ModeContextPalette:
		if model.ContextPalette() != nil {
			step.Overlay = "contexts:" + model.ContextPalette().Picker().Input()
		}
	case ModeTaskEdit:
		if editor := model.TaskEditor(); editor != nil {
			step.Overlay = "edit:" + editor.FocusedKey()
			if editor.Dirty(editor.FocusedKey()) {
				step.Overlay += "*"
			}
			if confirmation := editor.PendingConfirmation(); confirmation != nil {
				step.Overlay += " ?" + confirmation.Message
			}
			if conflict := editor.Conflict(); conflict != nil {
				step.Overlay += " conflict:" + conflict.Field
			}
		}
	}
	return step
}

// diffModel builds the root model over a sandbox directory with every seam
// pinned, so two runs over the same bytes produce the same trace.
func diffModel(t *testing.T, dir string) *Model {
	t.Helper()
	org := filepath.Join(dir, "tasks.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")
	now := diffPinnedNow(t)

	app, err := application.New(application.Options{
		Factory: func() application.Store {
			// Deliberately NO pinned Now: Ruby's Tui::App builds its store
			// without one too, so both sides stamp `updated` from the real
			// wall clock. The harness normalizes that one field and compares
			// every other byte, which keeps the comparison honest rather than
			// pinning one side and not the other.
			return store.NewWriter(org, archive, store.Options{
				JournalDir:    filepath.Join(dir, "journal"),
				Device:        "fixture",
				MaxDepth:      config.DefaultMaxDepth,
				CoalesceScope: "pinned-scope",
			})
		},
		TemporalContext: func() temporal.Context {
			return temporal.Context{Now: now, Timezone: time.UTC, TimezoneID: "Etc/UTC"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := New(Options{
		App: app,
		Paths: config.Paths{
			Org: org, Archive: archive,
			UrgentDays: config.DefaultUrgentDays, MaxDepth: config.DefaultMaxDepth,
			Timezone: "Etc/UTC", TimeFormat: 24,
		},
		Env: determinism.Env{"XDG_STATE_HOME": filepath.Join(dir, "state"), "HOME": dir},
		Now: func() time.Time { return now },
	})
	model.width, model.height = 100, 30
	model.Refresh()
	return model
}

func diffPinnedNow(t *testing.T) time.Time {
	t.Helper()
	pinned := os.Getenv("TASKS_PIN_NOW")
	if pinned == "" {
		return fixedDay
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(pinned))
	if err != nil {
		t.Fatalf("unreadable TASKS_PIN_NOW %q: %v", pinned, err)
	}
	return parsed.UTC()
}
