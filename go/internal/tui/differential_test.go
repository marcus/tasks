package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/llm"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/term/agent"
	"tasks-go/internal/tui/term/ansi"
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
	Hint bool     `json:"hint"`
}

type diffStep struct {
	Key            string   `json:"key"`
	Mode           string   `json:"mode"`
	Selected       string   `json:"selected"`
	Flash          string   `json:"flash"`
	Overlay        string   `json:"overlay"`
	Panel          string   `json:"panel"`
	PanelIdentity  string   `json:"panel_identity"`
	PanelScroll    int      `json:"panel_scroll"`
	Quit           bool     `json:"quit"`
	Queue          []string `json:"queue"`
	ResponseScroll int      `json:"response_scroll"`
	// Response is the agent response pane's text, once a request has finished.
	Response string `json:"response,omitempty"`
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

	model, clock := diffModel(t, dir)
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
		// A synthetic token, not a key: it drains the agent queue once, the way
		// the tick does. It is the only way to compare a FINISHED request
		// across the two implementations without either depending on
		// wall-clock timing.
		if key == "<<tick>>" {
			model.PumpQueue()
		} else if key == "<<advance-midnight>>" {
			// Advance only the injected scenario clock. No update is sent and no
			// file changes; the open archive preview is exactly the one the user
			// saw on the previous day.
			*clock = clock.Add(24 * time.Hour)
		} else if key == "<<paste-multiline>>" {
			model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q\nnext\tvalue"), Paste: true})
		} else if key == "<<paste-date>>" {
			model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2026-08-09"), Paste: true})
		} else if key == "<<refresh>>" {
			model.Refresh()
		} else if key == "<<set-panel-scroll-3>>" {
			if model.Panel() != nil {
				model.Panel().Scroll = 3
			}
		} else if key == "<<edit-at-small>>" {
			model.Update(tea.WindowSizeMsg{Width: 46, Height: 7})
			model.Update(keyMessage("e"))
		} else if key == "<<resize-small>>" {
			model.Update(tea.WindowSizeMsg{Width: 46, Height: 7})
		} else if key == "<<resize-wide>>" {
			model.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
		} else if key == "<<external-delete-selected>>" {
			externalDeleteSelected(t, filepath.Join(dir, "tasks.jsonl"), script.Select)
		} else if key == "<<external-defer-selected>>" {
			externalReplace(t, filepath.Join(dir, "tasks.jsonl"),
				`"tags":["@computer","important","urgent"]`,
				`"tags":["@computer","important","urgent","defer"]`)
		} else if key == "<<external-done-selected>>" {
			externalReplace(t, filepath.Join(dir, "tasks.jsonl"),
				`"state":"NEXT","priority":"A","title":"Book flight`,
				`"state":"DONE","priority":"A","title":"Book flight`)
		} else if key == "<<external-proposal-change>>" {
			externalRetitle(t, filepath.Join(dir, "tasks.jsonl"), "Book flight in Concur", "Changed outside")
		} else if key == "<<mouse-keyhint-click>>" {
			layout := model.Layout()
			_, end := layout.FooterRows()
			model.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 4, Y: end - 1})
		} else if key == "<<seed-response-chrome>>" {
			model.resp = make([]string, 30)
			for index := range model.resp {
				model.resp[index] = fmt.Sprintf("response %d", index)
			}
			model.respOpen = true
			model.respScroll = 0
			model.Flash("visible flash")
			model.filter = "needle"
			model.contextFilters = []string{"@home"}
			model.RefreshRows()
		} else if key == "<<seed-response-flash-only>>" {
			model.resp = make([]string, 30)
			for index := range model.resp {
				model.resp[index] = fmt.Sprintf("response %d", index)
			}
			model.respOpen = true
			model.respScroll = 0
			model.Flash("visible flash")
		} else if key == "<<mouse-wheel-footer-chrome>>" {
			model.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
			layout := model.Layout()
			start, _ := layout.FooterRows()
			for index, line := range layout.Footer {
				if strings.Contains(line, "visible flash") || strings.Contains(line, "needle") ||
					strings.Contains(line, "@home") {
					model.Update(tea.MouseMsg{Type: tea.MouseWheelDown, X: 4, Y: start + index})
				}
			}
		} else if key == "<<mouse-wheel-flash-chrome>>" {
			model.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
			layout := model.Layout()
			start, _ := layout.FooterRows()
			for index, line := range layout.Footer {
				if strings.Contains(line, "visible flash") {
					model.Update(tea.MouseMsg{Type: tea.MouseWheelDown, X: 4, Y: start + index})
					break
				}
			}
		} else {
			model.Update(keyMessage(key))
		}
		steps = append(steps, diffObserve(model, key, script.Hint))
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
func diffObserve(model *Model, key string, hint ...bool) diffStep {
	step := diffStep{
		Key: key, Mode: string(model.Mode()), Flash: model.FlashMessage(), Quit: model.quitting,
		Queue: []string{},
	}
	step.ResponseScroll = model.respScroll
	if model.queue != nil {
		for _, request := range model.queue.Requests() {
			step.Queue = append(step.Queue, string(request.Status))
		}
	}
	if item := model.CurrentItem(); item != nil {
		step.Selected = item.ID
	} else if project := model.CurrentProject(); project != nil {
		step.Selected = project.ID
	}
	if !model.quitting && model.Panel() != nil {
		step.Panel = model.Panel().Kind
		step.PanelIdentity = model.Panel().Identity
		step.PanelScroll = model.Panel().Scroll
	}
	if !model.quitting && model.TaskEditor() != nil {
		step.Panel = "task_edit"
	}
	if model.Mode() == ModeList && model.respOpen {
		lines := []string{}
		for _, line := range model.resp {
			lines = append(lines, ansi.Strip(line))
		}
		step.Response = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	switch model.Mode() {
	case ModeModal, ModeModalFilter:
		if model.Modal() != nil {
			step.Overlay = string(model.Modal().Kind())
		}
	case ModeForm:
		if model.Form() != nil {
			step.Overlay = string(model.Form().Kind) + ":" + model.Form().Text()
			if len(hint) > 0 && hint[0] {
				step.Overlay += " |" + model.Form().Hint(80)
			}
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
	case ModePrompt:
		step.Overlay = "prompt:" + model.PromptText()
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

func externalDeleteSelected(t *testing.T, path, id string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	needle := `"id":"` + id + `"`
	kept := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, needle) {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func externalRetitle(t *testing.T, path, from, to string) {
	externalReplace(t, path, `"title":"`+from+`"`, `"title":"`+to+`"`)
}

func externalReplace(t *testing.T, path, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), from, to, 1)
	if updated == string(raw) {
		t.Fatalf("external mutation target not found: %q", from)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffModel builds the root model over a sandbox directory with every seam
// pinned, so two runs over the same bytes produce the same trace.
func diffModel(t *testing.T, dir string) (*Model, *time.Time) {
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
	// The SAME entry list Ruby derives from an empty config, built from the
	// shared registry rather than hand-listed, plus a queue over a scripted
	// adapter. No provider is ever invoked.
	entries := []AgentEntry{}
	for _, entry := range llm.Entries(llm.Config{}) {
		entries = append(entries, AgentEntry{
			ProviderName: entry.Provider, ModelName: entry.Model, Label: entry.UILabel(),
		})
	}
	queue, err := agent.NewQueue(agent.Options{
		Factory:      func(agent.Entry) (agent.Adapter, error) { return &diffAdapter{}, nil },
		Availability: func(agent.Entry) bool { return true },
		Clock:        func() float64 { return float64(now.UnixNano()) / float64(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}

	model := New(Options{
		App:     app,
		Entries: entries,
		Queue:   queue,
		Paths: config.Paths{
			Org: org, Archive: archive,
			UrgentDays: config.DefaultUrgentDays, MaxDepth: config.DefaultMaxDepth,
			Timezone: "Etc/UTC", TimeFormat: 24, Mouse: true,
		},
		Env: determinism.Env{"XDG_STATE_HOME": filepath.Join(dir, "state"), "HOME": dir},
		Now: func() time.Time { return now },
	})
	model.width, model.height = 100, 30
	model.Refresh()
	return model, &now
}

// diffAdapter is the differential's scripted agent. It answers every prompt
// with one fixed transcript, matching the Ruby driver's fake — what the
// differential compares is the TUI's behaviour around a request, never a
// model's words, and no provider is ever invoked.
type diffAdapter struct{ pumped bool }

func (a *diffAdapter) Available() bool            { return true }
func (a *diffAdapter) Start(string, string) error { return nil }
func (a *diffAdapter) Pump() (bool, error)        { a.pumped = true; return true, nil }
func (a *diffAdapter) Cancel() error              { return nil }
func (a *diffAdapter) Output() string {
	if a.pumped {
		return "fake agent transcript"
	}
	return "partial agent transcript"
}
func (a *diffAdapter) Success() bool           { return true }
func (a *diffAdapter) ExitStatus() (int, bool) { return 0, true }
func (a *diffAdapter) ProcessStatus() agent.ProcessStatus {
	return agent.ProcessStatus{Present: true, Exited: true}
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
