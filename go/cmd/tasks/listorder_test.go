package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The order `list` prints rows in, pinned against real fixture data.
//
// This is the one place the Go CLI deliberately does NOT reproduce Ruby's
// bytes. bin/tasks' cmd_list sorts each state group with a bare
// `sort_by { |i| i.priority || "Z" }`, and MRI's sort_by is not stable, so rows
// that tie on priority come out in whatever order introsort's partitioning left
// them in. Go breaks the tie on file order instead. See
// porting/intentional-differences.md § list-priority-tie-order — and note that
// lib/tasks/task_queries.rb:453 already reaches the same conclusion for the
// named views, which carry the source index precisely so ties keep file order.
//
// The invariant test below is the real assertion: it checks the RULE against
// every fixture rather than restating the implementation. The pinned sequences
// after it name the three concrete cases where Ruby and Go visibly disagree, so
// a future change to either side has to come back through this file.

// copyFixtureStore copies a fixture's store into a temp dir. Copied, not read in
// place: taking a store lock creates a `.tasks.jsonl.lock` sidecar, and leaving
// one behind changes the fixture tree's digest, which conformance records.
func copyFixtureStore(t *testing.T, fixture string) string {
	t.Helper()
	source := filepath.Join("..", "..", "..", "porting", "fixtures", "valid", fixture, "store")
	dir := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), content, 0o644); err != nil {
			t.Fatalf("write %s: %v", entry.Name(), err)
		}
	}
	return dir
}

// listRow is one row of `list --json`, which is the same population the human
// form prints, in file order and with the physical line each row came from.
type listRow struct {
	State    string `json:"state"`
	Priority string `json:"priority"`
	Title    string `json:"title"`
	Line     int    `json:"line"`
	Source   string `json:"source"`
}

// position is the row's place in the selection the filter produced. Line alone
// is not it: the `all` scope concatenates live and archive, whose line numbers
// both start at 1, so a live row and an archive row can share a line and the
// live one always comes first.
func (r listRow) position() (int, int) {
	if r.Source == "archive" {
		return 1, r.Line
	}
	return 0, r.Line
}

func (r listRow) before(other listRow) bool {
	leftSource, leftLine := r.position()
	rightSource, rightLine := other.position()
	if leftSource != rightSource {
		return leftSource < rightSource
	}
	return leftLine < rightLine
}

func listRows(t *testing.T, dir string, args ...string) []listRow {
	t.Helper()
	result := runCLI(t, dir, append([]string{"list"}, append(args, "--json")...)...)
	if result.status != 0 {
		t.Fatalf("list --json: exit %d, stderr %q", result.status, result.stderr)
	}
	var rows []listRow
	if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	return rows
}

// humanTitles is the titles the human form printed, in print order, with the
// state headings dropped. A row is "  [A] Title  @ctx …"; the title is what sits
// between the optional priority cookie and the first double-space run, which is
// what separates a title from every trailing decoration.
func humanTitles(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	result := runCLI(t, dir, append([]string{"list"}, args...)...)
	if result.status != 0 {
		t.Fatalf("list: exit %d, stderr %q", result.status, result.stderr)
	}
	titles := []string{}
	for _, line := range strings.Split(result.stdout, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue // a state heading, or the blank line between groups
		}
		row := strings.TrimPrefix(line, "  ")
		if strings.HasPrefix(row, "[") {
			if _, rest, found := strings.Cut(row, "] "); found {
				row = rest
			}
		}
		if index := strings.Index(row, "  "); index >= 0 {
			row = row[:index]
		}
		titles = append(titles, row)
	}
	return titles
}

// THE RULE, checked as a property against every valid fixture: within one state
// group, priority is non-decreasing (unprioritized last), and rows that tie on
// priority appear in ascending file order.
//
// A property rather than an expected transcript on purpose — a transcript
// generated from the implementation would agree with any ordering the
// implementation happened to produce, including the bug this test exists to
// catch.
func TestListOrdersByPriorityThenFileOrderWithinEveryStateGroup(t *testing.T) {
	fixtures, err := os.ReadDir(filepath.Join("..", "..", "..", "porting", "fixtures", "valid"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	for _, fixture := range fixtures {
		if !fixture.IsDir() {
			continue
		}
		t.Run(fixture.Name(), func(t *testing.T) {
			dir := copyFixtureStore(t, fixture.Name())
			if _, err := os.Stat(filepath.Join(dir, "tasks.jsonl")); err != nil {
				t.Skip("fixture has no live store")
			}
			rows := listRows(t, dir, "--all")
			byTitle := map[string]listRow{}
			for _, row := range rows {
				byTitle[row.Title] = row
			}
			titles := humanTitles(t, dir, "--all")
			if len(titles) != len(rows) {
				t.Fatalf("%d printed rows, %d JSON rows", len(titles), len(rows))
			}

			previousState, previousKey := "", ""
			previous := listRow{}
			for _, title := range titles {
				row, found := byTitle[title]
				if !found {
					t.Fatalf("printed row %q is in no JSON row", title)
				}
				key := row.Priority
				if key == "" {
					key = "Z" // unprioritized sorts after C
				}
				if row.State != previousState {
					previousState, previousKey, previous = row.State, key, row
					continue
				}
				if key < previousKey {
					t.Fatalf("%s: priority went backwards at %q (%s after %s)",
						row.State, title, key, previousKey)
				}
				if key == previousKey && row.before(previous) {
					t.Fatalf("%s: priority-%s tie broke out of file order at %q (%s line %d after %s line %d)",
						row.State, key, title, row.Source, row.Line, previous.Source, previous.Line)
				}
				previousKey, previous = key, row
			}
		})
	}
}

// The three fixtures where the rule above makes Go visibly disagree with Ruby.
// Each expected sequence is FILE ORDER within the tie; the Ruby column records
// what bin/tasks prints today, so the size and shape of the difference stays
// visible rather than becoming folklore.
func TestListTieOrderDivergesFromRubyOnTheseFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		state   string
		// want is the Go order: ascending file order within the tie.
		want []string
		// ruby is what bin/tasks prints for the same rows at any clock —
		// introsort's permutation, not a rule. Asserted only to be DIFFERENT,
		// so this test fails loudly if Ruby's side is ever fixed too.
		ruby []string
	}{
		{
			fixture: "scale-ordering",
			state:   "INBOX",
			want: []string{"Check the quarterly report (1.3.5)", "Review the guest bed (2.3.4)",
				"Plan the tyre pressure (3.3.3)", "Measure the quarterly report (4.3.2)",
				"Book the guest bed (5.3.1)"},
			ruby: []string{"Review the guest bed (2.3.4)", "Measure the quarterly report (4.3.2)",
				"Plan the tyre pressure (3.3.3)", "Check the quarterly report (1.3.5)",
				"Book the guest bed (5.3.1)"},
		},
		{
			// Every row ties (all unprioritized), and MRI's median-of-three pivot
			// swaps the first and last elements of an all-equal array.
			fixture: "link-corpus",
			state:   "TODO",
			want:    []string{"Org labelled link"},
			ruby:    []string{"Title link https://example.invalid/title/link comes first"},
		},
		{
			fixture: "recur-calendar-grammar",
			state:   "TODO",
			want:    []string{"Every other Monday standup"},
			ruby:    []string{"Quarterly from completion"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			dir := copyFixtureStore(t, testCase.fixture)
			rows := listRows(t, dir)
			byTitle := map[string]listRow{}
			for _, row := range rows {
				byTitle[row.Title] = row
			}
			titles := humanTitles(t, dir)

			// The printed rows of this state whose priority matches the first
			// expected row's — the tie run the case is about.
			anchor, found := byTitle[testCase.want[0]]
			if !found {
				t.Fatalf("fixture no longer holds %q", testCase.want[0])
			}
			run := []string{}
			for _, title := range titles {
				row := byTitle[title]
				if row.State == anchor.State && row.Priority == anchor.Priority {
					run = append(run, title)
				}
			}
			if len(run) < len(testCase.want) {
				t.Fatalf("%d rows in the tie run, want at least %d", len(run), len(testCase.want))
			}
			for index, want := range testCase.want {
				if run[index] != want {
					t.Fatalf("row %d = %q, want %q\nfull run: %v", index, run[index], want, run)
				}
			}
			if testCase.ruby[0] == testCase.want[0] {
				t.Fatalf("this case no longer diverges from Ruby — if bin/tasks was fixed, "+
					"retire the entry in porting/intentional-differences.md (fixture %s)",
					testCase.fixture)
			}
		})
	}
}

// A lead-hidden row names the span as the file STORES it — "3w", not
// "3 weeks". `show` spells the span out because it has a line to itself; a list
// row already carries priority, title, contexts, date, recurrence and the
// availability clause, and the cookie is also what `tasks lead` would take
// back. Found by sweeping `list --unavailable` across clock pins: the humanized
// form was the only non-ordering divergence in the whole read surface.
func TestListLeadSpanSuffixNamesTheStoredCookie(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"Renew the domain","deadline":"2026-11-01","lead":"3w"}
`)
	result := runCLI(t, dir, "list", "--unavailable")
	if !strings.Contains(result.stdout, "(unavailable until 2026-10-11 · 3w before 11/1)") {
		t.Fatalf("stdout = %q", result.stdout)
	}
	if strings.Contains(result.stdout, "3 weeks") {
		t.Fatalf("the list row spells the cookie, not the sentence: %q", result.stdout)
	}
	// `show` is the surface that does spell it out — the two are different
	// renderings on purpose, and both are Ruby's.
	shown := runCLI(t, dir, "show", "Renew")
	if !strings.Contains(shown.stdout, "lead:      3w (3 weeks)") {
		t.Fatalf("show = %q", shown.stdout)
	}
}

// The JSON form has no such divergence: `list --json` emits the selection in
// file order without the adapter's sort, so both implementations agree there.
// That is also why a --json-only sweep could not have caught this.
func TestListJSONIsFileOrderAndUnaffectedByTheTieBreak(t *testing.T) {
	dir := copyFixtureStore(t, "scale-ordering")
	rows := listRows(t, dir)
	for index := 1; index < len(rows); index++ {
		if !rows[index-1].before(rows[index]) {
			t.Fatalf("row %d (line %d) follows line %d — --json is file order",
				index, rows[index].Line, rows[index-1].Line)
		}
	}
}
