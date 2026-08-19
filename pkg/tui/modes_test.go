package tui

import (
	"strings"
	"testing"
)

// This is the surface an embedding host actually consumes (see
// testdata/external-tui-consumer). Anything unrendered that reaches here is
// rendered by somebody else's code, in somebody else's product.
func TestExportedCommandsNeverLeakATemplate(t *testing.T) {
	for _, command := range ExportCommands() {
		if strings.Contains(command.Description, "{") || strings.Contains(command.FooterLabel, "{") {
			t.Fatalf("%s carries an unrendered template: %q / %q",
				command.ID, command.Description, command.FooterLabel)
		}
	}
}

// A host that resolved its store's vocabulary — from `tasks config --json`, or
// the API's meta document — can hand it over and get a palette that names the
// words that store will accept.
func TestExportCommandsWithNamesTheHostsVocabulary(t *testing.T) {
	for _, command := range ExportCommandsWith([]string{"triage", "ship"}) {
		if command.ID != "delegate-selected" {
			continue
		}
		if !strings.Contains(command.Description, "triage · ship") {
			t.Fatalf("description = %q", command.Description)
		}
		if strings.Contains(command.Description, "refine") {
			t.Fatalf("description still names the built-in modes: %q", command.Description)
		}
		return
	}
	t.Fatal("the delegate command is not exported at all")
}

// An empty list is not a vocabulary; it means "I do not know", and the built-in
// set is the honest answer to that.
func TestExportCommandsWithFallsBackToTheBuiltInModes(t *testing.T) {
	for _, command := range ExportCommandsWith(nil) {
		if command.ID != "delegate-selected" {
			continue
		}
		if !strings.Contains(command.Description, "refine · research · implement") {
			t.Fatalf("description = %q", command.Description)
		}
		return
	}
	t.Fatal("the delegate command is not exported at all")
}
