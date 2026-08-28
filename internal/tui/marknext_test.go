package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// markNextFixture carries one row of every state `N` has to have an answer for:
// an undecided proposal, a closed task, and the open work it actually promotes.
const markNextFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"random thought about the garden"}
{"type":"task","id":"aaaa0010","parent":"aaaa0001","state":"PROPOSED","title":"Pending proposal"}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0006","parent":"aaaa0003","state":"TODO","priority":"A","title":"Midyear self-eval","tags":["@computer"],"scheduled":"2026-07-03"}
{"type":"task","id":"aaaa0008","parent":"aaaa0003","state":"DONE","title":"Old finished thing","closed":"2026-06-20"}
{"type":"task","id":"aaaa000a","parent":"aaaa0003","state":"NEXT","title":"Water the plants","tags":["@home"]}
`

func liveState(t *testing.T, harness *modelHarness, id string) string {
	t.Helper()
	item, ok := harness.model.read.Queries().FindLive(id)
	if !ok {
		t.Fatalf("task %s is gone", id)
	}
	return item.State
}

// The headline journey: one keystroke on an open TODO and the task is a next
// action, on the tab that only lists next actions.
func TestMarkNextPromotesATodoOntoTheNextTab(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0006")
	harness.press('N')

	if got := liveState(t, harness, "aaaa0006"); got != "NEXT" {
		t.Fatalf("state = %q, want NEXT", got)
	}
	if !strings.Contains(harness.model.FlashMessage(), "NEXT: Midyear self-eval") {
		t.Errorf("flash = %q", harness.model.FlashMessage())
	}
	harness.model.SwitchView(ViewNext)
	if text := rowsText(harness); !strings.Contains(text, "Midyear self-eval") {
		t.Fatalf("the promoted task is not on the Next tab:\n%s", text)
	}
}

// The detail panel is the other place a reader decides what to work on next, so
// `N` has to reach the same task from inside it.
func TestMarkNextWorksFromTheDetailPanel(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0006")
	harness.model.OpenDetail()
	if harness.model.CurrentSpatialFocus() != SpatialFocusDetail {
		t.Fatalf("detail did not take focus: %q", harness.model.CurrentSpatialFocus())
	}
	harness.press('N')

	if got := liveState(t, harness, "aaaa0006"); got != "NEXT" {
		t.Fatalf("state = %q, want NEXT", got)
	}
}

// An INBOX item is open work, so `N` promotes it too — and it does so WITHOUT
// dating it. The INBOX→TODO promotion belongs to `d`; this key must not smuggle
// a date in behind it.
func TestMarkNextPromotesAnInboxItemWithoutDatingIt(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewInbox)
	harness.selectRowByID("aaaa0002")
	harness.press('N')

	item, ok := harness.model.read.Queries().FindLive("aaaa0002")
	if !ok {
		t.Fatal("the inbox item is gone")
	}
	if item.State != "NEXT" {
		t.Fatalf("state = %q, want NEXT", item.State)
	}
	if item.Scheduled != "" || item.Deadline != "" {
		t.Errorf("N dated the task: scheduled=%q deadline=%q", item.Scheduled, item.Deadline)
	}
}

// Pressing `N` on a task that is already NEXT is a no-op, not a toggle: the file
// is untouched, so there is no empty step for `u` to rewind past either.
func TestMarkNextOnAnAlreadyNextTaskWritesNothing(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID("aaaa000a")
	before := harness.content()
	harness.press('N')

	if after := harness.content(); after != before {
		t.Fatalf("N rewrote the store for an already-NEXT task:\n--- before ---\n%s\n--- after ---\n%s",
			before, after)
	}
	if got := liveState(t, harness, "aaaa000a"); got != "NEXT" {
		t.Errorf("state = %q, want NEXT", got)
	}
}

// The two states `N` cannot promote refuse OUT LOUD, in the same voice the
// delegation keys use, because a swallowed key on a visible row reads as a
// broken keyboard.
func TestMarkNextRefusesProposalsAndClosedWork(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		id    string
		view  string
		state string
		want  string
		// reveal is the toggle that has to be on for the row to be on screen
		// at all. A closed task only paints in the Outline once `C` asks for it.
		reveal func(*Model)
	}{
		{name: "proposal", id: "aaaa0010", view: ViewInbox, state: "PROPOSED",
			want: "approve the proposal first \u2014 a proposal can't be marked NEXT"},
		{name: "closed", id: "aaaa0008", view: ViewOutline, state: "DONE",
			want: "done tasks can't be marked NEXT", reveal: (*Model).ToggleClosed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newModelHarness(t, harnessOptions{live: markNextFixture})
			harness.model.SwitchView(testCase.view)
			if testCase.reveal != nil {
				testCase.reveal(harness.model)
			}
			harness.selectRowByID(testCase.id)
			harness.press('N')

			if got := harness.model.FlashMessage(); got != testCase.want {
				t.Errorf("flash = %q, want %q", got, testCase.want)
			}
			if got := liveState(t, harness, testCase.id); got != testCase.state {
				t.Errorf("state = %q, want %q unchanged", got, testCase.state)
			}
		})
	}
}

// One key, one undo step: `u` puts the task back in the state it was promoted
// from rather than half-way.
func TestMarkNextIsOneUndoStep(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0006")
	harness.press('N')
	if got := liveState(t, harness, "aaaa0006"); got != "NEXT" {
		t.Fatalf("state = %q, want NEXT", got)
	}

	harness.press('u')
	if got := liveState(t, harness, "aaaa0006"); got != "TODO" {
		t.Fatalf("after undo state = %q, want TODO", got)
	}
}

// A key nobody can find is a key nobody uses: `N` is in the `?` overlay and in
// the `:` palette, in both the list and the detail context.
func TestMarkNextIsDiscoverableInHelpAndThePalette(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0006")

	help := strings.Join(HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(help, "mark NEXT (fills the Next tab)") {
		t.Errorf("the ? overlay does not document N:\n%s", help)
	}

	harness.model.OpenActionPalette()
	found := false
	for _, entry := range harness.model.ActionPalette().entries {
		if entry.Handler == "mark_next" {
			found = true
		}
	}
	if !found {
		t.Error("the action palette does not offer mark_next on an open task")
	}
	harness.model.CloseActionPalette()

	for _, context := range []shortcuts.Context{shortcuts.List, shortcuts.Detail} {
		entry, ok := shortcuts.Match("N", context, nil)
		if !ok || entry.Handler != "mark_next" {
			t.Errorf("N in %s resolves to %+v", context, entry)
		}
	}
}

// The palette drops the action on rows it cannot act on, so the list of
// available actions stays honest.
func TestMarkNextLeavesThePaletteOnAProposal(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewInbox)
	harness.selectRowByID("aaaa0010")
	harness.model.OpenActionPalette()
	for _, entry := range harness.model.ActionPalette().entries {
		if entry.Handler == "mark_next" {
			t.Fatal("the palette offered mark_next on an undecided proposal")
		}
	}
}

// Uppercase only. Lowercase `n` is not a list mutation — it is the modal's
// cancel — and adding `N` must not have changed that.
func TestLowercaseNIsNotAStateChange(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: markNextFixture})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0006")
	before := harness.content()
	harness.press('n')

	if got := liveState(t, harness, "aaaa0006"); got != "TODO" {
		t.Fatalf("lowercase n changed the state to %q", got)
	}
	if after := harness.content(); after != before {
		t.Fatal("lowercase n wrote to the store")
	}

	// …and it still cancels a confirmation.
	harness.press('#')
	if harness.model.Mode() != ModeModal {
		t.Fatalf("# left the mode at %s", harness.model.Mode())
	}
	harness.press('n')
	if harness.model.Mode() != ModeList {
		t.Fatalf("n did not cancel the confirmation; mode is %s", harness.model.Mode())
	}
	if got := liveState(t, harness, "aaaa0006"); got != "TODO" {
		t.Fatalf("the cancelled confirmation still changed the task: %q", got)
	}
}
