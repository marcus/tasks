package store

import (
	"testing"

	"github.com/marcus/tasks/internal/links"
)

// A create's links are written by the create's own transaction — one record, one
// journal entry — and validated by the SAME rules a `links` patch goes through.

func TestCaptureWritesItsFormalLinksInOrderInTheSameRecord(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	result := store.CreateTask(CreateCommand{
		Title: "renew the lease",
		Links: []links.FormalLink{
			{URL: "https://example.com/thread", Label: "slack"},
			{URL: "https://example.com/doc"},
		},
	}, "2026-03-14")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	want := `{"type":"task","id":"bbbb0001","parent":"1a2b3c01","state":"INBOX","title":"renew the lease",` +
		`"links":[{"url":"https://example.com/thread","label":"slack"},{"url":"https://example.com/doc"}],` +
		`"body":"Captured [2026-03-14].","updated":"2026-03-14T15:09:26Z#fixture"}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Errorf("store bytes missing the linked record:\n%s", got)
	}
}

// One create, one undoable step: undoing it takes the links with the task.
func TestCaptureWithLinksIsOneUndoableStep(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	before := readStore(t, store)
	if result := store.CreateTask(CreateCommand{
		Title: "chase the invoice",
		Links: []links.FormalLink{{URL: "https://example.com/invoice"}},
	}, "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("create: status = %q, errors = %v", result.Status, result.Errors)
	}
	if outcome, label := store.HistoryStep(-1); outcome != HistoryOK ||
		label != "capture: chase the invoice" {
		t.Fatalf("undo: outcome = %v, label = %q", outcome, label)
	}
	if got := readStore(t, store); got != before {
		t.Errorf("one undo left the store changed:\n got %q\nwant %q", got, before)
	}
}

func TestCaptureRefusesInvalidLinksWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links []links.FormalLink
		want  string
	}{
		{"non-web URL", []links.FormalLink{{URL: "ftp://example.com/x"}},
			"link URL must be an http or https URL with a host"},
		{"no host", []links.FormalLink{{URL: "https://"}},
			"link URL must be an http or https URL with a host"},
		{"blank label", []links.FormalLink{{URL: "https://example.com/a", Label: " "}},
			"link label must be non-empty, trimmed single-line text"},
		{"duplicate", []links.FormalLink{{URL: "https://example.com/a"}, {URL: "https://example.com/a"}},
			"duplicate formal link URL: https://example.com/a"},
		{"over the cap", tooManyLinks(links.MaxFormalLinks + 1),
			"links may contain at most 50 entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := writerFixture(t, fixtureStore)
			before := readStore(t, store)
			result := store.CreateTask(CreateCommand{Title: "bad", Links: tc.links}, "2026-03-14")
			if result.Status != MutationInvalid {
				t.Fatalf("status = %q, want invalid", result.Status)
			}
			if !containsEntry(result.Errors, tc.want) {
				t.Fatalf("errors = %v, want %q", result.Errors, tc.want)
			}
			if got := readStore(t, store); got != before {
				t.Fatalf("a refused create wrote to the store:\n%s", got)
			}
		})
	}
}

// The command's slice is copied on the way in, so a caller that reuses it cannot
// retroactively change what was written.
func TestCaptureCopiesTheSuppliedLinkSlice(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	supplied := []links.FormalLink{{URL: "https://example.com/a", Label: "a"}}
	if result := store.CreateTask(CreateCommand{Title: "copied", Links: supplied},
		"2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	supplied[0] = links.FormalLink{URL: "https://example.com/mutated"}
	if got := readStore(t, store); !containsText(got, `"links":[{"url":"https://example.com/a","label":"a"}]`) {
		t.Errorf("stored links followed the caller's slice:\n%s", got)
	}
}

func tooManyLinks(count int) []links.FormalLink {
	out := make([]links.FormalLink, 0, count)
	for index := 0; index < count; index++ {
		out = append(out, links.FormalLink{URL: "https://example.com/" + itoa(index)})
	}
	return out
}

func containsEntry(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
