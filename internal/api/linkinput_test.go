package api

import (
	"strings"
	"testing"
)

// The two link conveniences `tasks capture` applies — shorthand expansion and
// title-URL lifting — reach the HTTP writes through the application layer, so
// the same input stores the same record whichever surface took it.

func linkConfigured(t *testing.T) *harness {
	t.Helper()
	return newHarnessConfigured(t, func(h *harness) {
		h.linkShorthands = map[string]string{"jira": "https://jira.example.test/browse/%s", "gh": "https://github.com/"}
		h.linkSystems = map[string]string{"forge": "git.example.test"}
	})
}

func TestCreateExpandsLinkShorthands(t *testing.T) {
	h := linkConfigured(t)
	created := h.json("POST", "/api/v1/tasks", `{"title":"Fix the pager","links":[
		{"url":"jira:OPS-1234"},
		{"url":"gh:marcus/tasks","label":"repo"},
		{"url":"https://example.test/doc"}]}`, nil)
	assertStatus(t, created, 201)
	want := `"links":[{"url":"https://jira.example.test/browse/OPS-1234","label":"jira:OPS-1234"},` +
		`{"url":"https://github.com/marcus/tasks","label":"repo"},{"url":"https://example.test/doc"}]`
	if got := string(h.storeBytes()); !strings.Contains(got, want) {
		t.Errorf("stored links:\n%s\nwant %s", got, want)
	}
}

func TestPatchExpandsLinkShorthandsAndDedupesOnTheExpandedURL(t *testing.T) {
	h := linkConfigured(t)
	patched := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"formal_links":[{"url":"jira:OPS-9"}]}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, patched, 200)
	formal, _ := patched.dig("data", "formal_links").([]any)
	if len(formal) != 1 || formal[0].(map[string]any)["url"] != "https://jira.example.test/browse/OPS-9" ||
		formal[0].(map[string]any)["label"] != "jira:OPS-9" {
		t.Errorf("formal_links = %#v", patched.dig("data", "formal_links"))
	}

	// A shorthand and the URL it expands to are one link.
	duplicate := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"formal_links":[
		{"url":"jira:OPS-9"},{"url":"https://jira.example.test/browse/OPS-9"}]}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertError(t, duplicate, 422, "validation_failed")
}

func TestUnknownShorthandsAreRefused(t *testing.T) {
	h := linkConfigured(t)
	for _, body := range []string{
		`{"title":"Bad","links":[{"url":"linear:ABC-1"}]}`,
		`{"title":"Bad","links":[{"url":"jira:"}]}`,
	} {
		before := string(h.storeBytes())
		refused := h.json("POST", "/api/v1/tasks", body, nil)
		assertError(t, refused, 422, "validation_failed")
		if !strings.Contains(refused.Body, "configured shorthand") {
			t.Errorf("%s: the refusal should name the shorthand option: %s", body, refused.Body)
		}
		if string(h.storeBytes()) != before {
			t.Fatalf("%s wrote to the store", body)
		}
	}
	// Without configuration a shorthand is just not a URL.
	plain := newHarness(t)
	assertError(t, plain.json("POST", "/api/v1/tasks", `{"title":"Bad","links":[{"url":"jira:OPS-1"}]}`, nil),
		422, "validation_failed")
}

func TestCreateLiftsATrailingTitleURLIntoAFormalLink(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Read the design doc https://example.test/doc."}`, nil)
	assertStatus(t, created, 201)
	if created.dig("data", "title") != "Read the design doc." {
		t.Errorf("title = %v", created.dig("data", "title"))
	}
	formal, _ := created.dig("data", "formal_links").([]any)
	if len(formal) != 1 || formal[0].(map[string]any)["url"] != "https://example.test/doc" {
		t.Errorf("formal_links = %#v", created.dig("data", "formal_links"))
	}

	// An explicit link of the same URL is not lifted twice and keeps its label.
	explicit := h.json("POST", "/api/v1/tasks", `{"title":"Review https://example.test/pr",
		"links":[{"url":"https://example.test/pr","label":"the PR"}]}`, nil)
	assertStatus(t, explicit, 201)
	if explicit.dig("data", "title") != "Review https://example.test/pr" {
		t.Errorf("title = %v", explicit.dig("data", "title"))
	}
	if formal, _ := explicit.dig("data", "formal_links").([]any); len(formal) != 1 {
		t.Errorf("formal_links = %#v", explicit.dig("data", "formal_links"))
	}

	// PATCH never rewrites a title: lifting is a capture convenience.
	patched := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"See https://example.test/x"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, patched, 200)
	if patched.dig("data", "title") != "See https://example.test/x" {
		t.Errorf("patched title = %v", patched.dig("data", "title"))
	}
}

func TestMetaPublishesTheResolvedLinkConfiguration(t *testing.T) {
	h := linkConfigured(t)
	data := h.get("/api/v1/meta").data()
	shorthands, _ := data["link_shorthands"].(map[string]any)
	if shorthands["jira"] != "https://jira.example.test/browse/%s" || shorthands["gh"] != "https://github.com/" {
		t.Errorf("link_shorthands = %#v", data["link_shorthands"])
	}
	systems, _ := data["link_systems"].(map[string]any)
	if systems["forge"] != "git.example.test" {
		t.Errorf("link_systems = %#v", data["link_systems"])
	}

	empty := newHarness(t).get("/api/v1/meta")
	if !strings.Contains(empty.Body, `"link_shorthands":{},"link_systems":{}`) {
		t.Errorf("an unconfigured server publishes empty maps, not nulls: %s", empty.Body)
	}
}
