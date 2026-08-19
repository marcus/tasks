package tui

import (
	"strings"
	"testing"
)

// The inbox is the one view that paints two lists on one screen: the APPROVALS
// queue and the INBOX itself. They are read as a single column of work, so
// every row in both blocks — proposal, inbox task, revealed decline, and the
// placeholder standing in for an empty block — starts at ONE left edge. The
// edge used to break because only the INBOX rows went through the subtree
// walker, which prefixes the two-cell collapse marker; approvals kept their
// titles two columns to the left of it.
const inboxAlignFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"unsorted note","tags":["@home"]}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"INBOX","title":"nested note","tags":["@home"]}
{"type":"task","id":"aaaa0004","parent":"aaaa0001","state":"PROPOSED","priority":"A","title":"proposed note","tags":["@home"],"deadline":"2026-07-02"}
{"type":"task","id":"aaaa0005","parent":"aaaa0001","state":"CANCELLED","title":"declined note","tags":["@home"],"closed":"2026-07-13","rejected":"2026-07-13"}
`

// emptyInboxFixture has nothing pending and nothing captured, so both blocks
// paint their placeholder and nothing else.
const emptyInboxFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
`

// contentColumn is the column a painted line's own content starts at, ignoring
// the frame's padding, the cursor gutter and the urgency band — i.e. the left
// edge the eye reads the list down.
func contentColumn(t *testing.T, frame, needle string) int {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		index := strings.Index(line, needle)
		if index < 0 {
			continue
		}
		return len([]rune(line[:index]))
	}
	t.Fatalf("no painted line contains %q:\n%s", needle, frame)
	return -1
}

func TestInboxSectionsShareOneLeftEdge(t *testing.T) {
	modes := []struct {
		name     string
		contexts []string
		filter   string
	}{
		{name: "tree"},
		{name: "tree with context filter", contexts: []string{"@home"}},
		{name: "flat under search", filter: "note"},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			harness := newModelHarness(t, harnessOptions{live: inboxAlignFixture})
			harness.model.SwitchView(ViewInbox)
			harness.press('R') // reveal the declined proposal
			harness.model.contextFilters = mode.contexts
			harness.model.filter = mode.filter
			harness.model.RefreshRows()
			frame := renderAt(t, harness, 100, 30)

			edge := contentColumn(t, frame, "proposed note")
			for _, needle := range []string{"unsorted note", "2026-07-13"} {
				if got := contentColumn(t, frame, needle); got != edge {
					t.Errorf("%q starts at column %d, the APPROVALS edge is %d:\n%s",
						needle, got, edge, frame)
				}
			}
			// A nested row hangs off its parent by design; it may only ever be
			// indented FURTHER, never back past the shared edge.
			if harness.model.useTree() {
				if got := contentColumn(t, frame, "nested note"); got <= edge {
					t.Errorf("a child row starts at column %d, at or left of the edge %d:\n%s",
						got, edge, frame)
				}
			}
		})
	}
}

func TestInboxPlaceholdersShareTheRowLeftEdge(t *testing.T) {
	filled := newModelHarness(t, harnessOptions{live: inboxAlignFixture})
	filled.model.SwitchView(ViewInbox)
	edge := contentColumn(t, renderAt(t, filled, 100, 30), "proposed note")

	empty := newModelHarness(t, harnessOptions{live: emptyInboxFixture})
	empty.model.SwitchView(ViewInbox)
	frame := renderAt(t, empty, 100, 30)
	for _, needle := range []string{"Nothing pending approval", "Inbox empty."} {
		if got := contentColumn(t, frame, needle); got != edge {
			t.Errorf("placeholder %q starts at column %d, rows start at %d:\n%s",
				needle, got, edge, frame)
		}
	}
}

// The marker gutter is exactly MarkerField wide, which is what lets
// placeholderIndent be spelled in fields rather than in counted spaces.
func TestMarkerFieldMatchesTheMarkerGlyphs(t *testing.T) {
	for _, marker := range []string{MarkLeaf, MarkExpanded, MarkCollapsed} {
		if got := len([]rune(marker)); got != MarkerField {
			t.Errorf("marker %q is %d cells, MarkerField is %d", marker, got, MarkerField)
		}
	}
}

// The gutter is a column, not padding: an approval row claims no marker hit
// target while every proposal is a leaf, so a click in those two cells must not
// be read as a fold.
func TestApprovalRowsClaimNoFoldTarget(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: inboxAlignFixture})
	harness.model.SwitchView(ViewInbox)
	for _, row := range harness.model.Rows() {
		if row.Item == nil || !isProposedState(row.Item.State) {
			continue
		}
		if row.HasMarker() {
			t.Errorf("proposal row %q claims a fold target at %d..%d",
				row.Item.Title, row.MarkerBegin, row.MarkerEnd)
		}
	}
}
