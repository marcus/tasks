package merge

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A hand-written scenario table only covers the cases the porter thought of.
// This one generates three-way merges from a menu of the mutations the CLI
// actually performs, and holds both implementations to the same answer — the
// merged bytes AND the audit log — for every one.
//
// The whole batch goes through a single Ruby process, so several hundred
// comparisons cost one interpreter start rather than several hundred.

const rubyBatchScript = `
root = ARGV[0]
$LOAD_PATH.unshift(File.join(root, "lib"))
require "json"
require "tasks/jsonl_merge"
cases = JSON.parse(File.read(ARGV[1], encoding: "UTF-8"))
answers = cases.map do |item|
  result = Tasks::JsonlMerge.merge(
    base_text: item["base"], ours_text: item["ours"], theirs_text: item["theirs"]
  )
  { "ok" => result.ok?, "log" => result.log_lines(pathname: "tasks.jsonl"), "text" => result.text.to_s }
end
File.write(ARGV[2], JSON.generate(answers))
`

type threeWay struct {
	Base   string `json:"base"`
	Ours   string `json:"ours"`
	Theirs string `json:"theirs"`
}

type answer struct {
	OK   bool     `json:"ok"`
	Log  []string `json:"log"`
	Text string   `json:"text"`
}

func rubyBatch(t *testing.T, cases []threeWay) []answer {
	t.Helper()
	ruby := requireRuby(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "cases.json")
	output := filepath.Join(dir, "answers.json")
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ruby, "-e", rubyBatchScript, "--", repoRoot(t), input, output)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ruby oracle failed: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var answers []answer
	if err := json.Unmarshal(raw, &answers); err != nil {
		t.Fatal(err)
	}
	return answers
}

// mutate applies one random, individually legal edit to a side. Every option is
// something a CLI command does, so a generated side is a store the product could
// really have produced.
func mutate(side doc, rng *lcg, tag string) doc {
	stamps := []any{
		"2026-07-16T09:00:00Z#home", "2026-07-16T10:00:00Z#home",
		"2026-07-16T11:00:00Z#work", "2026-07-17T08:30:00Z#" + tag, nil,
	}
	ids := []string{"10000002", "10000003", "10000004"}
	id := ids[rng.intn(len(ids))]
	stamp := stamps[rng.intn(len(stamps))]
	choice := rng.intn(14)
	// An earlier edit in this chain may already have deleted the record; leave
	// the side alone rather than inventing one.
	if side.find(id) == nil && choice != 11 && choice != 12 {
		return side
	}

	switch choice {
	case 0:
		return side.change(id, map[string]any{"title": "Title " + tag + fmt.Sprint(rng.intn(3)), "updated": stamp})
	case 1:
		tags := [][]any{{"@computer"}, {"@computer", "travel"}, {"travel", "alpha"}, {"@home", "@computer"}, nil}
		return side.change(id, map[string]any{"tags": tags[rng.intn(len(tags))], "updated": stamp})
	case 2:
		dates := []any{"2026-07-18", "2026-07-19", "2026-08-01", nil}
		return side.change(id, map[string]any{"scheduled": dates[rng.intn(len(dates))], "updated": stamp})
	case 3:
		times := []any{
			map[string]any{"local": "09:00"},
			map[string]any{"local": "14:00", "timezone": "Europe/London"},
			nil,
		}
		return side.change(id, map[string]any{
			"scheduled": "2026-07-20", "scheduled_time": times[rng.intn(len(times))], "updated": stamp})
	case 4:
		return side.change(id, map[string]any{
			"deadline": []any{"2026-07-30", nil}[rng.intn(2)], "updated": stamp})
	case 5:
		states := []string{"TODO", "NEXT", "WAITING", "SOMEDAY"}
		return side.change(id, map[string]any{
			"state": states[rng.intn(len(states))], "closed": nil, "updated": stamp})
	case 6:
		closed := []string{"2026-07-16", "2026-07-17"}
		terminal := []string{"DONE", "CANCELLED"}
		return side.change(id, map[string]any{
			"state": terminal[rng.intn(2)], "closed": closed[rng.intn(2)],
			"delegation": nil, "updated": stamp})
	case 7:
		bodies := []any{"Reservation started.", "Reservation started.\nOne", "Reservation started.\nOne\nTwo",
			"Different note", nil}
		return side.change(id, map[string]any{"body": bodies[rng.intn(len(bodies))], "updated": stamp})
	case 8:
		return side.change(id, map[string]any{
			"priority": []any{"A", "B", "C", nil}[rng.intn(4)], "updated": stamp})
	case 9:
		markers := []any{
			ready(nil),
			ready(map[string]any{"mode": "implement", "at": "2026-07-27T19:00:00Z"}),
			claim("worker/aaa", "2026-07-27T18:04:11Z", nil),
			claim("worker/bbb", "2026-07-27T18:09:00Z", map[string]any{"work_ref": "https://example.com/pr/1"}),
			map[string]any{"kind": "human", "status": "delegated", "assignee": "pat@example.com",
				"at": "2026-07-27T20:00:00Z"},
			nil,
		}
		return side.change(id, map[string]any{
			"state": "NEXT", "closed": nil, "delegation": markers[rng.intn(len(markers))], "updated": stamp})
	case 10:
		return side.without(id)
	case 11:
		fresh := fmt.Sprintf("2000000%d", rng.intn(6))
		return append(side.copy(), map[string]any{"type": "task", "id": fresh, "parent": "10000001",
			"state": "TODO", "title": "Added by " + tag, "updated": stamp})
	case 12:
		// Reorder two siblings: a rearrangement is a real edit and the merge has
		// an explicit rule for two sides doing it differently.
		reordered := side.copy()
		if len(reordered) >= 4 {
			left, right := 2+rng.intn(len(reordered)-2), 2+rng.intn(len(reordered)-2)
			reordered[left], reordered[right] = reordered[right], reordered[left]
		}
		return reordered
	default:
		return side.change(id, map[string]any{"updated": stamp})
	}
}

func TestRandomThreeWayMergesMatchRubyByteForByte(t *testing.T) {
	if testing.Short() {
		t.Skip("the randomized differential comparison shells out to Ruby")
	}
	rng := newLCG(0x5EED2026)
	const rounds = 400

	cases := make([]threeWay, 0, rounds)
	labels := make([]string, 0, rounds)
	for round := 0; round < rounds; round++ {
		base := baseRecords()
		for edits := rng.intn(2); edits > 0; edits-- {
			base = mutate(base, rng, "b")
		}
		ours, theirs := base, base
		for edits := 1 + rng.intn(3); edits > 0; edits-- {
			ours = mutate(ours, rng, "o")
		}
		for edits := 1 + rng.intn(3); edits > 0; edits-- {
			theirs = mutate(theirs, rng, "t")
		}
		baseText, oursText, theirsText := base.text(t), ours.text(t), theirs.text(t)
		cases = append(cases, threeWay{Base: baseText, Ours: oursText, Theirs: theirsText})
		labels = append(labels, fmt.Sprintf("round %d", round))
		// Both directions: a merge that is not commutative hands two devices
		// different data from the same three files.
		cases = append(cases, threeWay{Base: baseText, Ours: theirsText, Theirs: oursText})
		labels = append(labels, fmt.Sprintf("round %d swapped", round))
	}

	answers := rubyBatch(t, cases)
	if len(answers) != len(cases) {
		t.Fatalf("oracle returned %d answers for %d cases", len(answers), len(cases))
	}
	refusals := 0
	for index, item := range cases {
		result := Merge(item.Base, item.Ours, item.Theirs)
		wanted := answers[index]
		if !wanted.OK {
			refusals++
		}
		if result.OK() != wanted.OK {
			t.Fatalf("%s: Go ok=%v (%s), Ruby ok=%v", labels[index], result.OK(), result.Error, wanted.OK)
		}
		if result.Text != wanted.Text {
			t.Fatalf("%s: merged bytes differ.\n--- ruby ---\n%s\n--- go ---\n%s\nbase:\n%s\nours:\n%s\ntheirs:\n%s",
				labels[index], wanted.Text, result.Text, item.Base, item.Ours, item.Theirs)
		}
		got := result.LogLines("tasks.jsonl")
		if len(got) != len(wanted.Log) {
			t.Fatalf("%s: log length %d, want %d\n%v\n%v", labels[index], len(got), len(wanted.Log), got, wanted.Log)
		}
		for line := range got {
			if got[line] != wanted.Log[line] {
				t.Fatalf("%s: log line %d\n ruby: %q\n   go: %q", labels[index], line, wanted.Log[line], got[line])
			}
		}
	}
	// A run where every generated pair happened to refuse would compare nothing
	// interesting; assert the corpus actually exercised the merge.
	if refusals > len(cases)/2 {
		t.Fatalf("%d of %d generated cases refused; the corpus is not exercising the merge", refusals, len(cases))
	}
	t.Logf("compared %d generated three-way merges against Ruby (%d refusals)", len(cases), refusals)
}
