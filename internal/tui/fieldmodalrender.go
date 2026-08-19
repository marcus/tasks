package tui

import "strings"

// The paint and the hit map are the SAME pass.
//
// Every content line is built as a fieldModalLine carrying what is under it —
// a field's value, one option, one button, or nothing — and the box's final
// line list is recorded alongside it. A click is then answered by indexing the
// recorded rows, never by re-deriving which field "should" be on row 7. That is
// the palette's rule, and it is what survives a filtered option list, a scrolled
// box, and a terminal too short to show every field.
//
// What that buys is the ROW: the recorded map cannot disagree with the paint
// about which row belongs to which control. It does not by itself pin down what
// the row MEANT, because the vocabulary behind an option row is a runtime func
// that may change between the paint and the click — so an option row also
// records the value it painted, and the click resolves by that value rather than
// by its position.

// fieldModalTargetKind is what a painted row answers to.
type fieldModalTargetKind string

const (
	fieldModalInert  fieldModalTargetKind = "inert"
	fieldModalValue  fieldModalTargetKind = "field"
	fieldModalOption fieldModalTargetKind = "option"
	fieldModalButton fieldModalTargetKind = "button"
)

// The two reserved button ids.
const (
	fieldModalSubmitID = "submit"
	fieldModalCancelID = "cancel"
)

// fieldModalLine is one painted row plus what a pointer landing on it means.
type fieldModalLine struct {
	text   string
	kind   fieldModalTargetKind
	key    string
	target *modalField
	// valueCol is the box column the value starts at, and valueRow which
	// visible row of a multi-row value this is. Together they turn a click into
	// a caret offset.
	valueCol int
	valueRow int
	// optionIndex is where the option sat at paint time and optionValue is what
	// it WAS. The click trusts the value, not the position.
	optionIndex int
	optionValue string
	spans       []fieldModalSpan
	caret       bool
}

// fieldModalSpan is one button's column range within a row.
type fieldModalSpan struct {
	begin, end int
	id         string
}

// FieldModalRender is a painted modal.
type FieldModalRender struct {
	Lines []string
	// FocusedContentRow is the row within Lines carrying the caret, or -1.
	FocusedContentRow int
	Width             int
}

// Render paints the modal into a bordered box no larger than the budget, and
// records the hit map the pointer methods read.
func (f *FieldModal) Render(styler Styler, maxWidth, maxHeight int) FieldModalRender {
	width := f.boxWidth(styler)
	if maxWidth > 0 {
		width = min(width, maxWidth)
	}
	width = max(width, 8)
	inner := width - 2
	budget := max(maxHeight-2, 1)

	content, focusRow, caretRow := f.content(styler, inner)
	offset := formViewportOffset(len(content), budget, focusRow, caretRow)

	lines := []string{"┌" + padBorderLead0(truncateWithEllipsis(styler, " "+inlineText(f.title, " ")+" ", inner), inner) + "┐"}
	layout := []fieldModalLine{{kind: fieldModalInert}}
	painted := 0
	visibleCaret := -1
	for index := offset; index < len(content) && painted < budget; index++ {
		row := content[index]
		lines = append(lines, "│"+padTo(styler, truncateWithEllipsis(styler, row.text, inner), inner)+"│")
		if row.caret {
			visibleCaret = len(lines) - 1
		}
		layout = append(layout, row)
		painted++
	}
	for painted < budget {
		lines = append(lines, "│"+strings.Repeat(" ", inner)+"│")
		layout = append(layout, fieldModalLine{kind: fieldModalInert})
		painted++
	}
	lines = append(lines, "└"+strings.Repeat("─", inner)+"┘")
	layout = append(layout, fieldModalLine{kind: fieldModalInert})

	// renderWidth bounds the pointer's column axis. It is the PAINTED width, not
	// the natural one, so a box the terminal narrowed cannot be clicked outside.
	f.layout, f.renderWidth = layout, width
	return FieldModalRender{Lines: lines, FocusedContentRow: visibleCaret, Width: width}
}

// content builds every content row. It returns the rows, the row the focused
// field starts on, and the row carrying the caret, which is what the viewport
// scrolls against.
func (f *FieldModal) content(styler Styler, inner int) ([]fieldModalLine, int, int) {
	out := []fieldModalLine{}
	focusRow, caretRow := -1, -1
	for index, field := range f.fields {
		focused := index == f.focus
		if focused {
			focusRow = len(out)
		}
		before := len(out)
		out = append(out, f.fieldRows(styler, field, focused, inner)...)
		if focused {
			for row := before; row < len(out); row++ {
				if out[row].caret {
					caretRow = row
				}
			}
		}
	}
	out = append(out, f.statusRow(styler))
	out = append(out, f.buttonRow(styler))
	out = append(out, fieldModalLine{
		kind: fieldModalInert,
		text: styler.Paint("form_hint", "  "+f.keyHintText()),
	})
	return out, focusRow, caretRow
}

func (f *FieldModal) fieldRows(styler Styler, field *modalField, focused bool, inner int) []fieldModalLine {
	prefix, prefixWidth := f.rowPrefix(styler, field, focused)
	out := []fieldModalLine{}
	switch field.spec.Kind {
	case FieldTextArea:
		out = append(out, fieldModalLine{
			text: prefix, kind: fieldModalValue, key: field.spec.Key, target: field,
			valueCol: 1 + prefixWidth, valueRow: -1,
		})
		lead, leadWidth := f.continuation(styler, focused)
		window := max(inner-leadWidth, 1)
		field.valueWidth = window
		view := field.area.Render(window, field.spec.Rows)
		for row, text := range view.Lines {
			line := fieldModalLine{
				kind: fieldModalValue, key: field.spec.Key, target: field,
				valueCol: 1 + leadWidth, valueRow: row,
			}
			if focused && row == view.CursorRow {
				line.text = lead + renderCursorCell(styler, text, view.CursorColumn)
				line.caret = true
			} else {
				line.text = lead + styler.Paint("form_value", text)
			}
			out = append(out, line)
		}
	case FieldChoice:
		window := max(inner-prefixWidth, 1)
		field.valueWidth = window
		value := fieldModalLine{
			kind: fieldModalValue, key: field.spec.Key, target: field,
			valueCol: 1 + prefixWidth, valueRow: 0,
		}
		if field.spec.FreeText {
			view := field.input.Render(window)
			if focused {
				value.text = prefix + renderCursorCell(styler, view.Lines[0], view.CursorColumn)
				value.caret = true
			} else {
				value.text = prefix + styler.Paint("form_value", view.Lines[0])
			}
		} else {
			text := field.selectedLabel()
			if query := field.query.Text(); query != "" {
				text += "  ⌕" + query
			}
			value.text = prefix + styler.Paint("form_value", text)
		}
		out = append(out, value)
		out = append(out, f.optionRows(styler, field)...)
	default:
		window := max(inner-prefixWidth, 1)
		field.valueWidth = window
		view := field.input.Render(window)
		line := fieldModalLine{
			kind: fieldModalValue, key: field.spec.Key, target: field,
			valueCol: 1 + prefixWidth, valueRow: 0,
		}
		if focused {
			line.text = prefix + renderCursorCell(styler, view.Lines[0], view.CursorColumn)
			line.caret = true
		} else {
			line.text = prefix + styler.Paint("form_value", view.Lines[0])
		}
		out = append(out, line)
	}
	return append(out, f.hintRow(styler, field))
}

// optionRows paints the choice window. It always paints VisibleOptions rows,
// blank ones included, because a vocabulary that shrinks as it is filtered must
// not shrink the box with it.
func (f *FieldModal) optionRows(styler Styler, field *modalField) []fieldModalLine {
	options := field.filtered()
	field.clampList(len(options))
	out := []fieldModalLine{}
	if len(options) == 0 {
		out = append(out, fieldModalLine{
			kind: fieldModalInert,
			text: styler.Paint("form_hint", "    (no options available)"),
		})
	}
	for position := field.offset; position < len(options) && len(out) < field.spec.VisibleOptions; position++ {
		option := options[position]
		cursor := " "
		if position == field.highlight {
			cursor = styler.Paint("form_choice_cursor", ">")
		}
		mark := "[ ]"
		if option.Value == field.selected {
			mark = styler.Paint("form_choice_selected", "[x]")
		}
		out = append(out, fieldModalLine{
			kind: fieldModalOption, key: field.spec.Key, target: field,
			optionIndex: position, optionValue: option.Value,
			text: "  " + cursor + " " + mark + " " + styler.Paint("form_value", inlineText(option.Label, " ")),
		})
	}
	for len(out) < field.spec.VisibleOptions {
		out = append(out, fieldModalLine{kind: fieldModalInert})
	}
	return out
}

// hintRow is the per-field guidance line, and the SAME row an inline validation
// error is painted in. Costing an error zero extra rows is the whole reason the
// row is always there.
func (f *FieldModal) hintRow(styler Styler, field *modalField) fieldModalLine {
	if message := field.err; message != "" {
		return fieldModalLine{kind: fieldModalInert,
			text: styler.Paint("form_error", "    ! "+inlineText(message, " "))}
	}
	if field.spec.Hint == "" {
		return fieldModalLine{kind: fieldModalInert}
	}
	return fieldModalLine{kind: fieldModalInert,
		text: styler.Paint("form_hint", "    · "+inlineText(field.spec.Hint, " "))}
}

// statusRow carries the host's refusal or the armed discard latch. It is one
// fixed row, so posting a refusal never moves the buttons under the pointer.
func (f *FieldModal) statusRow(styler Styler) fieldModalLine {
	switch {
	case f.guard:
		return fieldModalLine{kind: fieldModalInert,
			text: styler.Paint("form_error", "  ! discard unsaved changes? esc again discards · anything else keeps editing")}
	case f.err != "":
		return fieldModalLine{kind: fieldModalInert,
			text: styler.Paint("form_error", "  ! "+inlineText(f.err, " "))}
	}
	return fieldModalLine{kind: fieldModalInert}
}

// buttonRow paints every affordance and records its columns. Each button names
// the key that also invokes it, so the mouse path advertises the keyboard path
// instead of hiding it.
func (f *FieldModal) buttonRow(styler Styler) fieldModalLine {
	line := fieldModalLine{kind: fieldModalButton}
	column := 1
	text := ""
	add := func(id, label, keyLabel string) {
		plain := "[ " + label + " ]"
		if keyLabel != "" {
			plain = "[ " + label + " (" + keyLabel + ") ]"
		}
		text += " " + styler.Paint("form_label", plain)
		column++
		width := len([]rune(plain))
		line.spans = append(line.spans, fieldModalSpan{begin: column, end: column + width, id: id})
		column += width
	}
	add(fieldModalSubmitID, f.submitLabel, "enter")
	add(fieldModalCancelID, f.cancelLabel, "esc")
	for _, action := range f.actions {
		add(action.ID, action.Label, action.KeyLabel)
	}
	line.text = text
	return line
}

func (f *FieldModal) keyHintText() string {
	parts := []string{"tab moves", "enter " + f.submitLabel, "esc cancels"}
	for _, field := range f.fields {
		if field.spec.Kind == FieldTextArea {
			// Return is text in a note, so the key that submits from one is put
			// on screen rather than left to a help modal. In a box too short to
			// show every row this line can scroll out of the viewport like any
			// other, so it is a convenience, not the only place the key is
			// documented — the exception is stated in fieldmodalinput.go's
			// contract and in the API docs Packet G reads.
			parts = append(parts, "ctrl-s "+f.submitLabel+" from a note")
			break
		}
	}
	return strings.Join(parts, " · ")
}

// rowPrefix is the focus mark, the unsaved/error mark and the label. The focus
// treatment is the task editor's — the same "›" in the same slot — rather than
// a second one invented here.
func (f *FieldModal) rowPrefix(styler Styler, field *modalField, focused bool) (string, int) {
	focusMark, focusPlain := " ", " "
	if focused {
		focusMark, focusPlain = styler.Paint("form_focus", "›"), "›"
	}
	status, statusPlain := " ", " "
	switch {
	case field.err != "":
		status, statusPlain = styler.Paint("form_error", "!"), "!"
	case field.value() != field.initial:
		status, statusPlain = styler.Paint("form_unsaved", "*"), "*"
	}
	label := inlineText(field.spec.Label, " ")
	painted := focusMark + status + " " + styler.Paint("form_label", label) + ": "
	plain := focusPlain + statusPlain + " " + label + ": "
	return painted, len([]rune(plain))
}

func (f *FieldModal) continuation(styler Styler, focused bool) (string, int) {
	if focused {
		return styler.Paint("form_focus", "│") + "  ", 3
	}
	return "   ", 3
}

// boxWidth measures the box ONCE, from the whole content, and caches it. Typing
// a longer value, filtering the vocabulary, or posting an error can then never
// resize the box or move its centered position.
func (f *FieldModal) boxWidth(styler Styler) int {
	if f.width > 0 {
		return f.width
	}
	widest := max(styler.Width(f.title)+6, f.minWidth)
	measure := func(text string) {
		widest = max(widest, styler.Width(text)+4)
	}
	for _, field := range f.fields {
		_, prefix := f.rowPrefix(styler, field, true)
		measure(strings.Repeat(" ", prefix) + field.spec.Initial)
		measure("    · " + inlineText(field.spec.Hint, " "))
		for _, option := range field.options() {
			measure("    [x] " + inlineText(option.Label, " "))
		}
	}
	measure(stripStyling(f.buttonRow(styler).text))
	measure("  " + f.keyHintText())
	f.width = max(widest, 40)
	return f.width
}

// Height is the box's natural height: every field's fixed rows, the status row,
// the buttons and the key hint, plus the border. A caller clamps it to the
// terminal; nothing about the content changes it.
func (f *FieldModal) Height() int {
	rows := 3
	for _, field := range f.fields {
		rows += field.rows()
	}
	return rows + 2
}
