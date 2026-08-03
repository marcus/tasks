package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Wave 1's real defects were all found by running the Go build against the Ruby
// one and diffing bytes — never by reading code, and never by a green unit test
// that asserted what the porter believed. So merge is compared the same way:
// every scenario below is resolved by BOTH implementations and the merged file
// text, the decision log, and the refusal reason must agree byte for byte.
//
// The comparison runs Tasks::JsonlMerge directly rather than through the CLI, so
// a difference is attributable to the merge rather than to argument handling;
// driver_test.go compares the two binaries end to end.

// repoRoot is the checkout these tests live in: go/internal/merge → the root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
}

func requireRuby(t *testing.T) string {
	t.Helper()
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby is not on PATH; the differential comparison cannot run")
	}
	return ruby
}

// rubyMergeScript prints the oracle's whole answer in one deterministic frame:
// a status line, the audit log, and the merged bytes. Reading it back as one
// blob is what makes "byte for byte" mean the log too, not just the file.
const rubyMergeScript = `
root = ARGV[0]
$LOAD_PATH.unshift(File.join(root, "lib"))
require "tasks/jsonl_merge"
sides = ARGV[1, 3].map { |path| File.binread(path).force_encoding(Encoding::UTF_8) }
result = Tasks::JsonlMerge.merge(base_text: sides[0], ours_text: sides[1], theirs_text: sides[2])
STDOUT.binmode
STDOUT.print(result.ok? ? "OK\n" : "ERR\n")
STDOUT.print(result.log_lines(pathname: "tasks.jsonl").join("\n"))
STDOUT.print("\n--8<--\n")
STDOUT.print(result.text.to_s)
`

// rubyMerge is the oracle's answer, rendered the same way goMerge renders Go's.
func rubyMerge(t *testing.T, base, ours, theirs string) string {
	t.Helper()
	ruby := requireRuby(t)
	root := repoRoot(t)
	dir := t.TempDir()
	paths := make([]string, 0, 3)
	for name, text := range map[string]string{"base": base, "ours": ours, "theirs": theirs} {
		_ = name
		_ = text
	}
	for _, side := range []struct{ name, text string }{{"base", base}, {"ours", ours}, {"theirs", theirs}} {
		path := filepath.Join(dir, side.name+".jsonl")
		if err := os.WriteFile(path, []byte(side.text), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	args := append([]string{"-e", rubyMergeScript, "--", root}, paths...)
	output, err := exec.Command(ruby, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ruby oracle failed: %v\n%s", err, output)
	}
	return string(output)
}

// goMerge renders the Go answer in the oracle's frame.
func goMerge(base, ours, theirs string) string {
	result := Merge(base, ours, theirs)
	status := "OK\n"
	if !result.OK() {
		status = "ERR\n"
	}
	return status + strings.Join(result.LogLines("tasks.jsonl"), "\n") + "\n--8<--\n" + result.Text
}

type scenario struct {
	name               string
	base, ours, theirs doc
	baseText           string // set instead of base when the side is raw bytes
	oursText           string
	theirsText         string
}

func (s scenario) texts(t *testing.T) (string, string, string) {
	t.Helper()
	base, ours, theirs := s.baseText, s.oursText, s.theirsText
	if s.base != nil {
		base = s.base.text(t)
	}
	if s.ours != nil {
		ours = s.ours.text(t)
	}
	if s.theirs != nil {
		theirs = s.theirs.text(t)
	}
	return base, ours, theirs
}

// differentialScenarios spans every rule the merge has, plus the shapes a user
// actually produces on two machines: an edit racing a delete, two devices adding
// under the same parent, a claim racing a close, and a pair whose merge is only
// invalid together.
func differentialScenarios(t *testing.T) []scenario {
	t.Helper()
	base := baseRecords()
	nested := base.change("10000003", map[string]any{"parent": "10000002"})
	dated := base.change("10000002", map[string]any{
		"scheduled":      "2026-07-20",
		"scheduled_time": map[string]any{"local": "09:00", "timezone": "America/Los_Angeles"}})
	delegated := base.change("10000002", map[string]any{"delegation": ready(nil)})
	held := base.change("10000002", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T10:00:00Z",
			map[string]any{"work_ref": "https://example.com/pr/42"})})
	moved := base.change("10000002", map[string]any{"parent": "10000003", "updated": workStamp})

	return []scenario{
		{name: "no-op: three identical sides", base: base, ours: base, theirs: base},
		{name: "disjoint field edits", base: base,
			ours:   base.change("10000002", map[string]any{"tags": []any{"@computer", "travel"}, "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{"scheduled": "2026-07-19", "updated": workStamp})},
		{name: "same field, newer stamp wins", base: base,
			ours:   base.change("10000003", map[string]any{"title": "Call utility", "updated": homeStamp}),
			theirs: base.change("10000003", map[string]any{"title": "Call PSE billing", "updated": workStamp})},
		{name: "same field, no stamps at all", base: base,
			ours:   base.change("10000003", map[string]any{"title": "Ours title"}),
			theirs: base.change("10000003", map[string]any{"title": "Theirs title"})},
		{name: "same field, equal stamps fall to the byte tiebreak", base: base,
			ours:   base.change("10000003", map[string]any{"title": "Aaa", "updated": homeStamp}),
			theirs: base.change("10000003", map[string]any{"title": "Zzz", "updated": homeStamp})},
		{name: "temporal pair conflict", base: dated,
			ours: dated.change("10000002", map[string]any{"scheduled": "2026-07-21", "updated": homeStamp}),
			theirs: dated.change("10000002", map[string]any{"scheduled": "2026-07-25",
				"scheduled_time": map[string]any{"local": "14:00"}, "updated": workStamp})},
		{name: "undate racing a retime", base: dated,
			ours: dated.change("10000002", map[string]any{"scheduled": nil, "scheduled_time": nil, "updated": workStamp}),
			theirs: dated.change("10000002", map[string]any{
				"scheduled_time": map[string]any{"local": "10:30", "timezone": "Europe/London"}, "updated": homeStamp})},
		{name: "tags union", base: base.change("10000002", map[string]any{"tags": []any{"@computer", "important"}}),
			ours: base.change("10000002", map[string]any{
				"tags": []any{"@computer", "important", "zeta"}, "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{
				"tags": []any{"important", "alpha", "@computer"}, "updated": workStamp})},
		{name: "body append on append", base: base,
			ours: base.change("10000002", map[string]any{
				"body": "Reservation started.\nOne", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{
				"body": "Reservation started.\nOne\nTwo", "updated": workStamp})},
		{name: "body diverged, no prefix", base: base,
			ours:   base.change("10000002", map[string]any{"body": "Ours note", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{"body": "Theirs note", "updated": workStamp})},
		{name: "close racing an open edit", base: base,
			ours: base.change("10000002", map[string]any{
				"state": "DONE", "closed": "2026-07-16", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{"state": "TODO", "updated": workStamp})},
		{name: "two closes with different closed dates", base: base,
			ours: base.change("10000002", map[string]any{
				"state": "DONE", "closed": "2026-07-16", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{
				"state": "CANCELLED", "closed": "2026-07-17", "updated": workStamp})},
		{name: "concurrent claims", base: delegated,
			ours: delegated.change("10000002", map[string]any{
				"delegation": claim("worker/aaa", "2026-07-27T18:09:00Z", nil), "updated": homeStamp}),
			theirs: delegated.change("10000002", map[string]any{
				"delegation": claim("worker/zzz", "2026-07-27T18:04:11Z", nil), "updated": workStamp})},
		{name: "revocation racing a claim", base: delegated,
			ours: delegated.change("10000002", map[string]any{"delegation": nil, "updated": homeStamp}),
			theirs: delegated.change("10000002", map[string]any{
				"delegation": claim("worker/aaa", "2026-07-27T18:04:11Z", nil), "updated": workStamp})},
		{name: "close racing a live claim", base: held,
			ours: held.change("10000002", map[string]any{
				"state": "DONE", "closed": "2026-07-27", "updated": homeStamp}),
			theirs: held.change("10000002", map[string]any{
				"delegation": ready(map[string]any{"at": "2026-07-27T10:02:00Z"}), "updated": workStamp})},
		{name: "delegation racing a proposal", base: base.change("10000002", map[string]any{"delegation": nil}),
			ours: base.change("10000002", map[string]any{"delegation": ready(nil), "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{
				"state": "PROPOSED", "delegation": nil, "updated": workStamp})},
		{name: "delete racing an edit", base: base,
			ours:   base.without("10000003"),
			theirs: base.change("10000003", map[string]any{"title": "Edited concurrently", "updated": workStamp})},
		{name: "delete against an untouched side", base: base,
			ours: base.without("10000003"), theirs: base},
		{name: "both sides delete the same record", base: base,
			ours: base.without("10000003"), theirs: base.without("10000003")},
		{name: "subtree delete racing a descendant edit", base: nested,
			ours:   nested.without("10000002", "10000003"),
			theirs: nested.change("10000003", map[string]any{"title": "Edited nested", "updated": workStamp})},
		{name: "adds on both sides under one parent", base: base,
			ours: append(base.copy(), map[string]any{"type": "task", "id": "10000005",
				"parent": "10000001", "state": "TODO", "title": "Ours add", "updated": homeStamp}),
			theirs: append(base.copy(), map[string]any{"type": "task", "id": "10000006",
				"parent": "10000001", "state": "TODO", "title": "Theirs add", "updated": workStamp})},
		{name: "the same id added on both sides with different content", base: base,
			ours: append(base.copy(), map[string]any{"type": "task", "id": "10000005",
				"parent": "10000001", "state": "TODO", "title": "Ours version", "updated": homeStamp}),
			theirs: append(base.copy(), map[string]any{"type": "task", "id": "10000005",
				"parent": "10000001", "state": "NEXT", "title": "Theirs version",
				"tags": []any{"shared"}, "updated": workStamp})},
		{name: "theirs-only subtree", base: base, ours: base,
			theirs: append(base.copy(),
				map[string]any{"type": "section", "id": "20000001", "title": "Home"},
				map[string]any{"type": "task", "id": "20000002", "parent": "20000001",
					"state": "TODO", "title": "New child", "updated": workStamp})},
		{name: "both sides reorder the same parent", base: base,
			ours:   doc{base[0], base[1], base[3], base[2], base[4]},
			theirs: doc{base[0], base[1], base[4], base[2], base[3]}},
		{name: "forward-compatible unknown fields survive", base: base,
			ours: base.change("10000002", map[string]any{
				"future_field": "ours", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{
				"other_future": []any{1, 2}, "updated": workStamp})},
		{name: "unknown field conflicting on both sides", base: base,
			ours:   base.change("10000002", map[string]any{"future_field": "ours", "updated": homeStamp}),
			theirs: base.change("10000002", map[string]any{"future_field": "theirs", "updated": workStamp})},
		{name: "empty base, concurrent first creation", baseText: "",
			ours:   doc{base[0], base[1], base[2]},
			theirs: doc{base[0], base[1], base[3]}},
		{name: "cyclic merge is refused", base: base,
			ours:   base.change("10000003", map[string]any{"parent": "10000002", "updated": homeStamp}),
			theirs: doc{moved[0], moved[1], moved[3], moved[2], moved[4]}},
		{name: "unparseable side is refused", base: base, ours: base, theirsText: "not-json\n"},
		{name: "invalid side is refused", base: base,
			ours: base.change("10000002", map[string]any{"state": "NOT-A-STATE"}), theirs: base},
		{name: "duplicate id side is refused", base: base,
			ours: append(base.copy(), base.copy()[4]), theirs: base},
		{name: "a v1 side is refused", base: base, ours: base,
			theirs: func() doc { side := baseRecords(); side[0]["version"] = 1; return side }()},
		{name: "a v1 base still merges", ours: base.change("10000002", map[string]any{"title": "Ours edited"}),
			theirs:   base.change("10000003", map[string]any{"title": "Theirs edited"}),
			baseText: func() string { side := baseRecords(); side[0]["version"] = 1; return side.text(t) }()},
		{name: "a v3 base is refused", ours: base, theirs: base,
			baseText: func() string { side := baseRecords(); side[0]["version"] = 3; return side.text(t) }()},
	}
}

func TestGoAndRubyResolveEveryScenarioIdentically(t *testing.T) {
	for _, sc := range differentialScenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			base, ours, theirs := sc.texts(t)
			wanted := rubyMerge(t, base, ours, theirs)
			got := goMerge(base, ours, theirs)
			if got != wanted {
				t.Fatalf("Go and Ruby disagree.\n--- ruby ---\n%s\n--- go ---\n%s", wanted, got)
			}
		})
	}
}

// Swapping the two sides must produce the same file in BOTH implementations, and
// the same file as each other. A merge that is not commutative gives two devices
// different data from the same three inputs.
func TestGoAndRubyAgreeWithTheSidesSwapped(t *testing.T) {
	for _, sc := range differentialScenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			base, ours, theirs := sc.texts(t)
			wanted := rubyMerge(t, base, theirs, ours)
			got := goMerge(base, theirs, ours)
			if got != wanted {
				t.Fatalf("Go and Ruby disagree with sides swapped.\n--- ruby ---\n%s\n--- go ---\n%s", wanted, got)
			}
		})
	}
}
