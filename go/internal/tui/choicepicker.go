package tui

import (
	"fmt"
	"sort"
	"strings"

	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/term/input"
)

// PickerOption is one choice. A command option (Kind == PickerCommand) is not a
// selection at all — it runs a mutation over the staged set, which is how
// "Clear all contexts" lives in the same list as the contexts it clears.
type PickerOption struct {
	ID         string
	Label      string
	SearchText []string
	Kind       PickerOptionKind
	// Metadata is the caller's own payload. The action palette hangs the whole
	// shortcut entry here so it can execute what the user picked without
	// re-deriving it from an id.
	Metadata any
}

// PickerOptionKind separates a selectable choice from a command.
type PickerOptionKind string

// The two option kinds.
const (
	PickerChoice  PickerOptionKind = "choice"
	PickerCommand PickerOptionKind = "command"
)

// PickerResultKind is what one key or click did.
type PickerResultKind string

// The picker outcomes. Accepted carries the ids the caller should apply.
const (
	PickerHandled   PickerResultKind = "handled"
	PickerChanged   PickerResultKind = "changed"
	PickerCancelled PickerResultKind = "cancelled"
	PickerAccepted  PickerResultKind = "accepted"
)

// PickerResult is one outcome.
type PickerResult struct {
	Kind PickerResultKind
	IDs  []string
}

// SelectionMode is single- or multiple-choice.
type SelectionMode string

// The two selection modes.
const (
	SelectSingle   SelectionMode = "single"
	SelectMultiple SelectionMode = "multiple"
)

// ChoicePicker is the stable, searchable overlay both palettes are built from.
//
// "Stable" is the operative word and it is why this is one type rather than two
// ad-hoc lists: the box width comes from the FULL option set, the viewport
// follows the cursor rather than the match count, and the layout the mouse hit
// test reads is the layout the last paint produced. A palette whose rows moved
// between the paint and the click would run the wrong action.
type ChoicePicker struct {
	title       string
	allOptions  []PickerOption
	mode        SelectionMode
	acceptLabel string
	emptyLabel  string
	maxVisible  int
	// toggleCommand is what a command option does to the staged selection.
	toggleCommand func(option PickerOption, staged []string) ([]string, bool)
	// searchNormalizer is applied to BOTH the query and the searchable text, so
	// the context palette can match "home" against "@home" without teaching the
	// ranking about the sigil.
	searchNormalizer func(string) string
	selectedStyle    string

	query          *input.Editor
	staged         []string
	initial        []string
	cursorIndex    int
	viewportStart  int
	failure        string
	naturalWidth   int
	resultCapacity int

	hitLayout pickerHitLayout
}

type pickerHitLayout struct {
	kind          string
	height        int
	optionRow     int
	optionsStart  int
	resultSlots   int
	viewportStart int
	matchCount    int
}

// ChoicePickerOptions builds a picker.
type ChoicePickerOptions struct {
	Title            string
	Options          []PickerOption
	Selection        []string
	Mode             SelectionMode
	AcceptLabel      string
	EmptyLabel       string
	MaxVisible       int
	PreferredID      string
	ToggleCommand    func(option PickerOption, staged []string) ([]string, bool)
	SearchNormalizer func(string) string
	SelectedStyle    string
}

// NewChoicePicker builds one.
func NewChoicePicker(options ChoicePickerOptions) *ChoicePicker {
	if options.Mode == "" {
		options.Mode = SelectSingle
	}
	if options.AcceptLabel == "" {
		options.AcceptLabel = "choose"
		if options.Mode == SelectMultiple {
			options.AcceptLabel = "apply"
		}
	}
	if options.EmptyLabel == "" {
		options.EmptyLabel = "no matching choices"
	}
	if options.MaxVisible < 1 {
		options.MaxVisible = 8
	}
	if options.SearchNormalizer == nil {
		options.SearchNormalizer = func(value string) string { return value }
	}
	picker := &ChoicePicker{
		title:            options.Title,
		mode:             options.Mode,
		acceptLabel:      options.AcceptLabel,
		emptyLabel:       options.EmptyLabel,
		maxVisible:       options.MaxVisible,
		toggleCommand:    options.ToggleCommand,
		searchNormalizer: options.SearchNormalizer,
		selectedStyle:    options.SelectedStyle,
		query:            input.New("", input.Options{NoKillToEnd: true}),
	}
	picker.allOptions = normalizePickerOptions(options.Options)
	picker.staged = picker.normalizeSelection(options.Selection)
	picker.initial = append([]string{}, picker.staged...)
	picker.cursorIndex = picker.initialCursor(options.PreferredID, picker.Results())
	return picker
}

// normalizePickerOptions drops duplicate ids, keeping the first. A palette with
// two rows carrying one id would execute an ambiguous action.
func normalizePickerOptions(options []PickerOption) []PickerOption {
	seen := map[string]bool{}
	out := []PickerOption{}
	for _, option := range options {
		if option.ID == "" || seen[option.ID] {
			continue
		}
		seen[option.ID] = true
		if option.Kind == "" {
			option.Kind = PickerChoice
		}
		if len(option.SearchText) == 0 {
			option.SearchText = []string{option.Label}
		}
		out = append(out, option)
	}
	return out
}

// Title, Input, CursorIndex, ViewportStart, Staged and Error are the accessors.
func (p *ChoicePicker) Title() string           { return p.title }
func (p *ChoicePicker) Input() string           { return p.query.Text() }
func (p *ChoicePicker) InputCursor() int        { return p.query.Cursor() }
func (p *ChoicePicker) CursorIndex() int        { return p.cursorIndex }
func (p *ChoicePicker) ViewportStart() int      { return p.viewportStart }
func (p *ChoicePicker) Error() string           { return p.failure }
func (p *ChoicePicker) Options() []PickerOption { return p.allOptions }

// Staged is the selection as it stands, sorted for a stable answer.
func (p *ChoicePicker) Staged() []string {
	out := append([]string{}, p.staged...)
	sort.Strings(out)
	return out
}

// SelectionChanged reports whether the staged set differs from what the picker
// opened with. The context palette reads it to decide whether Return means
// "apply what I toggled" or "apply just the thing under the cursor".
func (p *ChoicePicker) SelectionChanged() bool {
	return !equalStringSets(p.staged, p.initial)
}

// Results is the option set narrowed and RANKED by the live query.
//
// The ranking is what makes typing three letters land on the thing you meant:
// exact match, then prefix, then a word-boundary prefix, then a substring —
// ties broken by declaration order, so the answer is the same on every run.
func (p *ChoicePicker) Results() []PickerOption {
	query := p.normalizedQuery()
	if query == "" {
		return p.allOptions
	}
	type ranked struct {
		rank, index int
		option      PickerOption
	}
	matches := []ranked{}
	for index, option := range p.allOptions {
		if rank, ok := p.optionRank(option, query); ok {
			matches = append(matches, ranked{rank: rank, index: index, option: option})
		}
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].rank != matches[right].rank {
			return matches[left].rank < matches[right].rank
		}
		return matches[left].index < matches[right].index
	})
	out := make([]PickerOption, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.option)
	}
	return out
}

// Current is the option under the cursor, or nil.
func (p *ChoicePicker) Current() *PickerOption {
	results := p.Results()
	if p.cursorIndex < 0 || p.cursorIndex >= len(results) {
		return nil
	}
	return &results[p.cursorIndex]
}

// Selected reports whether an id is in the staged set.
func (p *ChoicePicker) Selected(id string) bool { return containsString(p.staged, id) }

// HandleKey routes one raw key.
func (p *ChoicePicker) HandleKey(key string) PickerResult {
	switch key {
	case "\x1b":
		return PickerResult{Kind: PickerCancelled}
	case "\r", "\n":
		return p.acceptCurrent()
	case "\x1b[A", "\x10":
		return p.Move(-1)
	case "\x1b[B", "\x0e":
		return p.Move(1)
	case " ":
		if p.mode == SelectMultiple {
			return p.toggleCurrent()
		}
	}
	return p.editInput(key, false)
}

// Paste inserts pasted text into the query.
func (p *ChoicePicker) Paste(text string) PickerResult { return p.editInput(text, true) }

// Fail attaches an error to the picker without closing it, so a failed action
// is reported where the user chose it.
func (p *ChoicePicker) Fail(message string) {
	p.failure = message
	p.naturalWidth = max(p.naturalWidth, p.computeNaturalWidth())
}

// Move steps the cursor and keeps it visible. Same step as ↑↓, for the wheel
// and for programmatic motion.
func (p *ChoicePicker) Move(delta int) PickerResult {
	count := len(p.Results())
	if count == 0 {
		p.cursorIndex = 0
	} else {
		p.cursorIndex = clamp(p.cursorIndex+delta, 0, count-1)
	}
	p.ensureCursorVisible(count, p.capacity())
	return PickerResult{Kind: PickerChanged}
}

// RefreshOptions swaps the option set while keeping the query, the staged
// selection and — where it still exists — the cursor's option.
func (p *ChoicePicker) RefreshOptions(options []PickerOption, selection []string) {
	query := p.query.Text()
	previousCursor := ""
	if current := p.Current(); current != nil {
		previousCursor = current.ID
	}
	p.allOptions = normalizePickerOptions(options)
	p.staged = p.normalizeSelection(selection)
	p.initial = p.normalizeSelection(p.initial)
	p.query.Replace(query)
	matches := p.Results()
	p.cursorIndex = -1
	for index, option := range matches {
		if option.ID == previousCursor {
			p.cursorIndex = index
			break
		}
	}
	if p.cursorIndex < 0 {
		p.cursorIndex = p.initialCursor(previousCursor, matches)
	}
	p.viewportStart = p.clampViewport(p.viewportStart, len(matches), p.capacity())
	p.naturalWidth = max(p.naturalWidth, p.computeNaturalWidth())
	p.resultCapacity = max(p.resultCapacity, p.computeCapacity())
}

// Popup renders the overlay at the given budget and records the layout the hit
// test reads.
//
// The three tiers are deliberate. A palette that simply refused to render in a
// short terminal would take a capability away from a user whose window happened
// to be small; each tier keeps the one thing that tier can still show — the
// current option, the query, and the hint.
func (p *ChoicePicker) Popup(styler Styler, maxWidth, maxHeight int, inlineInput func(string, int) string) []string {
	matches := p.Results()
	p.clampCursor(len(matches))
	p.ensureCursorVisible(len(matches), p.capacity())

	width := max(min(p.width(), maxWidth), 1)
	preferredHeight := p.capacity() + 5
	height := max(min(preferredHeight, maxHeight), 1)
	query := " search: " + inlineInput(p.query.Text(), p.query.Cursor())
	hint := p.statusHint(styler)

	if width < 6 || height < 3 {
		selected := styler.Paint("muted", p.emptyLabel)
		if current := p.Current(); current != nil {
			selected = p.compactLine(*current)
		}
		compact := firstN([]string{selected, query, " " + hint}, height)
		lines := []string{}
		for _, line := range compact {
			lines = append(lines, padTo(styler, styler.Truncate(line, width), width))
		}
		p.hitLayout = pickerHitLayout{kind: "compact", height: len(lines), optionRow: 0}
		return lines
	}

	if height < 6 {
		selected := styler.Paint("muted", "   "+p.emptyLabel)
		if current := p.Current(); current != nil {
			selected = p.optionLine(styler, *current, true)
		}
		inner := firstN([]string{selected, query, " " + hint}, max(height-2, 0))
		lines := p.box(styler, inner, width)
		p.hitLayout = pickerHitLayout{kind: "short", height: len(lines), optionRow: 1}
		return lines
	}

	slots := max(height-5, 0)
	p.ensureCursorVisible(len(matches), slots)
	visible := sliceOptions(matches, p.viewportStart, slots)
	optionLines := []string{}
	for offset, option := range visible {
		optionLines = append(optionLines,
			p.optionLine(styler, option, p.viewportStart+offset == p.cursorIndex))
	}
	if len(matches) == 0 && slots > 0 {
		optionLines = append(optionLines, styler.Paint("muted", "   "+p.emptyLabel))
	}
	for len(optionLines) < slots {
		optionLines = append(optionLines, "")
	}
	inner := append([]string{query, ""}, optionLines...)
	inner = append(inner, " "+hint)
	lines := p.box(styler, inner, width)
	// The box adds a top border, so the query lands on row 1, the blank on 2,
	// and the options start at 3. Hit testing reads exactly these numbers.
	p.hitLayout = pickerHitLayout{
		kind: "full", height: len(lines), optionsStart: 3, resultSlots: slots,
		viewportStart: p.viewportStart, matchCount: len(matches),
	}
	return lines
}

// Hit maps a popup-local row (0 = the top border) to a picker action, returning
// the SAME shapes HandleKey does so a click and a keypress cannot diverge.
func (p *ChoicePicker) Hit(rowOffset int) PickerResult {
	layout := p.hitLayout
	if layout.kind == "" || rowOffset < 0 || rowOffset >= layout.height {
		return PickerResult{Kind: PickerHandled}
	}
	switch layout.kind {
	case "compact", "short":
		if rowOffset != layout.optionRow {
			return PickerResult{Kind: PickerHandled}
		}
		if p.mode == SelectSingle {
			return p.acceptCurrent()
		}
		return p.toggleCurrent()
	case "full":
		slot := rowOffset - layout.optionsStart
		if slot < 0 || slot >= layout.resultSlots || layout.matchCount == 0 {
			return PickerResult{Kind: PickerHandled}
		}
		absolute := layout.viewportStart + slot
		if absolute >= layout.matchCount {
			return PickerResult{Kind: PickerHandled}
		}
		p.cursorIndex = absolute
		p.ensureCursorVisible(layout.matchCount, layout.resultSlots)
		if p.mode == SelectSingle {
			return p.acceptCurrent()
		}
		return p.toggleCurrent()
	}
	return PickerResult{Kind: PickerHandled}
}

// -- internals ------------------------------------------------------------------

func (p *ChoicePicker) box(styler Styler, inner []string, width int) []string {
	innerWidth := width - 4
	lines := []string{}
	title := styler.Truncate(" "+p.title+" ", innerWidth)
	lines = append(lines, "┌"+padBorder(title, width-2)+"┐")
	for _, line := range inner {
		lines = append(lines, "│ "+padTo(styler, styler.Truncate(line, innerWidth), innerWidth)+" │")
	}
	lines = append(lines, "└"+strings.Repeat("─", max(width-2, 0))+"┘")
	return lines
}

// padBorder lays a title into a horizontal rule one column in from the corner,
// which is where Border.box puts it with title_lead: 1.
func padBorder(title string, width int) string {
	plain := []rune(stripStyling(title))
	if len(plain) >= width {
		return strings.Repeat("─", max(width, 0))
	}
	return "─" + title + strings.Repeat("─", max(width-len(plain)-1, 0))
}

func (p *ChoicePicker) editInput(value string, paste bool) PickerResult {
	var result input.Result
	if paste {
		result = p.query.Insert(value)
	} else {
		result = p.query.HandleKey(value)
	}
	if result != input.Changed {
		return PickerResult{Kind: PickerHandled}
	}
	// A new query re-ranks everything, so the cursor goes back to the best
	// match rather than staying on a row that just moved underneath it.
	p.cursorIndex = 0
	p.viewportStart = 0
	p.failure = ""
	return PickerResult{Kind: PickerChanged}
}

func (p *ChoicePicker) acceptCurrent() PickerResult {
	option := p.Current()
	if option == nil {
		return PickerResult{Kind: PickerHandled}
	}
	if option.Kind == PickerCommand {
		p.applyCommand(*option)
		return PickerResult{Kind: PickerAccepted, IDs: p.Staged()}
	}
	if p.mode == SelectSingle {
		return PickerResult{Kind: PickerAccepted, IDs: []string{option.ID}}
	}
	return PickerResult{Kind: PickerAccepted, IDs: p.Staged()}
}

func (p *ChoicePicker) toggleCurrent() PickerResult {
	option := p.Current()
	if option == nil {
		return PickerResult{Kind: PickerHandled}
	}
	switch {
	case option.Kind == PickerCommand:
		p.applyCommand(*option)
	case containsString(p.staged, option.ID):
		p.staged = removeString(p.staged, option.ID)
	default:
		p.staged = append(p.staged, option.ID)
	}
	p.failure = ""
	return PickerResult{Kind: PickerChanged}
}

func (p *ChoicePicker) applyCommand(option PickerOption) {
	if p.toggleCommand == nil {
		return
	}
	if replacement, ok := p.toggleCommand(option, append([]string{}, p.staged...)); ok {
		p.staged = p.normalizeSelection(replacement)
	}
}

// normalizeSelection keeps only ids that name a real CHOICE. A staged id for a
// command option, or for an option that vanished on a refresh, would otherwise
// be applied as a filter nothing matches.
func (p *ChoicePicker) normalizeSelection(selection []string) []string {
	valid := map[string]bool{}
	for _, option := range p.allOptions {
		if option.Kind == PickerChoice {
			valid[option.ID] = true
		}
	}
	out := []string{}
	for _, id := range selection {
		if valid[id] && !containsString(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (p *ChoicePicker) initialCursor(preferred string, matches []PickerOption) int {
	candidates := []string{}
	if preferred != "" {
		candidates = append(candidates, preferred)
	}
	candidates = append(candidates, p.Staged()...)
	for _, id := range candidates {
		for index, option := range matches {
			if option.ID == id {
				return index
			}
		}
	}
	return 0
}

func (p *ChoicePicker) normalizedQuery() string {
	return p.searchNormalizer(strings.ToLower(strings.TrimSpace(p.query.Text())))
}

func (p *ChoicePicker) optionRank(option PickerOption, query string) (int, bool) {
	best, found := 0, false
	for _, raw := range option.SearchText {
		value := p.searchNormalizer(strings.ToLower(raw))
		rank := -1
		switch {
		case value == query:
			rank = 0
		case strings.HasPrefix(value, query):
			rank = 1
		case anyTokenHasPrefix(value, query):
			rank = 2
		case strings.Contains(value, query):
			rank = 3
		}
		if rank >= 0 && (!found || rank < best) {
			best, found = rank, true
		}
	}
	return best, found
}

// anyTokenHasPrefix is Ruby's `value.split(/[^[:alnum:]@_-]+/)`: the word
// boundary keeps `@` and `-` inside a token, so "@home-office" is one word and
// a query of "home" still ranks it above a mid-word substring hit.
func anyTokenHasPrefix(value, query string) bool {
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !(r == '@' || r == '_' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'))
	}) {
		if strings.HasPrefix(token, query) {
			return true
		}
	}
	return false
}

func (p *ChoicePicker) clampCursor(count int) {
	if count == 0 {
		p.cursorIndex = 0
		return
	}
	p.cursorIndex = clamp(p.cursorIndex, 0, count-1)
}

func (p *ChoicePicker) clampViewport(start, count, capacity int) int {
	return clamp(start, 0, max(count-capacity, 0))
}

func (p *ChoicePicker) ensureCursorVisible(count, capacity int) {
	if count == 0 {
		p.viewportStart = 0
		return
	}
	if p.cursorIndex < p.viewportStart {
		p.viewportStart = p.cursorIndex
	}
	if bottom := p.viewportStart + capacity - 1; p.cursorIndex > bottom {
		p.viewportStart = p.cursorIndex - capacity + 1
	}
	p.viewportStart = p.clampViewport(p.viewportStart, count, capacity)
}

func (p *ChoicePicker) capacity() int {
	if p.resultCapacity == 0 {
		p.resultCapacity = p.computeCapacity()
	}
	return p.resultCapacity
}

func (p *ChoicePicker) computeCapacity() int {
	return min(max(len(p.allOptions), 1), p.maxVisible)
}

func (p *ChoicePicker) width() int {
	if p.naturalWidth == 0 {
		p.naturalWidth = p.computeNaturalWidth()
	}
	return p.naturalWidth
}

// computeNaturalWidth sizes the box from the FULL option set and the hint, not
// from the current matches — so narrowing a search never shrinks the box under
// the reader.
func (p *ChoicePicker) computeNaturalWidth() int {
	optionWidth := 0
	for _, option := range p.allOptions {
		if width := len([]rune(stripStyling(option.Label))) + 8; width > optionWidth {
			optionWidth = width
		}
	}
	hintWidth := len([]rune(stripStyling(p.plainHint()))) + 4
	titleWidth := len([]rune(p.title)) + 8
	return max(max(max(optionWidth, hintWidth), titleWidth)+2, 40)
}

func (p *ChoicePicker) optionLine(styler Styler, option PickerOption, cursor bool) string {
	marker := " "
	if cursor {
		marker = styler.Paint("selection", "❯")
	}
	check := " "
	if option.Kind == PickerChoice && p.Selected(option.ID) {
		check = "●"
	}
	label := option.Label
	if option.Kind == PickerChoice && p.Selected(option.ID) && p.selectedStyle != "" {
		label = styler.Paint(p.selectedStyle, label)
	}
	return fmt.Sprintf(" %s %s %s", marker, check, label)
}

func (p *ChoicePicker) compactLine(option PickerOption) string {
	check := ""
	if option.Kind == PickerChoice && p.Selected(option.ID) {
		check = "● "
	}
	return "❯ " + check + option.Label
}

func (p *ChoicePicker) statusHint(styler Styler) string {
	if p.failure != "" {
		return styler.Paint("error", p.failure)
	}
	return styler.Paint("muted", p.plainHint())
}

func (p *ChoicePicker) plainHint() string {
	if p.failure != "" {
		return p.failure
	}
	if p.mode == SelectMultiple {
		return fmt.Sprintf("%d selected · ↑↓ move · space toggle · enter %s · esc cancel",
			len(p.staged), p.acceptLabel)
	}
	return "↑↓ choose · enter " + p.acceptLabel + " · esc cancel"
}

// -- small shared helpers ----------------------------------------------------------

func sliceOptions(options []PickerOption, start, count int) []PickerOption {
	if start < 0 || start >= len(options) || count <= 0 {
		return nil
	}
	return options[start:min(start+count, len(options))]
}

func firstN(values []string, count int) []string {
	if count < 0 {
		count = 0
	}
	return values[:min(count, len(values))]
}

func padTo(styler Styler, text string, width int) string {
	if pad := width - styler.Width(text); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}

// stripStyling removes escape sequences so a width can be measured without a
// styler. It composes the completed ansi primitive rather than re-deriving the
// pattern: two regexes for "what is an SGR sequence" is exactly the kind of
// duplication that ends with a box one column too narrow.
func stripStyling(text string) string { return ansi.Strip(text) }

func removeString(values []string, wanted string) []string {
	out := []string{}
	for _, value := range values {
		if value != wanted {
			out = append(out, value)
		}
	}
	return out
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, value := range left {
		if !containsString(right, value) {
			return false
		}
	}
	return true
}
