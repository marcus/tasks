package taskquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/store"
)

// One file that exercises every grouping rule the named views and the outline
// carry, pinned at 2026-07-20 by queriesFrom.
const namedViewFixture = `{"type":"meta","version":2}
{"type":"section","id":"a0000001","title":"Inbox"}
{"type":"task","id":"b0000001","parent":"a0000001","state":"INBOX","title":"loose capture"}
{"type":"task","id":"b0000002","parent":"a0000001","state":"PROPOSED","title":"unfiled proposal","priority":"C"}
{"type":"section","id":"a0000002","title":"Projects"}
{"type":"section","id":"a0000003","parent":"a0000002","title":"Launch","body":"Ship it."}
{"type":"task","id":"b0000003","parent":"a0000003","state":"NEXT","priority":"B","title":"write copy","tags":["@desk","@phone"],"deadline":"2026-07-19"}
{"type":"task","id":"b0000004","parent":"a0000003","state":"TODO","title":"book venue","deadline":"2026-07-21"}
{"type":"task","id":"b0000005","parent":"a0000003","state":"PROPOSED","priority":"A","title":"launch proposal"}
{"type":"task","id":"b0000006","parent":"a0000003","state":"INBOX","title":"filed capture"}
{"type":"section","id":"a0000004","parent":"a0000003","title":"Press"}
{"type":"task","id":"b0000007","parent":"a0000004","state":"DONE","title":"old press task","closed":"2026-07-01"}
{"type":"task","id":"b0000008","parent":"b0000007","state":"TODO","title":"hoisted child","scheduled":"2026-07-18"}
{"type":"section","id":"a0000005","title":"Home","state":"DONE","closed":"2026-07-02"}
{"type":"task","id":"b0000009","parent":"a0000005","state":"NEXT","title":"parked","tags":["defer"]}
{"type":"task","id":"b000000a","parent":"a0000005","state":"NEXT","priority":"A","title":"plain next"}
{"type":"section","id":"a0000006","title":"Someday / Maybe"}
`

func ids(items []store.Item) string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.ID)
	}
	return strings.Join(out, ",")
}

func groupsOf(result NamedViewResult) string {
	out := []string{}
	for _, group := range result.Groups {
		out = append(out, fmt.Sprintf("%s|%s=%s", group.Block, group.Key, ids(group.Items)))
	}
	return strings.Join(out, " ")
}

func TestNamedViewItemsAreTheCLISelection(t *testing.T) {
	q := queriesFrom(t, namedViewFixture)
	for name, want := range map[string][]store.Item{
		ViewAgenda: q.AgendaItems(), ViewNext: q.NextItems(), ViewQuadrants: q.QuadrantItems(),
	} {
		result, ok := q.NamedView(name, NamedViewOptions{})
		if !ok {
			t.Fatalf("%s is not a named view", name)
		}
		if got := ids(result.Items); got != ids(want) {
			t.Errorf("%s items = %s, want the CLI's %s", name, got, ids(want))
		}
	}
	inbox, _ := q.NamedView(ViewInbox, NamedViewOptions{})
	// Approvals in triage order, then the accepted inbox the CLI lists.
	if got, want := ids(inbox.Items), "b0000005,b0000002,"+ids(q.InboxItems()); got != want {
		t.Errorf("inbox items = %s, want %s", got, want)
	}
	if _, ok := q.NamedView("projects", NamedViewOptions{}); ok {
		t.Error("projects is not a named view; /projects owns it")
	}
}

func TestNamedViewGroups(t *testing.T) {
	q := queriesFrom(t, namedViewFixture)
	for _, test := range []struct {
		name, want string
		options    NamedViewOptions
	}{
		{ViewAgenda, "|overdue=b0000003 |today=b0000008 |tomorrow=b0000004 |later=", NamedViewOptions{}},
		// Two contexts, two groups; a context-less row files under (no context),
		// which sorts first by key like the tab.
		{ViewNext, "|(no context)=b000000a |@desk=b0000003 |@phone=b0000003", NamedViewOptions{}},
		{ViewNext, "|(no context)=b000000a,b0000009 |@desk=b0000003 |@phone=b0000003",
			NamedViewOptions{IncludeUnavailable: true}},
		{ViewQuadrants, "|Q1=b0000003 |Q2=b000000a |Q3=b0000004 |Q4=b0000001,b0000006,b0000008", NamedViewOptions{}},
		// Both blocks lay projects out first and the unfiled bucket last.
		{ViewInbox, "approvals|Launch=b0000005 approvals|Inbox=b0000002 inbox|Launch=b0000006 inbox|Inbox=b0000001",
			NamedViewOptions{}},
	} {
		result, _ := q.NamedView(test.name, test.options)
		if got := groupsOf(result); got != test.want {
			t.Errorf("%s %+v groups =\n  %s\nwant\n  %s", test.name, test.options, got, test.want)
		}
	}
}

func TestOutlineIsTheTabInFileOrder(t *testing.T) {
	q := queriesFrom(t, namedViewFixture)
	describe := func(nodes []OutlineNode) string {
		out := []string{}
		for _, node := range nodes {
			entry := fmt.Sprintf("%s:%s^%s@%d", node.Kind[:1], node.ID, node.ParentID, node.Depth)
			if node.Kind == OutlineSectionNode {
				entry += fmt.Sprintf("[%s %d/%d/%d/%d]", node.Section.Kind, node.Counts.Shown,
					node.Counts.Open, node.Counts.Closed, node.Counts.HiddenClosed)
			}
			out = append(out, entry)
		}
		return strings.Join(out, " ")
	}
	// Proposals never appear; the closed task and the closed Home section are
	// transparent, so their open children carry on one level up.
	want := "s:a0000001^@0[inbox 1/1/0/0] t:b0000001^a0000001@1 " +
		"s:a0000002^@0[projects_root 4/4/1/1] s:a0000003^a0000002@1[project 4/4/1/1] " +
		"t:b0000003^a0000003@2 t:b0000004^a0000003@2 t:b0000006^a0000003@2 " +
		"s:a0000004^a0000003@2[subsection 1/1/1/1] t:b0000008^a0000004@3 " +
		"t:b0000009^@0 t:b000000a^@0 s:a0000006^@0[list 0/0/0/0]"
	if got := describe(q.Outline(false)); got != want {
		t.Errorf("outline =\n  %s\nwant\n  %s", got, want)
	}
	shown := "s:a0000001^@0[inbox 1/1/0/0] t:b0000001^a0000001@1 " +
		"s:a0000002^@0[projects_root 5/4/1/0] s:a0000003^a0000002@1[project 5/4/1/0] " +
		"t:b0000003^a0000003@2 t:b0000004^a0000003@2 t:b0000006^a0000003@2 " +
		"s:a0000004^a0000003@2[subsection 2/1/1/0] t:b0000007^a0000004@3 t:b0000008^b0000007@4 " +
		"s:a0000005^@0[area 2/2/0/0] t:b0000009^a0000005@1 t:b000000a^a0000005@1 " +
		"s:a0000006^@0[list 0/0/0/0]"
	if got := describe(q.Outline(true)); got != shown {
		t.Errorf("outline with closed =\n  %s\nwant\n  %s", got, shown)
	}
}

func TestSectionActionRefusalGuardsOnlyTheStructuralSections(t *testing.T) {
	for _, kind := range []string{SectionInbox, SectionProjectsRoot} {
		for _, action := range []string{"rename", "complete", "drop", "archive"} {
			if SectionActionRefusal(kind, action) == "" {
				t.Errorf("%s %s was allowed", action, kind)
			}
		}
		if SectionActionRefusal(kind, "reopen") != "" {
			t.Errorf("reopen %s was refused", kind)
		}
	}
	for _, kind := range []string{SectionProject, SectionArea, SectionList, SectionSubsection} {
		if SectionActionRefusal(kind, "archive") != "" {
			t.Errorf("archive %s was refused", kind)
		}
	}
}
