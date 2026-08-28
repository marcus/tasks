package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/config"
)

// The delegation transition stamp in the TUI: the detail pane and the flash
// that names a lost race both read it, and both must say what `tasks show`
// says. One instant, one rendering, whichever surface is open.

// 2026-08-28T00:46:44Z is 2026-08-27 17:46 in America/Los_Angeles — a different
// day as well as a different clock.
const delegationStampFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"2026-08-28T00:46:44Z"}}
{"type":"task","id":"aaaa0005","parent":"aaaa0003","state":"NEXT","title":"Held by a worker","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"worker-1","at":"2026-08-28T00:46:44Z"}}
`

func stampHarness(t *testing.T, zone string, timeFormat int) *modelHarness {
	t.Helper()
	return newModelHarness(t, harnessOptions{
		live: delegationStampFixture,
		paths: func(paths *config.Paths) {
			paths.Timezone, paths.TimeFormat = zone, timeFormat
		},
	})
}

func TestDetailPaneProjectsTheDelegationStamp(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		zone       string
		timeFormat int
		want       string
	}{
		{"local 12-hour", "America/Los_Angeles", 12, "thu 08-27 5:46p"},
		{"local 24-hour", "America/Los_Angeles", 24, "thu 08-27 17:46"},
		{"another zone entirely", "Asia/Tokyo", 12, "fri 08-28 9:46a"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness := stampHarness(t, testCase.zone, testCase.timeFormat)
			text := detailFor(t, harness, "aaaa0004")
			if !strings.Contains(text, testCase.want) {
				t.Errorf("the detail pane is missing %q:\n%s", testCase.want, text)
			}
			if strings.Contains(text, "2026-08-28T00:46:44Z") {
				t.Errorf("the detail pane painted the stored UTC stamp:\n%s", text)
			}
		})
	}
}

// The panel and the CLI are one product. A stamp the two surfaces spelled
// differently would make "the same task" a question.
func TestDetailPaneStampMatchesTheShowRendering(t *testing.T) {
	harness := stampHarness(t, "America/Los_Angeles", 12)
	text := detailFor(t, harness, "aaaa0004")
	want := harness.model.temporalContext().StampLabel("2026-08-28T00:46:44Z")
	if !strings.Contains(text, want) {
		t.Fatalf("the panel does not carry the projected stamp %q:\n%s", want, text)
	}
}

func TestDetailPanePaintsAnUnparseableStampAsStored(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{
		live: `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"whenever"}}
`,
		paths: func(paths *config.Paths) {
			paths.Timezone, paths.TimeFormat = "America/Los_Angeles", 12
		},
	})
	if text := detailFor(t, harness, "aaaa0004"); !strings.Contains(text, "whenever") {
		t.Fatalf("an unparseable stamp did not survive to the panel:\n%s", text)
	}
}

// The claim-conflict flash names the instant somebody else took the work. It is
// read by a person deciding whether to wait or revoke, so it is their clock.
func TestClaimConflictFlashNamesTheLocalInstant(t *testing.T) {
	harness := stampHarness(t, "America/Los_Angeles", 12)
	openDelegateModal(t, harness, "aaaa0005")
	modal := harness.model.FieldModal()
	modal.SetValue(delegateFieldAssignee, "pat@example.com")
	harness.pressKeys("\r")

	message := modal.Error()
	if !strings.Contains(message, "already claimed by worker-1 at thu 08-27 5:46p") {
		t.Errorf("the conflict flash did not name the local instant: %q", message)
	}
	if strings.Contains(message, "2026-08-28T00:46:44Z") {
		t.Errorf("the conflict flash leaked the stored UTC stamp: %q", message)
	}
}
