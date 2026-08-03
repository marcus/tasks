// Package tui is the Go TUI's shell: the Bubble Tea root model, the screen
// geometry, view switching, the task list and its selection, and the right
// panel that shows task and project details.
//
// It is a REBUILD of lib/tui/app.rb on Bubble Tea, not a transliteration. The
// Ruby terminal event loop, its raw-mode key decoder, its paint-dirty
// bookkeeping and its winsize polling are all things Bubble Tea already owns,
// so none of them are ported. What IS ported is everything Bubble Tea has no
// opinion about: which rows a view emits, how selection survives a refresh,
// what the panel says, and how the screen is divided.
//
// Everything in this file's neighbourhood is deliberately pure. Row building,
// geometry, panel scrolling and detail content are functions from values to
// values, so they are tested by calling them rather than by scraping a
// terminal.
package tui

import "strings"

// Styler is the ONLY thing this package needs from the rendering half of the
// TUI (go/internal/tui/term, owned by another agent). It is deliberately tiny:
// four operations, no theme type, no color model, no escape-sequence vocabulary.
//
// Rows and detail lines name a semantic SLOT (":title", ":muted", ":due_soon")
// exactly as the Ruby builders do; the styler decides what a slot looks like,
// including deciding that it looks like nothing at all under NO_COLOR.
//
// Width, Truncate and Wrap are separate from Paint because a painted string
// contains bytes that occupy no cells, and every layout decision in this
// package is made in CELLS. A styler that paints must measure.
type Styler interface {
	// Paint renders text in the named slot.
	Paint(slot, text string) string
	// Width is the DISPLAY width of text in terminal cells: escape sequences
	// count zero and a wide character counts two.
	Width(text string) int
	// Truncate cuts text to at most width cells, preserving styling and never
	// splitting a wide character across the boundary.
	Truncate(text string, width int) string
	// Wrap folds text to lines of at most width cells.
	Wrap(text string, width int) []string
}

// PlainStyler is the boring default: no color, and width measured in runes.
//
// It exists so this package is testable and runnable before term/ lands, and so
// golden row fixtures are plain text rather than a wall of escape bytes. It is
// NOT the shipping styler — it counts a wide character as one cell, which is
// exactly the thing term/ is being written to get right.
type PlainStyler struct{}

// Paint returns the text unchanged.
func (PlainStyler) Paint(_, text string) string { return text }

// Width counts runes. See the type comment for why this is a placeholder.
func (PlainStyler) Width(text string) int { return len([]rune(text)) }

// Truncate cuts to width runes.
func (PlainStyler) Truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width])
}

// Wrap folds on spaces, breaking an over-long word rather than overflowing.
func (PlainStyler) Wrap(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	if text == "" {
		return []string{""}
	}
	lines := []string{}
	for _, paragraph := range strings.Split(text, "\n") {
		current := ""
		for _, word := range strings.Fields(paragraph) {
			switch {
			case current == "":
				current = word
			case len([]rune(current))+1+len([]rune(word)) <= width:
				current += " " + word
			default:
				lines = append(lines, current)
				current = word
			}
			for len([]rune(current)) > width {
				runes := []rune(current)
				lines = append(lines, string(runes[:width]))
				current = string(runes[width:])
			}
		}
		lines = append(lines, current)
	}
	return lines
}

var _ Styler = PlainStyler{}
