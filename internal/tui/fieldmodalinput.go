package tui

import "github.com/marcus/tasks/internal/tui/term/input"

// Keyboard and pointer are two spellings of ONE set of actions.
//
// The list is short and it is the whole contract:
//
//	move focus       tab / shift-tab            click a field
//	place the caret  arrows, home, end          click inside the value
//	choose an option arrows, prefix typing      click the option
//	scroll a note    arrows past the window     wheel over the note
//	scroll a list    wheel over it              wheel over it
//	drag a scrollbar press the thumb, drag       press the thumb, drag
//	jump a scrollbar click the track            click the track
//	submit           enter (ctrl-s in a note)   click [ Save ]
//	cancel           esc, twice when dirty      click [ Cancel ], twice when dirty
//	extra actions    the action's own key       click the action's button
//
// Every row of that table is a case in the parity test, which drives the action
// both ways and asserts the two leave identical state — cursor offsets, list
// scroll and all, not just the values.
//
// TWO DOCUMENTED ASYMMETRIES, and they are the only ones:
//
//   - In a multi-line field Return is TEXT, so submitting from a note is ctrl-s
//     or the button rather than Return.
//   - Wheeling a choice list is a PREVIEW, not a selection: it moves the window
//     over the vocabulary and leaves the value alone, so you can look at what
//     else is on offer without losing what you picked. The arrow keys have no
//     such gesture — they move the highlight, and the highlight IS the
//     selection. So "scroll a list" is a mouse-only affordance on purpose, and
//     nothing it does is unreachable from the keyboard: every option it brings
//     into view is selectable by arrowing or by typing a prefix.
//
//     The visible consequence, which is inherent to previewing rather than a
//     defect: wheel far enough and the selected option leaves the window, so
//     neither the `❯` cursor nor the `[x]` mark is painted and the field shows
//     no selection at all for as long as you are looking elsewhere. The value is
//     untouched — wheeling back, or moving focus away and returning, brings the
//     marks straight back. What made this state confusing was that it used to be
//     INVISIBLE: nothing on screen distinguished "the window moved" from "the
//     selection vanished". The painted scrollbar resolves exactly that — the
//     thumb now SHOWS where the window sits inside the vocabulary — so the
//     preview semantics stay and the confusion goes.
//
// Ctrl-C is NOT in the table on purpose: it is the shell's global binding, so a
// wedged overlay can always be escaped, and it reaches this modal exactly as it
// reaches the single-field popups.
//
// A click outside the box is INERT. It is consumed — the modal keeps the
// pointer, exactly as every other overlay does — but it neither submits nor
// cancels, because losing a half-typed delegation to a stray click on the list
// behind it is not a gesture anyone means.

// HandleKey routes one raw key sequence.
func (f *FieldModal) HandleKey(sequence string) FieldModalOutcome {
	// A key ends any in-flight scrollbar drag: the pointer gesture handed off
	// to the keyboard, and a thumb that kept chasing a pointer the hand has
	// abandoned would scroll whichever field the drag started on.
	f.scrollDrag = nil
	if sequence != "\x1b" {
		// Any input other than a second escape means the user carried on
		// working, so the discard latch disarms — the task-draft confirmation
		// behaves the same way.
		f.guard = false
	}
	switch sequence {
	case "\t":
		f.moveFocus(1)
		return fieldModalHandled()
	case "\x1b[Z":
		f.moveFocus(-1)
		return fieldModalHandled()
	case "\x1b":
		return f.Cancel()
	case "\x13":
		return f.Submit()
	case "\r", "\n":
		if field := f.focused(); field != nil && field.spec.Kind == FieldTextArea {
			return f.applyEdit(field, field.area.HandleRawKey(sequence))
		}
		return f.Submit()
	}
	for _, action := range f.actions {
		if action.Key != "" && action.Key == sequence {
			return FieldModalOutcome{Result: FieldModalActioned, ActionID: action.ID}
		}
	}
	return f.fieldKey(sequence)
}

func (f *FieldModal) fieldKey(sequence string) FieldModalOutcome {
	field := f.focused()
	if field == nil {
		return fieldModalHandled()
	}
	switch field.spec.Kind {
	case FieldTextArea:
		switch sequence {
		case "\x1b[A":
			field.area.ScrollLines(-1)
			return fieldModalHandled()
		case "\x1b[B":
			field.area.ScrollLines(1)
			return fieldModalHandled()
		}
		return f.applyEdit(field, field.area.HandleRawKey(sequence))
	case FieldChoice:
		switch sequence {
		case "\x1b[A":
			return f.moveHighlight(field, -1)
		case "\x1b[B":
			return f.moveHighlight(field, 1)
		case "\x1b[5~":
			return f.moveHighlight(field, -field.spec.VisibleOptions)
		case "\x1b[6~":
			return f.moveHighlight(field, field.spec.VisibleOptions)
		}
		if field.query.HandleRawKey(sequence) == input.Changed {
			f.applyQuery(field)
			return fieldModalChanged()
		}
		return fieldModalHandled()
	default:
		return f.applyEdit(field, field.input.HandleRawKey(sequence))
	}
}

// Paste inserts pasted text into the focused field. Pasted newlines are text in
// a note and are sanitized away by a single-line editor; neither is ever a
// command.
func (f *FieldModal) Paste(text string) FieldModalOutcome {
	field := f.focused()
	if field == nil {
		return fieldModalHandled()
	}
	f.guard = false
	switch {
	case field.spec.Kind == FieldTextArea:
		return f.applyEdit(field, field.area.Paste(text))
	case field.spec.Kind == FieldChoice:
		if field.query.Paste(text) == input.Changed {
			f.applyQuery(field)
			return fieldModalChanged()
		}
		return fieldModalHandled()
	default:
		return f.applyEdit(field, field.input.Paste(text))
	}
}

func (f *FieldModal) applyEdit(field *modalField, status input.Result) FieldModalOutcome {
	if status != input.Changed {
		return fieldModalHandled()
	}
	f.clearErrors(field)
	return fieldModalChanged()
}

// clearErrors drops the messages the user is in the middle of answering. Both
// the field's own error and the host's refusal go, because a refusal about a
// value that no longer exists is noise. An armed action disarms with them: its
// button and the confirmation message are ONE story, so editing anything
// retells it from the first press.
func (f *FieldModal) clearErrors(field *modalField) {
	field.err = ""
	f.err = ""
	f.armedAction = ""
}

func (f *FieldModal) moveFocus(offset int) {
	if len(f.fields) == 0 {
		return
	}
	f.focus = ((f.focus+offset)%len(f.fields) + len(f.fields)) % len(f.fields)
	if field := f.focused(); field != nil && field.spec.Kind == FieldChoice {
		field.focusGained()
	}
}

func (f *FieldModal) moveHighlight(field *modalField, offset int) FieldModalOutcome {
	options := field.filtered()
	if len(options) == 0 {
		return fieldModalHandled()
	}
	field.highlight = clamp(field.highlight+offset, 0, len(options)-1)
	field.revealHighlight(len(options))
	return f.choose(field, options[field.highlight])
}

// choose commits one option. A free-text field also adopts the option's spelling
// into its buffer, so the row reads as the thing that was picked rather than as
// the prefix that found it.
func (f *FieldModal) choose(field *modalField, option FieldOption) FieldModalOutcome {
	if field.selected == option.Value {
		return fieldModalHandled()
	}
	field.selected = option.Value
	if field.spec.FreeText {
		field.input.SyncValue(option.Value)
		field.input.SetCursor(len(termformGraphemes(option.Value)))
	}
	f.clearErrors(field)
	return fieldModalChanged()
}

// applyQuery re-reads the vocabulary after a keystroke.
//
// Two behaviors, and the difference is what FreeText means. A plain choice
// field's typing is a PREFIX JUMP: the best match becomes the selection, which
// is how a delegation mode is picked without ever touching an arrow key. A free-text
// field's typing is the VALUE: it filters the offered options, but what was
// typed stands on its own if it matches none of them.
func (f *FieldModal) applyQuery(field *modalField) {
	options := field.filtered()
	field.highlight, field.offset = 0, 0
	field.revealHighlight(len(options))
	f.clearErrors(field)
	if field.spec.FreeText {
		field.selected = field.input.Text()
		return
	}
	if len(options) > 0 {
		field.selected = options[0].Value
	}
}

// -- submit and cancel -----------------------------------------------------------

// Submit validates every field and then runs the caller's mutation. Validation
// runs first and a failure never reaches the host: asking a host to write a
// value the modal already knows is wrong just produces a worse error message,
// and focus lands on the first offending field so the user is looking at it.
func (f *FieldModal) Submit() FieldModalOutcome {
	f.guard = false
	f.err = ""
	f.armedAction = "" // a submit attempt answers whatever was armed
	invalid := -1
	for index, field := range f.fields {
		field.err = field.validationError()
		if field.err != "" && invalid < 0 {
			invalid = index
		}
	}
	if invalid >= 0 {
		f.focus = invalid
		return FieldModalOutcome{Result: FieldModalError}
	}
	if f.submit == nil {
		return FieldModalOutcome{Result: FieldModalSubmitted}
	}
	if message := f.submit(f.Values()); message != "" {
		f.err = message
		return FieldModalOutcome{Result: FieldModalError}
	}
	return FieldModalOutcome{Result: FieldModalSubmitted}
}

// Cancel discards, behind the unsaved-changes latch.
//
// A clean modal closes on the first gesture. A dirty one arms and reports
// Guarded, and only the SECOND cancel discards — the same two-step the task
// draft quit confirmation uses, for the same reason: work someone typed is not
// thrown away by one keystroke.
func (f *FieldModal) Cancel() FieldModalOutcome {
	if !f.Dirty() {
		f.guard = false
		return FieldModalOutcome{Result: FieldModalCancelled}
	}
	if !f.guard {
		f.guard = true
		return FieldModalOutcome{Result: FieldModalGuarded}
	}
	f.guard = false
	return FieldModalOutcome{Result: FieldModalCancelled}
}

// -- pointer ---------------------------------------------------------------------

// Click answers a left click at a BOX-RELATIVE row and column.
//
// Everything it needs was recorded by the last paint, so a row that is not on
// screen cannot be hit and a filtered option list cannot be off by the number of
// options the filter removed.
//
// BOTH axes are bounded. Bounding only the row was a real hole: a button's span
// is recorded against its untruncated text, so in a narrow box the span of a
// button whose label was cut off extended past the border, and a click that
// landed on the list BEHIND the modal invoked it.
func (f *FieldModal) Click(row, column int) FieldModalOutcome {
	line, present := f.lineAt(row, column)
	if !present {
		return fieldModalHandled()
	}
	// Any click other than a second one on Cancel means the user carried on
	// working, so the discard latch disarms exactly as it does for a key.
	armed := f.guard
	f.guard = false
	if line.scroll != nil && column == line.scrollBarCol {
		return f.scrollPress(row, line)
	}
	switch line.kind {
	case fieldModalButton:
		for _, span := range line.spans {
			if column < span.begin || column >= span.end {
				continue
			}
			switch span.id {
			case fieldModalSubmitID:
				return f.Submit()
			case fieldModalCancelID:
				// The button is the mouse's spelling of escape, latch included:
				// the first click on a dirty modal arms, the second discards.
				f.guard = armed
				return f.Cancel()
			default:
				return FieldModalOutcome{Result: FieldModalActioned, ActionID: span.id}
			}
		}
		return fieldModalHandled()
	case fieldModalOption:
		f.SetFocus(line.key)
		options := line.target.filtered()
		// The row's POSITION is only as good as the paint that recorded it: the
		// vocabulary is a runtime func, so it may have reordered — or lost an
		// entry — between the paint and the click. What was painted is the
		// option's value, so that is what is selected; an entry that is simply
		// gone selects nothing rather than whatever slid into its place.
		index := -1
		for position, option := range options {
			if option.Value == line.optionValue {
				index = position
				break
			}
		}
		if index < 0 {
			return fieldModalHandled()
		}
		line.target.highlight = index
		line.target.clampList(len(options))
		return f.choose(line.target, options[index])
	case fieldModalValue:
		f.SetFocus(line.key)
		f.placeCaret(line, column)
		return fieldModalHandled()
	}
	return fieldModalHandled()
}

// scrollPress answers a press on one painted scrollbar cell. A press on the
// thumb starts a drag (recording where inside the thumb the pointer grabbed);
// a press on the track jumps so the thumb TOP anchors at the click — macOS
// jump-to-spot, so the thumb never teleports its middle to the pointer.
func (f *FieldModal) scrollPress(boxRow int, line fieldModalLine) FieldModalOutcome {
	s := line.scroll
	// The pressed cell's track index was recorded by the paint — never re-derive
	// it from arithmetic that could disagree with what was drawn.
	trackRow := line.scrollRow
	thumb := ThumbLocFor(s.total, f.scrollOffset(line), s.track, s.track)
	if !thumb.Has {
		return fieldModalHandled()
	}
	if trackRow >= thumb.Pos && trackRow < thumb.Pos+thumb.Size {
		f.scrollDrag = &fieldModalScrollDrag{
			key: line.key, area: line.kind == fieldModalValue,
			grab: trackRow - thumb.Pos, trackTop: boxRow - trackRow,
		}
		return fieldModalHandled()
	}
	f.applyScrollOffset(line, OffsetAtRow(s.total, s.visible, s.track, trackRow))
	return fieldModalHandled()
}

// Drag continues an in-flight scrollbar drag: the motion's box row becomes a
// track row via the track origin captured at press, the grab offset restores
// the pointer's place within the thumb, and the params are rebuilt LIVE from
// the field — not trusted from the paint — because the vocabulary behind a
// choice list is a runtime func and a note may have been edited mid-drag.
func (f *FieldModal) Drag(row int) FieldModalOutcome {
	if f.scrollDrag == nil {
		return fieldModalHandled()
	}
	d := f.scrollDrag
	field := f.byKey[d.key]
	if field == nil {
		f.scrollDrag = nil
		return fieldModalHandled()
	}
	var total, visible int
	if d.area {
		total = field.area.LineCount(field.valueWidth)
		visible = field.spec.Rows
	} else {
		total = len(field.filtered())
		visible = field.spec.VisibleOptions
	}
	trackRow := row - d.trackTop - d.grab // where the thumb top should go
	target := OffsetAtRow(total, visible, visible, trackRow)
	current := f.fieldScrollOffset(field, d.area)
	if target == current {
		return fieldModalHandled()
	}
	if d.area {
		field.area.ScrollToRow(target)
	} else {
		field.offset = target
	}
	return fieldModalHandled()
}

// EndDrag releases the pointer after a scrollbar drag. It reports whether a
// drag was actually in flight, so a stray release can fall through.
func (f *FieldModal) EndDrag() bool {
	inFlight := f.scrollDrag != nil
	f.scrollDrag = nil
	return inFlight
}

// scrollOffset reads the surface's current offset from the painted geometry's
// owning line.
func (f *FieldModal) scrollOffset(line fieldModalLine) int {
	field := f.byKey[line.key]
	if field == nil {
		return 0
	}
	return f.fieldScrollOffset(field, line.kind == fieldModalValue)
}

func (f *FieldModal) fieldScrollOffset(field *modalField, area bool) int {
	if area {
		return field.area.RowOffset()
	}
	return field.offset
}

// applyScrollOffset moves the pressed surface so its offset becomes target.
func (f *FieldModal) applyScrollOffset(line fieldModalLine, target int) {
	field := f.byKey[line.key]
	if field == nil {
		return
	}
	if line.kind == fieldModalValue {
		field.area.ScrollToRow(target)
		return
	}
	field.offset = clamp(target, 0, max(len(field.filtered())-field.spec.VisibleOptions, 0))
}

// placeCaret turns a column into a caret offset by asking the field to invert
// its own layout. The renderer knows where the value started; only the field
// knows how far it had scrolled.
func (f *FieldModal) placeCaret(line fieldModalLine, column int) {
	field := line.target
	offset := column - line.valueCol
	if offset < 0 {
		offset = 0
	}
	switch {
	case field.spec.Kind == FieldTextArea:
		if line.valueRow < 0 {
			return
		}
		field.area.SetCursor(field.area.OffsetAt(field.valueWidth, line.valueRow, offset))
	case field.input != nil:
		field.input.SetCursor(field.input.OffsetAt(offset))
	}
}

// Wheel answers one wheel tick at a box-relative row. Direction is -1 for up.
//
// A tick over a choice list scrolls the OFFERED options without changing the
// selection, and a tick over a note scrolls the note by moving the caret a line
// — the same motion the arrow keys make, so the two never disagree about where
// the text is.
func (f *FieldModal) Wheel(row, column, direction int) FieldModalOutcome {
	line, present := f.lineAt(row, column)
	if !present {
		return fieldModalHandled()
	}
	// A wheel tick is input like any other, so it disarms the discard latch.
	// Leaving it out meant scrolling between the two escapes still discarded.
	f.guard = false
	switch {
	case line.kind == fieldModalOption, line.kind == fieldModalValue && line.target.spec.Kind == FieldChoice:
		field := line.target
		options := field.filtered()
		window := field.spec.VisibleOptions
		field.offset = clamp(field.offset+direction, 0, max(len(options)-window, 0))
		return fieldModalHandled()
	case line.kind == fieldModalValue && line.target.spec.Kind == FieldTextArea:
		line.target.area.ScrollLines(direction)
		return fieldModalHandled()
	}
	return fieldModalHandled()
}

// lineAt resolves a box-relative cell to the row the paint recorded there, and
// refuses any cell outside the painted box on either axis.
func (f *FieldModal) lineAt(row, column int) (fieldModalLine, bool) {
	if row < 0 || row >= len(f.layout) {
		return fieldModalLine{}, false
	}
	if column < 0 || column >= f.renderWidth {
		return fieldModalLine{}, false
	}
	return f.layout[row], true
}

func termformGraphemes(text string) []string { return input.Graphemes(text) }
