package tui

// Modal scrollbar geometry and painting.
//
// The math is adapted from sidecar's internal/scroll/thumb.go (the
// mouse-draggable scrollbar work): three state-free functions over plain ints,
// so the drawn thumb and the hit-tested one can never disagree, and so a later
// drag-gesture packet can reuse them unchanged. Kept here rather than in a
// subpackage because every modal in this package paints line lists, not
// viewports, and the column is appended to those lines one cell at a time.

import "strings"

// ScrollThumb is a scrollbar thumb's placement within its track.
type ScrollThumb struct {
	Pos  int  // first track row of the thumb.
	Size int  // track rows the thumb spans; always >= 1 when Has is set.
	Has  bool // false when all content fits and no thumb should be drawn.
}

// ThumbLocFor computes proportional thumb placement from list geometry: size
// is the visible fraction of the track with a floor of one row, position
// tracks the scroll offset across the remaining travel. Has is false when
// totalItems does not exceed visibleItems. Adapted from sidecar's
// internal/scroll/thumb.go.
func ThumbLocFor(totalItems, scrollOffset, visibleItems, trackHeight int) ScrollThumb {
	if trackHeight < 1 || totalItems <= visibleItems {
		return ScrollThumb{}
	}

	size := (visibleItems * trackHeight) / totalItems
	if size < 1 {
		size = 1
	}
	if size > trackHeight {
		size = trackHeight
	}

	maxOffset := totalItems - visibleItems
	if maxOffset < 1 {
		maxOffset = 1
	}
	pos := (scrollOffset * (trackHeight - size)) / maxOffset
	if pos < 0 {
		pos = 0
	}
	if pos > trackHeight-size {
		pos = trackHeight - size
	}

	return ScrollThumb{Size: size, Pos: pos, Has: true}
}

// OffsetAtRow maps a track row to the smallest scroll offset whose thumb top
// renders on or below that row — the jump-to-spot contract a future
// track-press gesture will use. Clamped to [0, totalItems-visibleItems];
// monotonic in row. Adapted from sidecar's internal/scroll/thumb.go.
func OffsetAtRow(totalItems, visibleItems, trackHeight, row int) int {
	loc := ThumbLocFor(totalItems, 0, visibleItems, trackHeight)
	if !loc.Has {
		return 0
	}
	maxOffset := totalItems - visibleItems
	travel := trackHeight - loc.Size
	if travel < 1 {
		return 0
	}
	row = min(max(row, 0), travel)
	offset := (row*maxOffset + travel - 1) / travel
	return min(max(offset, 0), maxOffset)
}

// RowForOffset maps a scroll offset to the track row where the thumb top
// renders, clamped to [0, trackHeight-1]. Adapted from sidecar's
// internal/scroll/thumb.go.
func RowForOffset(totalItems, visibleItems, trackHeight, offset int) int {
	maxOffset := totalItems - visibleItems
	offset = min(max(offset, 0), max(0, maxOffset))
	loc := ThumbLocFor(totalItems, offset, visibleItems, trackHeight)
	return loc.Pos
}

// ClampScroll bounds a scroll offset to what content actually allows.
func ClampScroll(offset, visible, total int) int {
	return min(max(offset, 0), max(total-visible, 0))
}

const (
	scrollbarThumbGlyph = "▌"
	scrollbarTrackGlyph = "╎"
)

// ScrollbarCell paints one row of a scrollbar column. Rows outside the thumb
// paint as track; when thumb.Has is false there is nothing to scroll and the
// caller should draw no column at all.
func ScrollbarCell(styler Styler, row int, thumb ScrollThumb) string {
	if thumb.Has && row >= thumb.Pos && row < thumb.Pos+thumb.Size {
		return styler.Paint("scrollbar_thumb", scrollbarThumbGlyph)
	}
	return styler.Paint("scrollbar_track", scrollbarTrackGlyph)
}

// PaintScrollbar paints a whole column at once, for callers that build a
// single string per row anyway. An empty result means "no scrollbar".
func PaintScrollbar(styler Styler, trackHeight int, thumb ScrollThumb) string {
	if !thumb.Has || trackHeight < 1 {
		return ""
	}
	cells := make([]string, 0, trackHeight)
	for row := range trackHeight {
		cells = append(cells, ScrollbarCell(styler, row, thumb))
	}
	return strings.Join(cells, "")
}
