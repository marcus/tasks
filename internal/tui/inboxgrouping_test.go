package tui

import (
	"strconv"
	"strings"
	"testing"
)

// Issue #13: intake is triaged by THEME, not by file line or by rank alone.
// Both blocks of the Inbox tab — the approvals queue and the accepted captures —
// group their rows by the project the task sits under, with everything unfiled
// gathered into one trailing group.
//
// The fixture is in the DFS pre-order the store enforces, and it still puts the
// two blocks' rows in the wrong company:
//
//   - the approvals queue is ranked A, B, C, so the Bakery flour order lands
//     BETWEEN the two Aviator proposals when the queue is left flat;
//   - the two Aviator proposals rank in the reverse of both their line order and
//     their alphabetical order, so a group that re-sorted its own bucket by
//     either would be caught;
//   - the unfiled captures sit at the head of the file, so they lead the
//     accepted block when it is left in line order.
//
// Aviator carries the sooner deadline, so it also leads Bakery in the Projects
// tab's own order.
const intakeGroupingFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"cccc0004","parent":"aaaa0001","state":"INBOX","title":"unfiled scrap"}
{"type":"task","id":"cccc0008","parent":"aaaa0001","state":"PROPOSED","title":"unfiled idea"}
{"type":"section","id":"aaaa0010","title":"Projects"}
{"type":"section","id":"aaaa0011","parent":"aaaa0010","title":"Aviator"}
{"type":"task","id":"cccc0001","parent":"aaaa0011","state":"INBOX","title":"aviator radio check"}
{"type":"task","id":"cccc0003","parent":"aaaa0011","state":"INBOX","title":"aviator logbook"}
{"type":"task","id":"cccc0005","parent":"aaaa0011","state":"PROPOSED","priority":"C","title":"aviator checkride prep"}
{"type":"task","id":"cccc0007","parent":"aaaa0011","state":"PROPOSED","priority":"A","title":"aviator oil change"}
{"type":"task","id":"cccc0009","parent":"aaaa0011","state":"NEXT","title":"aviator flight review","deadline":"2026-07-16"}
{"type":"section","id":"aaaa0012","parent":"aaaa0010","title":"Bakery"}
{"type":"task","id":"cccc0002","parent":"aaaa0012","state":"INBOX","title":"bakery chore"}
{"type":"task","id":"cccc0006","parent":"aaaa0012","state":"PROPOSED","priority":"B","title":"bakery flour order"}
{"type":"task","id":"cccc000a","parent":"aaaa0012","state":"NEXT","title":"bakery oven service","deadline":"2026-08-20"}
`

// intakeLayout is what the two intake blocks contain, in painted order: for each
// block, the group headings it paints and the task titles under each of them.
type intakeLayout struct {
	approvals []string
	accepted  []string
}

// intakeSections splits the rendered rows into the two blocks and records what
// each one paints, headings included, in order. Headings are chrome, so they are
// told apart from task rows by being unselectable rather than by their text.
func intakeSections(t *testing.T, harness *modelHarness) intakeLayout {
	t.Helper()
	layout := intakeLayout{}
	block := ""
	for _, row := range harness.model.Rows() {
		text := strings.TrimSpace(row.Text)
		switch {
		case strings.HasPrefix(text, "APPROVALS"):
			block = "approvals"
			continue
		case strings.HasPrefix(text, "INBOX"):
			block = "accepted"
			continue
		}
		if text == "" {
			continue
		}
		entry := text
		if row.Item != nil {
			entry = row.Item.Title
		} else {
			entry = "# " + strings.Fields(text)[0]
		}
		switch block {
		case "approvals":
			layout.approvals = append(layout.approvals, entry)
		case "accepted":
			layout.accepted = append(layout.accepted, entry)
		}
	}
	return layout
}

// intakeHeadings is the badge each group heading in one block carries, keyed by
// its label. A rule is `label ─────… count`, so the label is the first field and
// the badge is the last — the fixtures here keep project titles to one word.
func intakeHeadings(t *testing.T, harness *modelHarness, want string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	block := ""
	for _, row := range harness.model.Rows() {
		text := strings.TrimSpace(row.Text)
		switch {
		case strings.HasPrefix(text, "APPROVALS"):
			block = "approvals"
			continue
		case strings.HasPrefix(text, "INBOX"):
			block = "accepted"
			continue
		}
		if row.Item != nil || text == "" || block != want {
			continue
		}
		fields := strings.Fields(text)
		badge, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			t.Fatalf("group heading %q carries no count: %v", text, err)
		}
		counts[fields[0]] = badge
	}
	return counts
}

func equalStrings(got, want []string) bool {
	return strings.Join(got, "|") == strings.Join(want, "|")
}

func TestInboxGroupsBothIntakeBlocksByProject(t *testing.T) {
	for _, mode := range []struct {
		name   string
		filter string
	}{
		{name: "tree"},
		{name: "flat under search", filter: "a"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			harness := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
			harness.model.SwitchView(ViewInbox)
			if mode.filter != "" {
				harness.model.filter = mode.filter
				harness.model.RefreshRows()
			}
			layout := intakeSections(t, harness)

			// The two Aviator proposals are neighbours here even though the
			// queue's own ranking puts the Bakery flour order between them —
			// and INSIDE the group they keep that ranking, so the A-priority
			// oil change leads the C-priority checkride prep even though the
			// checkride prep comes first by both line and title.
			wantApprovals := []string{
				"# Aviator", "aviator oil change", "aviator checkride prep",
				"# Bakery", "bakery flour order",
				"# Inbox", "unfiled idea",
			}
			if !equalStrings(layout.approvals, wantApprovals) {
				t.Errorf("APPROVALS = %v\nwant        %v\n\n%s",
					layout.approvals, wantApprovals, rowTexts(harness))
			}
			wantAccepted := []string{
				"# Aviator", "aviator radio check", "aviator logbook",
				"# Bakery", "bakery chore",
				"# Inbox", "unfiled scrap",
			}
			if !equalStrings(layout.accepted, wantAccepted) {
				t.Errorf("INBOX = %v\nwant     %v\n\n%s",
					layout.accepted, wantAccepted, rowTexts(harness))
			}
		})
	}
}

// The unfiled remainder is ONE group and it is the LAST one, in both blocks —
// never interleaved with the themed groups, whatever dates it carries. A capture
// with no project is the thing you decide about after the themed work, not a
// wall you hit halfway down the list.
func TestInboxKeepsUnfiledIntakeInOneTrailingGroup(t *testing.T) {
	// A dated unfiled capture, and one sitting under no section at all. The file
	// stays in DFS pre-order: the Inbox section's own rows sit inside it, and the
	// sectionless task closes the file at the root.
	live := strings.Replace(intakeGroupingFixture,
		`{"type":"section","id":"aaaa0010","title":"Projects"}`,
		`{"type":"task","id":"cccc000b","parent":"aaaa0001","state":"INBOX","title":"urgent unfiled","deadline":"2026-07-15"}
{"type":"section","id":"aaaa0010","title":"Projects"}`, 1) +
		`{"type":"task","id":"cccc000c","state":"INBOX","title":"sectionless scrap"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewInbox)
	layout := intakeSections(t, harness)

	// A dated unfiled capture would lead every project on the soonest-date rule;
	// the unfiled bucket goes last regardless.
	want := []string{
		"# Aviator", "aviator radio check", "aviator logbook",
		"# Bakery", "bakery chore",
		"# Inbox", "unfiled scrap", "urgent unfiled", "sectionless scrap",
	}
	if !equalStrings(layout.accepted, want) {
		t.Fatalf("INBOX = %v\nwant     %v\n\n%s", layout.accepted, want, rowTexts(harness))
	}
	// A task under the file's Inbox section and a task under no section at all
	// are the same thing to a reader triaging them, so they share ONE heading.
	if headings := strings.Count(strings.Join(layout.accepted, "\n"), "# Inbox"); headings != 1 {
		t.Fatalf("the unfiled remainder painted %d headings, want 1:\n%s",
			headings, rowTexts(harness))
	}
}

// A group with nothing in it is not painted: the approvals block and the
// accepted block hold different rows, so a project with proposals and no
// captures must not leave an empty heading in the block below it.
func TestInboxOmitsEmptyProjectGroups(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0010","title":"Projects"}
{"type":"section","id":"aaaa0011","parent":"aaaa0010","title":"Aviator"}
{"type":"task","id":"cccc0001","parent":"aaaa0011","state":"PROPOSED","title":"aviator proposal"}
{"type":"task","id":"cccc0003","parent":"aaaa0011","state":"NEXT","title":"aviator work"}
{"type":"section","id":"aaaa0012","parent":"aaaa0010","title":"Bakery"}
{"type":"task","id":"cccc0002","parent":"aaaa0012","state":"INBOX","title":"bakery capture"}
{"type":"task","id":"cccc0004","parent":"aaaa0012","state":"NEXT","title":"bakery work"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewInbox)
	layout := intakeSections(t, harness)

	// Bakery has no proposal and Aviator has no capture, so neither leaves a
	// heading standing over nothing in the other block.
	if want := []string{"# Aviator", "aviator proposal"}; !equalStrings(layout.approvals, want) {
		t.Errorf("APPROVALS = %v\nwant        %v\n\n%s",
			layout.approvals, want, rowTexts(harness))
	}
	if want := []string{"# Bakery", "bakery capture"}; !equalStrings(layout.accepted, want) {
		t.Errorf("INBOX = %v\nwant     %v\n\n%s", layout.accepted, want, rowTexts(harness))
	}
}

// A group heading counts TASKS, the same rule the section badge above it counts
// by — not the rows it painted. Tree mode rides a non-matching descendant along
// under a matching anchor, and folding an anchor hides rows without emptying the
// group; a heading that counted rows would overcount in the first case and
// shrink in the second, and the headings would stop summing to the badge.
func TestInboxGroupHeadingsCountTasksNotRows(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0010","title":"Projects"}
{"type":"section","id":"aaaa0011","parent":"aaaa0010","title":"Aviator"}
{"type":"task","id":"cccc0001","parent":"aaaa0011","state":"INBOX","title":"aviator capture"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"NEXT","title":"aviator rider"}
{"type":"task","id":"cccc0003","parent":"aaaa0011","state":"INBOX","title":"aviator second capture"}
{"type":"section","id":"aaaa0012","parent":"aaaa0010","title":"Bakery"}
{"type":"task","id":"cccc0004","parent":"aaaa0012","state":"INBOX","title":"bakery capture"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewInbox)
	badge := harness.model.intakeCounts(harness.model.filteredItems()).Inbox

	// The Aviator block paints three rows — two captures and the NEXT rider
	// riding along under the first — and stands for two inbox tasks.
	headings := intakeHeadings(t, harness, "accepted")
	if headings["Aviator"] != 2 || headings["Bakery"] != 1 {
		t.Fatalf("group headings = %v, want Aviator 2 and Bakery 1:\n%s",
			headings, rowTexts(harness))
	}
	if total := headings["Aviator"] + headings["Bakery"]; total != badge {
		t.Fatalf("the headings count %d between them, the INBOX badge says %d:\n%s",
			total, badge, rowTexts(harness))
	}

	harness.model.collapsed["cccc0001"] = true
	harness.model.RefreshRows()
	folded := intakeHeadings(t, harness, "accepted")
	if folded["Aviator"] != headings["Aviator"] || folded["Bakery"] != headings["Bakery"] {
		t.Fatalf("folding an anchor moved the headings from %v to %v:\n%s",
			headings, folded, rowTexts(harness))
	}
	if after := harness.model.intakeCounts(harness.model.filteredItems()).Inbox; after != badge {
		t.Fatalf("folding an anchor moved the INBOX badge from %d to %d", badge, after)
	}
}

// The flat path a `/` search drops the view into counts the same way: one row is
// one task there, so the two modes agree on every badge.
func TestInboxGroupHeadingCountsAgreeAcrossTreeAndFlatModes(t *testing.T) {
	tree := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
	tree.model.SwitchView(ViewInbox)
	treeCounts := intakeHeadings(t, tree, "accepted")

	flat := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
	flat.model.SwitchView(ViewInbox)
	flat.model.filter = "a"
	flat.model.RefreshRows()
	if flat.model.useTree() {
		t.Fatal("the search path did not drop the inbox into flat mode")
	}
	flatCounts := intakeHeadings(t, flat, "accepted")

	for label, count := range treeCounts {
		if flatCounts[label] != count {
			t.Fatalf("group %q counts %d in tree mode and %d in flat mode:\n%s",
				label, count, flatCounts[label], rowTexts(flat))
		}
	}
}

// A block whose ONLY group is the unfiled one paints no heading at all: "INBOX"
// followed by "Inbox" is a heading repeating the rule directly above it, and an
// inbox nobody has filed yet is the common case.
func TestInboxSkipsTheHeadingWhenEverythingIsUnfiled(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: inboxAlignFixture})
	harness.model.SwitchView(ViewInbox)
	layout := intakeSections(t, harness)
	for _, entry := range append(append([]string{}, layout.approvals...), layout.accepted...) {
		if strings.HasPrefix(entry, "# ") {
			t.Fatalf("an all-unfiled inbox painted the heading %q:\n%s", entry, rowTexts(harness))
		}
	}
}

// The two tabs agree. The Inbox does not compute a project sequence of its own —
// it adopts the one the Projects tab lists — so a reader who knows where Aviator
// sits on one tab finds it in the same place on the other.
func TestInboxProjectOrderFollowsTheProjectsView(t *testing.T) {
	// Bakery is alphabetically first; Aviator carries the sooner date and so
	// leads on the Projects view's soonest-date rule. Intake must follow the
	// date, not the alphabet.
	harness := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
	harness.model.SwitchView(ViewProjects)
	projects := []string{}
	for _, row := range harness.model.Rows() {
		if row.Project != nil {
			projects = append(projects, row.Project.Title)
		}
	}
	if want := []string{"Aviator", "Bakery"}; !equalStrings(projects, want) {
		t.Fatalf("the Projects tab lists %v, want %v", projects, want)
	}

	harness.model.SwitchView(ViewInbox)
	layout := intakeSections(t, harness)
	headings := []string{}
	for _, entry := range layout.accepted {
		if title, cut := strings.CutPrefix(entry, "# "); cut {
			headings = append(headings, title)
		}
	}
	if want := []string{"Aviator", "Bakery", "Inbox"}; !equalStrings(headings, want) {
		t.Fatalf("intake groups run %v, want %v\n\n%s", headings, want, rowTexts(harness))
	}
}

// Grouping reshuffles rows, which is exactly the condition under which a cursor
// that followed an INDEX would land on the wrong task. Selection follows an id,
// and a rebuild must leave it on the same task.
func TestInboxGroupingKeepsSelectionIdentityAcrossARebuild(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
	harness.model.SwitchView(ViewInbox)
	harness.selectRowByID("cccc0003") // "aviator logbook"

	before := harness.model.Selected()
	harness.model.RefreshRows()
	if got := harness.model.SelectedID(); got != "cccc0003" {
		t.Fatalf("a rebuild moved selection to %q, want cccc0003", got)
	}
	if got := harness.model.Selected(); got != before {
		t.Fatalf("a rebuild moved the cursor from row %d to %d", before, got)
	}
	if item := harness.model.CurrentItem(); item == nil || item.Title != "aviator logbook" {
		t.Fatalf("the cursor is no longer on the selected task: %#v", item)
	}
}

// The review pass is unchanged: a decision lands on the NEXT proposal in the
// queue as it is painted, so a/r stays one keystroke each. Grouping changes what
// "next" is on screen; it does not change the walk.
func TestApproveRejectWalksTheProposalsInPaintedOrder(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: intakeGroupingFixture})
	harness.model.SwitchView(ViewInbox)

	queue := []string{}
	for _, row := range harness.model.Rows() {
		if row.Item != nil && isProposedState(row.Item.State) {
			queue = append(queue, row.Item.Title)
		}
	}
	want := []string{
		"aviator oil change", "aviator checkride prep", "bakery flour order", "unfiled idea",
	}
	if !equalStrings(queue, want) {
		t.Fatalf("the approvals queue reads %v, want %v\n\n%s", queue, want, rowTexts(harness))
	}

	harness.selectRowByID("cccc0007") // "aviator oil change", first in the queue
	harness.press('a')
	if item := harness.model.CurrentItem(); item == nil || item.Title != "aviator checkride prep" {
		t.Fatalf("approve did not advance to the next proposal: %#v", item)
	}
	harness.press('r')
	if item := harness.model.CurrentItem(); item == nil || item.Title != "bakery flour order" {
		t.Fatalf("reject did not advance to the next proposal: %#v", item)
	}
	// The approved task is now an accepted capture, and it landed in its own
	// project's group rather than at the end of the block.
	layout := intakeSections(t, harness)
	wantAccepted := []string{
		"# Aviator", "aviator radio check", "aviator logbook", "aviator oil change",
		"# Bakery", "bakery chore",
		"# Inbox", "unfiled scrap",
	}
	if !equalStrings(layout.accepted, wantAccepted) {
		t.Fatalf("INBOX = %v\nwant     %v\n\n%s",
			layout.accepted, wantAccepted, rowTexts(harness))
	}
}
