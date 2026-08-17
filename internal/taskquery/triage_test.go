package taskquery

import "testing"

// The triage fixture is deliberately shuffled in the file: every ordering
// assertion below would also pass on file order if the fixture happened to be
// written in rank order, which would make the test prove nothing.
const triageFixture = `{"type":"meta","version":2}
{"type":"section","id":"tr000001","title":"Intake"}
{"type":"task","id":"tr000010","parent":"tr000001","state":"PROPOSED","title":"C tomorrow","priority":"C","deadline":"2026-07-20"}
{"type":"task","id":"tr000011","parent":"tr000001","state":"PROPOSED","title":"undated A","priority":"A"}
{"type":"task","id":"tr000012","parent":"tr000001","state":"PROPOSED","title":"none next week","deadline":"2026-07-26"}
{"type":"task","id":"tr000013","parent":"tr000001","state":"PROPOSED","title":"B far","priority":"B","deadline":"2026-09-01"}
{"type":"task","id":"tr000014","parent":"tr000001","state":"PROPOSED","title":"A overdue","priority":"A","deadline":"2026-07-01"}
{"type":"task","id":"tr000015","parent":"tr000001","state":"PROPOSED","title":"B soon","priority":"B","deadline":"2026-07-21"}
{"type":"task","id":"tr000016","parent":"tr000001","state":"PROPOSED","title":"undated none first","deadline":""}
{"type":"task","id":"tr000017","parent":"tr000001","state":"PROPOSED","title":"undated none second"}
{"type":"task","id":"tr000018","parent":"tr000001","state":"NEXT","title":"accepted work","priority":"A"}
`

func triageQueries(t *testing.T) *Queries {
	t.Helper()
	return queriesAt(t, triageFixture, "2026-07-19T12:00:00Z")
}

// The whole rule in one assertion: priority bands in A, B, C, none order;
// soonest first inside a band; undated last inside a band; file order breaking
// a full tie.
func TestRankByPriorityThenDueOrdersBandsThenDatesThenFileOrder(t *testing.T) {
	queries := triageQueries(t)
	ranked := queries.RankByPriorityThenDue(queries.Snapshot().Items())
	proposals := []string{}
	for _, item := range ranked {
		if item.State == "PROPOSED" {
			proposals = append(proposals, item.ID)
		}
	}
	want := []string{
		"tr000014", // A, overdue
		"tr000011", // A, undated — after every dated A
		"tr000015", // B, 07-21
		"tr000013", // B, 09-01
		"tr000010", // C, 07-20
		"tr000012", // none, 07-26
		"tr000016", // none, undated, first in the file
		"tr000017", // none, undated, second in the file
	}
	if !sameIDs(proposals, want...) {
		t.Errorf("ranked = %v\nwant    %v", proposals, want)
	}
}

// The acceptance case the issue names outright: priority strictly dominates the
// date, so a C due tomorrow still sorts below an A with no date at all.
func TestPriorityDominatesDueDateSoAnUndatedAOutranksAnImminentC(t *testing.T) {
	queries := triageQueries(t)
	undatedA := itemByID(t, queries, "tr000011")
	imminentC := itemByID(t, queries, "tr000010")
	if !queries.PriorityThenDueBefore(undatedA, imminentC) {
		t.Error("an undated A must outrank a C due tomorrow")
	}
	if queries.PriorityThenDueBefore(imminentC, undatedA) {
		t.Error("the comparator is not antisymmetric across priority bands")
	}
}

// A tie reports false in both directions, which is what lets a stable sort keep
// file order rather than inventing one.
func TestPriorityThenDueBeforeReportsNoOrderForATie(t *testing.T) {
	queries := triageQueries(t)
	first := itemByID(t, queries, "tr000016")
	second := itemByID(t, queries, "tr000017")
	if queries.PriorityThenDueBefore(first, second) || queries.PriorityThenDueBefore(second, first) {
		t.Error("two undated unprioritized rows tie; the sort's stability owns their order")
	}
}

// Ranking answers a COPY: the caller's slice — very often the snapshot's own
// item list — must not be reordered underneath it.
func TestRankByPriorityThenDueDoesNotMutateItsInput(t *testing.T) {
	queries := triageQueries(t)
	items := queries.List(filterFrom(t, "--all"))
	before := idsOf(items)
	queries.RankByPriorityThenDue(items)
	if !sameIDs(idsOf(items), before...) {
		t.Errorf("input reordered: %v", idsOf(items))
	}
}

// `--proposed` is ranked; the browsable scopes are not. Outline and `--open`
// order is a separate contract this change must not touch.
func TestProposedScopeIsRankedWhileOpenScopeKeepsFileOrder(t *testing.T) {
	queries := triageQueries(t)
	proposed := idsOf(queries.List(filterFrom(t, "--proposed")))
	if !sameIDs(proposed, "tr000014", "tr000011", "tr000015", "tr000013",
		"tr000010", "tr000012", "tr000016", "tr000017") {
		t.Errorf("--proposed = %v — the scope arrives pre-ranked", proposed)
	}
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"to000001","title":"Work"}
{"type":"task","id":"to000002","parent":"to000001","state":"NEXT","title":"undated none"}
{"type":"task","id":"to000003","parent":"to000001","state":"NEXT","title":"dated A","priority":"A","deadline":"2026-07-20"}
`
	open := idsOf(queriesAt(t, fixture, "2026-07-19T12:00:00Z").List(filterFrom(t)))
	if !sameIDs(open, "to000002", "to000003") {
		t.Errorf("--open = %v — the default list stays in file order", open)
	}
}
