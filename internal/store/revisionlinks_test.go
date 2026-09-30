package store

import (
	"testing"

	"github.com/marcus/tasks/internal/record"
)

// revisionOf is the revision of the single task in a one-task store.
func revisionOf(t *testing.T, task string) string {
	t.Helper()
	parsed := record.Parse([]byte(`{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
` + task + "\n"))
	if !parsed.OK() {
		t.Fatalf("parse: %v", parsed.Errors)
	}
	revisions, err := taskRevisions(parsed.Records)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	return revisions[`"aaaa0002"`]
}

// Stored formal links are an own field of the revision (#31): changing them
// changes the token, while an absent, null, or empty list is one value, and a
// label or member-order difference in an equivalent list is not a change.
func TestFormalLinksArePartOfTheRevision(t *testing.T) {
	const prefix = `{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"T"`
	bare := revisionOf(t, prefix+`}`)
	for _, same := range []string{`,"links":null}`, `,"links":[]}`} {
		if got := revisionOf(t, prefix+same); got != bare {
			t.Errorf("links%s changed the revision of a task with no links", same)
		}
	}
	first := revisionOf(t, prefix+`,"links":[{"url":"https://example.test/a"}]}`)
	second := revisionOf(t, prefix+`,"links":[{"url":"https://example.test/b"}]}`)
	labelled := revisionOf(t, prefix+`,"links":[{"url":"https://example.test/a","label":"A"}]}`)
	reordered := revisionOf(t, prefix+`,"links":[{"label":"A","url":"https://example.test/a"}]}`)
	if first == bare || first == second || first == labelled {
		t.Errorf("distinct link lists share a revision: bare %s, a %s, b %s, labelled %s",
			bare, first, second, labelled)
	}
	if reordered != labelled {
		t.Errorf("member order changed the revision: %s vs %s", reordered, labelled)
	}
}
