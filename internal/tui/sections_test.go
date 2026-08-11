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

// ruleLabelWord is a rule's first label word, past any fold marker or band
// glyph. The outline's within-section band rules (`▌ overdue ── 6`) lead with
// the band, and reading THAT as the label would make every band rule in the
// view look like the same heading.
func ruleLabelWord(text string) string {
	for _, field := range strings.Fields(text) {
		switch field {
		case strings.TrimSpace(MarkExpanded), strings.TrimSpace(MarkCollapsed), Band:
			continue
		}
		return field
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
		// CELLS, not bytes. The urgency band that now leads every row is three
		// bytes wide and one cell wide, and a byte index would report a column
		// the terminal never draws on.
		index := titleColumn(row)
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
	if head < BandField+PriorityField {
		t.Fatalf("no band and priority fields at the head of a row (title at %d)", head)
	}
}

// titleColumn is the CELL column a row's title starts on, or -1 if the title is
// not in the row at all.
func titleColumn(row Row) int {
	byteIndex := strings.Index(row.Text, row.Item.Title)
	if byteIndex < 0 {
		return -1
	}
	return len([]rune(row.Text[:byteIndex]))
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

// -- the urgency band and the context column -------------------------------

func TestEveryRowLeadsWithTheUrgencyBandField(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, view := range ViewKeys() {
		for _, row := range rowsFor(t, harness, view, 100) {
			if row.Item == nil {
				continue
			}
			head := []rune(row.Text)[:BandField]
			if string(head) != strings.Repeat(" ", BandField) &&
				string(head) != Band+" " {
				t.Fatalf("%s: row does not lead with the band field: %q", view, row.Text)
			}
		}
	}
}

func TestOnlyAnOpenDeadlineBandsOnItsOwn(t *testing.T) {
	// A scheduled date says when work became available, not when it is late.
	// Banding it would call a task overdue for having been startable.
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0001","state":"NEXT","title":"past deadline","deadline":"2026-07-01"}
{"type":"task","id":"bbbb0002","parent":"aaaa0001","state":"NEXT","title":"long available","scheduled":"2026-07-01"}
{"type":"task","id":"bbbb0003","parent":"aaaa0001","state":"DONE","title":"closed late","deadline":"2026-07-01","closed":"2026-07-02"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	request := harness.model.treeRequest()
	want := map[string]bool{"past deadline": true, "long available": false, "closed late": false}
	seen := 0
	for _, item := range harness.model.read.Items() {
		banded, ok := want[item.Title]
		if !ok {
			continue
		}
		seen++
		if _, got := bandDays(request, item); got != banded {
			t.Errorf("%q bands=%v, want %v", item.Title, got, banded)
		}
		if got := urgencyBand(request, item) != strings.Repeat(" ", BandField); got != banded {
			t.Errorf("%q painted a band=%v, want %v", item.Title, got, banded)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %d items", seen, len(want))
	}
}

// Inside a banded list every row continues the stripe, including the rows with
// no date of their own — a stripe with holes reads as a rendering fault.
func TestABandedListPaintsAnUnbrokenStripe(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: outlineBandStore})
	harness.model.SwitchView(ViewOutline)
	banded, rows := false, 0
	for _, row := range harness.model.Rows() {
		if strings.Contains(row.Text, Band+" overdue") ||
			strings.Contains(row.Text, Band+" today") ||
			strings.Contains(row.Text, Band+" later") {
			banded = true
			continue
		}
		if row.Project != nil {
			banded = false // a new section; Home is not banded at all
			continue
		}
		if row.Item == nil {
			continue
		}
		if banded {
			rows++
			if !strings.HasPrefix(row.Text, Band) {
				t.Fatalf("a hole in the stripe: %q", row.Text)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no banded rows at all")
	}
}

func TestContextsRightAlignIntoTheirOwnColumn(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	edge := -1
	for _, row := range rowsFor(t, harness, ViewOutline, 100) {
		if row.Item == nil || len(row.Item.Contexts) == 0 {
			continue
		}
		cells := []rune(row.Text)
		at := strings.LastIndex(string(cells), row.Item.Contexts[len(row.Item.Contexts)-1])
		if at < 0 {
			t.Fatalf("row lost its contexts: %q", row.Text)
		}
		end := len([]rune(string(cells)[:at])) + len([]rune(row.Item.Contexts[len(row.Item.Contexts)-1]))
		if edge < 0 {
			edge = end
			continue
		}
		if end != edge {
			t.Fatalf("context column right edge moved to %d, want %d: %q", end, edge, row.Text)
		}
	}
	if edge < 0 {
		t.Fatal("no row carried a context")
	}
}

// Below the width the two right columns need, contexts fall back inline rather
// than vanishing: the date is what the list is coloured by and keeps its column.
func TestANarrowFrameGivesUpTheContextColumnNotTheContexts(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	rows := rowsFor(t, harness, ViewOutline, MinWidth)
	found := false
	for _, row := range rows {
		if row.Item == nil || len(row.Item.Contexts) == 0 {
			continue
		}
		for _, context := range row.Item.Contexts {
			if context != row.ContextExcept && strings.Contains(row.Text, context) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("a narrow frame dropped contexts entirely:\n%s", agendaDump(rows))
	}
}

// A rule's count is the one thing a rule exists to carry, so a label long
// enough to run into the badge loses the LABEL, never the number.
func TestALongRuleLabelNeverPushesItsCountOffTheFrame(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"A section titled far past anything the meta column could survive"}
{"type":"task","id":"bbbb0001","parent":"aaaa0001","state":"NEXT","title":"one"}
{"type":"task","id":"bbbb0002","parent":"aaaa0001","state":"NEXT","title":"two"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	for width := MinWidth; width <= 120; width++ {
		rows := rowsFor(t, harness, ViewOutline, width)
		found := false
		for _, row := range rows {
			if !strings.Contains(row.Text, "A section titled") {
				continue
			}
			found = true
			if !strings.HasSuffix(strings.TrimRight(row.Text, " "), "2") {
				t.Fatalf("at width %d the rule lost its count: %q", width, row.Text)
			}
		}
		if !found {
			t.Fatalf("at width %d the section rule vanished", width)
		}
	}
}
