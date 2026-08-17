package taskquery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// The reader's today for every assertion below.
const rejectedToday = "2026-03-14T15:09:26Z"

// rejectedFixture is one live file plus one archive file, written by hand so the
// decline dates sit at exactly the edges of the documented window.
const rejectedLive = `{"type":"meta","version":2}
{"type":"section","id":"eeff0001","title":"Inbox"}
{"type":"task","id":"eeff0002","parent":"eeff0001","state":"PROPOSED","title":"Still pending"}
{"type":"task","id":"eeff0003","parent":"eeff0001","state":"CANCELLED","title":"Declined today","closed":"2026-03-14","rejected":"2026-03-14"}
{"type":"task","id":"eeff0004","parent":"eeff0001","state":"CANCELLED","title":"Declined a week ago","closed":"2026-03-07","rejected":"2026-03-07"}
{"type":"task","id":"eeff0005","parent":"eeff0001","state":"CANCELLED","title":"Declined long ago","closed":"2025-11-01","rejected":"2025-11-01"}
{"type":"task","id":"eeff0006","parent":"eeff0001","state":"CANCELLED","title":"Just cancelled","closed":"2026-03-13"}
{"type":"task","id":"eeff0007","parent":"eeff0001","state":"DONE","title":"Finished","closed":"2026-03-13"}
{"type":"task","id":"eeff0008","parent":"eeff0001","state":"TODO","title":"Open work"}
`

const rejectedArchive = `{"type":"meta","version":2}
{"type":"task","id":"eeff0009","state":"CANCELLED","title":"Declined then archived","closed":"2026-03-10","rejected":"2026-03-10","archived":"2026-03-12"}
`

func rejectedQueries(t *testing.T) *Queries {
	t.Helper()
	root := t.TempDir()
	live := filepath.Join(root, "tasks.jsonl")
	archive := filepath.Join(root, "archive.jsonl")
	if err := os.WriteFile(live, []byte(rejectedLive), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte(rejectedArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.New(live, archive).ReadSnapshot(true)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	now, err := time.Parse(time.RFC3339, rejectedToday)
	if err != nil {
		t.Fatal(err)
	}
	context, err := temporal.NewContext(now, "UTC", 24)
	if err != nil {
		t.Fatal(err)
	}
	return New(snapshot, context)
}

func titlesOf(items []store.Item) []string {
	titles := []string{}
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func listWithScope(t *testing.T, queries *Queries, scope string) []store.Item {
	t.Helper()
	filter, err := query.NewFilter(query.FilterOptions{Scope: &scope})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	return queries.List(filter)
}

// The decline scope is CANCELLED narrowed twice — the marker, and the window —
// and ordered newest first. An ordinary cancellation and a decline older than
// the window are both out; a recently archived decline is in.
func TestRejectedScopeListsRecentDeclinesNewestFirst(t *testing.T) {
	queries := rejectedQueries(t)
	got := titlesOf(listWithScope(t, queries, query.ScopeRejected))
	// Newest decline first, regardless of which file the row lives in.
	want := []string{"Declined today", "Declined then archived", "Declined a week ago"}
	if len(got) != len(want) {
		t.Fatalf("rejected scope = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rejected scope = %v, want %v", got, want)
		}
	}
	// RecentRejects is what the TUI reveals, and it must be the same list.
	if same := titlesOf(queries.RecentRejects()); len(same) != len(got) {
		t.Errorf("RecentRejects = %v, want the same rows as the CLI scope %v", same, got)
	}
}

// Nothing else changes. A decline is still a cancellation everywhere it always
// was, and no default view gains a row.
func TestDefaultViewsStillHideDeclines(t *testing.T) {
	queries := rejectedQueries(t)
	for _, item := range listWithScope(t, queries, query.ScopeOpen) {
		if item.Rejected != "" {
			t.Errorf("the open scope showed a decline: %q", item.Title)
		}
	}
	proposed := titlesOf(listWithScope(t, queries, query.ScopeProposed))
	if len(proposed) != 1 || proposed[0] != "Still pending" {
		t.Errorf("proposed scope = %v, want only the undecided proposal", proposed)
	}
	// `--done` and `--all` still carry declines, because there they are simply
	// the cancelled tasks they are.
	found := false
	for _, item := range listWithScope(t, queries, query.ScopeDone) {
		found = found || item.Title == "Declined today"
	}
	if !found {
		t.Error("the done scope must still show a declined task as CANCELLED")
	}
}

// The window is a documented number, so its edge is worth pinning: the last day
// inside it lists, the first day outside it does not.
func TestRejectedWindowEdge(t *testing.T) {
	queries := rejectedQueries(t)
	today, _ := temporal.ParseDate("2026-03-14")
	inside := store.Item{Rejected: today.AddDays(-RejectedRecentDays).ISO()}
	outside := store.Item{Rejected: today.AddDays(-(RejectedRecentDays + 1)).ISO()}
	if !queries.rejectedRecently(inside) {
		t.Errorf("%s is the last day inside the window", inside.Rejected)
	}
	if queries.rejectedRecently(outside) {
		t.Errorf("%s is one day past the window", outside.Rejected)
	}
	if queries.rejectedRecently(store.Item{Rejected: "not-a-date"}) {
		t.Error("an undateable stamp cannot be placed in the window")
	}
}
