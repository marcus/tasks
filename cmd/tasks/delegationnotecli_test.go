package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// The delegation BRIEFING on the CLI.
//
// Exercised as a black box for the same reason the vocabulary is: the claim is
// a cross-layer one — argv has to reach the marker, and the marker has to reach
// `show`, both delegation list scopes, and every `--json` an agent reads —
// and none of that is visible from a package boundary.

const delegationNoteFixture = `{"type":"meta","version":2}
{"type":"section","id":"ffff0001","title":"Work"}
{"type":"task","id":"ffff0002","parent":"ffff0001","state":"NEXT","title":"Ship the thing"}
{"type":"task","id":"ffff0003","parent":"ffff0001","state":"NEXT","title":"Second thing"}
`

// markerOf reads one task's stored delegation marker through `show --json`,
// which is the surface an agent actually reads.
func markerOf(t *testing.T, dir, ref string) map[string]any {
	t.Helper()
	result := runCLI(t, dir, "show", ref, "--json")
	if result.status != 0 {
		t.Fatalf("show --json: exit %d, stderr %q", result.status, result.stderr)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &decoded); err != nil {
		t.Fatalf("show --json is not JSON (%v): %s", err, result.stdout)
	}
	marker, _ := decoded["delegation"].(map[string]any)
	return marker
}

// withStdin runs a body with standard input replaced by the given text, which
// is what `--note-file -` reads.
func withStdin(t *testing.T, text string, body func()) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stdin
	os.Stdin = read
	go func() {
		_, _ = write.WriteString(text)
		write.Close()
	}()
	defer func() { os.Stdin = previous }()
	body()
}

// The headline case: who, in what mode, and the briefing, in one invocation.
func TestDelegateCarriesTheNoteInOneCommand(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	result := runCLI(t, dir, "delegate", "ffff0002", "implement",
		"--note", "Start with the failing test.")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	marker := markerOf(t, dir, "ffff0002")
	if marker["mode"] != "implement" || marker["note"] != "Start with the failing test." {
		t.Fatalf("marker = %v", marker)
	}
}

// A person can be given a mode and a briefing too: who holds the work and what
// kind of delegation it is are orthogonal facts.
func TestDelegateToAPersonTakesAModeAndANoteFile(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	briefing := "Please review the API shape.\n\nThe открытый question is the note limit.\n"
	var result cliResult
	withStdin(t, briefing, func() {
		result = runCLI(t, dir, "delegate", "ffff0002", "--to", "pat@example.com", "refine",
			"--note-file", "-")
	})
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	marker := markerOf(t, dir, "ffff0002")
	if marker["kind"] != "human" || marker["assignee"] != "pat@example.com" {
		t.Fatalf("marker = %v", marker)
	}
	if marker["mode"] != "refine" {
		t.Fatalf("a human delegation dropped the mode: %v", marker)
	}
	// The store trims the surrounding whitespace a file inevitably carries, and
	// keeps everything between: paragraphs and non-ASCII text both survive.
	if marker["note"] != strings.TrimSpace(briefing) {
		t.Fatalf("note = %q, want %q", marker["note"], strings.TrimSpace(briefing))
	}
}

// A note file that is not stdin works the same way, and an unreadable one says
// so rather than delegating with an empty briefing.
func TestNoteFileReadsFromDiskAndRefusesWhatItCannotRead(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	path := dir + "/brief.md"
	if err := os.WriteFile(path, []byte("Land it behind the flag.\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	result := runCLI(t, dir, "delegate", "ffff0002", "research", "--note-file", path)
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if markerOf(t, dir, "ffff0002")["note"] != "Land it behind the flag." {
		t.Fatalf("marker = %v", markerOf(t, dir, "ffff0002"))
	}

	missing := runCLI(t, dir, "delegate", "ffff0003", "research", "--note-file", dir+"/absent.md")
	if missing.status == 0 || !strings.Contains(missing.stderr, "cannot read the note file") {
		t.Fatalf("exit %d, stderr %q", missing.status, missing.stderr)
	}
	if markerOf(t, dir, "ffff0003") != nil {
		t.Fatal("a refused note file still delegated the task")
	}

	both := runCLI(t, dir, "delegate", "ffff0003", "research", "--note", "x", "--note-file", path)
	if both.status == 0 || !strings.Contains(both.stderr, "not both") {
		t.Fatalf("exit %d, stderr %q", both.status, both.stderr)
	}
}

// A CRLF file is a briefing, not a control-character violation.
//
// The note schema allows `\n` so a briefing can have paragraphs and refuses
// every other control character — `\r` included. `--note-file` is the flag that
// exists to carry multi-paragraph prose, so a file written on Windows or pasted
// through a CRLF-preserving editor would be refused for a character the user
// cannot see. Line endings are normalized at this input boundary; the schema is
// left exactly as strict.
func TestNoteFileNormalizesWindowsLineEndings(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	path := dir + "/crlf.md"
	if err := os.WriteFile(path, []byte("line one\r\n\r\nline two\r\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	result := runCLI(t, dir, "delegate", "ffff0002", "implement", "--note-file", path)
	if result.status != 0 {
		t.Fatalf("a CRLF briefing was refused: exit %d, stderr %q", result.status, result.stderr)
	}
	if got := markerOf(t, dir, "ffff0002")["note"]; got != "line one\n\nline two" {
		t.Fatalf("note = %q", got)
	}

	// A lone CR — a classic-Mac or mangled paste — is a line ending too, and
	// refusing it would be the same invisible-character refusal.
	lone := dir + "/cr.md"
	if err := os.WriteFile(lone, []byte("first\rsecond"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	if result := runCLI(t, dir, "delegate", "ffff0003", "implement",
		"--note-file", lone); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := markerOf(t, dir, "ffff0003")["note"]; got != "first\nsecond" {
		t.Fatalf("note = %q", got)
	}
}

// An option that ate the next flag must be loud.
//
// `--note --keep-state` is the argument parser doing what it is told. For every
// other option that misfire fails the option's own validation; a note is FREE
// TEXT, so a flag name is a perfectly valid briefing and the task would be
// delegated with `--keep-state` as its instructions and the flag's intent
// silently dropped, exit 0.
func TestNoteRefusesAFlagAsItsValue(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	swallowed := runCLI(t, dir, "delegate", "ffff0002", "--to", "pat@example.com",
		"--note", "--keep-state")
	if swallowed.status == 0 {
		t.Fatalf("a swallowed flag was accepted: stdout %q", swallowed.stdout)
	}
	if !strings.Contains(swallowed.stderr, "--note expects a value") {
		t.Fatalf("stderr = %q", swallowed.stderr)
	}
	if markerOf(t, dir, "ffff0002") != nil {
		t.Fatal("a refused command still delegated the task")
	}
	if file := runCLI(t, dir, "delegate", "ffff0002", "implement",
		"--note-file", "--json"); file.status == 0 ||
		!strings.Contains(file.stderr, "--note-file expects a value") {
		t.Fatalf("exit %d, stderr %q", file.status, file.stderr)
	}

	// A note that legitimately begins with a single dash is not a flag
	// spelling and must still work — that is the whole reason the rule is
	// "--" and not "-".
	dashed := runCLI(t, dir, "delegate", "ffff0002", "implement", "--note", "-x is dangerous")
	if dashed.status != 0 {
		t.Fatalf("a dash-leading briefing was refused: exit %d, stderr %q",
			dashed.status, dashed.stderr)
	}
	if got := markerOf(t, dir, "ffff0002")["note"]; got != "-x is dangerous" {
		t.Fatalf("note = %q", got)
	}
}

// The briefing survives the round trip byte for byte, including the two things
// a text pipeline is most likely to mangle.
func TestDelegationNoteRoundTripsNewlinesAndMultibyte(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	note := "Ligne un — accents.\n\nDeuxième: 日本語と emoji 🚀.\nFin."
	result := runCLI(t, dir, "delegate", "ffff0002", "refine", "--note", note)
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := markerOf(t, dir, "ffff0002")["note"]; got != note {
		t.Fatalf("note = %q, want %q", got, note)
	}

	// `show` prints it in full, one output line per note line — a briefing the
	// receiver has to act on must not be truncated where it is being read.
	shown := runCLI(t, dir, "show", "ffff0002")
	for _, line := range strings.Split(note, "\n") {
		if line != "" && !strings.Contains(shown.stdout, line) {
			t.Fatalf("show dropped %q:\n%s", line, shown.stdout)
		}
	}
	if !strings.Contains(shown.stdout, "  note:      Ligne un — accents.") {
		t.Fatalf("show does not label the note:\n%s", shown.stdout)
	}
}

// The bound is the schema's, counted in RUNES, and the refusal quotes it.
func TestOverlongDelegationNoteIsRefusedWithTheLimit(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	over := strings.Repeat("é", 2001)
	if utf8.RuneCountInString(over) != 2001 {
		t.Fatalf("fixture is %d runes", utf8.RuneCountInString(over))
	}
	refused := runCLI(t, dir, "delegate", "ffff0002", "refine", "--note", over)
	if refused.status == 0 {
		t.Fatal("a 2001-rune note was accepted")
	}
	if !strings.Contains(refused.stderr, "2000") || !strings.Contains(refused.stderr, "2001") {
		t.Fatalf("refusal does not quote the limit and the length: %q", refused.stderr)
	}
	if markerOf(t, dir, "ffff0002") != nil {
		t.Fatal("a refused note still delegated the task")
	}

	fits := runCLI(t, dir, "delegate", "ffff0002", "refine", "--note", strings.Repeat("é", 2000))
	if fits.status != 0 {
		t.Fatalf("2000 multibyte runes refused: %q", fits.stderr)
	}
}

// Clearing. The spelling is the work reference's, and for the same reason: both
// words are reserved mode names, so a clear instruction can never be read as a
// mode. Re-delegating WITHOUT saying anything about the note leaves it alone.
func TestClearingADelegationNote(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	runCLI(t, dir, "delegate", "ffff0002", "refine", "--note", "Read the RFC first.")

	kept := runCLI(t, dir, "delegate", "ffff0002", "implement")
	if kept.status != 0 {
		t.Fatalf("exit %d, stderr %q", kept.status, kept.stderr)
	}
	if markerOf(t, dir, "ffff0002")["note"] != "Read the RFC first." {
		t.Fatalf("an omitted note changed the briefing: %v", markerOf(t, dir, "ffff0002"))
	}

	for _, word := range []string{"off", "none", "OFF", ""} {
		runCLI(t, dir, "delegate", "ffff0002", "implement", "--note", "Read the RFC first.")
		cleared := runCLI(t, dir, "delegate", "ffff0002", "--note", word)
		if cleared.status != 0 {
			t.Fatalf("--note %q: exit %d, stderr %q", word, cleared.status, cleared.stderr)
		}
		if !strings.Contains(cleared.stdout, "delegation note cleared") {
			t.Fatalf("--note %q printed %q", word, cleared.stdout)
		}
		if note, present := markerOf(t, dir, "ffff0002")["note"]; present {
			t.Fatalf("--note %q left %v", word, note)
		}
	}
}

// The note has a rewrite form that does not restate the delegation: an owner
// correcting instructions should not have to name the mode again.
func TestNoteOnlyDelegateRewritesTheBriefing(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	runCLI(t, dir, "delegate", "ffff0002", "refine")

	set := runCLI(t, dir, "delegate", "ffff0002", "--note", "Land it behind the flag.")
	if set.status != 0 {
		t.Fatalf("exit %d, stderr %q", set.status, set.stderr)
	}
	marker := markerOf(t, dir, "ffff0002")
	if marker["note"] != "Land it behind the flag." || marker["mode"] != "refine" {
		t.Fatalf("marker = %v", marker)
	}

	// An undelegated task has no briefing to write.
	orphan := runCLI(t, dir, "delegate", "ffff0003", "--note", "hello")
	if orphan.status == 0 || !strings.Contains(orphan.stderr, "not delegated") {
		t.Fatalf("exit %d, stderr %q", orphan.status, orphan.stderr)
	}
}

// One command, ONE undo step. Undo restores the marker the task had before —
// not a half-undone delegation with the briefing still attached.
func TestDelegateWithANoteIsOneUndoStep(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	runCLI(t, dir, "delegate", "ffff0002", "refine", "--note", "First briefing.")
	before := markerOf(t, dir, "ffff0002")

	if result := runCLI(t, dir, "delegate", "ffff0002", "implement",
		"--note", "Second briefing."); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if undone := runCLI(t, dir, "undo"); undone.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undone.status, undone.stderr)
	}

	after := markerOf(t, dir, "ffff0002")
	if after["mode"] != before["mode"] || after["note"] != before["note"] {
		t.Fatalf("one undo did not restore the prior marker: %v, want %v", after, before)
	}

	// The human delegation composes a STATE change on top, and that is still
	// one undo: the state and the marker come back together.
	runCLI(t, dir, "delegate", "ffff0002", "--to", "pat@example.com", "--note", "Over to you.")
	if state := taskState(t, dir, "ffff0002"); state != "WAITING" {
		t.Fatalf("state = %q", state)
	}
	runCLI(t, dir, "undo")
	restored := markerOf(t, dir, "ffff0002")
	if restored["note"] != before["note"] || restored["mode"] != before["mode"] {
		t.Fatalf("marker = %v, want %v", restored, before)
	}
	if state := taskState(t, dir, "ffff0002"); state != "NEXT" {
		t.Fatalf("one undo left the state at %q", state)
	}
}

func taskState(t *testing.T, dir, ref string) string {
	t.Helper()
	result := runCLI(t, dir, "show", ref, "--json")
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &decoded); err != nil {
		t.Fatalf("show --json: %v", err)
	}
	state, _ := decoded["state"].(string)
	return state
}

// The claimable queue is what an agent heartbeat reads, so the briefing has to
// be in ITS json — a queue row that made an agent fetch the task again to learn
// what it was asked to do would defeat the point.
func TestAgentReadyJSONCarriesTheModeAndTheNote(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	runCLI(t, dir, "delegate", "ffff0002", "implement", "--note", "Keep the diff small.")

	result := runCLI(t, dir, "list", "--agent-ready", "--json")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
		t.Fatalf("not JSON (%v): %s", err, result.stdout)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	marker, _ := rows[0]["delegation"].(map[string]any)
	if marker["mode"] != "implement" || marker["note"] != "Keep the diff small." {
		t.Fatalf("queue row marker = %v", marker)
	}
}

// Both delegation list scopes show the mode and preview the briefing. A list is
// a scan, so the note is a preview; `show` and `--json` carry it whole.
func TestDelegationListScopesRenderTheModeAndTheNote(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	runCLI(t, dir, "delegate", "ffff0002", "implement", "--note", "Keep the diff small.")
	runCLI(t, dir, "delegate", "ffff0003", "--to", "pat@example.com", "refine",
		"--note", "First line of the brief.\nSecond line.")

	ready := runCLI(t, dir, "list", "--agent-ready")
	if !strings.Contains(ready.stdout, "agent-ready (implement): Ship the thing") {
		t.Fatalf("agent-ready listing:\n%s", ready.stdout)
	}
	if !strings.Contains(ready.stdout, "note: Keep the diff small.") {
		t.Fatalf("agent-ready listing has no note:\n%s", ready.stdout)
	}

	delegated := runCLI(t, dir, "list", "--delegated")
	if !strings.Contains(delegated.stdout, "delegated → pat@example.com (refine, WAITING): Second thing") {
		t.Fatalf("delegated listing:\n%s", delegated.stdout)
	}
	// A multi-line briefing previews its first line, marked as continuing.
	if !strings.Contains(delegated.stdout, "note: First line of the brief. …") {
		t.Fatalf("delegated listing note preview:\n%s", delegated.stdout)
	}
	if strings.Contains(delegated.stdout, "Second line.") {
		t.Fatalf("a list row printed the whole briefing:\n%s", delegated.stdout)
	}

	// `show` renders the person, the mode and the briefing in full.
	shown := runCLI(t, dir, "show", "ffff0003")
	if !strings.Contains(shown.stdout, "→ pat@example.com (refine)") {
		t.Fatalf("show:\n%s", shown.stdout)
	}
	if !strings.Contains(shown.stdout, "Second line.") {
		t.Fatalf("show truncated the briefing:\n%s", shown.stdout)
	}
}

// A refusal quotes the vocabulary actually ENFORCED, even when the note is the
// interesting part of the command.
func TestNoteBearingDelegateRefusalQuotesTheConfiguredVocabulary(t *testing.T) {
	dir := seedStore(t, delegationNoteFixture)
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	refused := runCLI(t, dir, "delegate", "ffff0002", "research", "--note", "anything")
	if refused.status == 0 {
		t.Fatal("a mode outside the configured list was accepted")
	}
	if !strings.Contains(refused.stderr, "triage/ship") {
		t.Fatalf("refusal does not quote the configured list: %q", refused.stderr)
	}
	if strings.Contains(refused.stderr, "refine/research/implement") {
		t.Fatalf("refusal quotes the built-in list: %q", refused.stderr)
	}
	// The usage line quotes it too, so the two answers about the same word agree.
	usage := runCLI(t, dir, "delegate")
	if !strings.Contains(usage.stderr, "<triage|ship>") {
		t.Fatalf("usage = %q", usage.stderr)
	}
	if !strings.Contains(usage.stderr, "--note-file") {
		t.Fatalf("usage does not mention --note-file: %q", usage.stderr)
	}
}
