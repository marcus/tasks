package taskquery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// queriesFrom builds a read model over inline fixture text at a pinned instant.
// Inline rather than a shared fixture on purpose: each of these tests is about
// ONE rule, and a fixture that has to satisfy all of them stops demonstrating
// any of them.
func queriesFrom(t *testing.T, content string) *Queries {
	t.Helper()
	return queriesAt(t, content, "2026-07-20T12:00:00Z")
}

func queriesAt(t *testing.T, content, instant string) *Queries {
	t.Helper()
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	snapshot, err := store.New(org, filepath.Join(dir, "archive.jsonl")).ReadSnapshot(true)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	now, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		t.Fatalf("parse pin: %v", err)
	}
	context, err := temporal.NewContext(now, "UTC", 12)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	return New(snapshot, context)
}

func quadrantItem(priority string, tags []string, deadline, scheduled string) store.Item {
	item := store.Item{State: "NEXT", Priority: priority, Title: "t", Line: 1,
		Source: store.SourceLive, Deadline: deadline, Scheduled: scheduled}
	item.AllTags = tags
	return item
}

// today is the Ruby test's TODAY.
var quadrantToday = temporal.Date{Year: 2026, Month: time.July, Day: 1}

func date(offset int) string { return quadrantToday.AddDays(offset).ISO() }

// -- importance --------------------------------------------------------------

func TestPriorityAOrBIsImportant(t *testing.T) {
	if !Important(quadrantItem("A", nil, "", "")) || !Important(quadrantItem("B", nil, "", "")) {
		t.Fatal("A and B are the important priorities")
	}
}

func TestPriorityCOrNoneIsNotImportant(t *testing.T) {
	if Important(quadrantItem("C", nil, "", "")) || Important(quadrantItem("", nil, "", "")) {
		t.Fatal("C and unprioritized are not important")
	}
}

func TestImportantTagOverridesLowPriority(t *testing.T) {
	if !Important(quadrantItem("C", []string{"important"}, "", "")) ||
		!Important(quadrantItem("", []string{"important"}, "", "")) {
		t.Fatal("the tag is an explicit override")
	}
}

// -- urgency -----------------------------------------------------------------

func TestDeadlineWithinWindowIsUrgent(t *testing.T) {
	if !Urgent(quadrantItem("", nil, date(3), ""), quadrantToday, 3) {
		t.Error("the window is inclusive")
	}
	if !Urgent(quadrantItem("", nil, date(0), ""), quadrantToday, 3) {
		t.Error("today is urgent")
	}
}

func TestOverdueDeadlineIsUrgent(t *testing.T) {
	if !Urgent(quadrantItem("", nil, date(-5), ""), quadrantToday, 3) {
		t.Fatal("overdue counts")
	}
}

func TestDeadlinePastWindowIsNotUrgent(t *testing.T) {
	if Urgent(quadrantItem("", nil, date(4), ""), quadrantToday, 3) {
		t.Fatal("window + 1 is not urgent")
	}
}

func TestScheduledAloneIsNotUrgent(t *testing.T) {
	if Urgent(quadrantItem("", nil, "", date(1)), quadrantToday, 3) {
		t.Fatal(`"I can start" is not "I must finish"`)
	}
}

func TestNoDatesIsNotUrgent(t *testing.T) {
	if Urgent(quadrantItem("", nil, "", ""), quadrantToday, 3) {
		t.Fatal("nothing to be urgent about")
	}
}

func TestUrgentTagOverridesAbsentDeadline(t *testing.T) {
	if !Urgent(quadrantItem("", []string{"urgent"}, "", ""), quadrantToday, 3) {
		t.Fatal("the tag is an explicit override")
	}
}

func TestUrgentDaysIsConfigurable(t *testing.T) {
	far := quadrantItem("", nil, date(20), "")
	if Urgent(far, quadrantToday, 3) {
		t.Error("20 days out is not urgent in a 3-day window")
	}
	if !Urgent(far, quadrantToday, 30) {
		t.Error("a 30-day window reaches it")
	}
}

// -- combined ----------------------------------------------------------------

func TestQuadrantCoversAllFour(t *testing.T) {
	cases := []struct {
		name string
		item store.Item
		want string
	}{
		{"Q1", quadrantItem("A", nil, date(1), ""), "Q1"},
		{"Q2", quadrantItem("A", nil, "", ""), "Q2"},
		{"Q3", quadrantItem("", nil, date(1), ""), "Q3"},
		{"Q4", quadrantItem("", nil, "", ""), "Q4"},
	}
	for _, testCase := range cases {
		if got := Quadrant(testCase.item, quadrantToday, 3); got != testCase.want {
			t.Errorf("%s: got %s", testCase.name, got)
		}
	}
}

func TestDefaultUrgentDaysConstant(t *testing.T) {
	if DefaultUrgentDays != 3 {
		t.Fatalf("DefaultUrgentDays = %d", DefaultUrgentDays)
	}
}

func TestQuadrantLabelsCoverQ1ThroughQ4(t *testing.T) {
	want := []string{"Q1", "Q2", "Q3", "Q4"}
	if len(QuadrantLabels) != len(want) {
		t.Fatalf("%d labels", len(QuadrantLabels))
	}
	for index, key := range want {
		if QuadrantLabels[index][0] != key {
			t.Errorf("label %d = %s, want %s", index, QuadrantLabels[index][0], key)
		}
	}
}

// -- the named views ---------------------------------------------------------

const viewFixture = `{"type":"meta","version":2}
{"type":"section","id":"bbbb0001","title":"Work"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"INBOX","title":"unfiled"}
{"type":"task","id":"bbbb0003","parent":"bbbb0001","state":"INBOX","title":"held inbox","scheduled":"2027-01-01"}
{"type":"task","id":"bbbb0004","parent":"bbbb0001","state":"NEXT","title":"plain next"}
{"type":"task","id":"bbbb0005","parent":"bbbb0001","state":"NEXT","priority":"A","title":"urgent next"}
{"type":"task","id":"bbbb0006","parent":"bbbb0001","state":"NEXT","title":"held next","tags":["defer"]}
{"type":"task","id":"bbbb0007","parent":"bbbb0001","state":"DONE","title":"finished","closed":"2026-07-01"}
{"type":"task","id":"bbbb0008","parent":"bbbb0001","state":"TODO","title":"ordinary todo"}
`

func idsOf(items []store.Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func sameIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestNamedViewsKeepLegacySelectionAndOrder(t *testing.T) {
	queries := queriesFrom(t, viewFixture)
	if got := idsOf(queries.InboxItems()); !sameIDs(got, "bbbb0002") {
		t.Errorf("inbox = %v — only AVAILABLE inbox items", got)
	}
	if got := idsOf(queries.NextItems()); !sameIDs(got, "bbbb0005", "bbbb0004") {
		t.Errorf("next = %v — priority first, unavailable excluded", got)
	}
	if got := idsOf(queries.QuadrantItems()); !sameIDs(got, "bbbb0002", "bbbb0004", "bbbb0005", "bbbb0008") {
		t.Errorf("quadrants = %v — every available OPEN task in file order", got)
	}
	if got := idsOf(queries.AgendaItems()); len(got) != 0 {
		t.Errorf("agenda = %v — nothing here carries a date the reader can act on", got)
	}
}

// A closed task never appears in an open view, whatever its dates say.
func TestNamedViewsExcludeClosedWork(t *testing.T) {
	queries := queriesFrom(t, viewFixture)
	for name, items := range map[string][]store.Item{
		"inbox": queries.InboxItems(), "next": queries.NextItems(),
		"quadrants": queries.QuadrantItems(),
	} {
		for _, item := range items {
			if item.State == "DONE" || item.State == "CANCELLED" {
				t.Errorf("%s included %s", name, item.ID)
			}
		}
	}
}

// The claimable queue is not the only order that is a contract: `next` shuffling
// same-priority rows between runs is what an explicitly stable sort prevents.
func TestNextKeepsFileOrderForEqualPriorities(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Work"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"NEXT","title":"one"}
{"type":"task","id":"cccc0003","parent":"cccc0001","state":"NEXT","title":"two"}
{"type":"task","id":"cccc0004","parent":"cccc0001","state":"NEXT","title":"three"}
`
	queries := queriesFrom(t, fixture)
	for attempt := 0; attempt < 5; attempt++ {
		if got := idsOf(queries.NextItems()); !sameIDs(got, "cccc0002", "cccc0003", "cccc0004") {
			t.Fatalf("attempt %d: %v", attempt, got)
		}
	}
}
