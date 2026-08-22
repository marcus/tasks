package tui

import "strings"

// Footer keybinding chips: the "[tab] next   [enter] open" row under a modal's
// buttons. The key is a chip (its own slot, so a host palette can make keys
// read as chrome), the label is quiet, and the two-space gap between chips
// keeps the row scannable without inventing a box-drawing table.

// KeyChip is one advertised binding.
type KeyChip struct {
	Key   string // e.g. "tab", "ctrl-s", "↑↓"
	Label string // e.g. "next"
}

// PaintKeyChips renders chips left to right. An empty list paints an empty
// line — callers decide whether to spend the row at all.
func PaintKeyChips(styler Styler, chips []KeyChip) string {
	if len(chips) == 0 {
		return ""
	}
	parts := make([]string, 0, len(chips))
	for _, chip := range chips {
		parts = append(parts,
			styler.Paint("chip_key", "["+chip.Key+"]")+" "+styler.Paint("chip_label", chip.Label))
	}
	return strings.Join(parts, "   ")
}
