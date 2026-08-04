package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/charwidth"
	"github.com/marcus/tasks/internal/tui/term/input"
	"github.com/marcus/tasks/internal/tui/termform"
)

// formContextRows is how many lines of the surrounding form stay visible above
// a focused field when the content is scrolled, so bringing a field into view
// reads as scrolling rather than snapping it to the edge.
const formContextRows = 2

// FormRender is a rendered form box.
type FormRender struct {
	Lines []string
	// FocusedContentRow is the row, within Lines' content area, that holds the
	// caret — what a host needs to place a real terminal cursor. -1 when the
	// focused row scrolled out of view.
	FocusedContentRow int
}

// FormRenderRequest is everything a render needs. The caller supplies the WHOLE
// cell budget; this renderer never samples terminal geometry.
type FormRenderRequest struct {
	Model  termform.RenderModel
	Width  int
	Height int
	Title  string
	Hint   string
	// Error is a host-level failure — a rejected save — which outranks the
	// form's own validation messages, because it is the newer news.
	Error  string
	Suffix string
}

// RenderForm paints a TermForm render model into a bordered box.
//
// It is a pure function of the request: no terminal, no cursor addressing, no
// clock. That is what makes the fixed-size render fixtures in the tests
// meaningful — the same request produces the same bytes on every machine.
func RenderForm(styler Styler, request FormRenderRequest) FormRender {
	width := max(request.Width, 0)
	height := max(request.Height, 0)
	if width == 0 || height == 0 {
		return FormRender{Lines: []string{}, FocusedContentRow: request.Model.FocusedRowIndex()}
	}
	if width < 6 || height < 3 {
		return renderFormCompact(styler, request, width, height)
	}

	innerWidth := width - 2
	content := []string{}
	focusedContentRow := -1
	focusedFieldRow := -1
	for _, group := range request.Model.Groups {
		if label := strings.TrimSpace(inlineText(group.Label, " ")); label != "" {
			content = append(content, styler.Paint("form_group_label", " "+label+" "))
		}
		for _, row := range group.Rows {
			external := request.Error != "" && row.Focused
			lines, focusOffset := renderFieldRows(styler, row, innerWidth, request.Suffix, external)
			if row.Focused {
				focusedFieldRow = len(content)
				focusedContentRow = len(content) + focusOffset
			}
			content = append(content, lines...)
			content = append(content, renderPicker(styler, row, width-2)...)
			content = append(content, renderChoices(styler, row)...)
		}
	}

	message := request.Error
	if message == "" {
		message = firstErrorMessage(request.Model)
	}
	if message == "" {
		message = request.Hint
	}
	if message != "" {
		slot := "form_hint"
		cue := "· "
		if request.Error != "" || len(request.Model.Errors) > 0 {
			slot, cue = "form_error", "! "
		}
		content = append(content, styler.Paint(slot, cue+inlineText(message, " ")))
	}
	if len(content) == 0 {
		content = []string{styler.Paint("form_hint", "(empty form)")}
	}

	budget := height - 2
	offset := formViewportOffset(len(content), budget, focusedFieldRow, focusedContentRow)
	shown := []string{}
	for index := offset; index < len(content) && len(shown) < budget; index++ {
		shown = append(shown, padTo(styler, truncateWithEllipsis(styler, content[index], innerWidth), innerWidth))
	}
	for len(shown) < budget {
		shown = append(shown, strings.Repeat(" ", innerWidth))
	}

	title := truncateWithEllipsis(styler, " "+inlineText(request.Title, " ")+" ", innerWidth)
	lines := []string{"┌" + padBorderLead0(title, innerWidth) + "┐"}
	for _, line := range shown {
		lines = append(lines, "│"+line+"│")
	}
	lines = append(lines, "└"+strings.Repeat("─", innerWidth)+"┘")

	visibleFocus := -1
	if focusedContentRow >= 0 {
		visibleFocus = focusedContentRow - offset
	}
	return FormRender{Lines: lines, FocusedContentRow: visibleFocus}
}

// renderFormCompact is the one-line degradation for a terminal too small for a
// box. It keeps whichever single fact matters most right now — an error, an
// open picker's selected date, or the focused value.
func renderFormCompact(styler Styler, request FormRenderRequest, width, height int) FormRender {
	var row *termform.Row
	if focused := request.Model.FocusedRow(); focused != nil {
		row = focused
	} else if len(request.Model.Rows) > 0 {
		row = &request.Model.Rows[0]
	}
	label := ""
	if row != nil {
		label = strings.TrimSpace(inlineText(row.Label, " "))
	}
	if label == "" {
		label = inlineText(request.Title, " ")
	}
	compactValue := ""
	if row != nil {
		compactValue = inlineText(rowText(*row), " ↵ ")
	}
	plain := label
	switch {
	case request.Error != "":
		marker := ""
		if row != nil && row.Focused {
			marker = "›"
		}
		plain = marker + "! " + label + ": " + inlineText(request.Error, " ")
	case row != nil && pickerOpen(*row):
		plain = "> " + pickerSelectedISO(*row)
	case row != nil && compactValue != "":
		focus, dirty := " ", " "
		if row.Focused {
			focus = "›"
		}
		if row.Dirty {
			dirty = "*"
		}
		plain = focus + dirty + " " + compactValue
	}
	clipped := truncateWithEllipsis(styler, plain, width)
	if row != nil && (pickerOpen(*row) || (rowText(*row) == "" && request.Error == "")) {
		clipped = ansi.CellSlice(plain, 0, width)
	}
	line := padTo(styler, clipped, width)
	lines := []string{line}
	if height < 1 {
		lines = []string{}
	}
	focusRow := -1
	if row != nil {
		focusRow = 0
	}
	return FormRender{Lines: lines, FocusedContentRow: focusRow}
}

// renderFieldRows renders one field, wrapping when it is a note or a focused
// single-line input.
func renderFieldRows(styler Styler, row termform.Row, width int, suffix string, external bool) ([]string, int) {
	if !multilineValue(row) && !wrapFocusedInput(row) {
		return []string{renderRow(styler, row, suffix, external)}, 0
	}
	return renderMultilineRows(styler, row, width, suffix, external)
}

func renderRow(styler Styler, row termform.Row, suffix string, external bool) string {
	tail := ""
	if suffix != "" {
		tail = "  " + styler.Paint("form_hint", inlineText(suffix, " "))
	}
	return rowPrefix(styler, row, external) + renderValue(styler, row) + tail
}

func rowPrefix(styler Styler, row termform.Row, external bool) string {
	focus := " "
	if row.Focused {
		focus = styler.Paint("form_focus", "›")
	}
	label := inlineText(row.Label, " ")
	if row.Required {
		label += "*"
	}
	slot := "form_label"
	if !row.Enabled {
		slot = "form_disabled"
	}
	return focus + rowStatus(styler, row, external) + " " + styler.Paint(slot, label) + ": "
}

// renderValue paints the value, drawing the caret as a styled cell rather than
// moving a real terminal cursor. A form can be rendered into a panel, a popup
// or a test string; only one of those has a cursor to move.
func renderValue(styler Styler, row termform.Row) string {
	text := inlineText(rowText(row), " ")
	if !row.Focused || row.Cursor == nil {
		return styler.Paint("form_value", text)
	}
	clusters := input.Graphemes(text)
	cursor := clamp(*row.Cursor, 0, len(clusters))
	before := strings.Join(clusters[:cursor], "")
	at, after := " ", ""
	if cursor < len(clusters) {
		at = clusters[cursor]
		after = strings.Join(clusters[cursor+1:], "")
	}
	return styler.Paint("form_value", before) + styler.Paint("form_cursor", at) +
		styler.Paint("form_value", after)
}

func renderMultilineRows(styler Styler, row termform.Row, width int, suffix string, external bool) ([]string, int) {
	prefix := rowPrefix(styler, row, external)
	continuation := "   "
	if row.Focused {
		continuation = styler.Paint("form_focus", "│") + rowStatus(styler, row, external) + " "
	}
	firstWidth := max(width-styler.Width(prefix), 0)
	continuationWidth := max(width-styler.Width(continuation), 1)
	lines, cursorRow, cursorColumn := multilineLayout(rowText(row), firstWidth, continuationWidth, row.Cursor)

	out := make([]string, 0, len(lines))
	for index, segment := range lines {
		var value string
		if row.Focused && row.Cursor != nil && index == cursorRow {
			value = renderCursorCell(styler, segment, cursorColumn)
		} else {
			value = styler.Paint("form_value", segment)
		}
		if !row.Enabled {
			value = styler.Paint("form_disabled", value)
		}
		lead := continuation
		if index == 0 {
			lead = prefix
		}
		out = append(out, lead+value)
	}
	if suffix != "" && len(out) > 0 {
		out[len(out)-1] += "  " + styler.Paint("form_hint", inlineText(suffix, " "))
	}
	return out, cursorRow
}

// multilineLayout wraps a value across a first row that shares its width with
// the field label and continuation rows that do not.
//
// The two different widths are the whole reason this is not the field's own
// wrapping: the renderer knows how many cells the label ate, and the field does
// not.
func multilineLayout(value string, firstWidth, continuationWidth int, cursor *int) ([]string, int, int) {
	text := newlineRe.ReplaceAllString(value, "\n")
	units := input.Graphemes(text)
	cursorVisible := cursor != nil
	position := len(units)
	if cursorVisible {
		position = clamp(*cursor, 0, len(units))
	}
	lines := []string{""}
	positions := make([][2]int, len(units)+1)
	row, column, capacity := 0, 0, firstWidth
	newRow := func() {
		lines = append(lines, "")
		row++
		column = 0
		capacity = continuationWidth
	}
	// A label can consume the entire first row. Move to the first real value
	// row once; later newline handling can then tell a full line apart from an
	// intentional empty logical line without advancing twice.
	if capacity <= 0 && (len(units) > 0 || cursorVisible) {
		newRow()
	}
	for index, grapheme := range units {
		if grapheme == "\n" {
			if column == capacity {
				newRow()
				positions[index] = [2]int{row, column}
			} else {
				positions[index] = [2]int{row, column}
				newRow()
			}
			continue
		}
		cells := charwidth.Cluster(grapheme)
		if column == capacity || (column > 0 && column+cells > capacity) {
			newRow()
		}
		positions[index] = [2]int{row, column}
		if cells > capacity {
			lines[row] += strings.Repeat(" ", capacity)
			column += capacity
		} else {
			lines[row] += grapheme
			column += cells
		}
	}
	if column == capacity && cursorVisible {
		newRow()
	}
	positions[len(units)] = [2]int{row, column}
	at := positions[position]
	return lines, at[0], at[1]
}

func renderCursorCell(styler Styler, text string, column int) string {
	before := ansi.CellSlice(text, 0, column)
	cell := 0
	cluster := ""
	for _, grapheme := range input.Graphemes(text) {
		width := charwidth.Cluster(grapheme)
		if cell == column && width > 0 {
			cluster = grapheme
			break
		}
		cell += width
	}
	if cluster == "" {
		return styler.Paint("form_value", before) + styler.Paint("form_cursor", " ")
	}
	width := charwidth.Cluster(cluster)
	after := ansi.CellSlice(text, column+width, max(styler.Width(text)-column-width, 0))
	return styler.Paint("form_value", before) + styler.Paint("form_cursor", cluster) +
		styler.Paint("form_value", after)
}

func rowStatus(styler Styler, row termform.Row, external bool) string {
	switch {
	case external || row.Error() != "":
		return styler.Paint("form_error", "!")
	case row.Dirty:
		return styler.Paint("form_unsaved", "*")
	}
	return " "
}

// rowText is what the field wants shown: its own display text when it has one
// (a humanized recurrence, a date preview), the live search query for a
// searchable choice field, and otherwise the value itself.
func rowText(row termform.Row) string {
	if text, present := row.Metadata["text"].(string); present {
		return text
	}
	if searchable, _ := row.Metadata["searchable"].(bool); searchable {
		if query, present := row.Metadata["query"].(string); present {
			return query
		}
	}
	return valueText(row.Value)
}

func valueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case []string:
		return strings.Join(typed, " ")
	}
	return ""
}

func renderChoices(styler Styler, row termform.Row) []string {
	options, _ := row.Metadata["options"].([]map[string]any)
	out := []string{}
	for _, option := range options {
		cursor := " "
		if highlighted, _ := option["highlighted"].(bool); highlighted {
			cursor = styler.Paint("form_choice_cursor", ">")
		}
		selected := "[ ]"
		if chosen, _ := option["selected"].(bool); chosen {
			selected = styler.Paint("form_choice_selected", "[x]")
		}
		label, _ := option["label"].(string)
		out = append(out, "  "+cursor+" "+selected+" "+inlineText(label, " "))
	}
	return out
}

// renderPicker paints the calendar a date field opened.
func renderPicker(styler Styler, row termform.Row, width int) []string {
	if !pickerOpen(row) {
		return nil
	}
	calendar, present := row.Metadata["picker"].(termform.Calendar)
	if !present {
		return nil
	}
	month := calendar.Month
	selected := calendar.Selected
	if width < 28 {
		return []string{
			styler.Paint("form_group", monthYear(month)),
			styler.Paint("form_choice_cursor", ">") + " " +
				month3(selected) + " " + itoa(selected.Day) + " selected",
		}
	}
	lines := []string{
		styler.Paint("form_group", monthYear(month)),
		"Selected " + styler.Paint("form_choice_selected", "["+twoDigits(selected.Day)+"]") +
			" · " + selected.ISO(),
	}
	header := ""
	for _, label := range calendar.WeekdayLabels {
		header += " " + label + " "
	}
	lines = append(lines, header)
	for _, week := range calendar.Weeks {
		line := ""
		for _, day := range week {
			switch {
			case day == selected:
				line += styler.Paint("form_choice_selected", "["+twoDigits(day.Day)+"]")
			case day.Month == month.Month:
				line += " " + rightAlign(itoa(day.Day), 2) + " "
			default:
				line += "    "
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func pickerOpen(row termform.Row) bool {
	open, _ := row.Metadata["picker_open"].(bool)
	return open
}

func pickerSelectedISO(row termform.Row) string {
	calendar, present := row.Metadata["picker"].(termform.Calendar)
	if !present {
		return ""
	}
	return calendar.Selected.ISO()
}

func firstErrorMessage(model termform.RenderModel) string {
	if base := model.Errors["base"]; len(base) > 0 {
		return base[0]
	}
	for _, row := range model.Rows {
		if message := row.Error(); message != "" {
			return message
		}
	}
	return ""
}

func multilineValue(row termform.Row) bool {
	if kind, _ := row.Metadata["kind"].(string); kind == "text_area" {
		return true
	}
	return strings.ContainsAny(rowText(row), "\r\n")
}

// wrapFocusedInput wraps a single-line field only WHILE it is being edited, so
// the whole value stays visible instead of truncating at the panel edge. A
// blurred field keeps the compact one-line form.
func wrapFocusedInput(row termform.Row) bool {
	kind, _ := row.Metadata["kind"].(string)
	return row.Focused && kind == "input"
}

// formViewportOffset picks the first visible content row: the focused field is
// anchored a couple of rows below the top so navigation keeps context above it,
// and the CURSOR is followed downward only when the field is taller than the
// budget — so typing near the end of a long note never scrolls the caret away.
func formViewportOffset(size, budget, fieldRow, cursorRow int) int {
	if size <= budget || fieldRow < 0 {
		return 0
	}
	offset := fieldRow - formContextRows
	if cursorRow > offset+budget-1 {
		offset = cursorRow - budget + 1
	}
	return clamp(offset, 0, size-budget)
}

// -- text helpers -------------------------------------------------------------------

var newlineRe = regexp.MustCompile(`\r\n|\r|\n`)

func inlineText(value, separator string) string {
	return newlineRe.ReplaceAllString(value, separator)
}

// truncateWithEllipsis cuts to width and marks the cut, so a reader can tell a
// clipped line from a short one.
func truncateWithEllipsis(styler Styler, line string, width int) string {
	if width <= 0 {
		return ""
	}
	if styler.Width(line) <= width {
		return line
	}
	return ansi.CellSlice(line, 0, width-1) + styler.Paint("form_hint", "…")
}

// padBorderLead0 lays a title into the top rule starting at column 0, which is
// where FormRenderer puts it (title_lead: 0), unlike the picker's lead of 1.
func padBorderLead0(title string, width int) string {
	plain := len([]rune(stripStyling(title)))
	if plain >= width {
		return strings.Repeat("─", max(width, 0))
	}
	return title + strings.Repeat("─", width-plain)
}

func monthYear(date temporal.Date) string { return date.Month.String() + " " + itoa(date.Year) }

func month3(date temporal.Date) string {
	name := date.Month.String()
	if len(name) < 3 {
		return name
	}
	return name[:3]
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + itoa(value)
	}
	return itoa(value)
}

func itoa(value int) string { return strconv.Itoa(value) }

func rightAlign(text string, width int) string {
	if pad := width - len([]rune(text)); pad > 0 {
		return strings.Repeat(" ", pad) + text
	}
	return text
}
