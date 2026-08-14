package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/temporal"
)

func TestNewRuntimePinsTheWholeTUIClockFromTheEnvironment(t *testing.T) {
	root := t.TempDir()
	model, err := NewRuntime(RuntimeOptions{
		Paths: config.Paths{
			Org:        filepath.Join(root, "tasks.jsonl"),
			Archive:    filepath.Join(root, "archive.jsonl"),
			MaxDepth:   config.DefaultMaxDepth,
			Timezone:   "America/Los_Angeles",
			TimeFormat: config.DefaultTimeFormat,
		},
		Env: determinism.Env{
			"HOME":                   root,
			"XDG_STATE_HOME":         filepath.Join(root, "state"),
			determinism.NameNow:      "2026-08-13T12:00:00-07:00",
			determinism.NameHostname: "demo.tasks.local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if queue := model.Queue(); queue != nil {
		t.Cleanup(func() { queue.Shutdown() })
	}

	wantInstant := time.Date(2026, time.August, 13, 19, 0, 0, 0, time.UTC)
	if got := model.now(); !got.Equal(wantInstant) {
		t.Fatalf("model clock = %s, want %s", got, wantInstant)
	}
	wantDate, _ := temporal.NewDate(2026, time.August, 13)
	if got, want := model.currentDate(), wantDate; got != want {
		t.Fatalf("current date = %v, want %v", got, want)
	}
	if got := model.temporalContext().Now; !got.Equal(wantInstant) {
		t.Fatalf("operation clock = %s, want %s", got, wantInstant)
	}
}
