package tui

import (
	"strconv"
	"strings"
	"testing"
)

// rowsFor builds one view at a known width — the input the whole column layout
// is a function of.
func rowsFor(t *testing.T, harness *modelHarness, view string, width int) []Row {
	t.Helper()
	harness.model.SwitchView(view)
	read := harness.model.ReadModel()
	request := BuildRequest{
		View: view, Styler: PlainStyler{}, Queries: read.Queries(),
		Items: read.Items(), Tree: read.Queries().Tree().Roots, UseTree: true,
		Collapsed: map[string]bool{}, Width: width,
		IntakeCounts: harness.model.intakeCounts(read.Items()),
	}
	if view == ViewProjects {
		request.Projects = read.Queries().Projects()
	}
	return BuildRows(request)
}

func TestEveryViewSharesOneMetaColumn(t *testing.T) {
	// The point of the column is the shared edge, and it is shared ACROSS views:
	// switching tabs must not move the column the eye is already reading. A rule
	// is painted flush to the pane edge and so is CursorField wider than a row;
	// both still end on the same column.
	const width = 78
	for _, view := range ViewKeys() {
		harness := newModelHarness(t, harnessOptions{})
		for _, row := range rowsFor(t, harness, view, width) {
			if strings.TrimSpace(row.Text) == "" {
				continue
			}
			want := width
			if row.Chrome {
				want += CursorField
			}
			if got := len([]rune(row.Text)); got != want {
				t.Fatalf("%s: row is %d cells wide, want %d: %q", view, got, want, row.Text)
			}
		}
	}
}

func TestEveryViewOpensEachBlockWithARuleThatCountsIt(t *testing.T) {
	const width = 78
	for _, view := range ViewKeys() {
		harness := newModelHarness(t, harnessOptions{})
		rows := rowsFor(t, harness, view, width)

		// Projects is the one view whose badge does not count the block's rows:
		// a section of projects counts PROJECTS, and the tasks nested under each
		// are that project's rows, not the section's.
		countsRows := view != ViewProjects
		label, counted, checked := "", 0, 0
		check := func() {
			if label == "" {
				return
			}
			checked++
			if tally := headingTally(rows, label); countsRows && tally != "" && counted > 0 {
				if !strings.Contains(tally, "0") && tally != strconv.Itoa(counted) {
					t.Fatalf("%s: rule %q counts %s but carries %d rows:\n%s",
						view, label, tally, counted, agendaDump(rows))
				}
			}
		}
		for _, row := range rows {
			// A rule is any non-task row carrying the rule glyph. In the outline
			// the rule is a SELECTABLE section row that leads with a fold marker,
			// so rule-ness is checked before selectability and the marker is
			// skipped when reading the label.
			switch {
			case row.Item == nil && strings.Contains(row.Text, "─"):
				check()
				label, counted = ruleLabelWord(row.Text), 0
			case row.Item != nil || row.Project != nil:
				counted++
			}
		}
		check()
		if checked == 0 {
			t.Fatalf("%s emitted no section rule at all:\n%s", view, agendaDump(rows))
		}
	}
}

// ruleLabelWord is a rule's first label word, past any fold marker.
func ruleLabelWord(text string) string {
	for _, field := range strings.Fields(text) {
		if field != strings.TrimSpace(MarkExpanded) && field != strings.TrimSpace(MarkCollapsed) {
			return field
		}
	}
	return ""
}

func TestEveryTaskRowStartsItsTitleOnTheSameColumn(t *testing.T) {
	// Priority is a FIELD, not a prefix: a prioritized row and an unprioritized
	// one begin their titles on the same column, so a scan reads one edge.
	harness := newModelHarness(t, harnessOptions{})
	rows := rowsFor(t, harness, ViewNext, 78)
	head := -1
	for _, row := range rows {
		if row.Item == nil {
			continue
		}
		index := strings.Index(row.Text, row.Item.Title)
		if index < 0 {
			continue
		}
		if head < 0 {
			head = index
			continue
		}
		if index != head {
			t.Fatalf("title starts at column %d, want %d: %q", index, head, row.Text)
		}
	}
	if head < PriorityField {
		t.Fatalf("no priority field at the head of a row (title at %d)", head)
	}
}

func TestNextDoesNotRepeatTheContextItsSectionIsNamedAfter(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	rows := rowsFor(t, harness, ViewNext, 78)
	section := ""
	for _, row := range rows {
		if row.Item == nil {
			if fields := strings.Fields(row.Text); len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
				section = fields[0]
			}
			continue
		}
		if section != "" && strings.Contains(row.Text, section) {
			t.Fatalf("row under %s repeats it: %q", section, row.Text)
		}
	}
	if section == "" {
		t.Fatal("the Next view emitted no context section")
	}
}

func TestASectionWithNothingInItPaintsOnlyWhenItHasSomethingToSay(t *testing.T) {
	// Quadrants always paints all four — the grid IS the view. The agenda drops
	// an empty day group, because an empty OVERDUE costs two lines to say
	// nothing a person needed told.
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	quadrants := agendaDump(rowsFor(t, harness, ViewQuadrants, 78))
	for _, label := range []string{"Q1", "Q2", "Q3", "Q4"} {
		if !strings.Contains(quadrants, label) {
			t.Fatalf("quadrant %s did not paint:\n%s", label, quadrants)
		}
	}
	agenda := agendaDump(rowsFor(t, harness, ViewAgenda, 78))
	if strings.Contains(agenda, "OVERDUE") {
		t.Fatalf("an empty day group painted anyway:\n%s", agenda)
	}
}

func TestTheDetailRailLeadsWithTheStateAndTheID(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	text := detailFor(t, harness, fixFlight)
	first := strings.SplitN(text, "\n", 2)[0]
	if !strings.HasPrefix(first, "NEXT ") || !strings.HasSuffix(first, "aaaa0004") {
		t.Fatalf("the rail did not open on `STATE ──── id`: %q", first)
	}
}
