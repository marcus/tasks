// Package input holds the terminal input primitives: the raw key sequences the
// TUI recognizes, a raw-bytes-to-semantic-event key map, and the grapheme-aware
// single-line/multi-line text editor used by the prompt and by form fields.
//
// Go port of Ruby's lib/term_form/text.rb (Text + TextEditor), the key-byte
// table from lib/term_form/event.rb, and lib/tui/text_input.rb (which is only a
// compatibility alias in Ruby and needs no separate Go type).
//
// This package deliberately does NOT decode terminal input: no raw-mode CSI
// parser, no escape-versus-alt timing heuristic, no bracketed-paste framing, no
// UTF-8 continuation buffer. Bubble Tea owns all of that. What is left — and
// what Bubble Tea does not provide — is the editing model: grapheme-indexed
// cursor motion, word kills, and paste sanitizing. KeyBytes names the sequences
// the editor recognizes, so a framework key message can be mapped onto them.
package input

import (
	"strings"
	"unicode"

	"github.com/marcus/tasks/internal/tui/term/charwidth"
)

// Control bytes the editor understands.
const (
	CtrlA = "\x01"
	CtrlB = "\x02"
	CtrlD = "\x04"
	CtrlE = "\x05"
	CtrlF = "\x06"
	CtrlH = "\x08"
	CtrlK = "\x0b"
	CtrlU = "\x15"
	CtrlW = "\x17"
)

// KeyBytes maps a named key to the byte sequence a terminal sends for it.
var KeyBytes = map[string]string{
	"tab": "\t", "shift_tab": "\x1b[Z", "return": "\r", "escape": "\x1b",
	"up": "\x1b[A", "down": "\x1b[B", "left": "\x1b[D", "right": "\x1b[C",
	"home": "\x1b[H", "end": "\x1b[F", "delete": "\x1b[3~", "backspace": "\x7f",
}

// Result reports what a key did. An unhandled key returns None so the caller
// can try another binding.
type Result string

const (
	None    Result = ""
	Handled Result = "handled"
	Changed Result = "changed"
)

// Normalize returns valid UTF-8, dropping invalid bytes. Field text is user
// input rather than transcript output, so an undecodable byte is discarded
// rather than replaced with a visible marker.
func Normalize(value string) string {
	return strings.ToValidUTF8(value, "")
}

// CellWidth is the display width of a plain string in terminal cells.
func CellWidth(value string) int { return charwidth.String(Normalize(value)) }

// Graphemes splits a value into grapheme clusters — the units the cursor moves
// over, never bytes or codepoints.
func Graphemes(value string) []string { return charwidth.Clusters(Normalize(value)) }

// CellSlice slices a plain string by terminal cells. Partial wide graphemes
// become spaces, preserving the columns of content on either side.
func CellSlice(value string, start, width int) string {
	if start < 0 {
		start = 0
	}
	if width < 0 {
		width = 0
	}
	if width == 0 {
		return ""
	}
	finish := start + width
	cell := 0
	var out strings.Builder
	for _, gc := range Graphemes(value) {
		gw := charwidth.Cluster(gc)
		clusterEnd := cell + gw
		if gw == 0 {
			if cell >= start && cell < finish {
				out.WriteString(gc)
			}
		} else if clusterEnd > start && cell < finish {
			overlapStart := cell
			if start > overlapStart {
				overlapStart = start
			}
			overlapEnd := clusterEnd
			if finish < overlapEnd {
				overlapEnd = finish
			}
			if overlapStart == cell && overlapEnd == clusterEnd {
				out.WriteString(gc)
			} else {
				out.WriteString(strings.Repeat(" ", overlapEnd-overlapStart))
			}
		}
		cell = clusterEnd
		if cell >= finish {
			break
		}
	}
	return out.String()
}

// Editor is mutable editing state. Cursor offsets count grapheme clusters.
type Editor struct {
	units     []string
	cursor    int
	multiline bool
	// killToEnd disables ctrl-k when false, so a host can reserve that key.
	killToEnd bool
}

// Options configures an editor.
type Options struct {
	Multiline bool
	// NoKillToEnd reserves ctrl-k for the host instead of "kill to end of line".
	NoKillToEnd bool
}

// New builds an editor holding text, with the cursor at the end.
func New(text string, opts Options) *Editor {
	e := &Editor{multiline: opts.Multiline, killToEnd: !opts.NoKillToEnd}
	e.Replace(text)
	return e
}

func (e *Editor) Text() string   { return strings.Join(e.units, "") }
func (e *Editor) String() string { return e.Text() }
func (e *Editor) Empty() bool    { return len(e.units) == 0 }
func (e *Editor) Cursor() int    { return e.cursor }
func (e *Editor) Length() int    { return len(e.units) }

// SetCursor moves the cursor, clamped into the text.
func (e *Editor) SetCursor(position int) {
	if position < 0 {
		position = 0
	}
	if position > len(e.units) {
		position = len(e.units)
	}
	e.cursor = position
}

// Replace swaps the whole buffer and parks the cursor at the end.
func (e *Editor) Replace(raw string) {
	e.units = Graphemes(e.sanitize(raw))
	e.cursor = len(e.units)
}

// Clear empties the buffer.
func (e *Editor) Clear() { e.Replace("") }

// Insert types text at the cursor. It reports None when the sanitized input is
// empty, so a host can distinguish "nothing happened" from an edit.
func (e *Editor) Insert(raw string) Result {
	incoming := Graphemes(e.sanitize(raw))
	if len(incoming) == 0 {
		return None
	}
	rest := append([]string{}, e.units[e.cursor:]...)
	e.units = append(append(e.units[:e.cursor:e.cursor], incoming...), rest...)
	e.cursor += len(incoming)
	return Changed
}

// HandleKey applies one raw key sequence.
func (e *Editor) HandleKey(key string) Result {
	switch key {
	case CtrlA, "\x1b[H", "\x1b[1~", "\x1bOH", "\x1b[1;5H", "\x1b[1;3H":
		return e.moveStart()
	case CtrlE, "\x1b[F", "\x1b[4~", "\x1bOF", "\x1b[1;5F", "\x1b[1;3F":
		return e.moveEnd()
	case CtrlB, "\x1b[D":
		return e.moveLeft()
	case CtrlF, "\x1b[C":
		return e.moveRight()
	case "\x1bb", "\x1b[1;5D", "\x1b[1;3D", "\x1b[5D", "\x1b[3D":
		return e.wordLeft()
	case "\x1bf", "\x1b[1;5C", "\x1b[1;3C", "\x1b[5C", "\x1b[3C":
		return e.wordRight()
	case "\x1bd", "\x1b[3;3~":
		return e.killWordForward()
	case CtrlD, "\x1b[3~":
		return e.deleteForward()
	case CtrlH, "\x7f": // ctrl-h and DEL are the two backspace encodings
		return e.backspace()
	case CtrlK:
		if e.killToEnd {
			return e.killToEndOfLine()
		}
		return None
	case CtrlU:
		return e.killToStart()
	case CtrlW, "\x1b\x7f", "\x1b\x08":
		return e.killWordBack()
	case "\r", "\n":
		if e.multiline {
			return e.Insert("\n")
		}
		return None
	default:
		if Printable(key) {
			return e.Insert(key)
		}
		return None
	}
}

// Printable reports whether every grapheme in key is printable, the test that
// separates typed text from control sequences.
func Printable(key string) bool {
	if key == "" {
		return false
	}
	for _, gc := range charwidth.Clusters(key) {
		if !printableCluster(gc) {
			return false
		}
	}
	return true
}

// printableCluster mirrors Ruby's `grapheme.match?(/[[:print:]]/)`, which is a
// partial match: a cluster counts as printable when ANY of its codepoints is.
// That is what admits a ZWJ emoji or a variation selector — whose joiners and
// selectors are format characters — while still rejecting a control cluster
// such as "\r\n".
func printableCluster(gc string) bool {
	for _, r := range gc {
		if unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

// sanitize normalizes pasted text: a single-line editor folds every line break
// and tab into a space, a multi-line one keeps newlines and folds tabs.
func (e *Editor) sanitize(raw string) string {
	text := Normalize(raw)
	if e.multiline {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		text = strings.ReplaceAll(text, "\t", " ")
	} else {
		text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	}
	var out strings.Builder
	for _, gc := range charwidth.Clusters(text) {
		if gc == "\n" || printableCluster(gc) {
			out.WriteString(gc)
		}
	}
	return out.String()
}

func (e *Editor) moveStart() Result {
	e.cursor = 0
	return Handled
}

func (e *Editor) moveEnd() Result {
	e.cursor = len(e.units)
	return Handled
}

func (e *Editor) moveLeft() Result {
	if e.cursor > 0 {
		e.cursor--
	}
	return Handled
}

func (e *Editor) moveRight() Result {
	if e.cursor < len(e.units) {
		e.cursor++
	}
	return Handled
}

func (e *Editor) backspace() Result {
	if e.cursor == 0 {
		return Handled
	}
	e.units = append(e.units[:e.cursor-1], e.units[e.cursor:]...)
	e.cursor--
	return Changed
}

func (e *Editor) deleteForward() Result {
	if e.cursor >= len(e.units) {
		return Handled
	}
	e.units = append(e.units[:e.cursor], e.units[e.cursor+1:]...)
	return Changed
}

func (e *Editor) killToStart() Result {
	if e.cursor == 0 {
		return Handled
	}
	e.units = append([]string{}, e.units[e.cursor:]...)
	e.cursor = 0
	return Changed
}

func (e *Editor) killToEndOfLine() Result {
	if e.cursor >= len(e.units) {
		return Handled
	}
	e.units = e.units[:e.cursor]
	return Changed
}

func (e *Editor) killWordBack() Result {
	if e.cursor == 0 {
		return Handled
	}
	index := e.wordStartBefore(e.cursor)
	e.units = append(append([]string{}, e.units[:index]...), e.units[e.cursor:]...)
	e.cursor = index
	return Changed
}

func (e *Editor) killWordForward() Result {
	if e.cursor >= len(e.units) {
		return Handled
	}
	end := e.wordEndAfter(e.cursor)
	e.units = append(e.units[:e.cursor], e.units[end:]...)
	return Changed
}

func (e *Editor) wordLeft() Result {
	e.cursor = e.wordStartBefore(e.cursor)
	return Handled
}

func (e *Editor) wordRight() Result {
	e.cursor = e.wordEndAfter(e.cursor)
	return Handled
}

func (e *Editor) wordEndAfter(from int) int {
	index := from
	for index < len(e.units) && !isSpace(e.units[index]) {
		index++
	}
	for index < len(e.units) && isSpace(e.units[index]) {
		index++
	}
	return index
}

func (e *Editor) wordStartBefore(from int) int {
	index := from
	for index > 0 && isSpace(e.units[index-1]) {
		index--
	}
	for index > 0 && !isSpace(e.units[index-1]) {
		index--
	}
	return index
}

func isSpace(gc string) bool {
	for _, r := range gc {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return gc != ""
}
