package application

import (
	"testing"

	"github.com/marcus/tasks/internal/links"
)

// The application layer carries a create's links through unchanged and defends
// the caller's slice, so every surface — CLI `--link`, API `links` — reaches the
// store with the same ordered list it submitted.

func TestApplicationCreateCarriesLinksThroughInOrder(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	supplied := []links.FormalLink{
		{URL: "https://example.com/thread", Label: "slack"},
		{URL: "https://example.com/doc"},
	}
	result := h.app.CreateTask(CreateCommand{
		Title: "Linked at birth", Project: "Work", Links: supplied,
	}, nil)
	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	parsed, found := h.recordFor("Linked at birth")
	if !found {
		t.Fatal("the task was not written")
	}
	want := `[{"url":"https://example.com/thread","label":"slack"},{"url":"https://example.com/doc"}]`
	if got := string(mustGet(parsed, "links")); got != want {
		t.Fatalf("links = %s, want %s", got, want)
	}

	// The command was copied on acceptance: mutating the caller's slice now
	// cannot rewrite what was persisted.
	supplied[0] = links.FormalLink{URL: "https://example.com/mutated"}
	parsed, _ = h.recordFor("Linked at birth")
	if got := string(mustGet(parsed, "links")); got != want {
		t.Fatalf("links after caller mutation = %s, want %s", got, want)
	}
}

// PrepareCreateTask is what the CLI's dry-run previews, so it must not lose the
// links a preview would otherwise fail to mention.
func TestPrepareCreateTaskKeepsLinksAndCopiesThem(t *testing.T) {
	h := newHarness(t, harnessOptions{hostContext: "@home"})
	supplied := []links.FormalLink{{URL: "https://example.com/a", Label: "a"}}
	prepared := h.app.PrepareCreateTask(CreateCommand{Title: "Prepared", Links: supplied})
	if len(prepared.Links) != 1 || prepared.Links[0].URL != "https://example.com/a" ||
		prepared.Links[0].Label != "a" {
		t.Fatalf("prepared links = %#v", prepared.Links)
	}
	supplied[0] = links.FormalLink{URL: "https://example.com/mutated"}
	if prepared.Links[0].URL != "https://example.com/a" {
		t.Fatalf("prepared links follow the caller's slice: %#v", prepared.Links)
	}
}

func TestApplicationCreateRefusesAnInvalidLinkWithoutWriting(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	before := h.read()
	result := h.app.CreateTask(CreateCommand{
		Title: "Bad link", Project: "Work",
		Links: []links.FormalLink{{URL: "ftp://example.com/x"}},
	}, nil)
	if result.OK() {
		t.Fatal("an invalid link must refuse the create")
	}
	if after := h.read(); after != before {
		t.Fatalf("a refused create wrote to the store:\n%s", after)
	}
}

// The two capture conveniences belong to the application, so every surface
// that creates through it — CLI, TUI, HTTP — gets them identically.
func TestPrepareCreateTaskExpandsShorthandsAndLiftsATitleURL(t *testing.T) {
	h := newHarness(t, harnessOptions{
		linkShorthands: map[string]string{"jira": "https://jira.example.test/browse/%s"},
	})
	prepared := h.app.PrepareCreateTask(CreateCommand{
		Title: "Fix the pager, see https://example.test/runbook.",
		Links: []links.FormalLink{{URL: "jira:OPS-1"}, {URL: "jira:OPS-2", Label: "follow-up"}},
	})
	if prepared.Title != "Fix the pager, see." {
		t.Errorf("title = %q", prepared.Title)
	}
	want := []links.FormalLink{
		{URL: "https://jira.example.test/browse/OPS-1", Label: "jira:OPS-1"},
		{URL: "https://jira.example.test/browse/OPS-2", Label: "follow-up"},
		{URL: "https://example.test/runbook"},
	}
	if len(prepared.Links) != len(want) {
		t.Fatalf("links = %#v", prepared.Links)
	}
	for index := range want {
		if prepared.Links[index] != want[index] {
			t.Errorf("link %d = %#v, want %#v", index, prepared.Links[index], want[index])
		}
	}
}

// A dry-run preview followed by the create prepares once: a title ending in
// two URLs loses only the last, not both.
func TestPrepareCreateTaskIsIdempotent(t *testing.T) {
	h := newHarness(t, harnessOptions{hostContext: "@home"})
	once := h.app.PrepareCreateTask(CreateCommand{Title: "Compare https://a.example.test https://b.example.test"})
	twice := h.app.PrepareCreateTask(once)
	if once.Title != "Compare https://a.example.test" || twice.Title != once.Title {
		t.Errorf("once = %q, twice = %q", once.Title, twice.Title)
	}
	if len(twice.Links) != 1 || len(twice.Tags) != 1 {
		t.Errorf("twice = %#v", twice)
	}
	result := h.app.CreateTask(once, nil)
	if !result.OK() {
		t.Fatalf("status = %q errors = %v", result.Status, result.Errors)
	}
	if _, found := h.recordFor("Compare https://a.example.test"); !found {
		t.Error("the create prepared the command a second time")
	}
}
