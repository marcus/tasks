package api

import (
	"strings"
	"testing"
)

// `POST /tasks` with `links` is the API half of `tasks capture --link`: the same
// entry shape as PATCH's `formal_links`, the same refusals, and installed by the
// create's own transaction.

func TestCreateStoresOrderedLinksInTheSameWrite(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks", `{
		"title":"Renew the lease","project":"Work",
		"links":[{"url":"https://example.com/thread","label":"slack thread"},
		         {"url":"https://example.com/doc"}]}`, nil)
	assertStatus(t, created, 201)
	resource := created.data()
	formal, ok := resource["formal_links"].([]any)
	if !ok || len(formal) != 2 {
		t.Fatalf("formal_links = %#v", resource["formal_links"])
	}
	first := formal[0].(map[string]any)
	if first["url"] != "https://example.com/thread" || first["label"] != "slack thread" {
		t.Errorf("first link = %#v", first)
	}
	second := formal[1].(map[string]any)
	if second["url"] != "https://example.com/doc" {
		t.Errorf("second link = %#v", second)
	}
	// The derived union sees them as formal, which is what makes them openable.
	union, ok := resource["links"].([]any)
	if !ok || len(union) < 2 || union[0].(map[string]any)["source"] != "formal" {
		t.Fatalf("links = %#v", resource["links"])
	}
	// One record carries them: the create wrote the links, not a follow-up patch.
	if got := string(h.storeBytes()); !strings.Contains(got,
		`"links":[{"url":"https://example.com/thread","label":"slack thread"},{"url":"https://example.com/doc"}]`) {
		t.Errorf("store bytes missing the created links:\n%s", got)
	}
}

// A create that carries links still touches exactly ONE record, which is what
// makes it one history step — the API has no undo route of its own, so the
// single-write property is asserted where it is visible: in the bytes.
func TestCreateWithLinksTouchesOnlyItsOwnRecord(t *testing.T) {
	h := newHarness(t)
	before := strings.Split(strings.TrimRight(string(h.storeBytes()), "\n"), "\n")
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Chase the invoice","links":[{"url":"https://example.com/invoice"}]}`, nil)
	assertStatus(t, created, 201)
	after := strings.Split(strings.TrimRight(string(h.storeBytes()), "\n"), "\n")
	if len(after) != len(before)+1 {
		t.Fatalf("line count %d → %d, want exactly one new record", len(before), len(after))
	}
	added := 0
	for _, line := range after {
		if !containsLine(before, line) {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("%d lines differ, want only the new task's:\n%s", added, strings.Join(after, "\n"))
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func TestCreateRefusesInvalidLinks(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{
		`{"title":"Bad","links":[{"url":"file:///tmp/x"}]}`,
		`{"title":"Bad","links":[{"url":"https://"}]}`,
		`{"title":"Bad","links":[{"url":"https://example.com/a","label":"  "}]}`,
		`{"title":"Bad","links":[{"url":"https://example.com/a"},{"url":"https://example.com/a"}]}`,
		`{"title":"Bad","links":[{"url":"https://example.com/a","note":"x"}]}`,
		`{"title":"Bad","links":"https://example.com/a"}`,
		`{"title":"Bad","links":[{"url":"https://example.com/a","label":null}]}`,
	} {
		before := string(h.storeBytes())
		refused := h.json("POST", "/api/v1/tasks", body, nil)
		assertError(t, refused, 422, "validation_failed")
		if after := string(h.storeBytes()); after != before {
			t.Fatalf("%s wrote to the store", body)
		}
	}
}

// The create field is `links` and nothing else: `formal_links` is the PATCH
// spelling and is not silently accepted here, so a client cannot believe it
// filed links that were dropped.
func TestCreateRejectsThePatchSpellingOfLinks(t *testing.T) {
	h := newHarness(t)
	refused := h.json("POST", "/api/v1/tasks",
		`{"title":"Bad","formal_links":[{"url":"https://example.com/a"}]}`, nil)
	assertError(t, refused, 422, "validation_failed")
}
