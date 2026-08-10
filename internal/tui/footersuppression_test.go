package tui

import (
	"errors"
	"strings"
	"testing"
)

// suppressKeyHints removes exactly ONE row: the key hint. suppressFooter
// removes the whole stack. These tests pin the boundary between them, because
// the first external host discovered it by typing into an invisible prompt.

func TestSuppressKeyHintsKeepsEveryOtherFooterElement(t *testing.T) {
	harness := newAgentHarness(t,
		&fakeAdapter{available: true, chunks: 99, output: "line one\nline two"})
	harness.model.embedded, harness.model.suppressKeyHints = true, true
	harness.model.readErr = errors.New("store is a directory")
	harness.model.contextFilters = []string{"@home"}
	harness.submit("go")

	footer := strings.Join(harness.model.Footer(), "\n")
	for _, wanted := range []string{
		"is working",                 // agent transcript header
		"line two",                   // live transcript
		"cannot read the task store", // ADR-0015 store-read banner
		"starting agent request",     // flash
		"@home",                      // context filter line
		"tab to ask the agent",       // the prompt row itself
	} {
		if !strings.Contains(footer, wanted) {
			t.Errorf("suppressing key hints also removed %q:\n%s", wanted, footer)
		}
	}
	if strings.Contains(footer, "j/k") || strings.Contains(footer, "1-6 views") {
		t.Errorf("the ordinary key hint survived suppression:\n%s", footer)
	}
}

func TestSuppressKeyHintsKeepsTheFilterLines(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.suppressKeyHints = true
	harness.press('/')
	harness.press('p')
	if footer := strings.Join(harness.model.Footer(), "\n"); !strings.Contains(footer, "/p") {
		t.Fatalf("the live search buffer went missing:\n%s", footer)
	}
	harness.press('\r')
	if footer := strings.Join(harness.model.Footer(), "\n"); !strings.Contains(footer, "filter /p") {
		t.Fatalf("the applied filter line went missing:\n%s", footer)
	}
}

// The reported failure: focus the prompt with key hints suppressed and the
// caret must be on screen.
func TestSuppressKeyHintsLeavesTheFocusedPromptVisible(t *testing.T) {
	harness := newAgentHarness(t, scripted("done", true))
	harness.model.suppressKeyHints = true
	harness.pressKeys("\t")
	if harness.model.Mode() != ModePrompt {
		t.Fatalf("tab produced mode %s", harness.model.Mode())
	}
	for _, key := range strings.Split("hello", "") {
		harness.pressKeys(key)
	}
	frame := renderAt(t, harness.modelHarness, 80, 24)
	if !strings.Contains(frame, "❯") || !strings.Contains(frame, "hello") {
		t.Fatalf("the focused prompt is invisible:\n%s", frame)
	}
}

// SuppressFooter is unchanged: it still drops the ENTIRE stack.
func TestSuppressFooterStillDropsTheWholeStack(t *testing.T) {
	harness := newAgentHarness(t,
		&fakeAdapter{available: true, chunks: 99, output: "line one"})
	harness.model.embedded, harness.model.suppressFooter = true, true
	harness.model.readErr = errors.New("store is a directory")
	harness.submit("go")
	if footer := harness.model.Footer(); len(footer) != 0 {
		t.Fatalf("SuppressFooter painted %d rows: %v", len(footer), footer)
	}
	if roles := harness.model.footerRoles(); len(roles) != 0 {
		t.Fatalf("a suppressed footer still classified rows for the hit map: %v", roles)
	}
}

// The hit map has to agree with what was painted: with the hint gone, the last
// footer row is the prompt, not chrome.
func TestSuppressKeyHintsKeepsTheHitMapAlignedWithThePaintedFooter(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.suppressKeyHints = true
	if got, want := len(harness.model.footerRoles()), len(harness.model.Footer()); got != want {
		t.Fatalf("roles=%d painted rows=%d", got, want)
	}
	roles := harness.model.footerRoles()
	if len(roles) == 0 || roles[len(roles)-1] != "prompt" {
		t.Fatalf("the last footer row is not the prompt: %v", roles)
	}
}
