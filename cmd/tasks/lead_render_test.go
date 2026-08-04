package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// leadFixture writes a one-task store and returns queries over it at `now`.
func leadFixture(t *testing.T, records string, now string) (*taskquery.Queries, store.Item) {
	t.Helper()
	root := t.TempDir()
	live := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(live, []byte(records), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := store.New(live, filepath.Join(root, "archive.jsonl")).ReadSnapshot(true)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	instant, err := time.Parse(time.RFC3339, now)
	if err != nil {
		t.Fatalf("parse instant: %v", err)
	}
	context, err := temporal.NewContext(instant, "America/Denver", 12)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	queries := taskquery.New(snapshot, context)
	item, found := queries.FindLive("dddd0002")
	if !found {
		t.Fatal("fixture task not found")
	}
	return queries, item
}

// An unavailable review shows the INTENT beside the derived date, and it shows
// the stored span rather than its long form: the suffix sits at the end of an
// already long row, and Ruby prints "· 3w before 11/1" here.
func TestLeadSpanSuffixPrintsTheStoredSpan(t *testing.T) {
	const records = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"TODO","title":"Renew the passport","deadline":"2026-11-01","lead":"3w"}
`
	queries, item := leadFixture(t, records, "2026-10-01T12:00:00Z")
	if got, want := leadSpanSuffix(queries, item), " · 3w before 11/1"; got != want {
		t.Fatalf("leadSpanSuffix = %q, want %q", got, want)
	}

	// Once the window is open the lead no longer explains the row's gate, so
	// the suffix has nothing to add.
	open, openItem := leadFixture(t, records, "2026-10-20T12:00:00Z")
	if got := leadSpanSuffix(open, openItem); got != "" {
		t.Fatalf("leadSpanSuffix after the window opens = %q, want nothing", got)
	}
}

// A lead Check would report gates nothing, and must not print an explanation
// for a window it did not derive.
func TestLeadSpanSuffixSaysNothingForAnUncanonicalSpan(t *testing.T) {
	const records = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"TODO","title":"Renew the passport","deadline":"2026-11-01","lead":"0w"}
`
	queries, item := leadFixture(t, records, "2026-10-01T12:00:00Z")
	if got := leadSpanSuffix(queries, item); got != "" {
		t.Fatalf("leadSpanSuffix = %q, want nothing", got)
	}
}
