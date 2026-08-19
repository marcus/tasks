package shortcuts

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/record"
)

// The registry names a HOLE where the delegation vocabulary goes, which means
// every path that turns an entry into text a human reads has to fill it. The
// export projection is the easiest one to forget and the worst one to get
// wrong: an embedding host copies Description straight into its own palette and
// footer, so an unfilled placeholder is a literal "{modes}" on somebody else's
// screen, in a codebase that cannot even see this package.
func TestNoExportedCommandLeaksTheModesPlaceholder(t *testing.T) {
	for _, command := range ExportCommands() {
		if strings.Contains(command.Description, ModesPlaceholder) {
			t.Fatalf("%s in %s leaks the placeholder: %q",
				command.ID, command.Context, command.Description)
		}
		if strings.Contains(command.FooterLabel, ModesPlaceholder) {
			t.Fatalf("%s footer label leaks the placeholder: %q", command.ID, command.FooterLabel)
		}
	}
}

// With no vocabulary supplied the export describes delegation with the BUILT-IN
// set — a host that never heard of modes still renders words, not a template.
func TestExportedCommandsDescribeDelegationWithTheBuiltInModes(t *testing.T) {
	found := 0
	for _, command := range ExportCommands() {
		if command.ID != "delegate-selected" {
			continue
		}
		found++
		if !strings.Contains(command.Description, "email · refine · research · implement · release · off") {
			t.Fatalf("description = %q", command.Description)
		}
	}
	if found == 0 {
		t.Fatal("the delegate command is not exported at all")
	}
}

// An embedder that knows its store's vocabulary gets its store's vocabulary.
func TestExportCommandsWithFillsTheConfiguredModes(t *testing.T) {
	commands := ExportCommandsWith(record.ModeSet{"triage", "ship"})
	found := 0
	for _, command := range commands {
		if strings.Contains(command.Description, ModesPlaceholder) {
			t.Fatalf("%s leaks the placeholder: %q", command.ID, command.Description)
		}
		if command.ID != "delegate-selected" {
			continue
		}
		found++
		if !strings.Contains(command.Description, "email · triage · ship · release · off") {
			t.Fatalf("description = %q", command.Description)
		}
		if strings.Contains(command.Description, "refine") {
			t.Fatalf("description still quotes the built-in modes: %q", command.Description)
		}
	}
	if found == 0 {
		t.Fatal("the delegate command is not exported at all")
	}
}

// No registry entry may name a mode as a VALUE. The registry is built during
// init, before any configuration exists, so a value baked in here is a wrong
// answer on every configured store — which is exactly the defect the
// placeholder replaced.
func TestNoRegistryEntryHardCodesTheModeVocabulary(t *testing.T) {
	for _, mode := range record.BuiltinModes().Modes() {
		for _, entry := range Registry {
			if strings.Contains(entry.Description, mode) || strings.Contains(entry.HelpDescription, mode) {
				t.Fatalf("entry %q names the mode %q as a literal; use %s",
					entry.CommandID, mode, ModesPlaceholder)
			}
		}
	}
}
