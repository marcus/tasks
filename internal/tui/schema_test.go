package tui

import (
	"strings"
	"testing"
)

func TestAnUnsupportedSchemaIsStatedRatherThanRead(t *testing.T) {
	// Ruby opens a modal saying the store declares a schema this build does not
	// implement, and refuses to edit. This build has no modal layer yet (that is
	// the editor packet), so the requirement it CAN meet is the important half:
	// never paint a store it cannot interpret as though it read it.
	harness := newModelHarness(t, harnessOptions{
		live: "{\"type\":\"meta\",\"version\":99}\n",
	})
	frame := harness.model.Render()
	if len(harness.model.Rows()) != 0 {
		t.Fatalf("an unsupported schema produced %d rows", len(harness.model.Rows()))
	}
	if !strings.Contains(frame, "cannot read") && !strings.Contains(frame, "format error") {
		t.Fatalf("an unsupported schema rendered silently as an empty list:\n%s", frame)
	}
}
