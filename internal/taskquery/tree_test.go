package taskquery

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/store"
)

// nestedFixture is test/test_tree.rb's NESTED: a section with a task carrying
// body links, a task with a subtask, and a second section.
const nestedFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"aaaa1111","parent":"aaaa0001","state":"NEXT","priority":"A","title":"Fix billing outage","tags":["@computer"],"deadline":"2026-07-10","body":"Context in [[https://acme.slack.com/archives/C042/p171][the incident thread]].\nTicket: https://acme.atlassian.net/browse/OPS-1234."}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"NEXT","title":"Review Q3 planning doc","body":"https://docs.google.com/document/d/abc/edit"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"TODO","title":"Leave comments for Dana","body":"Dana prefers suggestions mode."}
{"type":"section","id":"aaaa0004","title":"Home"}
{"type":"task","id":"aaaa0005","parent":"aaaa0004","state":"TODO","title":"Renew passport","body":"Photo specs: https://travel.state.gov/photos.html, then book."}
`

func itemByTitle(t *testing.T, queries *Queries, fragment string) store.Item {
	t.Helper()
	for _, item := range queries.LiveItems() {
		if strings.Contains(item.Title, fragment) {
			return item
		}
	}
	t.Fatalf("no item whose title contains %q", fragment)
	return store.Item{}
}

func TestTreeNestsSectionsTasksAndSubtasks(t *testing.T) {
	tree := queriesFrom(t, nestedFixture).Tree()
	if len(tree.Roots) != 2 {
		t.Fatalf("%d roots", len(tree.Roots))
	}
	if tree.Roots[0].Title != "Work" || tree.Roots[1].Title != "Home" {
		t.Fatalf("roots = %q, %q", tree.Roots[0].Title, tree.Roots[1].Title)
	}
	work := tree.Roots[0]
	if !work.Section() {
		t.Fatal("a section node carries no item")
	}
	if len(work.Children) != 2 {
		t.Fatalf("%d children — the subtask hangs off its task, not off the section", len(work.Children))
	}
	review := work.Children[1]
	if !review.Task() {
		t.Fatal("the second child is a task")
	}
	if review.Children[0].Item.Title != "Leave comments for Dana" {
		t.Fatalf("subtask = %q", review.Children[0].Item.Title)
	}
	if review.Children[0].Parent != review {
		t.Fatal("a child points back at its parent")
	}
}

func TestNodeBodyIsOwnLinesOnly(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	review := itemByTitle(t, queries, "Review Q3")
	body := strings.Join(queries.Body(review), "\n")
	if !strings.Contains(body, "docs.google") {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "suggestions mode") {
		t.Fatal("a child's body is not the parent's — they are separate records")
	}
}

func TestNodeProjectIsNearestAncestor(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	dana := queries.NodeFor(itemByTitle(t, queries, "Dana"))
	if dana == nil {
		t.Fatal("no node")
	}
	if got := dana.Project().Title; got != "Review Q3 planning doc" {
		t.Fatalf("project = %q", got)
	}
	if got := dana.Project().Project().Title; got != "Work" {
		t.Fatalf("grandparent = %q", got)
	}
}

// The tree indexes the LIVE file only: an archived item has no structural
// context by construction, which is also why its availability is `closed`
// without a walk.
func TestTreeIndexesTheLiveFileOnly(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	archived := store.Item{Line: 3, ID: "aaaa1111", HasID: true, Title: "Fix billing outage",
		Source: store.SourceArchive}
	if node := queries.NodeFor(archived); node != nil {
		t.Fatalf("archive item resolved to a live node %q", node.Title)
	}
}

// A closed task ancestor is TRANSPARENT for grouping: an open subtask of a
// finished parent still groups under something real rather than heading a dead
// group or vanishing from the Projects view.
func TestOpenProjectClimbsPastClosedAncestors(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"DONE","title":"finished parent","closed":"2026-07-01"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"TODO","title":"open child"}
`
	queries := queriesFrom(t, fixture)
	project, found := queries.Project(itemByTitle(t, queries, "open child"))
	if !found || project != "Work" {
		t.Fatalf("project = %q (found=%v), want Work", project, found)
	}
}

// A top-level task whose every ancestor is closed has NO project, which is how
// it falls out of the Projects view instead of heading a dead group.
func TestOpenProjectIsAbsentWhenEveryAncestorIsClosed(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"task","id":"aaaa0002","state":"DONE","title":"finished parent","closed":"2026-07-01"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"TODO","title":"open child"}
`
	queries := queriesFrom(t, fixture)
	if project, found := queries.Project(itemByTitle(t, queries, "open child")); found {
		t.Fatalf("project = %q, want none", project)
	}
}

// Body extraction degrades to empty for a stale ID-LESS item rather than
// resolving to whatever task now occupies its line. Answering with the wrong
// task's notes is worse than answering with none.
func TestNodeForRefusesADifferentTaskAtAHeldLine(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	held := store.Item{Line: 999, Title: "Renew passport", Source: store.SourceLive}
	if body := queries.Body(held); len(body) != 0 {
		t.Fatalf("body = %v", body)
	}
}

// Link extraction reads the title as well as the body, so a URL pasted straight
// into a headline is still a link the surfaces can open.
func TestLinksIncludeTitleURLs(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"Review https://github.com/acme/app/pull/7"}
`
	queries := queriesFrom(t, fixture)
	found := queries.Links(itemByTitle(t, queries, "Review"))
	if len(found) != 1 || found[0].System != "github" {
		t.Fatalf("links = %+v", found)
	}
}

func TestLinksReadBodyInFileOrder(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	found := queries.Links(itemByTitle(t, queries, "billing"))
	if len(found) != 2 || found[0].System != "slack" || found[1].System != "jira" {
		t.Fatalf("links = %+v", found)
	}
	if found[0].Label == nil || *found[0].Label != "the incident thread" {
		t.Fatalf("label = %v", found[0].Label)
	}
	if found[1].Label != nil {
		t.Fatalf("a bare URL has no label, got %q", *found[1].Label)
	}
}
