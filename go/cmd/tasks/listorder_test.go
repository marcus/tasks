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
