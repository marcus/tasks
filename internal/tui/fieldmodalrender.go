package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/tui/term/border"
)

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
//
// The chrome comes from the shared modal components (chrome.go, button.go,
// scrollregion.go, keychips.go, statusline.go), so every modal family paints
// the same boxes, buttons, scrollbar, and footer chips instead of each growing
// its own. The geometry stays FIXED: a label row, bordered value box, option
// box, and hint row are reserved per field whether or not they currently have
// anything to say, so posting an error never moves a button under the pointer.

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

// The input prompt marking controls that take typed text.
const fieldModalPrompt = "> "

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
	// scrollBarCol is the box column of the scrollbar cell, scroll the track
	// geometry, and scrollRow this row's index within the track. All three are
	// recorded by the paint, so a press on the thumb is answered with exactly
	// the numbers that drew it. scroll is nil on rows without a live thumb.
	scrollBarCol int
	scroll       *fieldModalScroll
	scrollRow    int
}

// fieldModalScroll is one painted scrollbar's hit-test geometry.
type fieldModalScroll struct {
	total   int
	visible int
	track   int
}

// fieldModalSpan is one button's column range within a row, in BOX columns —
// the component returns string-relative spans and this renderer adds the one
// left-border cell, because the click arrives in box coordinates.
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

// withText returns the row carrying different painted text, metadata intact —
// the value rows are built before the box that wraps them.
func (l fieldModalLine) withText(text string) fieldModalLine {
	l.text = text
	return l
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

	// The status row, the buttons, and the key chips are a FIXED FOOTER: they
	// pin to the bottom of whatever budget the terminal allows, and only the
	// field body scrolls. A modal whose submit button disappears because the
	// note grew is a modal that cannot be submitted by mouse in exactly the
	// terminals too small to spare it — the same fixed-footer rule sidecar's
	// frames follow.
	footer := f.footerRowCount()
	split := max(len(content)-footer, 0)
	body, pinned := content[:split], content[split:]
	// When the budget cannot afford the whole footer, the body yields FIRST —
	// a floor of one content row in a terminal too small for the footer is how
	// the submit button used to get scrolled out of exactly those terminals.
	bodyBudget := max(budget-len(pinned), 0)
	offset := 0
	if bodyBudget > 0 {
		offset = formViewportOffset(len(body), bodyBudget, focusRow, caretRow)
	}

	lines, layout := f.chromeFrame(styler, width, inner)
	painted := 0
	visibleCaret := -1
	rail := f.chromePiece(styler, border.Round.V)
	for index := offset; index < len(body) && painted < bodyBudget; index++ {
		row := body[index]
		lines = append(lines, rail+padTo(styler, truncateWithEllipsis(styler, row.text, inner), inner)+rail)
		if row.caret {
			visibleCaret = len(lines) - 1
		}
		layout = append(layout, row)
		painted++
	}
	for painted < bodyBudget {
		lines = append(lines, rail+strings.Repeat(" ", inner)+rail)
		layout = append(layout, fieldModalLine{kind: fieldModalInert})
		painted++
	}
	// The pinned rows are dropped by priority, not by position: the key chips
	// go first, then the status line, and the buttons outlive them both — a box
	// that cannot show its submit cannot be submitted by mouse.
	fit := min(len(pinned), max(budget-painted, 0))
	shown := pinned
	switch {
	case fit <= 0:
		shown = nil
	case fit == 1:
		shown = pinned[1:2] // just the buttons
	default:
		shown = pinned[:min(fit, len(pinned))]
	}
	for _, row := range shown {
		lines = append(lines, rail+padTo(styler, truncateWithEllipsis(styler, row.text, inner), inner)+rail)
		if row.caret {
			visibleCaret = len(lines) - 1
		}
		layout = append(layout, row)
	}
	bottom := f.chromePiece(styler, border.Round.BL) +
		f.chromePiece(styler, strings.Repeat(border.Round.H, inner)) +
		f.chromePiece(styler, border.Round.BR)
	lines = append(lines, bottom)
	layout = append(layout, fieldModalLine{kind: fieldModalInert})

	// renderWidth bounds the pointer's column axis. It is the PAINTED width, not
	// the natural one, so a box the terminal narrowed cannot be clicked outside.
	f.layout, f.renderWidth = layout, width
	return FieldModalRender{Lines: lines, FocusedContentRow: visibleCaret, Width: width}
}

// chromePiece paints one border run in the frame's variant slot. The variant is
// the configured one, overridden to warning while the discard latch is armed —
// color-only news, so no geometry can move under the pointer when it flips.
func (f *FieldModal) chromePiece(styler Styler, piece string) string {
	return PaintChrome(styler, f.variantForPaint(), piece)
}

func (f *FieldModal) variantForPaint() BoxVariant {
	if f.guard || f.armedAction != "" {
		return BoxWarning
	}
	return f.variant
}

// footerRowCount is how many trailing content rows are the pinned footer:
// the status row, the buttons, and — when there is anything worth advertising
// — the key chips.
func (f *FieldModal) footerRowCount() int {
	count := 2 // status, buttons
	if len(f.footerChips()) > 0 {
		count++
	}
	return count
}

// chromeFrame opens the box: a rounded top border carrying the centered title.
func (f *FieldModal) chromeFrame(styler Styler, width, inner int) ([]string, []fieldModalLine) {
	title := truncateWithEllipsis(styler, " "+inlineText(f.title, " ")+" ", inner)
	mid := width - 2 - styler.Width(title)
	left := max(mid/2, 0)
	right := max(mid-left, 0)
	line := f.chromePiece(styler, border.Round.TL) +
		f.chromePiece(styler, strings.Repeat(border.Round.H, left)) +
		styler.Paint("modal_title", title) +
		f.chromePiece(styler, strings.Repeat(border.Round.H, right)) +
		f.chromePiece(styler, border.Round.TR)
	return []string{line}, []fieldModalLine{{kind: fieldModalInert}}
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
	out = f.appendButtonRow(out, styler, inner)
	if chips := f.footerChips(); len(chips) > 0 {
		out = append(out, fieldModalLine{
			kind: fieldModalInert,
			text: "  " + PaintKeyChips(styler, chips),
		})
	}
	return out, focusRow, caretRow
}

// fieldRows paints ONE field: its label, its bordered value box, and — for a
// choice — its bordered option list, then the hint row every field reserves.
func (f *FieldModal) fieldRows(styler Styler, field *modalField, focused bool, inner int) []fieldModalLine {
	out := []fieldModalLine{f.labelRow(styler, field, focused)}
	switch field.spec.Kind {
	case FieldTextArea:
		out = append(out, f.textAreaRows(styler, field, focused, inner)...)
	case FieldChoice:
		out = append(out, f.choiceBoxRow(styler, field, focused, inner)...)
		out = append(out, f.optionListRows(styler, field, inner)...)
	default:
		out = append(out, f.inputBoxRow(styler, field, focused, inner)...)
	}
	return append(out, f.hintRow(styler, field))
}

// labelRow names the field. The focus treatment is the task editor's — the same
// "›" in the same slot — and the unsaved/error mark rides along, because both
// are news about the WHOLE field rather than about one keystroke.
func (f *FieldModal) labelRow(styler Styler, field *modalField, focused bool) fieldModalLine {
	focusMark := " "
	if focused {
		focusMark = styler.Paint("form_focus", "›")
	}
	status := " "
	switch {
	case field.err != "":
		status = styler.Paint("form_error", "!")
	case field.value() != field.initial:
		status = styler.Paint("form_unsaved", "*")
	}
	label := strings.ToUpper(inlineText(field.spec.Label, " "))
	return fieldModalLine{
		kind: fieldModalInert,
		text: " " + focusMark + status + " " + styler.Paint("form_label", label),
	}
}

// fieldBorderSlot picks the bordered-box slot: a focused field gets its own
// border color so the eye lands on the control being edited, and an unfocused
// one shares the ordinary field border.
func fieldBorderSlot(focused bool) string {
	if focused {
		return "field_border_focused"
	}
	return "field_border"
}

// boxedValueRow wraps ONE already-painted interior line in a bordered box whose
// corners, rails, and rules all paint in slot.
func boxedValueRow(styler Styler, slot, content string, innerWidth int) []string {
	horizontal := PaintBorderSlot(styler, slot, strings.Repeat(border.Round.H, innerWidth))
	interior := padTo(styler, truncateWithEllipsis(styler, content, innerWidth), innerWidth)
	return []string{
		PaintBorderSlot(styler, slot, border.Round.TL) + horizontal + PaintBorderSlot(styler, slot, border.Round.TR),
		PaintBorderSlot(styler, slot, border.Round.V) + interior + PaintBorderSlot(styler, slot, border.Round.V),
		PaintBorderSlot(styler, slot, border.Round.BL) + horizontal + PaintBorderSlot(styler, slot, border.Round.BR),
	}
}

// inputBoxRow is a single-line field's bordered editor, prompt included.
func (f *FieldModal) inputBoxRow(styler Styler, field *modalField, focused bool, inner int) []fieldModalLine {
	window := max(inner-2-len(fieldModalPrompt), 1)
	field.valueWidth = window
	view := field.input.Render(window)
	content := fieldModalPrompt
	if focused {
		content += renderCursorCell(styler, view.Lines[0], view.CursorColumn)
	} else {
		content += styler.Paint("form_value", view.Lines[0])
	}
	row := fieldModalLine{
		kind: fieldModalValue, key: field.spec.Key, target: field,
		valueCol: 1 + len(fieldModalPrompt), valueRow: 0,
		caret: focused,
	}
	slot := fieldBorderSlot(focused)
	box := boxedValueRow(styler, slot, content, inner-2)
	return []fieldModalLine{
		{kind: fieldModalInert, text: box[0]},
		row.withText(box[1]),
		{kind: fieldModalInert, text: box[2]},
	}
}

// choiceBoxRow is a choice field's bordered value row: the selection spelled out
// with a ▾ saying more is offered beneath, or a prompted free-text editor when
// typing IS the value.
func (f *FieldModal) choiceBoxRow(styler Styler, field *modalField, focused bool, inner int) []fieldModalLine {
	var content string
	valueCol := 1 + len(fieldModalPrompt)
	if field.spec.FreeText {
		window := max(inner-2-len(fieldModalPrompt), 1)
		field.valueWidth = window
		view := field.input.Render(window)
		value := styler.Paint("form_value", view.Lines[0])
		if focused {
			content = fieldModalPrompt + renderCursorCell(styler, view.Lines[0], view.CursorColumn)
		} else {
			content = fieldModalPrompt + value
		}
	} else {
		window := max(inner-2-3, 1)
		field.valueWidth = window
		content = " " + styler.Paint("form_value", inlineText(field.selectedLabel(), " ")) + styler.Paint("form_hint", " ▾")
		valueCol = 2
	}
	row := fieldModalLine{
		kind: fieldModalValue, key: field.spec.Key, target: field,
		valueCol: valueCol, valueRow: 0,
		caret: focused && field.spec.FreeText,
	}
	slot := fieldBorderSlot(focused)
	box := boxedValueRow(styler, slot, content, inner-2)
	return []fieldModalLine{
		{kind: fieldModalInert, text: box[0]},
		row.withText(box[1]),
		{kind: fieldModalInert, text: box[2]},
	}
}

// textAreaRows is a note's bordered editor: the field's reserved window of
// wrapped rows inside one box, with a real scrollbar column on the right rail
// driven by the wrapped total against the window. The paint draws the caret,
// because a composited overlay has no terminal cursor to move.
func (f *FieldModal) textAreaRows(styler Styler, field *modalField, focused bool, inner int) []fieldModalLine {
	const (
		leadPad   = 1 // the space after the left rail
		scrollCol = 1 // the reserved scrollbar column
	)
	window := max(inner-2-leadPad-scrollCol, 1)
	field.valueWidth = window
	view := field.area.Render(window, field.spec.Rows)
	total := field.area.LineCount(window)

	rows := make([]fieldModalLine, 0, field.spec.Rows)
	for row, text := range view.Lines {
		line := fieldModalLine{
			kind: fieldModalValue, key: field.spec.Key, target: field,
			valueCol: 1 + leadPad, valueRow: row,
		}
		if focused && row == view.CursorRow {
			line.text = renderCursorCell(styler, text, view.CursorColumn)
			line.caret = true
		} else {
			line.text = styler.Paint("form_value", text)
		}
		rows = append(rows, line)
	}
	return borderedRows(styler, fieldBorderSlot(focused), inner, rows, func(index int) (string, *fieldModalScroll) {
		if total > field.spec.Rows {
			thumb := ThumbLocFor(total, view.RowOffset, field.spec.Rows, field.spec.Rows)
			return ScrollbarCell(styler, index, thumb), &fieldModalScroll{
				total: total, visible: field.spec.Rows, track: field.spec.Rows,
			}
		}
		return " ", nil
	}, leadPad)
}

// optionListRows is the choice vocabulary as a bordered list hanging directly
// off the value box. It always paints VisibleOptions rows, blank ones
// included, because a vocabulary that shrinks as it is filtered must not
// shrink the box with it. A wheel-previewed window gets a real scrollbar
// column; the marks stay with the selection wherever the window sits.
func (f *FieldModal) optionListRows(styler Styler, field *modalField, inner int) []fieldModalLine {
	options := field.filtered()
	field.clampList(len(options))

	rows := make([]fieldModalLine, 0, field.spec.VisibleOptions)
	if len(options) == 0 {
		rows = append(rows, fieldModalLine{
			kind: fieldModalInert,
			text: styler.Paint("form_hint", "(no options available)"),
		})
	}
	for position := field.offset; position < len(options) && len(rows) < field.spec.VisibleOptions; position++ {
		option := options[position]
		cursor := " "
		if position == field.highlight {
			cursor = styler.Paint("form_choice_cursor", "❯")
		}
		mark := "[ ]"
		if option.Value == field.selected {
			mark = styler.Paint("form_choice_selected", "[x]")
		}
		rows = append(rows, fieldModalLine{
			kind: fieldModalOption, key: field.spec.Key, target: field,
			optionIndex: position, optionValue: option.Value,
			text: cursor + " " + mark + " " + styler.Paint("form_value", inlineText(option.Label, " ")),
		})
	}
	for len(rows) < field.spec.VisibleOptions {
		rows = append(rows, fieldModalLine{kind: fieldModalInert})
	}

	const leadPad = 1 // the space after the left rail
	thumb := ScrollThumb{}
	var geom *fieldModalScroll
	if len(options) > field.spec.VisibleOptions {
		track := field.spec.VisibleOptions
		thumb = ThumbLocFor(len(options), field.offset, track, track)
		geom = &fieldModalScroll{total: len(options), visible: track, track: track}
	}
	return borderedRows(styler, "field_border", inner, rows, func(index int) (string, *fieldModalScroll) {
		if thumb.Has {
			return ScrollbarCell(styler, index, thumb), geom
		}
		return " ", nil
	}, leadPad)
}

// borderedRows wraps rows in a bordered box, appending one trailing cell per
// content row from trailing (a scrollbar glyph or a space, plus the track
// geometry when the cell is a live scrollbar), with leadPad spaces between the
// left rail and each row's content.
func borderedRows(styler Styler, slot string, inner int, rows []fieldModalLine, trailing func(index int) (string, *fieldModalScroll), leadPad int) []fieldModalLine {
	const scrollCol = 1 // the reserved trailing column
	window := max(inner-2-leadPad-scrollCol, 1)
	barCol := 1 + leadPad + window // box column of the trailing cell
	horizontal := PaintBorderSlot(styler, slot, strings.Repeat(border.Round.H, inner-2))
	out := make([]fieldModalLine, 0, len(rows)+2)
	out = append(out, fieldModalLine{
		kind: fieldModalInert,
		text: PaintBorderSlot(styler, slot, border.Round.TL) + horizontal + PaintBorderSlot(styler, slot, border.Round.TR),
	})
	for index, line := range rows {
		cell, geom := trailing(index)
		line.text = PaintBorderSlot(styler, slot, border.Round.V) +
			strings.Repeat(" ", leadPad) +
			padTo(styler, truncateWithEllipsis(styler, line.text, window), window) +
			cell +
			PaintBorderSlot(styler, slot, border.Round.V)
		if geom != nil {
			line.scrollBarCol = barCol
			line.scroll = geom
			line.scrollRow = index
		}
		out = append(out, line)
	}
	out = append(out, fieldModalLine{
		kind: fieldModalInert,
		text: PaintBorderSlot(styler, slot, border.Round.BL) + horizontal + PaintBorderSlot(styler, slot, border.Round.BR),
	})
	return out
}

// hintRow is the per-field guidance line, and the SAME row an inline validation
// error is painted in. Costing an error zero extra rows is the whole reason the
// row is always there.
func (f *FieldModal) hintRow(styler Styler, field *modalField) fieldModalLine {
	if message := field.err; message != "" {
		return fieldModalLine{kind: fieldModalInert,
			text: "    " + styler.Paint("form_error", "! "+inlineText(message, " "))}
	}
	if field.spec.Hint == "" {
		return fieldModalLine{kind: fieldModalInert}
	}
	return fieldModalLine{kind: fieldModalInert,
		text: "    " + styler.Paint("form_hint", "· "+inlineText(field.spec.Hint, " "))}
}

// statusRow carries the host's refusal or the armed discard latch. It is one
// fixed row, so posting a refusal never moves the buttons under the pointer.
func (f *FieldModal) statusRow(styler Styler) fieldModalLine {
	switch {
	case f.guard:
		return fieldModalLine{kind: fieldModalInert,
			text: PaintStatusLine(styler, StatusWarning,
				"discard unsaved changes? esc again discards · anything else keeps editing")}
	case f.err != "":
		return fieldModalLine{kind: fieldModalInert,
			text: PaintStatusLine(styler, StatusError, f.err)}
	}
	return fieldModalLine{kind: fieldModalInert}
}

// modalButtons is the action row: submit filled primary, cancel muted, extra
// affordances danger — armed ones inverted to demand the second press. Each
// button names the key that also invokes it, so the mouse path advertises the
// keyboard path instead of hiding it.
func (f *FieldModal) modalButtons() []ModalButton {
	buttons := []ModalButton{
		{ID: fieldModalSubmitID, Label: f.submitLabel, KeyLabel: "enter", Variant: ButtonPrimary},
		{ID: fieldModalCancelID, Label: f.cancelLabel, KeyLabel: "esc", Variant: ButtonMuted},
	}
	for _, action := range f.actions {
		variant := ButtonDanger
		if action.ID == f.armedAction {
			variant = ButtonDangerArmed
		}
		buttons = append(buttons, ModalButton{
			ID: action.ID, Label: action.Label, KeyLabel: action.KeyLabel, Variant: variant,
		})
	}
	return buttons
}

// appendButtonRow paints every affordance and records its columns. Component
// spans index the text PaintButtonRow returned; the recorded ones are box
// columns, because that is what clicks arrive in. Two cells separate the two
// coordinate systems, and both have to be counted: Render lays each row out as
// rail + text, which puts row.text at box column 1, and this row's text is
// itself " " + the painted buttons. Counting only the rail put every span one
// cell left of its paint, so the blank between two buttons invoked the button
// to its right — on this modal that is Release and Undelegate.
//
// Spans are clamped to the interior the paint actually shows: a narrow box
// truncates the row, but the recorded spans were built against the full text,
// and an unclamped end would let a click on the RIGHT BORDER invoke a button
// whose label was cut off — the exact hole the both-axes bound in Click exists
// to close. The clamp is applied after the shift, so a button that begins past
// the interior is dropped rather than being clamped into a live one-cell span.
func (f *FieldModal) appendButtonRow(out []fieldModalLine, styler Styler, inner int) []fieldModalLine {
	const textOrigin = 2 // one rail cell, one leading space
	text, spans := PaintButtonRow(styler, f.modalButtons())
	recorded := make([]fieldModalSpan, 0, len(spans))
	for _, span := range spans {
		begin := span.Begin + textOrigin
		end := min(span.End+textOrigin, inner+1) // inner+1: first cell past the interior
		if begin >= end {
			continue // truncated away entirely
		}
		recorded = append(recorded, fieldModalSpan{begin: begin, end: end, id: span.ID})
	}
	return append(out, fieldModalLine{
		kind:  fieldModalButton,
		text:  " " + text,
		spans: recorded,
	})
}

// footerChips advertises the bindings that have NO button: moving between
// fields, paging a choice list, and the note's newline. Enter and escape live
// on the buttons above, so they are deliberately not repeated here — one
// surface per binding.
func (f *FieldModal) footerChips() []KeyChip {
	chips := []KeyChip{{Key: "tab", Label: "next"}}
	for _, field := range f.fields {
		if field.spec.Kind == FieldChoice {
			chips = append(chips, KeyChip{Key: "↑↓", Label: "options"})
			break
		}
	}
	for _, field := range f.fields {
		if field.spec.Kind == FieldTextArea {
			// Return is text in a note, so the key that submits from one is put
			// on screen rather than left to a help modal. In a box too short to
			// show every row this line can scroll out of the viewport like any
			// other, so it is a convenience, not the only place the key is
			// documented — the exception is stated in fieldmodalinput.go's
			// contract and in the API docs Packet G reads.
			chips = append(chips, KeyChip{Key: "ctrl-s", Label: f.submitLabel + " from a note"})
			break
		}
	}
	return chips
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
		widest = max(widest, styler.Width(strings.ToUpper(inlineText(field.spec.Label, " ")))+8)
		measure(fieldModalPrompt + field.spec.Initial)
		measure("    · " + inlineText(field.spec.Hint, " "))
		if field.spec.Kind == FieldChoice {
			for _, option := range field.options() {
				measure("❯ [x] " + inlineText(option.Label, " "))
			}
		}
	}
	buttons, _ := PaintButtonRow(styler, f.modalButtons())
	measure(" " + buttons)
	if chips := f.footerChips(); len(chips) > 0 {
		measure("  " + PaintKeyChips(styler, chips))
	}
	f.width = max(widest, 40)
	return f.width
}

// Height is the box's natural height: every field's fixed rows, the status row,
// the buttons, the key chips, plus the border. A caller clamps it to the
// terminal; nothing about the content changes it.
func (f *FieldModal) Height() int {
	rows := f.footerRowCount()
	for _, field := range f.fields {
		rows += field.rows()
	}
	return rows + 2
}
