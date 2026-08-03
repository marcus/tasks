package taskquery

import (
	"testing"

	"tasks-go/internal/query"
	"tasks-go/internal/store"
)

func availabilityOf(t *testing.T, queries *Queries, id string) Availability {
	t.Helper()
	item, found := queries.FindLive(id)
	if !found {
		t.Fatalf("no live item %q", id)
	}
	return queries.AvailabilityFor(item)
}

func filterFrom(t *testing.T, args ...string) query.Filter {
	t.Helper()
	parsed, err := query.ParseCLI(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return parsed.Filter()
}

// An available-from date is INCLUSIVE: the task opens ON the date, not the day
// after. A port that got this wrong would hide a day's work once per stamp.
func TestTimedAvailabilityIsInclusiveAndFiltersDefaultAndNamedViews(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Work"}
{"type":"task","id":"aa000002","parent":"aa000001","state":"NEXT","title":"Timed parent","scheduled":"2026-07-15"}
{"type":"task","id":"aa000003","parent":"aa000002","state":"NEXT","title":"Inheriting child"}
`
	before := queriesAt(t, fixture, "2026-07-14T12:00:00Z")
	parent := availabilityOf(t, before, "aa000002")
	if parent.Reason != ReasonScheduled || parent.BlockerID != "aa000002" {
		t.Fatalf("parent = %+v", parent)
	}
	child := availabilityOf(t, before, "aa000003")
	if child.Reason != ReasonAncestorScheduled || child.BlockerID != "aa000002" {
		t.Fatalf("child = %+v", child)
	}
	if child.Scheduled.ISO() != "2026-07-15" {
		t.Errorf("child gate = %s", child.Scheduled.ISO())
	}
	if got := idsOf(before.NextItems()); len(got) != 0 {
		t.Errorf("next = %v on the day before", got)
	}
	if got := idsOf(before.List(filterFrom(t))); len(got) != 0 {
		t.Errorf("list = %v on the day before", got)
	}
	if got := idsOf(before.List(filterFrom(t, "--deferred"))); !sameIDs(got, "aa000002", "aa000003") {
		t.Errorf("--deferred = %v — the held scope is where they show", got)
	}

	onDate := queriesAt(t, fixture, "2026-07-15T00:00:00Z")
	if !availabilityOf(t, onDate, "aa000002").Available() ||
		!availabilityOf(t, onDate, "aa000003").Available() {
		t.Fatal("both are available ON the date")
	}
	if got := idsOf(onDate.NextItems()); !sameIDs(got, "aa000002", "aa000003") {
		t.Errorf("next = %v", got)
	}
	if got := idsOf(onDate.List(filterFrom(t))); !sameIDs(got, "aa000002", "aa000003") {
		t.Errorf("list = %v", got)
	}
}

// `--unavailable` is EFFECTIVE (own or inherited); `--someday` is the OWN
// indefinite hold. Conflating them would make a subtask look parked when only
// its parent is.
func TestUnavailableAndSomedayFiltersDistinguishEffectiveFromOwnHold(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"ad000001","title":"Work"}
{"type":"task","id":"ad000002","parent":"ad000001","state":"TODO","title":"Held parent","tags":["defer"]}
{"type":"task","id":"ad000003","parent":"ad000002","state":"NEXT","title":"Inherited child"}
{"type":"task","id":"ad000004","parent":"ad000001","state":"NEXT","title":"Timed task","scheduled":"2026-07-20"}
{"type":"task","id":"ad000005","parent":"ad000001","state":"DONE","title":"Closed hold","tags":["defer"],"closed":"2026-07-01"}
`
	queries := queriesAt(t, fixture, "2026-07-19T12:00:00Z")
	if got := idsOf(queries.List(filterFrom(t, "--unavailable"))); !sameIDs(got,
		"ad000002", "ad000003", "ad000004") {
		t.Errorf("--unavailable = %v", got)
	}
	if got := idsOf(queries.List(filterFrom(t, "--someday"))); !sameIDs(got, "ad000002") {
		t.Errorf("--someday = %v — only the OWN hold", got)
	}
	// In a closed scope the hold is read off the task's own marker: a finished
	// task has no effective availability to inherit.
	for _, args := range [][]string{{"--done", "--deferred"}, {"--done", "--someday"}} {
		if got := idsOf(queries.List(filterFrom(t, args...))); !sameIDs(got, "ad000005") {
			t.Errorf("%v = %v", args, got)
		}
	}
}

// An indefinite hold outranks every timed gate — there is no date at which it
// releases, so reporting the date one would have released on is a promise
// nothing keeps. Among timed gates the LATEST wins (all of them must pass), and
// an equal-date tie goes to the nearest blocker, which is the one a reader can
// act on.
func TestBlockerPrecedenceIsHoldThenLatestDateThenNearest(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"bb000001","title":"Work"}
{"type":"task","id":"bb000002","parent":"bb000001","state":"TODO","title":"Held root","tags":["defer"],"scheduled":"2026-07-30"}
{"type":"task","id":"bb000003","parent":"bb000002","state":"TODO","title":"Timed middle","scheduled":"2026-08-01"}
{"type":"task","id":"bb000004","parent":"bb000003","state":"TODO","title":"Timed leaf","scheduled":"2026-08-01"}
{"type":"task","id":"bb000005","parent":"bb000001","state":"TODO","title":"Latest root","scheduled":"2026-08-02"}
{"type":"task","id":"bb000006","parent":"bb000005","state":"TODO","title":"Earlier leaf","scheduled":"2026-08-01"}
`
	queries := queriesFrom(t, fixture)
	held := availabilityOf(t, queries, "bb000004")
	if held.Reason != ReasonAncestorOnHold || held.BlockerID != "bb000002" {
		t.Fatalf("held = %+v", held)
	}
	if !held.Scheduled.Zero() {
		t.Errorf("an indefinite hold names no release date, got %s", held.Scheduled.ISO())
	}

	latest := availabilityOf(t, queries, "bb000006")
	if latest.Reason != ReasonAncestorScheduled || latest.BlockerID != "bb000005" {
		t.Fatalf("latest = %+v", latest)
	}
	if latest.Scheduled.ISO() != "2026-08-02" {
		t.Errorf("gate = %s, want the LATER ancestor date", latest.Scheduled.ISO())
	}

	unheld := queriesFrom(t, `{"type":"meta","version":2}
{"type":"section","id":"bb000001","title":"Work"}
{"type":"task","id":"bb000002","parent":"bb000001","state":"TODO","title":"Held root","scheduled":"2026-07-30"}
{"type":"task","id":"bb000003","parent":"bb000002","state":"TODO","title":"Timed middle","scheduled":"2026-08-01"}
{"type":"task","id":"bb000004","parent":"bb000003","state":"TODO","title":"Timed leaf","scheduled":"2026-08-01"}
`)
	tie := availabilityOf(t, unheld, "bb000004")
	if tie.Reason != ReasonScheduled || tie.BlockerID != "bb000004" {
		t.Fatalf("self wins an equal-date tie, got %+v", tie)
	}
}

// A closed ancestor is transparent to lifecycle hoisting — its open child is
// still visible — but its own blocker still applies to the subtree it holds.
func TestClosedAncestorsAreHoistedButTheirBlockersStillApply(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"cc000001","title":"Work"}
{"type":"task","id":"cc000002","parent":"cc000001","state":"DONE","title":"Transparent closed","closed":"2026-07-01"}
{"type":"task","id":"cc000003","parent":"cc000002","state":"NEXT","title":"Visible child"}
{"type":"task","id":"cc000004","parent":"cc000001","state":"DONE","title":"Timed closed","scheduled":"2026-07-20","closed":"2026-07-01"}
{"type":"task","id":"cc000005","parent":"cc000004","state":"NEXT","title":"Timed hidden child"}
{"type":"task","id":"cc000006","parent":"cc000001","state":"DONE","title":"Held closed","tags":["defer"],"closed":"2026-07-01"}
{"type":"task","id":"cc000007","parent":"cc000006","state":"NEXT","title":"Held hidden child"}
`
	queries := queriesAt(t, fixture, "2026-07-19T12:00:00Z")
	if !availabilityOf(t, queries, "cc000003").Available() {
		t.Error("a closed parent with no blocker is transparent")
	}
	timed := availabilityOf(t, queries, "cc000005")
	if timed.Reason != ReasonAncestorScheduled || timed.BlockerID != "cc000004" {
		t.Errorf("timed = %+v", timed)
	}
	if got := availabilityOf(t, queries, "cc000007").Reason; got != ReasonAncestorOnHold {
		t.Errorf("held child reason = %s", got)
	}
	if got := idsOf(queries.NextItems()); !sameIDs(got, "cc000003") {
		t.Errorf("next = %v", got)
	}
	// The closed ancestor itself is `closed`, and names no blocker: the
	// availability question does not apply to finished work.
	closed := availabilityOf(t, queries, "cc000004")
	if closed.Reason != ReasonClosed || closed.BlockerID != "" {
		t.Errorf("closed = %+v", closed)
	}
	if got := idsOf(queries.List(filterFrom(t, "--done", "--deferred"))); !sameIDs(got, "cc000006") {
		t.Errorf("--done --deferred = %v", got)
	}
}

// `--delegated` is every marker, whoever holds it; `--agent-ready` is the
// narrower claimable queue — agent kind, unclaimed, accepted live state, and
// workable right now.
func TestDelegatedScopeSelectsEveryMarkerAndAgentReadyOnlyClaimableWork(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"dd000001","title":"Work"}
{"type":"task","id":"dd000002","parent":"dd000001","state":"NEXT","title":"to a person","delegation":{"kind":"person","status":"delegated","assignee":"dana@example.com","at":"2026-07-01T00:00:00Z"}}
{"type":"task","id":"dd000003","parent":"dd000001","state":"NEXT","title":"agent ready","delegation":{"kind":"agent","status":"ready","mode":"code","at":"2026-07-01T00:00:00Z"}}
{"type":"task","id":"dd000004","parent":"dd000001","state":"NEXT","title":"already claimed","delegation":{"kind":"agent","status":"claimed","mode":"code","assignee":"worker-1","at":"2026-07-01T00:00:00Z"}}
{"type":"task","id":"dd000005","parent":"dd000001","state":"NEXT","title":"ready but held","tags":["defer"],"delegation":{"kind":"agent","status":"ready","mode":"code","at":"2026-07-01T00:00:00Z"}}
{"type":"task","id":"dd000006","parent":"dd000001","state":"DONE","title":"closed provenance","closed":"2026-07-01","delegation":{"kind":"agent","status":"ready","mode":"code","at":"2026-07-01T00:00:00Z"}}
{"type":"task","id":"dd000007","parent":"dd000001","state":"NEXT","title":"undelegated"}
`
	queries := queriesFrom(t, fixture)
	// `--delegated` composes with the scope's ordinary availability rule rather
	// than replacing it: the held marker is still delegated, but the open scope
	// shows what is workable, and `--unavailable` is where the parked one lives.
	delegated := idsOf(queries.List(filterFrom(t, "--delegated")))
	if !sameIDs(delegated, "dd000002", "dd000003", "dd000004") {
		t.Errorf("--delegated = %v", delegated)
	}
	held := idsOf(queries.List(filterFrom(t, "--delegated", "--unavailable")))
	if !sameIDs(held, "dd000005") {
		t.Errorf("--delegated --unavailable = %v", held)
	}
	ready := idsOf(queries.List(filterFrom(t, "--agent-ready")))
	if !sameIDs(ready, "dd000003") {
		t.Errorf("--agent-ready = %v — claimed, held and closed work is not claimable", ready)
	}
	// A closed task keeps its marker as PROVENANCE, so the all scope still finds
	// it while the claimable queue does not.
	all := idsOf(queries.List(filterFrom(t, "--all", "--delegated")))
	found := false
	for _, id := range all {
		if id == "dd000006" {
			found = true
		}
	}
	if !found {
		t.Errorf("--all --delegated = %v, want the closed marker as provenance", all)
	}
}

// Archived items are `closed` without a tree walk: the archive has no
// structural context, so there is nothing to inherit.
func TestArchivedItemsAreClosedWithoutAWalk(t *testing.T) {
	queries := queriesFrom(t, nestedFixture)
	archived := store.Item{Line: 3, ID: "aaaa1111", HasID: true, State: "NEXT",
		Title: "Fix billing outage", Source: store.SourceArchive}
	if got := queries.AvailabilityFor(archived).Reason; got != ReasonClosed {
		t.Fatalf("reason = %s", got)
	}
}
