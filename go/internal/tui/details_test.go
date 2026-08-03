package tui

import (
	"strings"
	"testing"
)

func detailFor(t *testing.T, harness *modelHarness, id string) string {
	t.Helper()
	for _, item := range harness.model.ReadModel().Items() {
		if item.ID == id {
			content := BuildTaskDetails(PlainStyler{}, harness.model.ReadModel().Queries(),
				item, 60, harness.model.projectNameOf(item))
			return strings.Join(content.Lines, "\n")
		}
	}
	t.Fatalf("no item %s", id)
	return ""
}

func TestTaskDetailsShowsTheFieldsThatArePresent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	text := detailFor(t, harness, fixFlight)
	for _, want := range []string{
		"Book flight in Concur",
		"state      NEXT",
		"priority   [#A]",
		"deadline   2026-07-02",
		"project    Work",
		"contexts   @computer",
		"id         aaaa0004",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("detail panel is missing %q:\n%s", want, text)
		}
	}
}

func TestTaskDetailsOmitsFieldsThatAreAbsent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	text := detailFor(t, harness, fixPlants)
	for _, unwanted := range []string{"deadline", "available from", "repeats", "lead time", "closed"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("detail panel invented a %q row:\n%s", unwanted, text)
		}
	}
}

func TestTaskDetailsShowsTheBodyAsADescription(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	text := detailFor(t, harness, fixTravel)
	if !strings.Contains(text, "description") || !strings.Contains(text, "Some note line.") {
		t.Fatalf("the task body did not reach the panel:\n%s", text)
	}
}

func TestTaskDetailsSpellsOutAnAvailabilityBlock(t *testing.T) {
	live := strings.ReplaceAll(fixtureStore,
		`"title":"Water the plants","tags":["@home"]`,
		`"title":"Water the plants","tags":["@home"],"scheduled":"2099-01-01"`)
	harness := newModelHarness(t, harnessOptions{live: live})
	text := detailFor(t, harness, fixPlants)
	if !strings.Contains(text, "availability") || !strings.Contains(text, "unavailable until") {
		t.Fatalf("a deferred task did not say why it is unavailable:\n%s", text)
	}
}

func TestTaskDetailsRendersADelegationBlockInRecordOrder(t *testing.T) {
	live := strings.ReplaceAll(fixtureStore,
		`"title":"Water the plants","tags":["@home"]`,
		`"title":"Water the plants","tags":["@home"],"delegation":{"kind":"human","status":"delegated","assignee":"pat@example.com","at":"2026-07-01T00:00:00Z","work_ref":"https://example.com/pr/1"}`)
	harness := newModelHarness(t, harnessOptions{live: live})
	text := detailFor(t, harness, fixPlants)
	if !strings.Contains(text, "delegation") {
		t.Fatalf("the delegation block is missing:\n%s", text)
	}
	// Match the padded label column, not a bare substring: "at" also occurs
	// inside "Water the plants".
	order := []string{"  kind    ", "  status  ", "  assignee", "  at      ", "  work ref"}
	position := -1
	for _, key := range order {
		next := strings.Index(text, key)
		if next < 0 {
			t.Fatalf("delegation field %q is missing:\n%s", key, text)
		}
		if next < position {
			t.Fatalf("delegation field %q is out of record order:\n%s", key, text)
		}
		position = next
	}
}

func TestDelegationTextStripsControlCharacters(t *testing.T) {
	// A record written by an older binary or a merge can carry an escape. The
	// TUI must never be corrupted by data it merely displays: an escape in an
	// assignee bleeds reverse video into every following row and the border.
	if got := delegationText("pat\x1b[7m@example.com"); strings.Contains(got, "\x1b") {
		t.Fatalf("an escape survived: %q", got)
	}
}

func TestDelegationAssigneeCutsTheDomainFirst(t *testing.T) {
	styler := PlainStyler{}
	if got := delegationAssignee(styler, "pat@example.com"); got != "pat@example.com" {
		t.Fatalf("a short address was truncated: %q", got)
	}
	got := delegationAssignee(styler, "pat@an-extremely-long-domain.example.com")
	if !strings.HasPrefix(got, "pat@") || !strings.HasSuffix(got, "…") {
		t.Fatalf("a long address degraded to %q, not to local@…", got)
	}
	if styler.Width(got) > DelegationAssigneeWidth {
		t.Fatalf("degraded assignee is %d cells wide", styler.Width(got))
	}
}

func TestProjectDetailsRollsUpTheSection(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewProjects)
	var text string
	for _, row := range harness.model.Rows() {
		if row.Project != nil && row.Project.Title == "Work" {
			content := BuildProjectDetails(PlainStyler{}, harness.model.ReadModel().Queries(),
				*row.Project, harness.model.projectTasks(*row.Project), 60)
			text = strings.Join(content.Lines, "\n")
		}
	}
	if text == "" {
		t.Fatal("no Work project row")
	}
	for _, want := range []string{"Work", "kind", "open", "next", "open tasks", "Book flight in Concur"} {
		if !strings.Contains(text, want) {
			t.Errorf("project panel is missing %q:\n%s", want, text)
		}
	}
}

func TestOpeningDetailsThenReselectingKeepsScrollForTheSameTask(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	harness.model.OpenDetail()
	harness.model.Panel().Lines = linesOf(200)
	harness.model.Panel().ScrollPage(1, 20)
	before := harness.model.Panel().Scroll

	harness.model.RefreshRows() // an ordinary refresh, same selection
	if harness.model.Panel().Scroll != before {
		t.Fatalf("a refresh reset panel scroll from %d to %d",
			before, harness.model.Panel().Scroll)
	}
}

func TestMovingToAnotherTaskResetsPanelScroll(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	harness.model.OpenDetail()
	harness.model.Panel().Lines = linesOf(200)
	harness.model.Panel().ScrollPage(1, 20)

	harness.selectRowByID(fixFlight)
	if harness.model.Panel().Scroll != 0 {
		t.Fatalf("moving to another task kept scroll at %d", harness.model.Panel().Scroll)
	}
	if harness.model.Panel().Identity != fixFlight {
		t.Fatalf("the panel did not follow the selection: %q", harness.model.Panel().Identity)
	}
}

func TestEnterTogglesTheDetailPanel(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	harness.pressTypeEnter()
	if harness.model.Panel() == nil {
		t.Fatal("enter did not open the panel")
	}
	harness.pressTypeEnter()
	if harness.model.Panel() != nil {
		t.Fatal("enter did not close the panel")
	}
}

func TestSelectingAProjectHeaderOpensTheProjectPanel(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewProjects)
	for index, row := range harness.model.Rows() {
		if row.Project != nil {
			harness.model.selectRow(index)
			break
		}
	}
	harness.model.OpenDetail()
	if harness.model.Panel() == nil || harness.model.Panel().Kind != PanelProjectDetail {
		t.Fatalf("a project header did not open the project panel: %+v", harness.model.Panel())
	}
}
