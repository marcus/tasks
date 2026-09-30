package main

import (
	"strings"
	"testing"
)

// `due --explain` / `schedule --explain` and `lead --explain` are the CLI twins
// of GET /dates/parse and GET /lead/explain: same parser, same --json payload
// (the HTTP `data` member), no task and no write. The pinned clock is Monday
// 2026-07-20 12:00 UTC.

func TestCLIDateExplainNeedsNoTaskAndMatchesTheHTTPPayload(t *testing.T) {
	dir := seedStore(t, mutationFixture)

	human := runUnchanged(t, dir, "due", "--explain", "fri", "4pm")
	if human.stdout != "2026-07-24 16:00 — Fri 24 Jul, 4:00p\n" {
		t.Errorf("stdout = %q", human.stdout)
	}
	structured := runUnchanged(t, dir, "schedule", "--explain", "fri 4pm Europe/Berlin", "--json")
	want := `{"input":"fri 4pm Europe/Berlin","date":"2026-07-24",` +
		`"time":{"local":"16:00","timezone":"Europe/Berlin","fold":0},"human":"Fri 24 Jul, 4:00p Europe/Berlin"}` + "\n"
	if structured.stdout != want {
		t.Errorf("json:\n got %s\nwant %s", structured.stdout, want)
	}
	// The --timezone flag is the CLI spelling of the trailing zone word.
	flagged := runUnchanged(t, dir, "due", "--explain", "fri 4pm", "--timezone", "Europe/Berlin", "--json")
	if flagged.stdout != strings.Replace(want, "fri 4pm Europe/Berlin", "fri 4pm", 1) {
		t.Errorf("--timezone: %s", flagged.stdout)
	}

	bad := runCLI(t, dir, "due", "--explain", "blursday", "--json")
	if bad.status != 1 || bad.stdout != `{"input":"blursday","error":"unrecognized date: blursday"}`+"\n" {
		t.Errorf("exit %d, stdout %q", bad.status, bad.stdout)
	}
	bad = runCLI(t, dir, "due", "--explain", "blursday")
	if bad.status != 1 || !strings.Contains(bad.stderr, "unrecognized date: blursday") {
		t.Errorf("exit %d, stderr %q", bad.status, bad.stderr)
	}
	refused := runCLI(t, dir, "due", "--explain", "fri", "--dry-run")
	if refused.status != 1 || !strings.Contains(refused.stderr, "--explain previews a date") {
		t.Errorf("exit %d, stderr %q", refused.status, refused.stderr)
	}
}

func TestCLIDateExplainHonorsTheConfiguredDateOrder(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if got := runUnchanged(t, dir, "due", "--explain", "10/2").stdout; !strings.HasPrefix(got, "2026-10-02 ") {
		t.Errorf("mdy: %q", got)
	}
	seedConfig(t, dir, "date_order = dmy\n")
	if got := runUnchanged(t, dir, "due", "--explain", "10/2").stdout; !strings.HasPrefix(got, "2027-02-10 ") {
		t.Errorf("dmy: %q", got)
	}
}

func TestCLILeadExplainPreviewsTheSpanAndItsWindow(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	human := runUnchanged(t, dir, "lead", "--explain", "3 weeks", "--anchor", "2026-11-01")
	if human.stdout != "3w — 3 weeks before 2026-11-01 — opens 2026-10-11\n" {
		t.Errorf("stdout = %q", human.stdout)
	}
	structured := runUnchanged(t, dir, "lead", "--explain", "3 weeks", "--anchor", "2026-11-01", "--json")
	if want := `{"input":"3 weeks","canonical":"3w","human":"3 weeks","opens":"2026-10-11"}` + "\n"; structured.stdout != want {
		t.Errorf("json:\n got %s\nwant %s", structured.stdout, want)
	}
	if off := runUnchanged(t, dir, "lead", "--explain", "off"); off.stdout != "off — clears any lead time on the task\n" {
		t.Errorf("off: %q", off.stdout)
	}
	bad := runCLI(t, dir, "lead", "--explain", "soonish", "--json")
	if bad.status != 1 || !strings.Contains(bad.stdout, `"error"`) {
		t.Errorf("exit %d, stdout %q", bad.status, bad.stdout)
	}
	misplaced := runCLI(t, dir, "lead", "Ship the release", "3w", "--anchor", "2026-11-01")
	if misplaced.status != 1 || !strings.Contains(misplaced.stderr, "--anchor only applies with --explain") {
		t.Errorf("exit %d, stderr %q", misplaced.status, misplaced.stderr)
	}
}

// `due --explain` previews what `due` stores, so the write takes the same
// trailing zone word — and naming the zone twice is refused, not resolved.
func TestCLIDueTakesTheSameTrailingWordsItsPreviewDoes(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "due", "--explain", "fri 4pm Europe/Berlin")
	if !strings.HasPrefix(preview.stdout, "2026-07-24 16:00 Europe/Berlin — ") {
		t.Fatalf("preview = %q", preview.stdout)
	}
	written := runCLI(t, dir, "due", "Ship the release", "fri 4pm Europe/Berlin")
	if written.status != 0 {
		t.Fatalf("exit %d, stderr %q", written.status, written.stderr)
	}
	if got := storeBytes(t, dir); !strings.Contains(got, `"local":"16:00"`) || !strings.Contains(got, "Europe/Berlin") {
		t.Errorf("the write did not store the previewed value:\n%s", got)
	}
	twice := runCLI(t, dir, "due", "--explain", "fri 4pm Europe/Berlin", "--timezone", "UTC")
	if twice.status != 1 || !strings.Contains(twice.stderr, "the zone is given twice") {
		t.Errorf("exit %d, stderr %q", twice.status, twice.stderr)
	}
	foldTwice := runCLI(t, dir, "due", "Ship the release", "2026-11-01 01:30 America/New_York fold=later", "--fold", "later")
	if foldTwice.status != 1 || !strings.Contains(foldTwice.stderr, "the fold is given twice") {
		t.Errorf("exit %d, stderr %q", foldTwice.status, foldTwice.stderr)
	}
}

func TestCLILeadExplainNamesWhatTheSpanMeasuresFrom(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if got := runUnchanged(t, dir, "lead", "--explain", "3w").stdout; got != "3w — 3 weeks before the task's date\n" {
		t.Errorf("no anchor: %q", got)
	}
	if got := runUnchanged(t, dir, "lead", "--explain", "5h", "--anchor", "2026-11-01").stdout; got != "5h — 5 hours before 2026-11-01\n" {
		t.Errorf("hour span: %q", got)
	}
}
