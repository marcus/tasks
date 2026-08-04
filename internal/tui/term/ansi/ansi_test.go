package ansi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Mirrors test/test_ansi.rb, one Go test per Ruby test, plus the wide-character
// cases the port is required to prove.

func TestVisLenPlainASCIIMatchesBytesize(t *testing.T) {
	if got := VisLen("hello"); got != 5 {
		t.Fatalf("VisLen(hello) = %d", got)
	}
	if got := VisLen(""); got != 0 {
		t.Fatalf("VisLen(empty) = %d", got)
	}
	if got, want := VisLen("hello world!"), len("hello world!"); got != want {
		t.Fatalf("VisLen = %d, want %d", got, want)
	}
}

func TestVisLenIgnoresEscapeCodes(t *testing.T) {
	if got := VisLen(Bold(Red("hello"))); got != 5 {
		t.Fatalf("VisLen = %d, want 5", got)
	}
}

func TestStrip(t *testing.T) {
	if got := Strip(Dim("hi") + " " + Cyan("there")); got != "hi there" {
		t.Fatalf("Strip = %q", got)
	}
}

func TestVPadPadsToVisibleWidth(t *testing.T) {
	if got := VisLen(VPad(Bold("ab"), 5)); got != 5 {
		t.Fatalf("padded width = %d", got)
	}
}

func TestVPadLeavesWideStringsAlone(t *testing.T) {
	if got := VPad("abcdef", 3); got != "abcdef" {
		t.Fatalf("VPad = %q", got)
	}
}

func TestVTruncShortStringUnchanged(t *testing.T) {
	if got := VTrunc("abc", 10); got != "abc" {
		t.Fatalf("VTrunc = %q", got)
	}
}

func TestVTruncTruncatesToWidth(t *testing.T) {
	out := VTrunc("abcdefghij", 5)
	if VisLen(out) != 5 {
		t.Fatalf("width = %d", VisLen(out))
	}
	if !strings.Contains(Strip(out), "…") {
		t.Fatalf("missing ellipsis: %q", out)
	}
}

func TestVTruncPreservesCodesAndResets(t *testing.T) {
	out := VTrunc(Red("abcdefghij"), 5)
	if !strings.Contains(out, "\x1b[31m") || !strings.Contains(out, "\x1b[0m") {
		t.Fatalf("codes lost: %q", out)
	}
	if VisLen(out) != 5 {
		t.Fatalf("width = %d", VisLen(out))
	}
}

func TestWrapWrapsWords(t *testing.T) {
	lines := Wrap("one two three four five", 10)
	for _, l := range lines {
		if VisLen(l) > 10 {
			t.Fatalf("line over budget: %q", l)
		}
	}
	if got := strings.Join(lines, " "); got != "one two three four five" {
		t.Fatalf("rejoined = %q", got)
	}
}

// Ruby's "".split("\n", -1) is [], so wrapping nothing yields no lines at all
// — not one empty line. Callers substitute their own placeholder.
func TestWrapOfEmptyTextYieldsNoLines(t *testing.T) {
	if got := Wrap("", 10); len(got) != 0 {
		t.Fatalf("Wrap(empty) = %#v", got)
	}
}

func TestWrapKeepsBlankLines(t *testing.T) {
	got := Wrap("a\n\nb", 10)
	want := []string{"a", "", "b"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("Wrap = %#v", got)
	}
}

func TestWrapPreservesANSI(t *testing.T) {
	lines := Wrap(Bold("styled text"), 20)
	if len(lines) != 1 || Strip(lines[0]) != "styled text" {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "\x1b[1m") || !strings.HasSuffix(lines[0], "\x1b[0m") {
		t.Fatalf("style lost: %q", lines[0])
	}
}

func TestWrapCarriesActiveStyleAcrossExplicitNewlines(t *testing.T) {
	lines := Wrap(Bold("first\nsecond"), 20)
	if len(lines) != 2 || Strip(lines[0]) != "first" || Strip(lines[1]) != "second" {
		t.Fatalf("lines = %#v", lines)
	}
	for _, l := range lines {
		if !strings.Contains(l, "\x1b[1m") || !strings.HasSuffix(l, "\x1b[0m") {
			t.Fatalf("style not carried: %q", l)
		}
	}
}

func TestWrapHardBreaksOverlongWords(t *testing.T) {
	lines := Wrap(strings.Repeat("x", 25), 10)
	want := []string{strings.Repeat("x", 10), strings.Repeat("x", 10), strings.Repeat("x", 5)}
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestWrapHandlesBinaryEncodedUTF8(t *testing.T) {
	// Subprocess reads arrive as raw bytes; multibyte content must not break.
	binary := "moved “Book flight” → 07-03 ✓"
	lines := Wrap(binary, 20)
	if !strings.Contains(strings.Join(lines, " "), "→ 07-03 ✓") {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestWrapScrubsInvalidUTF8(t *testing.T) {
	// A multibyte char split across a read boundary leaves invalid bytes.
	full := "task done ✓"
	truncated := full[:len(full)-1]
	lines := Wrap(truncated, 20)
	if len(lines) == 0 || !strings.Contains(lines[0], "task done") {
		t.Fatalf("lines = %#v", lines)
	}
	for _, l := range lines {
		if !isValidUTF8(l) {
			t.Fatalf("invalid utf8 survived: %q", l)
		}
	}
}

func TestWrapUsesTerminalCellsForWideAndCombiningGraphemes(t *testing.T) {
	samples := []string{
		"界界界界界",
		"👩‍💻👩‍💻 task",
		"éééé",
		Cyan("界界 styled text"),
	}
	for _, sample := range samples {
		for width := 2; width <= 7; width++ {
			for _, line := range Wrap(sample, width) {
				if VisLen(line) > width {
					t.Fatalf("%q exceeded %d: %q", sample, width, line)
				}
			}
		}
	}
}

func TestWrapNeverSplitsGraphemeClusters(t *testing.T) {
	text := "界👩‍💻é界"
	lines := Wrap(text, 3)
	var joined strings.Builder
	var widths []int
	for _, l := range lines {
		joined.WriteString(Strip(l))
		widths = append(widths, VisLen(l))
	}
	if joined.String() != text {
		t.Fatalf("rejoined = %q, want %q", joined.String(), text)
	}
	want := []int{2, 3, 2}
	if len(widths) != len(want) {
		t.Fatalf("widths = %v", widths)
	}
	for i := range want {
		if widths[i] != want[i] {
			t.Fatalf("widths = %v, want %v", widths, want)
		}
	}
}

func TestWrapReplacesAClusterThatCannotFitTheBudget(t *testing.T) {
	lines := Wrap("界", 1)
	if len(lines) != 1 || lines[0] != " " {
		t.Fatalf("Wrap = %#v", lines)
	}
}

func TestVisLenCountsEmojiAsTwoCells(t *testing.T) {
	if got := VisLen("✨"); got != 2 {
		t.Fatalf("VisLen(✨) = %d", got)
	}
	if got := VisLen("Inbox empty. ✨"); got != 15 {
		t.Fatalf("VisLen = %d, want 15", got)
	}
}

func TestVisLenTextPresentationSymbolsStayOneCell(t *testing.T) {
	for _, s := range []string{"⚠", "✓", "▸"} {
		if got := VisLen(s); got != 1 {
			t.Fatalf("VisLen(%q) = %d, want 1", s, got)
		}
	}
}

func TestVisLenEmojiVariationSelectorForcesWide(t *testing.T) {
	if got := VisLen("⚠️"); got != 2 {
		t.Fatalf("VisLen(⚠️) = %d, want 2", got)
	}
}

func TestVisLenZeroWidthJoinerSequenceIsOneCluster(t *testing.T) {
	// A ZWJ family/profession emoji renders as a single two-cell glyph.
	if got := VisLen("👩‍💻"); got != 2 {
		t.Fatalf("VisLen(👩‍💻) = %d, want 2", got)
	}
	if got := VisLen("👨‍👩‍👧‍👦"); got != 2 {
		t.Fatalf("VisLen(family) = %d, want 2", got)
	}
}

func TestVisLenCombiningMarksAddNothing(t *testing.T) {
	if got := VisLen("é"); got != 1 {
		t.Fatalf("VisLen(e+combining acute) = %d, want 1", got)
	}
	if got := VisLen("à́̂"); got != 1 {
		t.Fatalf("VisLen(stacked marks) = %d, want 1", got)
	}
}

func TestVisLenCJKIsTwoCellsPerCharacter(t *testing.T) {
	if got := VisLen("界界界"); got != 6 {
		t.Fatalf("VisLen = %d, want 6", got)
	}
	if got := VisLen("こんにちは"); got != 10 {
		t.Fatalf("VisLen = %d, want 10", got)
	}
	if got := VisLen("한글"); got != 4 {
		t.Fatalf("VisLen = %d, want 4", got)
	}
}

func TestVPadAccountsForEmojiWidth(t *testing.T) {
	// The empty-inbox bug: padding must reserve two cells for the emoji so the
	// line does not overflow its box and wrap to a new terminal row.
	if got := VisLen(VPad(Dim("Inbox empty. ✨"), 20)); got != 20 {
		t.Fatalf("padded width = %d", got)
	}
}

func TestVTruncDoesNotSplitWideChar(t *testing.T) {
	out := VTrunc("ab✨cd", 4)
	if VisLen(out) > 4 {
		t.Fatalf("over budget: %q", out)
	}
	if strings.Contains(Strip(out), "✨") {
		t.Fatalf("half of a two-cell cluster emitted: %q", out)
	}
}

func TestVTruncBinaryWrappedLineRoundtrip(t *testing.T) {
	line := Wrap(strings.Repeat("é", 50), 40)[0]
	if got := VisLen(VTrunc(line, 10)); got != 10 {
		t.Fatalf("width = %d", got)
	}
}

func TestVTruncZeroWidthIsEmpty(t *testing.T) {
	if got := VTrunc("界", 0); got != "" {
		t.Fatalf("VTrunc = %q", got)
	}
}

func TestCellSliceUsesCellOffsetsAndPadsPartialWideClusters(t *testing.T) {
	cases := []struct {
		start, width int
		want         string
	}{
		{0, 2, "a "},
		{1, 2, "界"},
		{2, 2, " b"},
	}
	for _, c := range cases {
		if got := CellSlice("a界b", c.start, c.width); got != c.want {
			t.Fatalf("CellSlice(%d,%d) = %q, want %q", c.start, c.width, got, c.want)
		}
	}
}

func TestCellSlicePreservesStylesAndClosesThem(t *testing.T) {
	sliced := CellSlice(Red("a界b"), 2, 2)
	if Strip(sliced) != " b" {
		t.Fatalf("visible = %q", Strip(sliced))
	}
	if !strings.Contains(sliced, "\x1b[31m") || !strings.HasSuffix(sliced, "\x1b[0m") {
		t.Fatalf("styles = %q", sliced)
	}
}

func TestCellSliceNormalizesInvalidBinaryUTF8(t *testing.T) {
	sliced := CellSlice("ok \xE2\x9C", 0, 10)
	if !isValidUTF8(sliced) {
		t.Fatalf("invalid utf8: %q", sliced)
	}
	if !strings.Contains(sliced, "ok ") {
		t.Fatalf("content lost: %q", sliced)
	}
}

func TestCellSliceToEndTakesEverythingFromStart(t *testing.T) {
	if got := CellSliceToEnd("abcdef", 2); got != "cdef" {
		t.Fatalf("CellSliceToEnd = %q", got)
	}
}

func TestCompositeEmptySGRIsNoop(t *testing.T) {
	if got := Composite("", "hello"); got != "hello" {
		t.Fatalf("Composite = %q", got)
	}
	styled := Bold("hi")
	if got := Composite("", styled); got != styled {
		t.Fatalf("Composite = %q", got)
	}
}

func TestCompositeReinjectsAfterEmbeddedReset(t *testing.T) {
	body := "a" + Red("b") + "c"
	if got, want := Composite("\x1b[1m", body), "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc"; got != want {
		t.Fatalf("Composite = %q, want %q", got, want)
	}
}

func TestCompositeDoesNotAppendTrailingReset(t *testing.T) {
	out := Composite("\x1b[1m", "plain")
	if strings.HasSuffix(out, "\x1b[0m") || out != "\x1b[1mplain" {
		t.Fatalf("Composite = %q", out)
	}
}

func TestCompositeIgnoresFieldOpenerWithZeroParam(t *testing.T) {
	body := "\x1b[38;2;0;0;0mx\x1b[0m"
	want := "\x1b[1m\x1b[38;2;0;0;0mx\x1b[0m\x1b[1m"
	if got := Composite("\x1b[1m", body); got != want {
		t.Fatalf("Composite = %q, want %q", got, want)
	}
}

func TestCloseClosesOpenStylingOnlyWhenNeeded(t *testing.T) {
	if got := Close("plain"); got != "plain" {
		t.Fatalf("Close = %q", got)
	}
	if got := Close("\x1b[1mopen"); got != "\x1b[1mopen\x1b[0m" {
		t.Fatalf("Close = %q", got)
	}
	if got := Close("\x1b[1mdone\x1b[0m"); got != "\x1b[1mdone\x1b[0m" {
		t.Fatalf("Close = %q", got)
	}
	if got := Close("\x1b[1mdone\x1b[m"); got != "\x1b[1mdone\x1b[m" {
		t.Fatalf("Close = %q", got)
	}
}

func isValidUTF8(s string) bool { return utf8.ValidString(s) }
