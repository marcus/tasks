// Package ansi holds the SGR-aware, width-aware string helpers. Everything
// that knows about escape codes lives here so the rest of the TUI can treat
// styled strings as opaque. Bare codepoint/grapheme widths come from the
// shared charwidth kernel; this package adds the SGR-aware layer on top.
//
// Go port of Ruby's lib/tui/ansi.rb.
package ansi

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/tui/term/charwidth"
)

// SGR matches a Select Graphic Rendition escape sequence.
var SGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// reset matches only true resets (\e[0m / \e[m), never a field opener that
// merely carries a 0 parameter such as \e[38;2;0;0;0m.
var resetRe = regexp.MustCompile("\x1b\\[0?m")

// Color wraps str in an SGR sequence built from codes, closing with a reset.
func Color(str string, codes ...string) string {
	return "\x1b[" + strings.Join(codes, ";") + "m" + str + "\x1b[0m"
}

func colorN(str string, code int) string { return Color(str, strconv.Itoa(code)) }

func Bold(s string) string   { return colorN(s, 1) }
func Dim(s string) string    { return colorN(s, 90) }
func Red(s string) string    { return colorN(s, 31) }
func Yellow(s string) string { return colorN(s, 33) }
func Cyan(s string) string   { return colorN(s, 36) }
func Invert(s string) string { return colorN(s, 7) }

// Composite lays sgr (an opening SGR sequence, e.g. "\e[1m") over str, whose
// embedded field styling already closes with a reset. It re-injects sgr
// immediately after every true reset so the overlay survives each field
// boundary instead of being cleared by it. It does NOT append a trailing reset
// — the caller closes once, after any padding. An empty sgr returns str
// unchanged.
func Composite(sgr, str string) string {
	if sgr == "" {
		return str
	}
	return sgr + resetRe.ReplaceAllStringFunc(str, func(m string) string { return m + sgr })
}

// Normalize returns valid UTF-8, substituting U+FFFD for invalid bytes.
// Subprocess output arrives as raw bytes and can end on an incomplete
// sequence, so every entry point normalizes defensively.
func Normalize(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// Strip removes every SGR sequence, leaving the visible text.
func Strip(s string) string { return SGR.ReplaceAllString(Normalize(s), "") }

// Close closes any styling a composed line still has open, so a row can never
// leak color into the one painted below it. A line with no SGR at all, or one
// that already ends in a reset, is returned unchanged.
func Close(s string) string {
	text := Normalize(s)
	if !SGR.MatchString(text) {
		return text
	}
	if strings.HasSuffix(text, "\x1b[0m") || strings.HasSuffix(text, "\x1b[m") {
		return text
	}
	return text + "\x1b[0m"
}

// RuneWidth and ClusterWidth are delegators onto the shared kernel.
func RuneWidth(r rune) int       { return charwidth.Rune(r) }
func ClusterWidth(gc string) int { return charwidth.Cluster(gc) }
func Clusters(s string) []string { return charwidth.Clusters(s) }

// plainASCII reports whether s is printable ASCII only (no ESC, no controls,
// no DEL). Those bytes are all width 1, so grapheme/Unicode tables are pure
// overhead.
func plainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7E {
			return false
		}
	}
	return true
}

// VisLen is the visible display width in terminal cells: escape codes are
// ignored and wide/emoji graphemes count as two cells.
func VisLen(s string) int {
	text := Normalize(s)
	if plainASCII(text) {
		return len(text)
	}
	return charwidth.String(Strip(text))
}

// token is either an SGR escape or a single grapheme cluster.
type token struct {
	text string
	sgr  bool
}

// scanCells tokenizes text into SGR escapes and grapheme clusters, the Go
// equivalent of Ruby's CELL_SCAN regex.
func scanCells(text string) []token {
	out := make([]token, 0, len(text))
	for text != "" {
		if loc := SGR.FindStringIndex(text); loc != nil && loc[0] == 0 {
			out = append(out, token{text: text[:loc[1]], sgr: true})
			text = text[loc[1]:]
			continue
		}
		gc, rest := charwidth.FirstCluster(text)
		out = append(out, token{text: gc})
		text = rest
	}
	return out
}

// CellSlice returns the visible cell window [start, start+width) without
// splitting grapheme clusters. SGR styling is retained and closed at the slice
// boundary. If a boundary crosses a wide cluster, spaces occupy the partial
// cells so content after that cluster stays at its original terminal column.
func CellSlice(s string, start, width int) string {
	return cellSlice(s, start, width, true)
}

// CellSliceToEnd returns everything from start to the end of the string — the
// Ruby call with the width argument omitted.
func CellSliceToEnd(s string, start int) string {
	return cellSlice(s, start, 0, false)
}

func cellSlice(s string, start, width int, bounded bool) string {
	text := Normalize(s)
	if start < 0 {
		start = 0
	}
	if bounded {
		if width < 0 {
			width = 0
		}
		if width == 0 {
			return ""
		}
	}
	finish := start + width

	var prefix, out strings.Builder
	cell := 0
	started := false
	usedSGR := false

	begin := func() {
		if !started {
			out.WriteString(prefix.String())
			if prefix.Len() > 0 {
				usedSGR = true
			}
			started = true
		}
	}

	for _, tok := range scanCells(text) {
		if tok.sgr {
			if !started && cell <= start {
				prefix.WriteString(tok.text)
			} else if !bounded || cell < finish {
				out.WriteString(tok.text)
				usedSGR = true
			}
			continue
		}

		cw := charwidth.Cluster(tok.text)
		if cw == 0 {
			if cell >= start && (!bounded || cell < finish) {
				begin()
				out.WriteString(tok.text)
			}
			continue
		}

		clusterEnd := cell + cw
		if clusterEnd <= start {
			cell = clusterEnd
			continue
		}
		if bounded && cell >= finish {
			break
		}

		overlapStart := max(cell, start)
		overlapEnd := clusterEnd
		if bounded && finish < overlapEnd {
			overlapEnd = finish
		}
		if overlapEnd > overlapStart {
			begin()
			if overlapStart == cell && overlapEnd == clusterEnd {
				out.WriteString(tok.text)
			} else {
				out.WriteString(strings.Repeat(" ", overlapEnd-overlapStart))
			}
		}
		cell = clusterEnd
	}

	result := out.String()
	if usedSGR && !strings.HasSuffix(result, "\x1b[0m") {
		result += "\x1b[0m"
	}
	return result
}

// VPad pads s to visible width w (a no-op if it is already wider).
func VPad(s string, w int) string {
	text := Normalize(s)
	if pad := w - VisLen(text); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}

// VTrunc truncates s to visible width w, appending a dim ellipsis. Escape
// codes are preserved and a reset is appended so styles cannot leak.
func VTrunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	text := Normalize(s)
	if VisLen(text) <= w {
		return text
	}
	return CellSlice(text, 0, w-1) + Dim("…")
}

// Wrap word-wraps text to a terminal-cell width, preserving ANSI styling and
// grapheme clusters.
func Wrap(text string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, line := range styledLines(Normalize(text)) {
		out = append(out, wrapLine(line, w)...)
	}
	return out
}

// styledLines splits on newlines and carries the active SGR state onto each
// following line, so an explicit newline inside styled text does not drop the
// style.
func styledLines(text string) []string {
	// Ruby's "".split("\n", -1) is [], not [""] — an empty input wraps to no
	// lines at all, and callers rely on that (the activity view substitutes its
	// own placeholder line).
	if text == "" {
		return nil
	}
	var state strings.Builder
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for index, line := range parts {
		styled := line
		if index > 0 {
			styled = state.String() + line
		}
		for _, sgr := range SGR.FindAllString(line, -1) {
			state.WriteString(sgr)
		}
		out = append(out, styled)
	}
	return out
}

type placedCluster struct {
	text       string
	start, end int
}

type cellRange struct{ from, to int }

func wrapLine(line string, w int) []string {
	plain := Strip(line)
	var clusters []placedCluster
	cell := 0
	for _, gc := range charwidth.Clusters(plain) {
		cw := charwidth.Cluster(gc)
		clusters = append(clusters, placedCluster{text: gc, start: cell, end: cell + cw})
		cell += cw
	}

	var words [][]placedCluster
	var current []placedCluster
	for _, entry := range clusters {
		if isSpaceCluster(entry.text) {
			if len(current) > 0 {
				words = append(words, current)
			}
			current = nil
		} else {
			current = append(current, entry)
		}
	}
	if len(current) > 0 {
		words = append(words, current)
	}
	if len(words) == 0 {
		return []string{""}
	}

	var ranges []cellRange
	open := false
	var lineStart, lineEnd int
	for _, word := range words {
		wordStart := word[0].start
		wordEnd := word[len(word)-1].end
		if wordEnd-wordStart > w {
			if open {
				ranges = append(ranges, cellRange{lineStart, lineEnd})
			}
			ranges = append(ranges, hardWrapRanges(word, w)...)
			open = false
		} else if open && wordEnd-lineStart > w {
			ranges = append(ranges, cellRange{lineStart, lineEnd})
			lineStart, lineEnd = wordStart, wordEnd
		} else {
			if !open {
				lineStart = wordStart
				open = true
			}
			lineEnd = wordEnd
		}
	}
	if open {
		ranges = append(ranges, cellRange{lineStart, lineEnd})
	}

	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, CellSlice(line, r.from, r.to-r.from))
	}
	return out
}

func hardWrapRanges(word []placedCluster, w int) []cellRange {
	var ranges []cellRange
	open := false
	var from, to int
	for _, c := range word {
		cw := c.end - c.start
		if cw > w {
			if open {
				ranges = append(ranges, cellRange{from, to})
			}
			ranges = append(ranges, cellRange{c.start, c.start + w})
			open = false
		} else if open && c.end-from > w {
			ranges = append(ranges, cellRange{from, to})
			from, to = c.start, c.end
		} else {
			if !open {
				from = c.start
				open = true
			}
			to = c.end
		}
	}
	if open {
		ranges = append(ranges, cellRange{from, to})
	}
	return ranges
}

// isSpaceCluster mirrors Ruby's /\A\s+\z/ test on a single grapheme cluster.
func isSpaceCluster(gc string) bool {
	if gc == "" {
		return false
	}
	for _, r := range gc {
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
		default:
			return false
		}
	}
	return true
}
