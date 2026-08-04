package agent

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/ansi"
)

// Mirrors test/test_agent_activity.rb.

func f(v float64) *float64 { return &v }

type snapOpts struct {
	id                    int
	status                Status
	prompt, output, error string
	provider, model       string
	startedAt, finishedAt *float64
}

func request(mutate func(*snapOpts)) Snapshot {
	o := snapOpts{
		prompt: "capture milk", output: "done",
		provider: "claude-cli", model: "sonnet",
		startedAt: f(10), finishedAt: f(18),
	}
	if mutate != nil {
		mutate(&o)
	}
	var exit *int
	if o.status == Succeeded {
		zero := 0
		exit = &zero
	}
	label := "claude:" + o.model
	if o.provider != "claude-cli" {
		label = o.provider + ":" + o.model
	}
	return Snapshot{
		ID: o.id, Prompt: o.prompt,
		Entry:  SimpleEntry{ProviderName: o.provider, ModelName: o.model, Label: label},
		Status: o.status, QueuedAt: 5, StartedAt: o.startedAt, FinishedAt: o.finishedAt,
		Output: o.output, ExitStatus: exit, Error: o.error,
	}
}

func plain(c Content) string {
	out := make([]string, 0, len(c.Lines))
	for _, l := range c.Lines {
		out = append(out, ansi.Strip(l))
	}
	return strings.Join(out, "\n")
}

func TestRendersPromptResultEntryStatusAndElapsedPerRequest(t *testing.T) {
	requests := []Snapshot{
		request(func(o *snapOpts) { o.id, o.status = 1, Succeeded }),
		request(func(o *snapOpts) {
			o.id, o.status = 2, Running
			o.prompt, o.output = "move flight", "thinking"
			o.provider, o.model = "hermes", "qwen"
			o.finishedAt = nil
		}),
		request(func(o *snapOpts) {
			o.id, o.status = 3, Queued
			o.prompt, o.output = "review inbox", ""
			o.startedAt, o.finishedAt = nil, nil
		}),
	}
	content := Activity(nil, requests, 52, 80)
	text := plain(content)

	if content.Title != "Agent activity · 1 running · 1 queued · 1 finished" {
		t.Fatalf("title = %q", content.Title)
	}
	for _, want := range []string{
		"✓ #1 · claude:sonnet · succeeded · 8s",
		"⠸ #2 · hermes:qwen · running · 42s",
		"○ #3 · claude:sonnet · queued #1",
		"request  capture milk",
		"result   done",
		"result   (waiting)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if len(content.Lines) != len(content.FilterGroups) {
		t.Fatalf("%d lines vs %d filter groups", len(content.Lines), len(content.FilterGroups))
	}
	unique := map[int]bool{}
	for _, g := range content.FilterGroups {
		unique[g] = true
	}
	if len(unique) != 3 {
		t.Fatalf("%d filter groups, want one per request", len(unique))
	}
}

func TestFailureAndEmptyOrLiveOutputRemainDistinct(t *testing.T) {
	requests := []Snapshot{
		request(func(o *snapOpts) { o.id, o.status, o.output, o.error = 1, Failed, "", "agent exited 7" }),
		request(func(o *snapOpts) { o.id, o.status, o.output, o.error = 2, Cancelled, "partial", "cancelled" }),
		request(func(o *snapOpts) { o.id, o.status, o.output, o.finishedAt = 3, Running, "", nil }),
	}
	text := plain(Activity(nil, requests, 20, 50))

	for _, want := range []string{
		"✗ #1", "result   (no output)", "error    agent exited 7",
		"– #2", "partial", "(working; no output yet)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	// A cancellation's own "cancelled" note is not shown as an error line.
	if strings.Contains(text, "error    cancelled") {
		t.Fatalf("cancellation shown as an error:\n%s", text)
	}
}

func TestWrapsUnicodePromptAndTranscriptToRequestedWidth(t *testing.T) {
	content := Activity(nil, []Snapshot{
		request(func(o *snapOpts) {
			o.id, o.status = 1, Succeeded
			o.prompt = strings.Repeat("界", 40)
			o.output = strings.Repeat("✨ result ", 20)
		}),
	}, 20, 40)

	if len(content.Lines) <= 4 {
		t.Fatalf("%d lines, want the content wrapped", len(content.Lines))
	}
	for _, line := range content.Lines {
		if ansi.VisLen(line) > 120 {
			t.Fatalf("line over budget (%d cells): %q", ansi.VisLen(line), line)
		}
	}
}

func TestEmptyHistoryHasAClearMessage(t *testing.T) {
	content := Activity(nil, nil, 0, 80)
	if !strings.Contains(plain(content), "No agent requests") {
		t.Fatalf("content = %#v", content)
	}
	if content.Title != "Agent activity · 0 running · 0 queued · 0 finished" {
		t.Fatalf("title = %q", content.Title)
	}
}

func TestElapsedSwitchesToMinutesAndSeconds(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{0, "0s"}, {8.4, "8s"}, {59, "59s"}, {60, "1m00s"}, {61.6, "1m02s"}, {3600, "60m00s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.seconds); got != c.want {
			t.Fatalf("formatElapsed(%v) = %q, want %q", c.seconds, got, c.want)
		}
	}
}

func TestElapsedIsZeroBeforeAStartAndFrozenAfterAFinish(t *testing.T) {
	queued := request(func(o *snapOpts) { o.status, o.startedAt, o.finishedAt = Queued, nil, nil })
	if got := queued.Elapsed(999); got != 0 {
		t.Fatalf("queued elapsed = %v", got)
	}
	done := request(func(o *snapOpts) { o.status = Succeeded })
	if got := done.Elapsed(999); got != 8 {
		t.Fatalf("finished elapsed = %v, want the frozen 8s", got)
	}
}

func TestTranscriptWithInvalidUTF8IsScrubbedNotDropped(t *testing.T) {
	text := plain(Activity(nil, []Snapshot{
		request(func(o *snapOpts) { o.id, o.status, o.output = 1, Succeeded, "task done \xE2\x9C" }),
	}, 20, 80))
	if !strings.Contains(text, "task done") {
		t.Fatalf("content lost:\n%s", text)
	}
}
