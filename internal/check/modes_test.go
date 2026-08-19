package check

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/record"
)

// A record naming a mode the ACTIVE vocabulary does not list is the ordinary
// consequence of changing a setting, or of syncing a file from a machine
// configured differently. It must never invalidate the file: check warns, the
// record loads, and every write the store performs still goes through, because
// the preflight that guards those writes runs this very validation.
func TestAnUnconfiguredModeOnDiskIsAWarningNotAnError(t *testing.T) {
	at := `"at":"2026-07-27T18:04:11Z"`
	input := delegatedTask(`{"kind":"agent","mode":"research","status":"ready",`+at+`}`, "NEXT")
	options := Options{Modes: record.ModeSet{"triage", "ship"}}

	result := CheckTextWith(input, options)
	if !result.OK() {
		t.Fatalf("an unconfigured mode invalidated the file: %v", result.Errors)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0].Message, `delegation.mode "research" is not in the configured vocabulary triage/ship`) {
		t.Fatalf("warning = %q", result.Warnings[0].Message)
	}
	if result.Warnings[0].Line != 3 {
		t.Fatalf("warning line = %d, want the record's own line", result.Warnings[0].Line)
	}

	// A configured mode is clean, and a mode of the wrong SHAPE is still an
	// error: no configuration could have produced it.
	if clean := CheckTextWith(delegatedTask(`{"kind":"agent","mode":"ship","status":"ready",`+at+`}`, "NEXT"), options); !clean.OK() || len(clean.Warnings) != 0 {
		t.Fatalf("errors = %v, warnings = %v", clean.Errors, clean.Warnings)
	}
	shaped := CheckTextWith(delegatedTask(`{"kind":"agent","mode":"Ship","status":"ready",`+at+`}`, "NEXT"), options)
	if shaped.OK() {
		t.Fatal("a mode of the wrong shape was accepted")
	}
	if !strings.Contains(shaped.Errors[0].Message, "must be one of triage/ship") {
		t.Fatalf("error = %q", shaped.Errors[0].Message)
	}
}

// The default vocabulary behaves identically: this is one rule about
// membership, not a special case for configured stores.
func TestAnUnknownModeIsAWarningUnderTheBuiltInVocabularyToo(t *testing.T) {
	input := delegatedTask(`{"kind":"human","mode":"triage","status":"delegated","assignee":"pat@example.com","at":"2026-07-27T18:04:11Z"}`, "NEXT")
	result := CheckText(input)
	if !result.OK() {
		t.Fatalf("errors = %v", result.Errors)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0].Message, "refine/research/implement") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}
