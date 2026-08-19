package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/record"
)

// The delegate modal, driven the way a user drives it.
//
// Packet F proved the COMPONENT both ways — every gesture once from the
// keyboard and once from the pointer, compared on the whole observable state.
// This file extends that approach to the FEATURE rather than inventing a second
// one: the same two paths are driven end to end through the model, and the
// assertion is the delegation that landed on disk, which is the only thing the
// two paths owe each other.

const delegateModalFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Book flight in Concur"}
{"type":"task","id":"aaaa0005","parent":"aaaa0003","state":"NEXT","title":"Held by a worker","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"worker-1","at":"2026-07-10T09:00:00Z"}}
{"type":"task","id":"aaaa0006","parent":"aaaa0003","state":"WAITING","title":"Already with Pat","delegation":{"kind":"human","mode":"refine","status":"delegated","assignee":"pat@example.com","at":"2026-07-11T09:00:00Z","note":"Ask the vendor first."}}
`

const (
	delegateFixFresh   = "aaaa0004"
	delegateFixClaimed = "aaaa0005"
	delegateFixWithPat = "aaaa0006"
)

// openDelegateModal selects a task and presses `D`.
func openDelegateModal(t *testing.T, harness *modelHarness, id string) *FieldModal {
	t.Helper()
	// Outline shows every open task, WAITING delegations included.
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(id)
	harness.pressKeys("D")
	modal := harness.model.FieldModal()
	if harness.model.Mode() != ModeFieldModal || modal == nil {
		t.Fatalf("D produced mode %s (%q)", harness.model.Mode(), harness.model.FlashMessage())
	}
	if modal.Kind() != FieldModalDelegate {
		t.Fatalf("D opened the %s modal", modal.Kind())
	}
	return modal
}

func delegateHarness(t *testing.T, mouse bool) *modelHarness {
	t.Helper()
	return newModelHarness(t, harnessOptions{
		live:  delegateModalFixture,
		paths: func(paths *config.Paths) { paths.Mouse = mouse },
	})
}

func typeKeys(harness *modelHarness, text string) {
	for _, key := range strings.Split(text, "") {
		harness.pressKeys(key)
	}
}

// clickModal turns a box-relative cell into the screen click the model sees.
func clickModal(t *testing.T, harness *modelHarness, row, column int) {
	t.Helper()
	box := harness.model.Overlay()
	if box == nil {
		t.Fatal("no overlay was painted")
	}
	if !harness.model.HandleMouse(tea.MouseClickMsg{
		X: box.Col + column, Y: box.Row + row, Button: tea.MouseLeft}) {
		t.Fatalf("the click at %d,%d was not consumed", row, column)
	}
}

// TestDelegateModalKeyboardWritesAllThreePartsInOneStep is the keyboard path
// end to end: an address, a mode, and a briefing, submitted once.
func TestDelegateModalKeyboardWritesAllThreePartsInOneStep(t *testing.T) {
	harness := delegateHarness(t, false)
	openDelegateModal(t, harness, delegateFixFresh)

	typeKeys(harness, "pat@example.com")
	harness.pressKeys("\t")
	// The Mode field opens on the first mode; one arrow moves to the second.
	harness.pressKeys("\x1b[B")
	harness.pressKeys("\t")
	typeKeys(harness, "Book the aisle seat.")
	// Return is TEXT in a note, so ctrl-s is what submits from one.
	harness.pressKeys("\x13")

	if harness.model.Mode() != ModeFieldModal {
		if harness.model.Mode() != ModeList {
			t.Fatalf("the modal left mode %s", harness.model.Mode())
		}
	}
	line := taskLineIn(harness.content(), delegateFixFresh)
	for _, want := range []string{
		`"kind":"human"`, `"mode":"research"`, `"assignee":"pat@example.com"`,
		`"note":"Book the aisle seat."`, `"state":"WAITING"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the delegation is missing %s:\n%s", want, line)
		}
	}
	if !strings.Contains(harness.model.FlashMessage(), "delegated → pat@example.com") {
		t.Errorf("the write was not reported: %q", harness.model.FlashMessage())
	}
}

// TestDelegateModalMouseWritesAllThreePartsInOneStep is the same journey with
// the pointer only: pick the assignee from the offered list, pick the mode from
// its list, click into the note, and click [ Delegate ].
func TestDelegateModalMouseWritesAllThreePartsInOneStep(t *testing.T) {
	harness := delegateHarness(t, true)
	modal := openDelegateModal(t, harness, delegateFixFresh)
	harness.model.View()

	// pat@example.com is offered because the list has delegated to them before.
	row, _ := rowOf(t, modal, fieldModalOption, delegateFieldAssignee, func(line fieldModalLine) bool {
		return line.optionValue == "pat@example.com"
	})
	clickModal(t, harness, row, 5)
	if modal.Value(delegateFieldAssignee) != "pat@example.com" {
		t.Fatalf("the offered assignee click chose %q", modal.Value(delegateFieldAssignee))
	}

	harness.model.View()
	row, _ = rowOf(t, modal, fieldModalOption, delegateFieldMode, func(line fieldModalLine) bool {
		return line.optionValue == "research"
	})
	clickModal(t, harness, row, 5)
	if modal.Value(delegateFieldMode) != "research" {
		t.Fatalf("the mode click chose %q", modal.Value(delegateFieldMode))
	}

	harness.model.View()
	row, line := rowOf(t, modal, fieldModalValue, delegateFieldNote, func(line fieldModalLine) bool {
		return line.valueRow == 0
	})
	clickModal(t, harness, row, line.valueCol)
	typeKeys(harness, "Book the aisle seat.")

	harness.model.View()
	row, span := buttonSpan(t, modal, fieldModalSubmitID)
	clickModal(t, harness, row, span.begin)

	if harness.model.Mode() != ModeList {
		t.Fatalf("the submit click left mode %s (%q)",
			harness.model.Mode(), harness.model.FlashMessage())
	}
	written := taskLineIn(harness.content(), delegateFixFresh)
	for _, want := range []string{
		`"mode":"research"`, `"assignee":"pat@example.com"`, `"note":"Book the aisle seat."`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the mouse path is missing %s:\n%s", want, written)
		}
	}
}

// The agent pool is an OPTION, not a word grammar: picking it delegates to the
// pool at the chosen mode.
func TestDelegateModalDelegatesToTheAgentPool(t *testing.T) {
	harness := delegateHarness(t, false)
	openDelegateModal(t, harness, delegateFixFresh)
	typeKeys(harness, delegateAgentValue)
	harness.pressKeys("\r")

	line := taskLineIn(harness.content(), delegateFixFresh)
	if !strings.Contains(line, `"kind":"agent"`) || !strings.Contains(line, `"status":"ready"`) {
		t.Errorf("the agent delegation did not land:\n%s", line)
	}
	if strings.Contains(line, `"assignee"`) {
		t.Errorf("an agent delegation named an assignee:\n%s", line)
	}
}

// Re-delegating starts from the delegation that exists — all three parts of it.
func TestDelegateModalPrefillsEveryPartWhenRedelegating(t *testing.T) {
	harness := delegateHarness(t, false)
	modal := openDelegateModal(t, harness, delegateFixWithPat)

	if got := modal.Value(delegateFieldAssignee); got != "pat@example.com" {
		t.Errorf("assignee prefilled as %q", got)
	}
	if got := modal.Value(delegateFieldMode); got != "refine" {
		t.Errorf("mode prefilled as %q", got)
	}
	if got := modal.Value(delegateFieldNote); got != "Ask the vendor first." {
		t.Errorf("note prefilled as %q", got)
	}
	if !strings.Contains(modal.Title(), "now delegated → pat@example.com") {
		t.Errorf("the title does not say what holds it: %q", modal.Title())
	}

	// An agent delegation prefills the pool rather than an empty field.
	agent := openDelegateModal(t, delegateHarness(t, false), delegateFixClaimed)
	if got := agent.Value(delegateFieldAssignee); got != delegateAgentValue {
		t.Errorf("an agent delegation prefilled %q", got)
	}
	if got := agent.Value(delegateFieldMode); got != "implement" {
		t.Errorf("an agent delegation prefilled mode %q", got)
	}
}

// Emptying the note clears the briefing, in the same single write.
func TestDelegateModalClearsTheNoteWhenItIsEmptied(t *testing.T) {
	harness := delegateHarness(t, false)
	modal := openDelegateModal(t, harness, delegateFixWithPat)
	modal.SetValue(delegateFieldNote, "")
	harness.pressKeys("\r")

	if line := taskLineIn(harness.content(), delegateFixWithPat); strings.Contains(line, `"note"`) {
		t.Errorf("the briefing survived an emptied note:\n%s", line)
	}
}

// -- refusals ---------------------------------------------------------------------

// Every refusal is answered IN the box, and the box does not move while it is
// answered — that is the whole reason the status and hint rows are always
// painted, and it is what makes a correction one keystroke rather than a hunt.
func TestDelegateModalRefusalsStayInsideAMotionlessBox(t *testing.T) {
	cases := []struct {
		name  string
		drive func(*testing.T, *modelHarness, *FieldModal)
		field string
		want  string
		task  string
	}{
		{
			name: "a bad email",
			task: delegateFixFresh,
			drive: func(t *testing.T, harness *modelHarness, modal *FieldModal) {
				typeKeys(harness, "pat@")
				harness.pressKeys("\r")
			},
			field: delegateFieldAssignee, want: "isn't an email address",
		},
		{
			name: "an unconfigured mode",
			task: delegateFixFresh,
			drive: func(t *testing.T, harness *modelHarness, modal *FieldModal) {
				typeKeys(harness, "pat@example.com")
				// The vocabulary changed underneath the open modal.
				modal.SetValue(delegateFieldMode, "shipit")
				harness.pressKeys("\r")
			},
			field: delegateFieldMode, want: "is not a configured mode",
		},
		{
			name: "an over-long note",
			task: delegateFixFresh,
			drive: func(t *testing.T, harness *modelHarness, modal *FieldModal) {
				typeKeys(harness, "pat@example.com")
				modal.SetValue(delegateFieldNote, strings.Repeat("x", record.DelegationNoteLimit+1))
				harness.pressKeys("\r")
			},
			field: delegateFieldNote, want: "at most 2000 characters",
		},
		{
			// A live claim is somebody else's work in flight, and the store is
			// the one that knows. Its sentence is shown verbatim, in the status
			// row, and it names the way out.
			name: "a conflict with a live claim",
			task: delegateFixClaimed,
			drive: func(t *testing.T, harness *modelHarness, modal *FieldModal) {
				modal.SetValue(delegateFieldAssignee, "pat@example.com")
				harness.pressKeys("\r")
			},
			field: "", want: "already claimed by worker-1",
		},
	}

	for _, subject := range cases {
		t.Run(subject.name, func(t *testing.T) {
			harness := delegateHarness(t, false)
			modal := openDelegateModal(t, harness, subject.task)
			harness.model.View()
			before := harness.model.Overlay()
			was := harness.content()

			subject.drive(t, harness, modal)

			if harness.model.Mode() != ModeFieldModal {
				t.Fatalf("a refusal closed the modal (mode %s)", harness.model.Mode())
			}
			got := modal.Error()
			if subject.field != "" {
				got = modal.FieldError(subject.field)
			}
			if !strings.Contains(got, subject.want) {
				t.Fatalf("refusal = %q, want it to mention %q", got, subject.want)
			}
			if harness.content() != was {
				t.Fatal("a refused delegation still wrote")
			}
			harness.model.View()
			after := harness.model.Overlay()
			if before.Row != after.Row || before.Col != after.Col ||
				len(before.Lines) != len(after.Lines) {
				t.Fatalf("the box moved: %d,%d×%d became %d,%d×%d",
					before.Row, before.Col, len(before.Lines),
					after.Row, after.Col, len(after.Lines))
			}
		})
	}
}

// -- the two escape hatches -------------------------------------------------------

// Release is the owner's forced release, and it is a BUTTON with a key rather
// than a word typed into a text field.
func TestDelegateModalReleaseIsAnExplicitAffordance(t *testing.T) {
	for _, path := range []string{"key", "mouse"} {
		t.Run(path, func(t *testing.T) {
			harness := delegateHarness(t, path == "mouse")
			modal := openDelegateModal(t, harness, delegateFixClaimed)
			harness.model.View()
			if path == "key" {
				harness.pressKeys("\x12")
			} else {
				row, span := buttonSpan(t, modal, delegateReleaseAction)
				clickModal(t, harness, row, span.begin)
			}
			if harness.model.Mode() != ModeList {
				t.Fatalf("release left mode %s (%q)",
					harness.model.Mode(), harness.model.FlashMessage())
			}
			line := taskLineIn(harness.content(), delegateFixClaimed)
			if !strings.Contains(line, `"status":"ready"`) || strings.Contains(line, "worker-1") {
				t.Errorf("the claim was not handed back:\n%s", line)
			}
			if !strings.Contains(harness.model.FlashMessage(), "released") {
				t.Errorf("release said %q", harness.model.FlashMessage())
			}
		})
	}
}

// Undelegate revokes a live claim, so it takes TWO deliberate gestures — the
// same shape as the discard latch, and the thing the old `o`/`n` spelling never
// had. The first gesture only arms; nothing is written until the second.
func TestDelegateModalUndelegateConfirmsBeforeItRevokes(t *testing.T) {
	for _, path := range []string{"key", "mouse"} {
		t.Run(path, func(t *testing.T) {
			harness := delegateHarness(t, path == "mouse")
			modal := openDelegateModal(t, harness, delegateFixClaimed)
			harness.model.View()
			invoke := func() {
				harness.model.View()
				if path == "key" {
					harness.pressKeys("\x18")
					return
				}
				row, span := buttonSpan(t, modal, delegateUndelegateAction)
				clickModal(t, harness, row, span.begin)
			}
			before := harness.content()

			invoke()
			if harness.model.Mode() != ModeFieldModal {
				t.Fatal("the first gesture closed the modal")
			}
			if modal.Error() != delegateUndelegateConfirm {
				t.Fatalf("the first gesture said %q", modal.Error())
			}
			if harness.content() != before {
				t.Fatal("the first gesture already revoked the claim")
			}

			invoke()
			if harness.model.Mode() != ModeList {
				t.Fatalf("the second gesture left mode %s", harness.model.Mode())
			}
			if line := taskLineIn(harness.content(), delegateFixClaimed); strings.Contains(line, "delegation") {
				t.Errorf("the marker survived:\n%s", line)
			}
			if !strings.Contains(harness.model.FlashMessage(), "undelegated") {
				t.Errorf("undelegate said %q", harness.model.FlashMessage())
			}
		})
	}
}

// Editing between the two gestures re-arms the confirmation: the message the
// user was answering is gone, so the next ctrl-x must not act on it.
func TestDelegateModalUndelegateRearmsAfterAnEdit(t *testing.T) {
	harness := delegateHarness(t, false)
	modal := openDelegateModal(t, harness, delegateFixClaimed)
	harness.pressKeys("\x18")
	typeKeys(harness, "p")
	if modal.Error() != "" {
		t.Fatalf("typing left the confirmation up: %q", modal.Error())
	}
	before := harness.content()
	harness.pressKeys("\x18")
	if harness.content() != before {
		t.Fatal("the claim was revoked without a fresh confirmation")
	}
	if modal.Error() != delegateUndelegateConfirm {
		t.Fatalf("the re-armed message is %q", modal.Error())
	}
}

// -- what the modal offers ---------------------------------------------------------

// The assignee field OFFERS the people this list has handed work to, without
// demanding that anyone be on the list: it is free text, so a new address is
// simply typed.
func TestDelegateModalOffersKnownAssigneesAndStillAcceptsANewOne(t *testing.T) {
	harness := delegateHarness(t, false)
	modal := openDelegateModal(t, harness, delegateFixFresh)

	offered := []string{}
	for _, option := range modal.Options(delegateFieldAssignee) {
		offered = append(offered, option.Value)
	}
	if len(offered) < 2 || offered[0] != delegateAgentValue {
		t.Fatalf("the agent pool is not offered first: %v", offered)
	}
	if !containsString(offered, "pat@example.com") {
		t.Fatalf("a known assignee is not offered: %v", offered)
	}

	typeKeys(harness, "sam@example.com")
	harness.pressKeys("\r")
	if !strings.Contains(taskLineIn(harness.content(), delegateFixFresh), `"assignee":"sam@example.com"`) {
		t.Errorf("a typed address did not land:\n%s", taskLineIn(harness.content(), delegateFixFresh))
	}
}

// The Mode field's vocabulary is the STORE's, read at paint time.
func TestDelegateModalOffersTheConfiguredModes(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{
		live: delegateModalFixture, modes: record.ModeSet{"triage", "ship"}})
	modal := openDelegateModal(t, harness, delegateFixFresh)

	offered := []string{}
	for _, option := range modal.Options(delegateFieldMode) {
		offered = append(offered, option.Value)
	}
	if strings.Join(offered, ",") != "triage,ship" {
		t.Fatalf("the Mode field offers %v", offered)
	}
	if got := modal.Value(delegateFieldMode); got != "triage" {
		t.Fatalf("a fresh delegation preselects %q", got)
	}
}

// The detail panel describes the same three parts the modal wrote, so what was
// stated and what is shown are one model.
func TestDetailPanelShowsTheModeAndTheNote(t *testing.T) {
	harness := delegateHarness(t, false)
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(delegateFixWithPat)
	harness.pressKeys("\r")
	frame := harness.model.Render()
	for _, want := range []string{"mode", "refine", "note", "Ask the vendor first."} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the detail panel does not show %q:\n%s", want, frame)
		}
	}
}
