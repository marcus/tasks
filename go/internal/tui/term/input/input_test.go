package input

import (
	"strings"
	"testing"
)

// Mirrors test/test_text_input.rb and the text-editing invariants of
// test/test_term_form_text_fields.rb (the form-model half of that file belongs
// to the forms package).

func editor(text string) *Editor { return New(text, Options{}) }

func TestEmacsMovementAndInsertion(t *testing.T) {
	e := editor("abcd")
	e.HandleKey(CtrlB)
	e.HandleKey(CtrlB)
	e.HandleKey("X")
	if got := e.Text(); got != "abXcd" {
		t.Fatalf("text = %q", got)
	}
	e.HandleKey(CtrlA)
	e.HandleKey(">")
	e.HandleKey(CtrlE)
	e.HandleKey("<")
	if got := e.Text(); got != ">abXcd<" {
		t.Fatalf("text = %q", got)
	}
}

func TestDeleteBackspaceAndKillShortcuts(t *testing.T) {
	e := editor("alpha beta gamma")
	for i := 0; i < 6; i++ {
		e.HandleKey(CtrlB) // before " gamma"
	}
	e.HandleKey(CtrlK)
	if got := e.Text(); got != "alpha beta" {
		t.Fatalf("after ctrl-k = %q", got)
	}
	e.HandleKey(CtrlW)
	if got := e.Text(); got != "alpha " {
		t.Fatalf("after ctrl-w = %q", got)
	}
	e.HandleKey(CtrlU)
	if got := e.Text(); got != "" {
		t.Fatalf("after ctrl-u = %q", got)
	}
}

func TestForwardDeleteAndArrows(t *testing.T) {
	e := editor("abc")
	e.HandleKey(CtrlA)
	e.HandleKey(CtrlD)
	if got := e.Text(); got != "bc" {
		t.Fatalf("after ctrl-d = %q", got)
	}
	e.HandleKey("\x1b[C")
	e.HandleKey("X")
	if got := e.Text(); got != "bXc" {
		t.Fatalf("after right+X = %q", got)
	}
	e.HandleKey("\x1b[3~")
	if got := e.Text(); got != "bX" {
		t.Fatalf("after delete = %q", got)
	}
}

func TestModifiedArrowsMoveByWord(t *testing.T) {
	e := editor("alpha beta gamma")
	e.HandleKey(CtrlA)
	e.HandleKey("\x1b[1;5C")
	e.HandleKey("X")
	if got := e.Text(); got != "alpha Xbeta gamma" {
		t.Fatalf("after word-right = %q", got)
	}
	e.HandleKey("\x1b[1;5D")
	e.HandleKey("Y")
	if got := e.Text(); got != "alpha YXbeta gamma" {
		t.Fatalf("after word-left = %q", got)
	}
}

func TestHandleKeyDistinguishesChangedFromHandled(t *testing.T) {
	e := editor("a")
	if got := e.HandleKey(CtrlB); got != Handled {
		t.Fatalf("ctrl-b = %q", got)
	}
	if got := e.HandleKey(CtrlB); got != Handled {
		t.Fatalf("ctrl-b at the start = %q", got)
	}
	if got := e.HandleKey("b"); got != Changed {
		t.Fatalf("typing = %q", got)
	}
	if got := e.HandleKey("\x7f"); got != Changed {
		t.Fatalf("DEL = %q", got)
	}
	if got := e.HandleKey("\x00"); got != None {
		t.Fatalf("NUL = %q", got)
	}
}

func TestModifiedHomeEndSequencesAreHandled(t *testing.T) {
	e := editor("abc")
	if got := e.HandleKey("\x1b[1;5H"); got != Handled {
		t.Fatalf("modified home = %q", got)
	}
	e.HandleKey(">")
	if got := e.Text(); got != ">abc" {
		t.Fatalf("text = %q", got)
	}
	if got := e.HandleKey("\x1b[1;5F"); got != Handled {
		t.Fatalf("modified end = %q", got)
	}
	e.HandleKey("<")
	if got := e.Text(); got != ">abc<" {
		t.Fatalf("text = %q", got)
	}
}

func TestPasteSanitizesLineBreaksWithoutSubmitting(t *testing.T) {
	e := editor("")
	e.Insert("first\nsecond\thttps://example.com")
	if got := e.Text(); got != "first second https://example.com" {
		t.Fatalf("text = %q", got)
	}
}

func TestUnicodeCursorMovesByGrapheme(t *testing.T) {
	e := editor("aé🙂b")
	e.HandleKey(CtrlB)
	e.HandleKey("X")
	if got := e.Text(); got != "aé🙂Xb" {
		t.Fatalf("text = %q", got)
	}
}

func TestInputEditsCombiningAndWideGraphemesAsUnits(t *testing.T) {
	// A combining sequence and a ZWJ emoji are each one cursor unit.
	e := editor("aé👩‍💻b")
	if got := e.Length(); got != 4 {
		t.Fatalf("grapheme length = %d, want 4", got)
	}
	e.HandleKey("\x7f") // delete b
	e.HandleKey("\x7f") // delete the whole ZWJ cluster
	if got := e.Text(); got != "aé" {
		t.Fatalf("text = %q", got)
	}
	e.HandleKey("\x7f") // delete the whole combining cluster
	if got := e.Text(); got != "a" {
		t.Fatalf("text = %q", got)
	}
}

func TestInputReservesCtrlKWhenTheHostAsks(t *testing.T) {
	e := New("alpha beta", Options{NoKillToEnd: true})
	e.SetCursor(5)
	if got := e.HandleKey(CtrlK); got != None {
		t.Fatalf("ctrl-k = %q, want the host to keep it", got)
	}
	if got := e.Text(); got != "alpha beta" {
		t.Fatalf("text = %q", got)
	}
}

func TestSingleLineReturnIsNotConsumed(t *testing.T) {
	e := editor("abc")
	if got := e.HandleKey("\r"); got != None {
		t.Fatalf("return = %q, want the host to submit", got)
	}
	if got := e.Text(); got != "abc" {
		t.Fatalf("text = %q", got)
	}
}

func TestTextAreaReturnInsertsNewlineWhileTabStaysWithTheHost(t *testing.T) {
	e := New("", Options{Multiline: true})
	e.Insert("one")
	if got := e.HandleKey("\r"); got != Changed {
		t.Fatalf("return = %q", got)
	}
	e.Insert("two")
	if got := e.Text(); got != "one\ntwo" {
		t.Fatalf("text = %q", got)
	}
	if got := e.HandleKey("\t"); got != None {
		t.Fatalf("tab = %q, want form traversal", got)
	}
}

func TestTextAreaPastePreservesLineBreaksAndNormalizesTabsOnEntry(t *testing.T) {
	e := New("", Options{Multiline: true})
	e.Insert("first\r\nsecond\tthird\rfourth")
	if got := e.Text(); got != "first\nsecond third\nfourth" {
		t.Fatalf("text = %q", got)
	}
}

func TestSetCursorClampsIntoTheText(t *testing.T) {
	e := editor("abc")
	e.SetCursor(-5)
	if e.Cursor() != 0 {
		t.Fatalf("cursor = %d", e.Cursor())
	}
	e.SetCursor(99)
	if e.Cursor() != 3 {
		t.Fatalf("cursor = %d", e.Cursor())
	}
}

func TestReplaceAndClearParkTheCursorAtTheEnd(t *testing.T) {
	e := editor("abc")
	e.SetCursor(0)
	e.Replace("longer text")
	if e.Cursor() != e.Length() {
		t.Fatalf("cursor = %d, length = %d", e.Cursor(), e.Length())
	}
	e.Clear()
	if !e.Empty() || e.Cursor() != 0 {
		t.Fatalf("cleared editor = %q at %d", e.Text(), e.Cursor())
	}
}

func TestInsertRejectsControlOnlyInput(t *testing.T) {
	e := editor("abc")
	if got := e.Insert("\x00\x01"); got != None {
		t.Fatalf("control-only insert = %q", got)
	}
	if got := e.Text(); got != "abc" {
		t.Fatalf("text = %q", got)
	}
}

func TestCellWidthAndCellSliceUseTerminalCells(t *testing.T) {
	if got := CellWidth("a界b"); got != 4 {
		t.Fatalf("CellWidth = %d", got)
	}
	cases := []struct {
		start, width int
		want         string
	}{
		{0, 2, "a "},
		{1, 2, "界"},
		{2, 2, " b"},
		{0, 0, ""},
	}
	for _, c := range cases {
		if got := CellSlice("a界b", c.start, c.width); got != c.want {
			t.Fatalf("CellSlice(%d,%d) = %q, want %q", c.start, c.width, got, c.want)
		}
	}
}

func TestNormalizeDropsUndecodableBytes(t *testing.T) {
	if got := Normalize("ok \xE2\x9C"); got != "ok " {
		t.Fatalf("Normalize = %q", got)
	}
}

func TestPrintableRejectsControlSequences(t *testing.T) {
	for _, key := range []string{"", "\x00", "\x1b[A", "\t", "\r"} {
		if Printable(key) {
			t.Fatalf("Printable(%q) = true", key)
		}
	}
	for _, key := range []string{"a", " ", "界", "👩‍💻", "é", strings.Repeat("x", 5)} {
		if !Printable(key) {
			t.Fatalf("Printable(%q) = false", key)
		}
	}
}

func TestKeyBytesNameTheTerminalSequences(t *testing.T) {
	cases := map[string]string{
		"tab": "\t", "shift_tab": "\x1b[Z", "return": "\r", "escape": "\x1b",
		"up": "\x1b[A", "down": "\x1b[B", "left": "\x1b[D", "right": "\x1b[C",
		"home": "\x1b[H", "end": "\x1b[F", "delete": "\x1b[3~", "backspace": "\x7f",
	}
	for name, want := range cases {
		if got := KeyBytes[name]; got != want {
			t.Fatalf("KeyBytes[%q] = %q, want %q", name, got, want)
		}
	}
}
