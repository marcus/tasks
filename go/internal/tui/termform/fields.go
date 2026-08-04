package termform

import (
	"strings"
	"time"

	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/term/charwidth"
	"tasks-go/internal/tui/term/input"
)

// View is one field's laid-out window: the visible lines plus where the cursor
// sits in them and where it sits in the whole value.
//
// The two cursors are both needed. CursorRow/Column is where to put the
// terminal caret; VirtualCursorRow/Column is where the cursor is in the value,
// which is what scrolling decisions are made from. Collapsing them would make a
// scrolled field's caret follow the viewport instead of the text.
type View struct {
	Lines               []string
	CursorRow           int
	CursorColumn        int
	VirtualCursorRow    int
	VirtualCursorColumn int
	RowOffset           int
	ColumnOffset        int
	Width               int
	Height              int
}

// Option is one choice in a Select or MultiSelect.
type Option struct {
	Value    any
	Label    string
	Metadata map[string]any
}

// NewOption builds an option, defaulting the label to the value.
func NewOption(value any, label string) Option {
	if label == "" {
		label = optionText(value)
	}
	return Option{Value: value, Label: label}
}

func optionText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	}
	return ""
}

// -- text fields ----------------------------------------------------------------

// TextField is the shared behavior of the single-line and multi-line editors.
// The editing buffer is PRIVATE to the field; the form's value is the text it
// produces. That is what makes cursor position survive a refresh that did not
// change the text.
type TextField struct {
	Base
	editor       *input.Editor
	multiline    bool
	rowOffset    int
	columnOffset int
	width        int
	height       int
}

func newTextField(base Base, kind string, multiline bool) TextField {
	text, _ := base.Value.(string)
	editor := input.New(text, input.Options{Multiline: multiline, NoKillToEnd: true})
	base.Value = editor.Text()
	if base.BaselineSet {
		baseline, _ := base.Baseline.(string)
		base.Baseline = input.New(baseline, input.Options{Multiline: multiline, NoKillToEnd: true}).Text()
	}
	meta := map[string]any{"kind": kind}
	for key, value := range base.Metadata() {
		meta[key] = value
	}
	base.Meta = meta
	return TextField{Base: base, editor: editor, multiline: multiline, width: 1, height: 1}
}

// Text is the current buffer.
func (t *TextField) Text() string { return t.editor.Text() }

// Cursor is the grapheme offset of the caret.
func (t *TextField) Cursor() int { return t.editor.Cursor() }

// HandleRawKey applies one raw key to the buffer.
func (t *TextField) HandleRawKey(key string) input.Result { return t.editor.HandleKey(key) }

// Paste inserts text at the caret.
func (t *TextField) Paste(text string) input.Result { return t.editor.Insert(text) }

// NormalizeValue runs a value through the same sanitizer the editor applies, so
// a host-supplied value with a tab in it cannot differ from the same value
// typed by hand.
func (t *TextField) NormalizeValue(value any) any {
	text, _ := value.(string)
	return input.New(text, input.Options{Multiline: t.multiline, NoKillToEnd: true}).Text()
}

// SyncValue adopts a value the form changed underneath the buffer. An UNCHANGED
// value deliberately leaves the buffer alone, which is what preserves a
// deliberately placed mid-buffer cursor across a blur and refocus.
func (t *TextField) SyncValue(value any) {
	normalized, _ := t.NormalizeValue(value).(string)
	if t.editor.Text() != normalized {
		t.editor.Replace(normalized)
	}
}

// CursorFor exposes the caret to the renderer.
func (t *TextField) CursorFor(any, Context) *int {
	cursor := t.editor.Cursor()
	return &cursor
}

func (t *TextField) editResult(status input.Result) *Result {
	switch status {
	case input.Changed:
		return ChangedResult(t.editor.Text())
	case input.Handled:
		return HandledResult(t.editor.Text())
	}
	return nil
}

// HandleEvent is the shared text behavior: paste inserts, everything else goes
// to the editor's own key table.
func (t *TextField) HandleEvent(event Event, _ any, _ Context) *Result {
	switch event.Type {
	case EventPaste:
		return t.editResult(t.Paste(event.Text))
	case EventInput:
		return t.editResult(t.HandleRawKey(DecodedKey(event)))
	case EventKey:
		return t.editResult(t.HandleRawKey(DecodedKey(event)))
	}
	return nil
}

// Input is a single-line text field.
type Input struct{ TextField }

// NewInput builds one.
func NewInput(base Base) *Input {
	return &Input{TextField: newTextField(base, "input", false)}
}

// Render lays the value out in a window of width cells, scrolling horizontally
// so the caret stays visible.
func (i *Input) Render(width int) View {
	if width < 1 {
		width = 1
	}
	cursorCell := cellsBefore(i.Text(), i.Cursor())
	offset := i.columnOffset
	if cursorCell < offset {
		offset = cursorCell
	}
	if cursorCell >= offset+width {
		offset = cursorCell - width + 1
	}
	if offset < 0 {
		offset = 0
	}
	maxOffset := charwidth.String(i.Text()) - width + 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	i.columnOffset, i.width, i.height = offset, width, 1
	return View{
		Lines:     []string{input.CellSlice(i.Text(), offset, width)},
		CursorRow: 0, CursorColumn: cursorCell - offset,
		VirtualCursorRow: 0, VirtualCursorColumn: cursorCell,
		RowOffset: 0, ColumnOffset: offset, Width: width, Height: 1,
	}
}

// TextArea is a multi-line text field with wrapped vertical motion.
type TextArea struct{ TextField }

// NewTextArea builds one.
func NewTextArea(base Base) *TextArea {
	return &TextArea{TextField: newTextField(base, "text_area", true)}
}

// FocusGained shows a tall value from the TOP. The cursor after a sync sits at
// end-of-buffer, which would scroll a long note so only its last lines are
// visible — the opposite of what someone opening a note wants to see.
func (a *TextArea) FocusGained(any, Context) {
	if a.Cursor() == len(input.Graphemes(a.Text())) {
		a.editor.SetCursor(0)
	}
}

// HandleEvent adds vertical motion on top of the shared text behavior.
func (a *TextArea) HandleEvent(event Event, value any, context Context) *Result {
	if event.Type == EventKey {
		switch DecodedKey(event) {
		case "\x1b[A":
			return a.editResult(a.moveVertical(-1))
		case "\x1b[B":
			return a.editResult(a.moveVertical(1))
		}
	}
	if inherited := a.TextField.HandleEvent(event, value, context); inherited != nil {
		return inherited
	}
	switch event.Type {
	case EventCommit:
		// Return inserts a newline rather than committing: in a note, Enter is
		// text. The editor is left with ctrl-s / tab / ctrl-o to save.
		return a.editResult(a.HandleRawKey("\r"))
	case EventNext:
		if DecodedKey(event) == "\x1b[B" {
			return a.editResult(a.moveVertical(1))
		}
	case EventPrevious:
		if DecodedKey(event) == "\x1b[A" {
			return a.editResult(a.moveVertical(-1))
		}
	}
	return nil
}

// Render wraps the value to width and scrolls vertically to keep the caret in
// the window.
func (a *TextArea) Render(width, height int) View {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	lines, positions := wrappedLayout(a.Text(), width)
	virtual := positions[clampInt(a.Cursor(), 0, len(positions)-1)]
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := minInt(a.rowOffset, maxOffset)
	if virtual[0] < offset {
		offset = virtual[0]
	}
	if virtual[0] >= offset+height {
		offset = virtual[0] - height + 1
	}
	offset = clampInt(offset, 0, maxOffset)
	a.rowOffset, a.columnOffset, a.width, a.height = offset, 0, width, height

	visible := []string{}
	for index := offset; index < len(lines) && len(visible) < height; index++ {
		visible = append(visible, lines[index])
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	return View{
		Lines: visible, CursorRow: virtual[0] - offset, CursorColumn: virtual[1],
		VirtualCursorRow: virtual[0], VirtualCursorColumn: virtual[1],
		RowOffset: offset, ColumnOffset: 0, Width: width, Height: height,
	}
}

func (a *TextArea) moveVertical(offset int) input.Result {
	width := a.width
	if width < 1 {
		width = 1
	}
	_, positions := wrappedLayout(a.Text(), width)
	current := positions[clampInt(a.Cursor(), 0, len(positions)-1)]
	targetRow := current[0] + offset
	best, found := 0, false
	for index, position := range positions {
		if position[0] != targetRow {
			continue
		}
		if !found || absInt(position[1]-current[1]) < absInt(positions[best][1]-current[1]) {
			best, found = index, true
		}
	}
	if !found {
		return input.Handled
	}
	a.editor.SetCursor(best)
	return input.Handled
}

// wrappedLayout folds text to width cells and records, for every grapheme
// offset INCLUDING the end, which row and column the caret would sit at.
//
// The trailing-position rule is the fiddly part and it is deliberate: a line
// that exactly fills the width gets a new empty row, so a caret at the end of a
// full line renders at the start of the next row rather than one cell past the
// edge of the terminal.
func wrappedLayout(text string, width int) ([]string, [][2]int) {
	lines := []string{""}
	positions := [][2]int{}
	row, column := 0, 0
	for _, grapheme := range input.Graphemes(text) {
		if grapheme == "\n" {
			if column == width {
				lines = append(lines, "")
				row++
				column = 0
				positions = append(positions, [2]int{row, column})
			} else {
				positions = append(positions, [2]int{row, column})
				lines = append(lines, "")
				row++
				column = 0
			}
			continue
		}
		cells := charwidth.Cluster(grapheme)
		if column == width || (column > 0 && column+cells > width) {
			lines = append(lines, "")
			row++
			column = 0
		}
		positions = append(positions, [2]int{row, column})
		if cells > width {
			// A grapheme wider than the whole field cannot be shown; blanking
			// its cells keeps every column after it aligned.
			lines[row] += strings.Repeat(" ", width)
			column += width
		} else {
			lines[row] += grapheme
			column += cells
		}
	}
	if column == width {
		lines = append(lines, "")
		row++
		column = 0
	}
	positions = append(positions, [2]int{row, column})
	return lines, positions
}

func cellsBefore(text string, cursor int) int {
	total := 0
	for index, grapheme := range input.Graphemes(text) {
		if index >= cursor {
			break
		}
		total += charwidth.Cluster(grapheme)
	}
	return total
}

// -- choice fields ----------------------------------------------------------------

// ChoiceField is the shared searchable-option behavior.
type ChoiceField struct {
	Base
	optionSource   func(Context) []Option
	searchable     bool
	query          *input.Editor
	highlightIndex int
	open           bool
	// selectedValues and availability are the two hooks the single- and
	// multiple-selection subtypes override.
	selectedValues func(value any) []any
	availability   func(value any, context Context) []string
}

func newChoiceField(base Base, kind string, options func(Context) []Option, searchable bool) ChoiceField {
	meta := map[string]any{"kind": kind}
	for key, value := range base.Metadata() {
		meta[key] = value
	}
	base.Meta = meta
	return ChoiceField{
		Base:         base,
		optionSource: options,
		searchable:   searchable,
		query:        input.New("", input.Options{NoKillToEnd: true}),
	}
}

// Query is the live search text.
func (c *ChoiceField) Query() string { return c.query.Text() }

// Open reports whether the option list is showing.
func (c *ChoiceField) Open() bool { return c.open }

// HighlightIndex is the highlighted option's position in the filtered list.
func (c *ChoiceField) HighlightIndex() int { return c.highlightIndex }

// Options is the full option set for the current context.
func (c *ChoiceField) Options(context Context) []Option {
	if c.optionSource == nil {
		return nil
	}
	return c.optionSource(context)
}

// FilteredOptions is the option set narrowed by the live query.
func (c *ChoiceField) FilteredOptions(context Context) []Option {
	choices := c.Options(context)
	if query := c.Query(); query != "" {
		needle := strings.ToLower(query)
		kept := []Option{}
		for _, option := range choices {
			if strings.Contains(strings.ToLower(option.Label), needle) ||
				strings.Contains(strings.ToLower(optionText(option.Value)), needle) {
				kept = append(kept, option)
			}
		}
		choices = kept
	}
	c.clampHighlight(len(choices))
	return choices
}

// HighlightedOption is the option the cursor is on, or nil.
func (c *ChoiceField) HighlightedOption(context Context) *Option {
	choices := c.FilteredOptions(context)
	if len(choices) == 0 {
		return nil
	}
	return &choices[c.highlightIndex]
}

// ValidationErrors adds "this selection no longer exists" to the base rules,
// which is what catches a context or tag deleted while the form was open.
func (c *ChoiceField) ValidationErrors(value any, context Context) []string {
	errors := c.Base.ValidationErrors(value, context)
	return append(errors, c.availability(value, context)...)
}

// MetadataFor publishes the option list, selection and open state the renderer
// paints from.
func (c *ChoiceField) MetadataFor(value any, context Context) map[string]any {
	choices := c.FilteredOptions(context)
	selected := c.selectedValues(value)
	options := make([]map[string]any, 0, len(choices))
	for index, option := range choices {
		options = append(options, map[string]any{
			"value": option.Value, "label": option.Label, "metadata": option.Metadata,
			"selected": containsValue(selected, option.Value), "highlighted": index == c.highlightIndex,
		})
	}
	out := map[string]any{}
	for key, entry := range c.Metadata() {
		out[key] = entry
	}
	out["open"] = c.open
	out["query"] = c.Query()
	out["searchable"] = c.searchable
	out["invalid_selection"] = len(c.availability(value, context)) > 0
	out["options"] = options
	return out
}

// CursorFor exposes the search caret only when the field is searchable.
func (c *ChoiceField) CursorFor(any, Context) *int {
	if !c.searchable {
		return nil
	}
	cursor := c.query.Cursor()
	return &cursor
}

func (c *ChoiceField) moveHighlight(offset int, context Context) *Result {
	choices := c.FilteredOptions(context)
	c.open = true
	if len(choices) == 0 {
		c.highlightIndex = 0
	} else {
		c.highlightIndex = ((c.highlightIndex+offset)%len(choices) + len(choices)) % len(choices)
	}
	return HandledResult(nil)
}

func (c *ChoiceField) updateQuery(event Event) *Result {
	if !c.searchable {
		return nil
	}
	var status input.Result
	switch event.Type {
	case EventPaste:
		status = c.query.Insert(event.Text)
	case EventInput, EventKey:
		status = c.query.HandleKey(DecodedKey(event))
	}
	if status != input.Changed {
		return nil
	}
	c.open = true
	c.highlightIndex = 0
	return HandledResult(nil)
}

func (c *ChoiceField) clearQuery() {
	c.query.Clear()
	c.highlightIndex = 0
}

func (c *ChoiceField) clampHighlight(count int) {
	maximum := count - 1
	if maximum < 0 {
		maximum = 0
	}
	c.highlightIndex = clampInt(c.highlightIndex, 0, maximum)
}

func arrowOffset(event Event) (int, bool) {
	switch DecodedKey(event) {
	case "\x1b[A":
		return -1, true
	case "\x1b[B":
		return 1, true
	}
	return 0, false
}

func isReturn(event Event) bool { return Command(event, EventCommit, "\r", "\n") }
func isCancel(event Event) bool { return Command(event, EventCancel, "\x1b") }

// Select is a single-choice field.
type Select struct{ ChoiceField }

// NewSelect builds one.
func NewSelect(base Base, options func(Context) []Option, searchable bool) *Select {
	field := &Select{ChoiceField: newChoiceField(base, "select", options, searchable)}
	field.selectedValues = func(value any) []any { return []any{value} }
	field.availability = field.availabilityErrors
	field.Value = field.NormalizeValue(field.Value)
	if field.BaselineSet {
		field.Baseline = field.NormalizeValue(field.Baseline)
	}
	return field
}

func (s *Select) availabilityErrors(value any, context Context) []string {
	for _, option := range s.Options(context) {
		if equalValues(option.Value, value) {
			return nil
		}
	}
	return []string{"selection is no longer available"}
}

// HandleEvent is the select interaction: arrows highlight, Return opens then
// picks, Escape closes without changing anything.
func (s *Select) HandleEvent(event Event, value any, context Context) *Result {
	if offset, arrow := arrowOffset(event); arrow {
		return s.moveHighlight(offset, context)
	}
	if isCancel(event) && s.open {
		s.open = false
		s.clearQuery()
		return HandledResult(value)
	}
	if isReturn(event) {
		if !s.open {
			s.open = true
			s.highlightIndex = 0
			for index, option := range s.FilteredOptions(context) {
				if equalValues(option.Value, value) {
					s.highlightIndex = index
					break
				}
			}
			return HandledResult(value)
		}
		chosen := s.HighlightedOption(context)
		s.open = false
		s.clearQuery()
		if chosen == nil {
			return HandledResult(value)
		}
		if equalValues(chosen.Value, value) {
			return HandledResult(value)
		}
		return ChangedResult(chosen.Value)
	}
	return s.updateQuery(event)
}

// MultiSelect is a multiple-choice field whose value is an ordered token list.
type MultiSelect struct {
	ChoiceField
	// Creatable lets a typed token that matches nothing become a new value —
	// which is how a brand-new @context is added without leaving the editor.
	Creatable       bool
	tokenNormalizer func(string) string
}

// NewMultiSelect builds one.
func NewMultiSelect(base Base, options func(Context) []Option, searchable, creatable bool,
	normalizer func(string) string) *MultiSelect {

	field := &MultiSelect{
		ChoiceField:     newChoiceField(base, "multi_select", options, searchable),
		Creatable:       creatable,
		tokenNormalizer: normalizer,
	}
	field.selectedValues = func(value any) []any {
		out := []any{}
		for _, token := range field.tokens(value) {
			out = append(out, token)
		}
		return out
	}
	field.availability = field.availabilityErrors
	field.Value = field.NormalizeValue(field.Value)
	if field.BaselineSet {
		field.Baseline = field.NormalizeValue(field.Baseline)
	}
	return field
}

// NormalizeValue trims, normalizes and de-duplicates the token list. The order
// of what survives is the order it was given in, so a user's arrangement of
// tags is not silently re-sorted.
func (m *MultiSelect) NormalizeValue(value any) any {
	out := []string{}
	for _, token := range toStringSlice(value) {
		normalized := token
		if m.tokenNormalizer != nil {
			normalized = m.tokenNormalizer(token)
		}
		if normalized == "" {
			continue
		}
		if !containsString(out, normalized) {
			out = append(out, normalized)
		}
	}
	return out
}

func (m *MultiSelect) tokens(value any) []string {
	list, _ := m.NormalizeValue(value).([]string)
	return list
}

func (m *MultiSelect) availabilityErrors(value any, context Context) []string {
	if m.Creatable {
		return nil
	}
	available := []string{}
	for _, option := range m.Options(context) {
		available = append(available, optionText(option.Value))
	}
	missing := []string{}
	for _, token := range m.tokens(value) {
		if !containsString(available, token) {
			missing = append(missing, token)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"selection is no longer available: " + strings.Join(missing, ", ")}
}

// MetadataFor adds the token list the renderer draws as chips.
func (m *MultiSelect) MetadataFor(value any, context Context) map[string]any {
	out := m.ChoiceField.MetadataFor(value, context)
	out["tokens"] = m.tokens(value)
	out["creatable"] = m.Creatable
	return out
}

// HandleEvent is the multi-select interaction. Backspace on an empty query pops
// the last token, which is the one gesture that makes a token list feel like
// text rather than a form control.
func (m *MultiSelect) HandleEvent(event Event, value any, context Context) *Result {
	current := m.tokens(value)
	if offset, arrow := arrowOffset(event); arrow {
		return m.moveHighlight(offset, context)
	}
	if isCancel(event) && m.open {
		m.open = false
		m.clearQuery()
		return HandledResult(current)
	}
	if m.isBackspace(event) && m.Query() == "" && len(current) > 0 {
		return ChangedResult(append([]string{}, current[:len(current)-1]...))
	}
	if isReturn(event) {
		token := ""
		if query := m.Query(); query != "" && m.Creatable {
			token = query
			for _, option := range m.FilteredOptions(context) {
				if strings.EqualFold(option.Label, query) ||
					strings.EqualFold(optionText(option.Value), query) {
					token = optionText(option.Value)
					break
				}
			}
		} else if chosen := m.HighlightedOption(context); chosen != nil {
			token = optionText(chosen.Value)
		}
		if token != "" && m.tokenNormalizer != nil {
			token = m.tokenNormalizer(token)
		}
		m.open = false
		m.clearQuery()
		if token == "" || containsString(current, token) {
			return HandledResult(current)
		}
		return ChangedResult(append(append([]string{}, current...), token))
	}
	return m.updateQuery(event)
}

func (m *MultiSelect) isBackspace(event Event) bool {
	if event.Type != EventKey && event.Type != EventInput {
		return false
	}
	key := DecodedKey(event)
	return key == "\x7f" || key == "\b"
}

// Confirm is a yes/no field.
type Confirm struct {
	Base
	YesLabel    string
	NoLabel     string
	Consequence string
}

// NewConfirm builds one.
func NewConfirm(base Base, yesLabel, noLabel string) *Confirm {
	meta := map[string]any{"kind": "confirm"}
	for key, value := range base.Metadata() {
		meta[key] = value
	}
	base.Meta = meta
	field := &Confirm{Base: base, YesLabel: yesLabel, NoLabel: noLabel}
	field.Value = field.NormalizeValue(field.Value)
	if field.BaselineSet {
		field.Baseline = field.NormalizeValue(field.Baseline)
	}
	return field
}

// NormalizeValue coerces anything to a bool.
func (c *Confirm) NormalizeValue(value any) any {
	boolean, _ := value.(bool)
	return boolean
}

// HandleEvent accepts y/n, left/right, space and Return.
func (c *Confirm) HandleEvent(event Event, value any, _ Context) *Result {
	current, _ := value.(bool)
	var next bool
	var decided bool
	if Command(event, EventCommit, "\r", "\n") {
		next, decided = !current, true
	} else {
		switch DecodedKey(event) {
		case "y", "Y", "\x1b[C", "\x1b[B":
			next, decided = true, true
		case "n", "N", "\x1b[D", "\x1b[A":
			next, decided = false, true
		case " ":
			next, decided = !current, true
		}
	}
	if !decided {
		return nil
	}
	if next == current {
		return HandledResult(next)
	}
	return ChangedResult(next)
}

// MetadataFor publishes the two labels and which one is selected.
func (c *Confirm) MetadataFor(value any, _ Context) map[string]any {
	current, _ := value.(bool)
	out := map[string]any{}
	for key, entry := range c.Metadata() {
		out[key] = entry
	}
	out["options"] = []map[string]any{
		{"value": false, "label": c.NoLabel, "selected": !current},
		{"value": true, "label": c.YesLabel, "selected": current},
	}
	out["consequence"] = c.Consequence
	return out
}

// -- the date field ------------------------------------------------------------------

// DateHooks are the four things a date field's HOST owns: how text becomes a
// value, how a value becomes text, and how a value and a calendar date convert
// into each other.
//
// They are a struct of functions rather than subclass overrides because the
// task editor's date value is not a Date — it is a whole temporal value with a
// wall time and a zone — and the calendar picker still has to be able to move
// it a day at a time without losing the rest.
type DateHooks struct {
	// Parse turns typed text into a value, or reports why it cannot.
	Parse func(text string, today temporal.Date) (any, error)
	// Format renders a value as the text the editor holds.
	Format func(value any) string
	// Parsed reports whether a value is a real parsed value rather than
	// leftover unparseable text.
	Parsed func(value any) bool
	// DateOf extracts the calendar date a value sits on.
	DateOf func(value any) (temporal.Date, bool)
	// WithDate rebuilds a value on a different calendar date, keeping
	// everything else the value carries.
	WithDate func(date temporal.Date, current any) any
}

// DateInput is a text field that also opens a calendar picker.
type DateInput struct {
	Base
	hooks        DateHooks
	today        func() temporal.Date
	suggestions  []string
	editor       *input.Editor
	pickerOpen   bool
	anchor       temporal.Date
	hasAnchor    bool
	columnOffset int
	parseError   string
	// exposeParseErrors surfaces the parser's own message instead of a generic
	// "is not a valid date". The parser's message names the fix; the generic
	// one teaches nothing.
	exposeParseErrors bool
}

// NewDateInput builds one.
func NewDateInput(base Base, hooks DateHooks, today func() temporal.Date, suggestions []string,
	exposeParseErrors bool) *DateInput {

	meta := map[string]any{"kind": "date_input"}
	for key, value := range base.Metadata() {
		meta[key] = value
	}
	base.Meta = meta
	field := &DateInput{
		Base: base, hooks: hooks, today: today, suggestions: suggestions,
		exposeParseErrors: exposeParseErrors,
	}
	normalized := field.NormalizeValue(base.Value)
	text := ""
	if hooks.Parsed(normalized) {
		text = hooks.Format(normalized)
	} else if raw, isText := normalized.(string); isText {
		text = raw
	}
	field.editor = input.New(text, input.Options{NoKillToEnd: true})
	field.Value = normalized
	if field.BaselineSet {
		field.Baseline = field.NormalizeValue(field.Baseline)
	}
	return field
}

// Text is the buffer.
func (d *DateInput) Text() string { return d.editor.Text() }

// Cursor is the caret offset.
func (d *DateInput) Cursor() int { return d.editor.Cursor() }

// PickerOpen reports whether the calendar is showing.
func (d *DateInput) PickerOpen() bool { return d.pickerOpen }

// PickerDate is the calendar's highlighted day.
func (d *DateInput) PickerDate() (temporal.Date, bool) { return d.anchor, d.hasAnchor }

// NormalizeValue turns text into a parsed value where it can, and leaves
// unparseable text as text so the user can see and fix what they typed.
func (d *DateInput) NormalizeValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		if parsed := d.parseText(typed); parsed != nil {
			return parsed
		}
		return typed
	}
	if d.hooks.Parsed(value) {
		return value
	}
	return value
}

// SyncValue adopts a form value into the buffer.
func (d *DateInput) SyncValue(value any) {
	normalized := d.NormalizeValue(value)
	if equalValues(d.NormalizeValue(d.Text()), normalized) {
		return
	}
	if d.hooks.Parsed(normalized) {
		d.editor.Replace(d.hooks.Format(normalized))
		return
	}
	text, _ := normalized.(string)
	d.editor.Replace(text)
}

// HandleEvent types into the buffer, or drives the calendar when it is open.
func (d *DateInput) HandleEvent(event Event, value any, context Context) *Result {
	if d.pickerOpen {
		return d.handlePicker(event, value)
	}
	if Command(event, EventCommit, "\r", "\n") {
		d.pickerOpen = true
		d.anchor, d.hasAnchor = d.anchorFor(value, context)
		return HandledResult(value)
	}
	var status input.Result
	switch event.Type {
	case EventPaste:
		status = d.editor.Insert(event.Text)
	case EventInput, EventKey:
		status = d.editor.HandleKey(DecodedKey(event))
	}
	if status != input.Changed {
		return nil
	}
	return ChangedResult(d.NormalizeValue(d.Text()))
}

func (d *DateInput) handlePicker(event Event, value any) *Result {
	if Command(event, EventCancel, "\x1b") {
		d.pickerOpen = false
		return HandledResult(value)
	}
	if Command(event, EventCommit, "\r", "\n") {
		selected := d.hooks.WithDate(d.anchor, value)
		d.editor.Replace(d.hooks.Format(selected))
		d.pickerOpen = false
		if equalValues(selected, value) {
			return HandledResult(selected)
		}
		return ChangedResult(selected)
	}
	switch DecodedKey(event) {
	case "\x1b[D":
		d.anchor = d.anchor.AddDays(-1)
	case "\x1b[C":
		d.anchor = d.anchor.AddDays(1)
	case "\x1b[A":
		d.anchor = d.anchor.AddDays(-7)
	case "\x1b[B":
		d.anchor = d.anchor.AddDays(7)
	case "\x1b[5~":
		d.anchor = shiftMonth(d.anchor, -1)
	case "\x1b[6~":
		d.anchor = shiftMonth(d.anchor, 1)
	case "t", "T":
		d.anchor = d.today()
	default:
		return nil
	}
	d.hasAnchor = true
	return HandledResult(value)
}

// ValidationErrors adds "that is not a date" for leftover unparseable text.
func (d *DateInput) ValidationErrors(value any, context Context) []string {
	errors := d.Base.ValidationErrors(value, context)
	if value != nil && !d.hooks.Parsed(value) {
		message := d.parseError
		if message == "" {
			message = "is not a valid date"
		}
		errors = append(errors, message)
	}
	return errors
}

// CursorFor hides the text caret while the calendar owns the keyboard.
func (d *DateInput) CursorFor(any, Context) *int {
	if d.pickerOpen {
		return nil
	}
	cursor := d.editor.Cursor()
	return &cursor
}

// MetadataFor publishes the buffer, the preview, the suggestions and — when it
// is open — the whole calendar the renderer paints.
func (d *DateInput) MetadataFor(value any, _ Context) map[string]any {
	out := map[string]any{}
	for key, entry := range d.Metadata() {
		out[key] = entry
	}
	preview := ""
	if d.hooks.Parsed(value) {
		preview = d.hooks.Format(value)
	} else if parsed := d.parseText(d.Text()); parsed != nil {
		preview = d.hooks.Format(parsed)
	}
	out["text"] = d.Text()
	out["preview"] = preview
	out["picker_open"] = d.pickerOpen
	out["suggestions"] = append([]string{}, d.suggestions...)
	if d.pickerOpen && d.hasAnchor {
		out["picker"] = CalendarFor(d.anchor)
	} else {
		out["picker"] = nil
	}
	return out
}

// Calendar is a month grid for the picker.
type Calendar struct {
	Month         temporal.Date
	Selected      temporal.Date
	WeekdayLabels []string
	Weeks         [][]temporal.Date
}

// CalendarFor builds the six-week grid around a date, Monday first.
func CalendarFor(date temporal.Date) Calendar {
	first, _ := temporal.NewDate(date.Year, date.Month, 1)
	// Ruby's `first - (first.cwday - 1)`: cwday is 1 for Monday, so this walks
	// back to the Monday of the week the 1st falls in.
	weekday := int(first.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := first.AddDays(-(weekday - 1))
	weeks := make([][]temporal.Date, 0, 6)
	for week := 0; week < 6; week++ {
		days := make([]temporal.Date, 0, 7)
		for day := 0; day < 7; day++ {
			days = append(days, start.AddDays(week*7+day))
		}
		weeks = append(weeks, days)
	}
	return Calendar{
		Month: first, Selected: date,
		WeekdayLabels: []string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"},
		Weeks:         weeks,
	}
}

func (d *DateInput) parseText(raw string) any {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	d.parseError = ""
	value, err := d.hooks.Parse(text, d.today())
	if err != nil {
		if d.exposeParseErrors {
			d.parseError = err.Error()
		}
		return nil
	}
	if !d.hooks.Parsed(value) {
		return nil
	}
	return value
}

func (d *DateInput) anchorFor(value any, _ Context) (temporal.Date, bool) {
	if date, ok := d.hooks.DateOf(value); ok {
		return date, true
	}
	if parsed := d.parseText(d.Text()); parsed != nil {
		if date, ok := d.hooks.DateOf(parsed); ok {
			return date, true
		}
	}
	return d.today(), true
}

// shiftMonth moves a date by whole months, clamping the day into the target
// month — so page-down from the 31st lands on the 30th rather than rolling into
// the following month.
func shiftMonth(date temporal.Date, offset int) temporal.Date {
	index := date.Year*12 + int(date.Month) - 1 + offset
	year := index / 12
	month := index % 12
	if month < 0 {
		month += 12
		year--
	}
	last := temporal.DaysIn(year, time.Month(month+1))
	day := date.Day
	if day > last {
		day = last
	}
	shifted, _ := temporal.NewDate(year, time.Month(month+1), day)
	return shifted
}

// -- shared helpers -------------------------------------------------------------------

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []any:
		out := []string{}
		for _, entry := range typed {
			if text, isText := entry.(string); isText {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsValue(values []any, wanted any) bool {
	for _, value := range values {
		if equalValues(value, wanted) {
			return true
		}
	}
	return false
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// Graphemes and ClusterWidth re-export the two measurements the fields make, so
// a caller measuring a rendered field line uses the SAME implementation the
// field laid it out with.
func Graphemes(text string) []string { return input.Graphemes(text) }

// ClusterWidth is one grapheme cluster's width in terminal cells.
func ClusterWidth(cluster string) int { return charwidth.Cluster(cluster) }

// SetText rewrites the buffer from a host that owns a richer control over the
// same value — the structured temporal picker writes the canonical spelling
// back after every arrow step, so the text and the control never disagree.
func (d *DateInput) SetText(text string) { d.editor.Replace(text) }
