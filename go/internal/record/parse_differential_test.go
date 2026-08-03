package record

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The parse diagnostics are user-visible in two places that matter: `tasks
// check` prints them, and the merge driver writes the one that refused a merge
// into the conflict marker the user is left holding. So they are compared
// against Ruby's rather than asserted from memory.
//
// This corpus grew from a real miss: a line that is not JSON at all ("not-json",
// "{broken") fell through every hand-written clause to a generic "unexpected
// token at end of stream", and the merge driver wrote that generic string into
// the user's file where Ruby names the offending token and its column.

const rubyParseScript = `
root = ARGV[0]
$LOAD_PATH.unshift(File.join(root, "lib"))
require "json"
require "tasks/format"
lines = JSON.parse(File.read(ARGV[1], encoding: "UTF-8"))
answers = lines.map do |line|
  result = Tasks::Format.parse(line)
  result.errors.map { |no, message| [no, message] }
end
File.write(ARGV[2], JSON.generate(answers))
`

func TestParseDiagnosticsMatchRuby(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby is not on PATH")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))

	lines := []string{
		"not-json\n",
		"  not-json\n",
		"garbage here\n",
		"nope{}\n",
		"hello world\n",
		"@\n",
		"tru\n",
		"nul\n",
		"true\n",
		"123\n",
		"\"a string\"\n",
		"[1,2]\n",
		"{not json at all}\n",
		"{broken\n",
		"{a:1}\n",
		"{ foo }\n",
		"{1:2}\n",
		"{true:1}\n",
		"{,}\n",
		"{@}\n",
		"{\"a\":1,x}\n",
		"{\"a\":x}\n",
		"{\"a\":xyz abc}\n",
		"{\"a\":[1,2,x]}\n",
		"{\"a\":{\"b\":q}}\n",
		"[abc]\n",
		"[1,x]\n",
		"{\"type\":\"meta\",\"version\":2}\n{not json at all}\n",
		"{\"type\":\"meta\"}\n{broken\n",
		"{\"a\":1}\n\n{\"b\":2}\n",
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "lines.json")
	output := filepath.Join(dir, "answers.json")
	encoded, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ruby, "-e", rubyParseScript, "--", root, input, output)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ruby oracle failed: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var answers [][][2]json.RawMessage
	if err := json.Unmarshal(raw, &answers); err != nil {
		t.Fatal(err)
	}

	for index, line := range lines {
		got := Parse([]byte(line)).Errors
		wanted := answers[index]
		if len(got) != len(wanted) {
			t.Errorf("%q: %d errors, want %d (%v)", line, len(got), len(wanted), wanted)
			continue
		}
		for position, entry := range wanted {
			var wantedLine int
			var wantedMessage string
			if err := json.Unmarshal(entry[0], &wantedLine); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(entry[1], &wantedMessage); err != nil {
				t.Fatal(err)
			}
			if got[position].Line != wantedLine || got[position].Message != wantedMessage {
				t.Errorf("%q error %d:\n ruby: line %d %q\n   go: line %d %q",
					line, position, wantedLine, wantedMessage, got[position].Line, got[position].Message)
			}
		}
	}
}
