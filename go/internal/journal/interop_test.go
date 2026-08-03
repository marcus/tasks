package journal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Both binaries are installed during the cutover compatibility window, and both
// point at the same journal directory for the same store file. So the journal
// is not merely "ported" — each implementation has to read the other's history
// and write bytes the other accepts, or an undo taken from the wrong binary
// silently finds nothing to undo.

const rubyJournalScript = `
root, dir, org, mode = ARGV[0], ARGV[1], ARGV[2], ARGV[3]
$LOAD_PATH.unshift(File.join(root, "lib"))
require "json"
require "tasks/journal"
journal = Tasks::Journal.new(dir: dir, org: org, limit: 50, coalesce_scope: "test-scope")
case mode
when "record"
  steps = JSON.parse(File.read(ARGV[4], encoding: "UTF-8"))
  steps.each do |step|
    journal.record(
      label: step["label"],
      before: { org: step["before"], archive: nil },
      after: { org: step["after"], archive: nil },
      coalesce_key: step["coalesce_key"],
      repair: step["repair"] == true
    )
  end
when "plan"
  plan = journal.plan(ARGV[4].to_i)
  answer = plan.nil? ? nil : { "label" => plan[:label], "repair" => plan[:repair],
                               "expect" => plan[:expect][:org], "target" => plan[:target][:org] }
  File.write(ARGV[5], JSON.generate({ "plan" => answer }))
end
`

func rubyJournal(t *testing.T, dir, org, mode string, extra ...string) {
	t.Helper()
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby is not on PATH")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	args := append([]string{"-e", rubyJournalScript, "--", root, dir, org, mode}, extra...)
	command := exec.Command(ruby, args...)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ruby journal failed: %v\n%s", err, combined)
	}
}

type rubyStep struct {
	Label       string `json:"label"`
	Before      string `json:"before"`
	After       string `json:"after"`
	CoalesceKey any    `json:"coalesce_key"`
	Repair      bool   `json:"repair"`
}

func rubyRecord(t *testing.T, dir, org string, steps []rubyStep) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "steps.json")
	encoded, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	rubyJournal(t, dir, org, "record", path)
}

type rubyPlan struct {
	Plan *struct {
		Label  string `json:"label"`
		Repair bool   `json:"repair"`
		Expect string `json:"expect"`
		Target string `json:"target"`
	} `json:"plan"`
}

func rubyPlanFor(t *testing.T, dir, org string, delta int) rubyPlan {
	t.Helper()
	out := filepath.Join(t.TempDir(), "plan.json")
	rubyJournal(t, dir, org, "plan", itoa(delta), out)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var answer rubyPlan
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestGoReadsAHistoryRubyWrote(t *testing.T) {
	dir, org := seed(t)
	rubyRecord(t, dir, org, []rubyStep{
		{Label: "state → DONE: Book flight", Before: "first", After: "second"},
		{Label: "priority → C: Book flight", Before: "second", After: "third"},
	})

	step := mustPlan(t, Open(dir, org), -1)
	if step.Label != "priority → C: Book flight" {
		t.Fatalf("label = %q", step.Label)
	}
	if !step.Expect.Equal(orgOnly("third")) || !step.Target.Equal(orgOnly("second")) {
		t.Fatalf("plan = %#v", step)
	}

	// And a Go undo of Ruby's history leaves a cursor Ruby then agrees with.
	if !Open(dir, org).Writable(50, scope).Commit(step.To) {
		t.Fatal("commit failed")
	}
	answer := rubyPlanFor(t, dir, org, -1)
	if answer.Plan == nil || answer.Plan.Label != "state → DONE: Book flight" {
		t.Fatalf("ruby plan after the Go undo = %#v", answer.Plan)
	}
	if answer.Plan.Target != "first" {
		t.Fatalf("ruby target = %q", answer.Plan.Target)
	}
}

func TestRubyReadsAHistoryGoWrote(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("state → DONE: Book flight", orgOnly("first"), orgOnly("second"), "", false)
	journal.Record("repair: scheduled", orgOnly("second"), orgOnly("third"), "edit-session", true)

	answer := rubyPlanFor(t, dir, org, -1)
	if answer.Plan == nil {
		t.Fatal("ruby found no history in the Go-written journal")
	}
	if answer.Plan.Label != "repair: scheduled" {
		t.Fatalf("label = %q", answer.Plan.Label)
	}
	if !answer.Plan.Repair {
		t.Fatal("the repair exemption must cross the language boundary")
	}
	if answer.Plan.Expect != "third" || answer.Plan.Target != "second" {
		t.Fatalf("plan = %#v", answer.Plan)
	}
}

// The strongest form of the interoperability claim: for the same sequence of
// mutations the two implementations write the SAME index.json, byte for byte,
// and the same content-addressed blobs. Anything less and a store that had been
// touched by both binaries would carry two spellings of one history.
func TestBothImplementationsWriteTheSameJournalBytes(t *testing.T) {
	steps := []rubyStep{
		{Label: "capture: Book flight", Before: "one", After: "two"},
		{Label: "title → T", Before: "two", After: "three", CoalesceKey: "edit-session"},
		{Label: "title → Ti", Before: "three", After: "four", CoalesceKey: "edit-session"},
		{Label: "repair: scheduled", Before: "four", After: "five", Repair: true},
		{Label: "state → DONE", Before: "five", After: "six"},
	}

	rubyDir, org := seed(t)
	rubyRecord(t, rubyDir, org, steps)

	goDir := filepath.Join(filepath.Dir(org), "journal-go")
	journal := writable(t, goDir, org, 50)
	for _, step := range steps {
		key := ""
		if text, ok := step.CoalesceKey.(string); ok {
			key = text
		}
		if !journal.Record(step.Label, orgOnly(step.Before), orgOnly(step.After), key, step.Repair) {
			t.Fatalf("record %q failed", step.Label)
		}
	}

	rubyIndex, err := os.ReadFile(filepath.Join(rubyDir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	goIndex, err := os.ReadFile(filepath.Join(goDir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The two directories differ only in name, and the index records the ORG
	// path rather than its own, so the bytes must match exactly.
	if string(rubyIndex) != string(goIndex) {
		t.Fatalf("index.json differs.\n--- ruby ---\n%s\n--- go ---\n%s", rubyIndex, goIndex)
	}

	rubyBlobs := blobNames(t, filepath.Join(rubyDir, "blobs"))
	goBlobs := blobNames(t, filepath.Join(goDir, "blobs"))
	if len(rubyBlobs) != len(goBlobs) {
		t.Fatalf("blob sets differ: %v vs %v", rubyBlobs, goBlobs)
	}
	for name := range rubyBlobs {
		if rubyBlobs[name] != goBlobs[name] {
			t.Fatalf("blob %s differs: %q vs %q", name, rubyBlobs[name], goBlobs[name])
		}
	}
}

func blobNames(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string]string{}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		blobs[entry.Name()] = string(content)
	}
	return blobs
}
