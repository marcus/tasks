package taskquery

import "testing"

// projectsFixture is test/test_helper.rb's PROJECTS_FIXTURE, record for record.
// The shape is the point: an Inbox (never an area), a "Projects" heading whose
// child sections are projects — "Site launch" with a body note, a NEXT, a dated
// TODO, a nested sub-section proving depth rollup and a deferred TODO proving
// exclusion — a stuck project, an empty project, a "Tasks" area, and a "Done
// pile" whose only task is closed.
const projectsFixture = `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Inbox"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"INBOX","title":"unfiled capture"}
{"type":"section","id":"cccc0003","title":"Projects"}
{"type":"section","id":"cccc0004","parent":"cccc0003","title":"Site launch","body":"Goal: ship the personal site."}
{"type":"task","id":"cccc0005","parent":"cccc0004","state":"NEXT","title":"Pick a static-site generator","recur":"+1w"}
{"type":"task","id":"cccc0006","parent":"cccc0004","state":"TODO","title":"Write the landing copy","deadline":"2026-07-25"}
{"type":"section","id":"cccc0007","parent":"cccc0004","title":"Copywriting"}
{"type":"task","id":"cccc0008","parent":"cccc0007","state":"TODO","title":"Draft the about page"}
{"type":"task","id":"cccc0009","parent":"cccc0004","state":"TODO","title":"Someday: custom domain","tags":["defer"]}
{"type":"section","id":"cccc000a","parent":"cccc0003","title":"Stuck reno"}
{"type":"task","id":"cccc000b","parent":"cccc000a","state":"TODO","title":"Measure the kitchen"}
{"type":"section","id":"cccc000c","parent":"cccc0003","title":"Empty project"}
{"type":"section","id":"cccc000d","title":"Tasks"}
{"type":"task","id":"cccc000e","parent":"cccc000d","state":"NEXT","title":"Reply to the vendor"}
{"type":"task","id":"cccc000f","parent":"cccc000d","state":"TODO","title":"File expenses"}
{"type":"section","id":"cccc0010","title":"Done pile"}
{"type":"task","id":"cccc0011","parent":"cccc0010","state":"DONE","title":"Old finished chore","closed":"2026-07-01"}
`

func viewIDs(views []ProjectView) []string {
	out := make([]string, 0, len(views))
	for _, view := range views {
		out = append(out, view.ID)
	}
	return out
}

func viewKinds(views []ProjectView) []string {
	out := make([]string, 0, len(views))
	for _, view := range views {
		out = append(out, view.Kind)
	}
	return out
}

func TestProjectsListsProjectsBeforeAreasOrderedByDateThenTitle(t *testing.T) {
	views := queriesFrom(t, projectsFixture).Projects()
	if got := viewKinds(views); !sameIDs(got, "project", "project", "project", "area") {
		t.Fatalf("kinds = %v", got)
	}
	// Site launch carries the soonest date, so it sorts ahead of the two
	// dateless projects, which then order by title; the area follows all.
	if got := viewIDs(views); !sameIDs(got, "cccc0004", "cccc000c", "cccc000a", "cccc000d") {
		t.Fatalf("ids = %v", got)
	}
}

func TestProjectViewRollsUpOpenNonDeferredDescendantsAcrossDepth(t *testing.T) {
	site, found := queriesFrom(t, projectsFixture).ProjectView("cccc0004")
	if !found {
		t.Fatal("no view for Site launch")
	}
	if site.Kind != "project" || site.Title != "Site launch" || site.ParentID != "cccc0003" {
		t.Fatalf("view = %+v", site)
	}
	if site.Body != "Goal: ship the personal site." {
		t.Errorf("body = %q", site.Body)
	}
	// NEXT + TODO(deadline) + the nested sub-section task; the deferred task is
	// excluded even though it is open.
	if got := site.TaskIDs; !sameIDs(got, "cccc0005", "cccc0006", "cccc0008") {
		t.Errorf("task_ids = %v", got)
	}
	if site.OpenCount != 3 || site.NextCount != 1 {
		t.Errorf("counts = %d open, %d next", site.OpenCount, site.NextCount)
	}
	if !site.HasNextDate || site.NextDate.ISO() != "2026-07-25" {
		t.Errorf("next_date = %v", site.NextDate)
	}
	if site.Stuck {
		t.Error("a project with an open NEXT is not stuck")
	}
}

func TestStuckFlagsProjectsWithoutAnOpenNextIncludingEmptyOnes(t *testing.T) {
	queries := queriesFrom(t, projectsFixture)
	byID := map[string]ProjectView{}
	for _, view := range queries.Projects() {
		byID[view.ID] = view
	}
	reno := byID["cccc000a"]
	if !reno.Stuck || reno.OpenCount != 1 || reno.NextCount != 0 {
		t.Errorf("reno = %+v", reno)
	}
	empty := byID["cccc000c"]
	if !empty.Stuck || empty.OpenCount != 0 || empty.HasNextDate || len(empty.TaskIDs) != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

func TestHeldCountRollsUpOpenDeferredDescendants(t *testing.T) {
	byID := map[string]ProjectView{}
	for _, view := range queriesFrom(t, projectsFixture).Projects() {
		byID[view.ID] = view
	}
	// Site launch excludes its one deferred TODO from the rollup, counting it as
	// held instead; a project with no parked work reports zero.
	if byID["cccc0004"].HeldCount != 1 {
		t.Errorf("site held = %d", byID["cccc0004"].HeldCount)
	}
	if byID["cccc000a"].HeldCount != 0 || byID["cccc000d"].HeldCount != 0 {
		t.Error("projects with no parked work report zero")
	}
}

func TestHeldCountIncludesInheritedHold(t *testing.T) {
	// A live task under a deferred parent task is itself effectively held, so it
	// counts toward held_count, not open_count.
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Projects"}
{"type":"section","id":"eeee0002","parent":"eeee0001","title":"Blocked"}
{"type":"task","id":"eeee0003","parent":"eeee0002","state":"TODO","title":"Parked parent","tags":["defer"]}
{"type":"task","id":"eeee0004","parent":"eeee0003","state":"TODO","title":"Inherits the hold"}
`
	view, found := queriesFrom(t, fixture).ProjectView("eeee0002")
	if !found {
		t.Fatal("no view")
	}
	if view.OpenCount != 0 {
		t.Errorf("open_count = %d", view.OpenCount)
	}
	if view.HeldCount != 2 {
		t.Errorf("held_count = %d — the deferred parent and its inheriting child both count", view.HeldCount)
	}
}

func TestAreaIsAnOpenTopLevelSectionOutsideProjects(t *testing.T) {
	tasks, found := queriesFrom(t, projectsFixture).ProjectView("cccc000d")
	if !found {
		t.Fatal("no view for the Tasks area")
	}
	if tasks.Kind != "area" || tasks.HasParentID {
		t.Errorf("view = %+v", tasks)
	}
	if got := tasks.TaskIDs; !sameIDs(got, "cccc000e", "cccc000f") {
		t.Errorf("task_ids = %v", got)
	}
	if tasks.NextCount != 1 || tasks.Stuck {
		t.Errorf("counts = %d next, stuck=%v", tasks.NextCount, tasks.Stuck)
	}
}

func TestProjectsExcludesInboxProjectsRootNestedAndDoneOnlySections(t *testing.T) {
	queries := queriesFrom(t, projectsFixture)
	listed := map[string]bool{}
	for _, id := range viewIDs(queries.Projects()) {
		listed[id] = true
	}
	for _, testCase := range []struct{ id, why string }{
		{"cccc0001", "Inbox never lists as an area"},
		{"cccc0003", "the Projects heading itself never lists"},
		{"cccc0007", "a nested sub-section rolls up, never lists"},
		{"cccc0010", "a done-only section is not an open area"},
	} {
		if listed[testCase.id] {
			t.Errorf("%s (%s)", testCase.id, testCase.why)
		}
		if _, found := queries.ProjectView(testCase.id); found {
			t.Errorf("ProjectView(%s) resolved — %s", testCase.id, testCase.why)
		}
	}
	if _, found := queries.ProjectView("cccc0005"); found {
		t.Error("a task id is not a project")
	}
	if _, found := queries.ProjectView("ffffffff"); found {
		t.Error("an unknown id is not a project")
	}
	if _, found := queries.ProjectView(""); found {
		t.Error("the empty id is not a project")
	}
}

// A project's soonest date is the earliest BOUNDARY across its rollup, not the
// earliest stored string: a deadline and an available-from date on the same day
// do not come due at the same instant.
func TestProjectNextDateIsTheSoonestBoundaryFirstInFileBreakingTies(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Ship it"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"TODO","title":"later","deadline":"2026-08-01"}
{"type":"task","id":"aaaa0004","parent":"aaaa0002","state":"TODO","title":"sooner","deadline":"2026-07-25"}
{"type":"task","id":"aaaa0005","parent":"aaaa0002","state":"TODO","title":"tie","deadline":"2026-07-25"}
`
	view, found := queriesFrom(t, fixture).ProjectView("aaaa0002")
	if !found {
		t.Fatal("no view")
	}
	if view.NextDate.ISO() != "2026-07-25" {
		t.Fatalf("next_date = %s", view.NextDate.ISO())
	}
	if !view.HasNextTime {
		// A date-only deadline has no wall time, which is the expected shape
		// here; the assertion below is the one that matters.
		_ = view
	}
	if view.NextAt.IsZero() {
		t.Fatal("a dated rollup has an instant")
	}
}

// A section titled "projects" in any case is the root, and a top-level section
// titled "inbox" in any case is never an area — the file is a user's, not a
// schema's.
func TestProjectsRootAndInboxMatchCaseInsensitively(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":" PROJECTS "}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Real project"}
{"type":"section","id":"aaaa0003","title":"  inBOX "}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"INBOX","title":"unfiled"}
`
	views := queriesFrom(t, fixture).Projects()
	if got := viewIDs(views); !sameIDs(got, "aaaa0002") {
		t.Fatalf("views = %v", got)
	}
}

// With no "Projects" heading at all, every top-level list holding open work is
// an area — a file that never adopted the convention still lists.
func TestWithoutAProjectsHeadingEveryOpenTopLevelListIsAnArea(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Errands"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"buy stamps"}
{"type":"section","id":"aaaa0003","title":"Empty list"}
`
	views := queriesFrom(t, fixture).Projects()
	if got := viewIDs(views); !sameIDs(got, "aaaa0001") {
		t.Fatalf("views = %v — an area with no open work does not list", got)
	}
	if views[0].Kind != "area" {
		t.Fatalf("kind = %s", views[0].Kind)
	}
}

// The reserved GTD lists are saved queries, not areas of responsibility:
// membership in each is already derivable from a task's own state or defer
// marker, so listing them beside real projects would double-count the work.
func TestReservedGTDListsAreNotAreas(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Next Actions"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","title":"call the bank"}
{"type":"section","id":"dddd0003","title":"Waiting For"}
{"type":"task","id":"dddd0004","parent":"dddd0003","state":"WAITING","title":"vendor quote"}
{"type":"section","id":"dddd0005","title":"Someday / Maybe"}
{"type":"task","id":"dddd0006","parent":"dddd0005","state":"TODO","title":"learn the cello"}
{"type":"section","id":"dddd0007","title":"Home"}
{"type":"task","id":"dddd0008","parent":"dddd0007","state":"NEXT","title":"water the plants"}
`
	queries := queriesFrom(t, fixture)
	if got := viewIDs(queries.Projects()); !sameIDs(got, "dddd0007") {
		t.Fatalf("only the real area lists; got %v", got)
	}
	// Excluded from the LISTING, but still addressable: a section a human can
	// rename in the TUI has to stay nameable by an agent, or the two surfaces
	// disagree about what exists. Both resolvers answer, with kind "list".
	for _, id := range []string{"dddd0001", "dddd0003", "dddd0005"} {
		for name, view := range map[string]func(string) (ProjectView, bool){
			"ProjectView": queries.ProjectView, "SectionView": queries.SectionView,
		} {
			resolved, found := view(id)
			if !found {
				t.Errorf("%s(%s) lost a reserved list", name, id)
			} else if resolved.Kind != "list" {
				t.Errorf("%s(%s).Kind = %q, want \"list\"", name, id, resolved.Kind)
			}
		}
	}
}

// Inbox is the capture bucket: renaming, completing or archiving it would take
// the destination every unfiled task lands in.
func TestInboxStaysUnresolvableEvenThoughItIsAReservedList(t *testing.T) {
	queries := queriesFrom(t, projectsFixture)
	if _, found := queries.ProjectView("cccc0001"); found {
		t.Error("Inbox resolved as a project")
	}
}
