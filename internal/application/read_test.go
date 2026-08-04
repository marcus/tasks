package application

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/store"
)

var revisionPattern = regexp.MustCompile(`^s1\.[0-9a-f]{64}$`)

// openScope is TaskFilter.new: the default scope, which lists OPEN and
// AVAILABLE tasks. The zero query.Filter is deliberately not that — it names no
// scope at all — so a test that means "the default" has to say so.
func openScope(t *testing.T) query.Filter {
	t.Helper()
	filter, err := query.NewFilter(query.FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func allScope(t *testing.T) query.Filter {
	t.Helper()
	parsed, err := query.ParseCLI([]string{"--all"})
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Filter()
}

// test_query_methods_return_phase_two_views_without_exposing_a_store
func TestQueryMethodsReturnViewsWithoutExposingAStore(t *testing.T) {
	h := newHarness(t, harnessOptions{archive: archiveFixture})

	items, err := h.app.ListTasks(allScope(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fixGarden, fixFlight, fixPR, fixEval, fixTravel, fixOld, fixPlants, "dead0001"}
	if got := idsOf(items); !equalStrings(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}

	if item, found, _ := h.app.GetTask(fixFlight, false, nil); !found || item.ID != fixFlight {
		t.Fatalf("live lookup = %+v found=%v", item, found)
	}
	if item, found, _ := h.app.GetTask("dead0001", true, nil); !found || item.ID != "dead0001" {
		t.Fatalf("archive lookup = %+v found=%v", item, found)
	}
	if _, found, _ := h.app.GetTask("does-not-exist", false, nil); found {
		t.Fatal("a missing id must be an ordinary not-found, not an error")
	}
	// An archived id is invisible without the archive half, which is the whole
	// point of include_archive being a parameter.
	if _, found, _ := h.app.GetTask("dead0001", false, nil); found {
		t.Fatal("an archived id must not resolve against a live-only read")
	}

	sections, err := h.app.ListSections(nil)
	if err != nil {
		t.Fatal(err)
	}
	sectionIDs := []string{}
	for _, section := range sections {
		sectionIDs = append(sectionIDs, section.String("id"))
	}
	if want := []string{fixInbox, fixWork, fixHome}; !equalStrings(sectionIDs, want) {
		t.Fatalf("sections = %v, want %v", sectionIDs, want)
	}

	agenda, err := h.app.ViewTasks("agenda", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fixFlight, fixEval}; !equalStrings(idsOf(agenda), want) {
		t.Fatalf("agenda = %v, want %v", idsOf(agenda), want)
	}
	if _, err := h.app.ViewTasks("nonsense", nil); err == nil {
		t.Fatal("an unknown view name must be refused")
	}
}

// test_every_application_call_gets_a_fresh_store_instance
func TestEveryApplicationCallGetsAFreshStoreInstance(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	if _, err := h.app.ListTasks(openScope(t), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ViewTasks("inbox", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.app.GetTask(fixGarden, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ListSections(nil); err != nil {
		t.Fatal(err)
	}

	if built := h.built.Load(); built != 4 {
		t.Fatalf("built %d stores, want one per operation (4)", built)
	}
}

// test_live_read_model_keeps_presentation_items_and_canonical_views_on_one_snapshot
func TestLiveReadModelKeepsItemsAndViewsOnOneSnapshot(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	first, err := h.app.ReadTasks(nil)
	if err != nil {
		t.Fatal(err)
	}
	flight, found := first.TaskFor(fixFlight)
	if !found {
		t.Fatal("the read model must resolve a live id")
	}
	if node := first.NodeFor(flight); node == nil || node.Parent == nil || node.Parent.Title != "Work" {
		t.Fatalf("node parent = %+v, want the Work section", node)
	}
	agenda, err := first.ViewTasks("agenda")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fixFlight, fixEval}; !equalStrings(idsOf(agenda), want) {
		t.Fatalf("model agenda = %v, want %v", idsOf(agenda), want)
	}

	// A model is a read that already happened: a later external write cannot
	// reach back into it, and a rebuilt model sees the new bytes.
	h.write(`{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Changed externally"}
`)
	second, err := h.app.ReadTasks(nil)
	if err != nil {
		t.Fatal(err)
	}
	held, _ := first.TaskFor(fixFlight)
	fresh, _ := second.TaskFor(fixFlight)
	if held.Title != "Book flight in Concur" {
		t.Fatalf("held model title = %q, want the bytes it was built over", held.Title)
	}
	if fresh.Title != "Changed externally" {
		t.Fatalf("rebuilt model title = %q", fresh.Title)
	}
}

// test_read_model_reports_staleness_after_an_external_write
func TestReadModelReportsStalenessAfterAnExternalWrite(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	model, err := h.app.ReadTasks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.Stale() {
		t.Fatal("a freshly built model must not report stale")
	}

	h.write(h.read() + `{"type":"task","id":"bbbb0001","parent":"aaaa0009","state":"TODO","title":"External write"}` + "\n")

	if !model.Stale() {
		t.Fatal("an external write must mark the held model stale")
	}
	rebuilt, err := h.app.ReadTasks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Stale() {
		t.Fatal("a rebuilt model over the new bytes is current")
	}
}

// test_application_injects_one_today_into_list_view_and_resource_reads
func TestApplicationInjectsOneTodayIntoListViewAndResourceReads(t *testing.T) {
	const records = `{"type":"meta","version":2}
{"type":"section","id":"dd000001","title":"Work"}
{"type":"task","id":"dd000002","parent":"dd000001","state":"NEXT","title":"Tomorrow","scheduled":"2026-07-15"}
`
	before := newHarness(t, harnessOptions{live: records, now: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)})
	onDate := newHarness(t, harnessOptions{live: records, now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)})

	items, err := before.app.ListTasks(openScope(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("a scheduled task is not listed before its date: %v", idsOf(items))
	}
	next, _ := before.app.ViewTasks("next", nil)
	if len(next) != 0 {
		t.Fatalf("next = %v before the scheduled date", idsOf(next))
	}
	blocked := before.task("dd000002")
	queries, err := before.app.Queries(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if availability := queries.AvailabilityFor(blocked); availability.Available() {
		t.Fatal("the task must be unavailable before its scheduled date")
	} else if availability.Reason != "scheduled" {
		t.Fatalf("availability reason = %q, want scheduled", availability.Reason)
	}

	items, _ = onDate.app.ListTasks(openScope(t), nil)
	if want := []string{"dd000002"}; !equalStrings(idsOf(items), want) {
		t.Fatalf("list on the date = %v, want %v", idsOf(items), want)
	}
	next, _ = onDate.app.ViewTasks("next", nil)
	if want := []string{"dd000002"}; !equalStrings(idsOf(next), want) {
		t.Fatalf("next on the date = %v, want %v", idsOf(next), want)
	}

	// The read model reads the same clock the direct methods do.
	model, err := before.app.ReadTasks(nil)
	if err != nil {
		t.Fatal(err)
	}
	modelNext, _ := model.ViewTasks("next")
	if len(modelNext) != 0 {
		t.Fatalf("the read model must inherit the same today: %v", idsOf(modelNext))
	}
}

// test_checked_results_carry_data_and_global_revision_from_one_snapshot
func TestCheckedResultsCarryDataAndGlobalRevisionFromOneSnapshot(t *testing.T) {
	h := newHarness(t, harnessOptions{archive: archiveFixture})

	first := h.app.ListTasksResult(allScope(t), nil)
	if !first.OK() {
		t.Fatalf("status = %q errors = %+v", first.Status, first.Errors)
	}
	if !revisionPattern.MatchString(first.StoreRevision) {
		t.Fatalf("revision = %q", first.StoreRevision)
	}
	want := []string{fixGarden, fixFlight, fixPR, fixEval, fixTravel, fixOld, fixPlants, "dead0001"}
	if got := idsOf(first.Data); !equalStrings(got, want) {
		t.Fatalf("data = %v, want %v", got, want)
	}

	// The revision is derived from BOTH files, so an archive-only edit changes
	// it — that is what makes it usable as a change token.
	if err := os.WriteFile(h.archive, []byte(`{"type":"meta","version":2}
{"type":"task","id":"dead0001","state":"DONE","title":"Archived update"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	second := h.app.ListTasksResult(allScope(t), nil)
	if second.StoreRevision == first.StoreRevision {
		t.Fatal("an archive edit must change the global revision")
	}
	if last := second.Data[len(second.Data)-1]; last.Title != "Archived update" {
		t.Fatalf("archive title = %q", last.Title)
	}
}

// test_checked_results_return_typed_safe_invalid_and_not_found_outcomes
func TestCheckedResultsReturnTypedSafeInvalidAndNotFoundOutcomes(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	missing := h.app.GetTaskResult("ffffffff", store.SourceLive, nil)
	if !missing.NotFound() {
		t.Fatalf("status = %q", missing.Status)
	}
	if missing.Data.ID != "" {
		t.Fatalf("a not-found result must carry no data: %+v", missing.Data)
	}
	if !revisionPattern.MatchString(missing.StoreRevision) {
		t.Fatalf("a not-found read still reports the revision it read: %q", missing.StoreRevision)
	}

	h.write("not json\n")
	invalid := h.app.ListSectionsResult(nil)
	if !invalid.StoreInvalid() {
		t.Fatalf("status = %q", invalid.Status)
	}
	if invalid.Data != nil {
		t.Fatalf("an invalid store must yield no data: %+v", invalid.Data)
	}
	if len(invalid.Errors) == 0 {
		t.Fatal("an invalid store must report why")
	}
	first := invalid.Errors[0]
	if first.Source != store.SourceLive || first.Line != 1 {
		t.Fatalf("error = %+v, want line 1 of the live file", first)
	}
	// A diagnostic must never leak the configured path: it is the one field an
	// API response would hand to a caller who has no business knowing it.
	if containsPath(first.Message, h.org) {
		t.Fatalf("diagnostic leaks the store path: %q", first.Message)
	}
}

func containsPath(message, path string) bool {
	return len(path) > 0 && len(message) >= len(path) && regexp.MustCompile(regexp.QuoteMeta(path)).MatchString(message)
}

// test_checked_status_treats_a_missing_archive_as_empty_but_requires_live_file
func TestCheckedStatusTreatsAMissingArchiveAsEmptyButRequiresLiveFile(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	status := h.app.ReadStatusResult(nil)
	if !status.OK() {
		t.Fatalf("a store without an archive is healthy: %q %+v", status.Status, status.Errors)
	}

	if err := os.Remove(h.org); err != nil {
		t.Fatal(err)
	}
	missing := h.app.ReadStatusResult(nil)
	if !missing.StoreInvalid() {
		t.Fatalf("status = %q", missing.Status)
	}
	if len(missing.Errors) == 0 || missing.Errors[0].Message != "file not found" {
		t.Fatalf("errors = %+v", missing.Errors)
	}
}

// test_checked_task_lookup_is_exact_to_the_requested_source
func TestCheckedTaskLookupIsExactToTheRequestedSource(t *testing.T) {
	h := newHarness(t, harnessOptions{archive: `{"type":"meta","version":2}
{"type":"task","id":"aaaa0004","state":"DONE","title":"Archived flight"}
`})

	live := h.app.GetTaskResult(fixFlight, store.SourceLive, nil)
	archived := h.app.GetTaskResult(fixFlight, store.SourceArchive, nil)

	if !live.OK() || live.Data.Source != store.SourceLive || live.Data.Title != "Book flight in Concur" {
		t.Fatalf("live = %q %+v", live.Status, live.Data)
	}
	if !archived.OK() || archived.Data.Source != store.SourceArchive || archived.Data.Title != "Archived flight" {
		t.Fatalf("archive = %q %+v", archived.Status, archived.Data)
	}
	// Ruby raises on an unknown source. A transport-facing layer that panics on
	// a query parameter is a worse answer than one that refuses it.
	unknown := h.app.GetTaskResult(fixFlight, store.Source("other"), nil)
	if !unknown.NotFound() || len(unknown.Errors) == 0 {
		t.Fatalf("unknown source = %q %+v", unknown.Status, unknown.Errors)
	}
}

// Projects reach the application unchanged, and the checked variant carries the
// same change token every other API-grade read does.
func TestProjectReadsCarryTheSameChangeToken(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	projects, err := h.app.ListProjects(nil)
	if err != nil {
		t.Fatal(err)
	}
	result := h.app.ListProjectsResult(nil)
	if !result.OK() {
		t.Fatalf("status = %q", result.Status)
	}
	if len(result.Data) != len(projects) {
		t.Fatalf("checked projects = %d, direct = %d", len(result.Data), len(projects))
	}
	if !revisionPattern.MatchString(result.StoreRevision) {
		t.Fatalf("revision = %q", result.StoreRevision)
	}
	if missing := h.app.ProjectResult("ffffffff", nil); !missing.NotFound() {
		t.Fatalf("an id that is not a project is not_found, got %q", missing.Status)
	}
}
