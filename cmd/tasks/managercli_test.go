package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/api"
	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// The CLI half of the manager endpoints: `history`, guarded `undo`/`redo`, and
// `archive --dry-run` / `--fingerprint`. The parity test at the bottom drives
// the HTTP routes over the same files and demands the same documents.

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	document := map[string]any{}
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatalf("not a JSON object: %q (%v)", text, err)
	}
	return document
}

func TestCLIHistoryPeekAndGuardedUndo(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	fresh := decodeObject(t, runCLI(t, dir, "history", "--json").stdout)
	if fresh["undo"] != nil || fresh["redo"] != nil || fresh["store_revision"] == "" {
		t.Fatalf("fresh history = %v", fresh)
	}
	if human := runCLI(t, dir, "history"); human.stdout != "undo: nothing to undo\nredo: nothing to redo\n" {
		t.Fatalf("human history = %q", human.stdout)
	}

	if result := runCLI(t, dir, "priority", "dddd0004", "B"); result.status != 0 {
		t.Fatalf("priority: %d %q", result.status, result.stderr)
	}
	peek := decodeObject(t, runCLI(t, dir, "history", "--json").stdout)
	label, _ := peek["undo"].(string)
	seen, _ := peek["store_revision"].(string)
	if label == "" {
		t.Fatalf("history after a write = %v", peek)
	}
	if human := runCLI(t, dir, "history"); !strings.HasPrefix(human.stdout, "undo: "+label+"\n") {
		t.Fatalf("human history = %q", human.stdout)
	}

	if result := runCLI(t, dir, "priority", "dddd0005", "C"); result.status != 0 {
		t.Fatalf("priority: %d %q", result.status, result.stderr)
	}
	before := storeBytes(t, dir)
	stale := runCLI(t, dir, "undo", "--store-revision", seen, "--json")
	if stale.status != 1 || storeBytes(t, dir) != before {
		t.Fatalf("stale undo: exit %d, wrote %v", stale.status, storeBytes(t, dir) != before)
	}
	refusal := decodeObject(t, stale.stdout)
	current := decodeObject(t, runCLI(t, dir, "history", "--json").stdout)["store_revision"]
	if refusal["error"] != "conflict" || refusal["reason"] != "stale_store_revision" ||
		refusal["store_revision"] != current || refusal["action"] != "undo" {
		t.Fatalf("stale refusal = %v", refusal)
	}

	undone := runCLI(t, dir, "undo", "--store-revision", current.(string), "--json")
	if undone.status != 0 {
		t.Fatalf("guarded undo: %d %q", undone.status, undone.stderr)
	}
	result := decodeObject(t, undone.stdout)
	after := decodeObject(t, runCLI(t, dir, "history", "--json").stdout)["store_revision"]
	if result["action"] != "undo" || result["label"] == "" || result["store_revision"] != after {
		t.Fatalf("undo result = %v, store now %v", result, after)
	}

	for _, argv := range [][]string{
		{"undo", "--store-revision"}, {"undo", "--store-revision", ""}, {"history", "extra"},
	} {
		if refused := runCLI(t, dir, argv...); refused.status != 1 {
			t.Errorf("%v = exit %d", argv, refused.status)
		}
	}
}

func TestCLIUndoJournalConflictCarriesAReason(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "priority", "dddd0004", "B"); result.status != 0 {
		t.Fatalf("priority: %d %q", result.status, result.stderr)
	}
	edited := strings.Replace(storeBytes(t, dir), "Unfiled capture", "Unfiled capture, edited", 1)
	if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	refused := runCLI(t, dir, "undo", "--json")
	document := decodeObject(t, refused.stdout)
	if refused.status != 1 || document["error"] != "conflict" || document["reason"] != "journal_conflict" ||
		document["label"] == "" {
		t.Fatalf("conflict = %d %v", refused.status, document)
	}
}

func TestCLIArchiveDryRunThenFingerprintedSweep(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	dry := runCLI(t, dir, "archive", "--dry-run", "--json")
	if dry.status != 0 || storeBytes(t, dir) != before {
		t.Fatalf("dry run: exit %d, wrote %v", dry.status, storeBytes(t, dir) != before)
	}
	preview := decodeObject(t, dry.stdout)
	fingerprint, _ := preview["fingerprint"].(string)
	if preview["roots"] != float64(1) || preview["records"] != float64(1) || fingerprint == "" {
		t.Fatalf("preview = %v", preview)
	}
	if human := runCLI(t, dir, "archive", "--dry-run"); human.stdout !=
		"Would archive 1 item and 0 descendants to archive.jsonl.\n" {
		t.Fatalf("human dry run = %q", human.stdout)
	}

	stale := runCLI(t, dir, "archive", "--fingerprint", "0000", "--json")
	refusal := decodeObject(t, stale.stdout)
	if stale.status != 1 || refusal["reason"] != "preview_changed" || refusal["fingerprint"] != fingerprint ||
		storeBytes(t, dir) != before {
		t.Fatalf("stale fingerprint = %d %v", stale.status, refusal)
	}
	if !strings.Contains(stale.stderr, "archive --dry-run") {
		t.Errorf("stale fingerprint message = %q", stale.stderr)
	}

	swept := runCLI(t, dir, "archive", "--fingerprint", fingerprint, "--json")
	if swept.status != 0 || strings.TrimSpace(swept.stdout) != `{"roots":1,"records":1,"moved_ids":["dddd0007"]}` {
		t.Fatalf("fingerprinted sweep = %d %q %q", swept.status, swept.stdout, swept.stderr)
	}
	for _, argv := range [][]string{
		{"archive", "--dry-run", "--fingerprint", fingerprint}, {"archive", "--fingerprint"},
		{"archive", "--fingerprint", ""},
	} {
		if refused := runCLI(t, dir, argv...); refused.status != 1 {
			t.Errorf("%v = exit %d", argv, refused.status)
		}
	}
}

func TestCLIArchiveDryRunShowsBlockedRootsAndExitsZero(t *testing.T) {
	blocked := strings.Replace(mutationFixture,
		`{"type":"task","id":"dddd0009","parent":"dddd0003","state":"TODO","title":"Parent of work"}`,
		`{"type":"task","id":"dddd0009","parent":"dddd0003","state":"DONE","title":"Parent of work","closed":"2026-07-01"}`, 1)
	dir := seedStore(t, blocked)
	dry := runCLI(t, dir, "archive", "--dry-run", "--json")
	preview := decodeObject(t, dry.stdout)
	rows, _ := preview["blocked"].([]any)
	if dry.status != 0 || preview["open_descendants"] != float64(1) || len(rows) != 1 {
		t.Fatalf("blocked dry run = %d %v", dry.status, preview)
	}
	human := runCLI(t, dir, "archive", "--dry-run")
	if human.status != 0 || !strings.Contains(human.stdout, "Blocked: 1 closed root has 1 open descendant") ||
		!strings.Contains(human.stdout, "Child of work") {
		t.Fatalf("human blocked dry run = %d %q", human.status, human.stdout)
	}
}

// PARITY. `tasks history --json` and `tasks archive --dry-run --json` print the
// exact `data` documents GET /api/v1/history and GET /api/v1/archive-preview
// answer with, over the same files, journal, and day.
func TestManagerDocumentsMatchTheHTTPRoutes(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "priority", "dddd0004", "B"); result.status != 0 {
		t.Fatalf("priority: %d %q", result.status, result.stderr)
	}
	org, archive := filepath.Join(dir, "tasks.jsonl"), filepath.Join(dir, "archive.jsonl")
	journalDir := journal.DirFor(org, determinism.Env{"XDG_STATE_HOME": filepath.Join(dir, "state")})
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	newStore := func() *store.Store {
		return store.NewWriter(org, archive, store.Options{JournalDir: journalDir, Now: func() time.Time { return now }})
	}
	context := func() temporal.Context {
		built, err := temporal.NewContext(now, "Etc/UTC", 12)
		if err != nil {
			t.Fatal(err)
		}
		return built
	}
	app, err := application.NewWithStore(newStore, context)
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.New(api.Options{App: app, Read: api.NewStoreReader(newStore, context), TemporalContext: context})
	if err != nil {
		t.Fatal(err)
	}
	httpData := func(path string) string {
		request := httptest.NewRequest("GET", path, nil)
		request.Host = "127.0.0.1:4747"
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return string(envelope.Data)
	}

	for _, pair := range []struct {
		argv []string
		path string
	}{
		{[]string{"history", "--json"}, "/api/v1/history"},
		{[]string{"archive", "--dry-run", "--json"}, "/api/v1/archive-preview"},
	} {
		cli := strings.TrimSpace(runCLI(t, dir, pair.argv...).stdout)
		if http := httpData(pair.path); cli != http {
			t.Errorf("%v\n  cli:  %s\n  http: %s", pair.argv, cli, http)
		}
	}
}
