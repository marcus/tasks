package main

import (
	"reflect"
	"strings"
	"testing"
)

// `capture --link` / `propose --link` exist so a context URL costs ONE write.
// These tests hold the three things that makes true and would otherwise rot:
// the links reach the file in caller order with their labels, they land in the
// same undo step as the task, and a bad link refuses the whole create rather
// than filing a task that is missing its context.

// storedLinks reads the `links` member off a record as ordered {url,label} pairs.
func storedLinks(t *testing.T, dir, title string) [][2]string {
	t.Helper()
	raw, present := recordForTitle(t, dir, title)["links"]
	if !present {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		t.Fatalf("links on %q = %#v, want an array", title, raw)
	}
	pairs := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("link entry = %#v, want an object", entry)
		}
		url, _ := object["url"].(string)
		label, _ := object["label"].(string)
		pairs = append(pairs, [2]string{url, label})
	}
	return pairs
}

func TestCLICaptureLinkStoresOrderedLabelledLinksInOneWrite(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "capture", "Renew the lease",
		"--link", "https://example.com/thread", "--label", "slack thread",
		"--link", "https://example.com/doc",
		"--link", "https://example.com/ticket", "--label", "JIRA-9")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	want := [][2]string{
		{"https://example.com/thread", "slack thread"},
		{"https://example.com/doc", ""},
		{"https://example.com/ticket", "JIRA-9"},
	}
	if got := storedLinks(t, dir, "Renew the lease"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLIProposeLinkStoresLinksAndKeepsProposedState(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "propose", "Ask about the renewal",
		"--link", "https://example.com/thread", "--label", "thread")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	record := recordForTitle(t, dir, "Ask about the renewal")
	if record["state"] != "PROPOSED" {
		t.Fatalf("state = %v, want PROPOSED", record["state"])
	}
	want := [][2]string{{"https://example.com/thread", "thread"}}
	if got := storedLinks(t, dir, "Ask about the renewal"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

// One undo step is the whole point of doing this at create time: `link add`
// after a capture is a second history entry, and undoing the capture would
// otherwise leave the link behind — or take two undos to clear.
func TestCLICaptureLinkIsUndoneWithTheTaskInOneStep(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	if result := runCLI(t, dir, "capture", "Chase the invoice",
		"--link", "https://example.com/invoice", "--label", "invoice"); result.status != 0 {
		t.Fatalf("capture: exit %d, stderr %q", result.status, result.stderr)
	}
	undo := runCLI(t, dir, "undo")
	if undo.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undo.status, undo.stderr)
	}
	if after := storeBytes(t, dir); after != before {
		t.Fatalf("one undo did not restore the store:\n%s", after)
	}
	if strings.Contains(storeBytes(t, dir), "example.com/invoice") {
		t.Fatal("the formal link survived the capture's undo")
	}
}

func TestCLICaptureLinkExpandsAConfiguredShorthandAndDefaultsItsLabel(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	seedConfig(t, dir, "link.gh = https://github.com/acme/app/issues/%s\n")
	result := runCLI(t, dir, "capture", "Fix the crash", "--link", "gh:12")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	want := [][2]string{{"https://github.com/acme/app/issues/12", "gh:12"}}
	if got := storedLinks(t, dir, "Fix the crash"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLICaptureLinkRefusalsWriteNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"non-web URL", []string{"capture", "Bad", "--link", "ftp://example.com/x"},
			"link URL must be http://, https://, or a configured shorthand"},
		{"not a URL at all", []string{"capture", "Bad", "--link", "just words"},
			"link URL must be http://, https://, or a configured shorthand"},
		{"missing value", []string{"capture", "Bad", "--link"},
			"missing value for --link"},
		{"duplicate URL", []string{"capture", "Bad",
			"--link", "https://example.com/a", "--link", "https://example.com/a"},
			"duplicate formal link URL: https://example.com/a"},
		{"orphan label", []string{"capture", "Bad", "--label", "stray"},
			"--label must immediately follow a --link"},
		{"label after another flag", []string{"capture", "Bad",
			"--link", "https://example.com/a", "--tag", "t", "--label", "late"},
			"--label must immediately follow a --link"},
		{"blank label", []string{"capture", "Bad", "--link", "https://example.com/a", "--label", " "},
			"link label must be non-empty trimmed single-line text"},
		{"propose too", []string{"propose", "Bad", "--link", "nope"},
			"link URL must be http://, https://, or a configured shorthand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedStore(t, mutationFixture)
			before := storeBytes(t, dir)
			result := runCLI(t, dir, tc.argv...)
			if result.status == 0 {
				t.Fatalf("expected a refusal, got exit 0 (stdout %q)", result.stdout)
			}
			if !strings.Contains(result.stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to mention %q", result.stderr, tc.want)
			}
			if after := storeBytes(t, dir); after != before {
				t.Fatalf("a refused capture wrote to the store:\n%s", after)
			}
		})
	}
}

// The after-the-fact path is untouched: `link add` still appends to a task that
// was captured with its own links, and it appends AFTER them.
func TestCLILinkAddStillAppendsAfterACaptureWithLinks(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "capture", "Both paths",
		"--link", "https://example.com/first", "--label", "first"); result.status != 0 {
		t.Fatalf("capture: exit %d, stderr %q", result.status, result.stderr)
	}
	if result := runCLI(t, dir, "link", "add", "Both paths",
		"https://example.com/second", "--label", "second"); result.status != 0 {
		t.Fatalf("link add: exit %d, stderr %q", result.status, result.stderr)
	}
	want := [][2]string{
		{"https://example.com/first", "first"},
		{"https://example.com/second", "second"},
	}
	if got := storedLinks(t, dir, "Both paths"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
	// And it still refuses a URL the capture already stored.
	result := runCLI(t, dir, "link", "add", "Both paths", "https://example.com/first")
	if result.status == 0 ||
		!strings.Contains(result.stderr, "formal link already exists: https://example.com/first") {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
}

// -- title-URL lifting (docs/ideas.md item 6) --------------------------------

func TestCLICaptureLiftsATrailingTitleURLIntoAFormalLink(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "capture", "read the renewal terms https://example.com/terms")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, "INBOX read the renewal terms\n") {
		t.Fatalf("stdout = %q", result.stdout)
	}
	want := [][2]string{{"https://example.com/terms", ""}}
	if got := storedLinks(t, dir, "read the renewal terms"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLIProposeLiftsATrailingTitleURLAfterExplicitLinks(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "propose", "review the vendor SOW https://example.com/sow",
		"--link", "https://example.com/thread", "--label", "thread")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	want := [][2]string{
		{"https://example.com/thread", "thread"},
		{"https://example.com/sow", ""},
	}
	if got := storedLinks(t, dir, "review the vendor SOW"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLICaptureTitleThatIsOnlyAURLKeepsItsTitleAndGainsTheLink(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "capture", "https://example.com/only"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	want := [][2]string{{"https://example.com/only", ""}}
	if got := storedLinks(t, dir, "https://example.com/only"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLICaptureDoesNotLiftATitleURLTwiceOrDropItsLabel(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "capture", "sign it https://example.com/same",
		"--link", "https://example.com/same", "--label", "the contract")
	if result.status != 0 || result.stderr != "" {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	// The title is left alone, because the URL was already named explicitly and
	// the caller's label is the better of the two facts about it.
	want := [][2]string{{"https://example.com/same", "the contract"}}
	if got := storedLinks(t, dir, "sign it https://example.com/same"); !reflect.DeepEqual(got, want) {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestCLICaptureLeavesANonTrailingOrNonWebTitleURLAlone(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	for _, title := range []string{
		"https://example.com/mid is the thread to read",
		"see example.com/bare",
		"ftp://example.com/file needs fetching",
	} {
		if result := runCLI(t, dir, "capture", title); result.status != 0 {
			t.Fatalf("%q: exit %d, stderr %q", title, result.status, result.stderr)
		}
		if got := storedLinks(t, dir, title); got != nil {
			t.Fatalf("%q: links = %#v, want none and the title unchanged", title, got)
		}
	}
}

// The preview must show the SAME title and the same absence of surprises the
// write would produce, lifting included.
func TestCLICaptureDryRunPreviewsTheLiftedTitleAndWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "capture", "read the terms https://example.com/terms",
		"--link", "https://example.com/thread", "--dry-run")
	if preview.stdout != "would capture under Inbox: INBOX read the terms\n" {
		t.Errorf("stdout = %q", preview.stdout)
	}
}
