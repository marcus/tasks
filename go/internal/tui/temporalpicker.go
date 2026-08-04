package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tasks-go/internal/temporal"
	"tasks-go/internal/timezones"
	"tasks-go/internal/tui/termform"
)

// The structured temporal picker: the five-row control Ruby's
// TaskEditForm::TemporalInput opens on Return.
//
// A date in this system is not a day. It is a day, optionally a wall time,
// optionally a zone that wall time is read in, and — when that reading is
// ambiguous because a DST fall-back repeats the hour — which of the two
// instants was meant. Typing all of that is possible and the field still
// accepts it; STEPPING it is what this control is for, and it is the only
// affordance that can show a user the fold row exists at all.

// temporalRow is one adjustable row of the control.
type temporalRow string

// The rows, in the order they are shown. Zone appears only for a fixed value,
// and Fold only when the chosen local time is genuinely ambiguous.
const (
	temporalRowDate temporalRow = "date"
	temporalRowTime temporalRow = "time"
	temporalRowMode temporalRow = "mode"
	temporalRowZone temporalRow = "zone"
	temporalRowFold temporalRow = "fold"
)

// temporalMode is what shape the value has.
type temporalMode string

const (
	temporalAllDay   temporalMode = "all_day"
	temporalFloating temporalMode = "floating"
	temporalFixed    temporalMode = "fixed"
)

var temporalModes = []temporalMode{temporalAllDay, temporalFloating, temporalFixed}

// TemporalInput is a date field that also opens the structured control.
//
// It embeds the neutral DateInput so the typed path, the parse errors and the
// calendar are unchanged; what it adds is a second overlay that owns the
// keyboard while it is open.
type TemporalInput struct {
	*termform.DateInput
	context temporal.Context
	today   func() temporal.Date

	open bool
	row  int
	// value is the control's working copy. It is written back into the field's
	// buffer on every adjustment, so the text and the control never disagree.
	value *temporal.Value
	// calendar is the day-picker opened FROM the date row, and zoneSearch the
	// identifier search opened from the zone row. Each takes the keyboard from
	// the control while it is up.
	calendar    *temporal.Date
	zoneSearch  *string
	zoneIndex   int
	parseError  string
	suggestions []string
}

// NewTemporalInput builds one.
func NewTemporalInput(base termform.Base, hooks termform.DateHooks, today func() temporal.Date,
	suggestions []string, context temporal.Context) *TemporalInput {

	field := &TemporalInput{
		DateInput: termform.NewDateInput(base, hooks, today, suggestions, true),
		context:   context, today: today, suggestions: suggestions,
	}
	return field
}

// PickerOpen reports either overlay: the structured control or the calendar the
// base field opens.
func (t *TemporalInput) PickerOpen() bool { return t.open || t.DateInput.PickerOpen() }

// ControlOpen reports the structured control specifically.
func (t *TemporalInput) ControlOpen() bool { return t.open }

// Row is the highlighted row.
func (t *TemporalInput) Row() temporalRow {
	rows := t.visibleRows(t.value)
	return rows[clamp(t.row, 0, len(rows)-1)]
}

// Value is the control's working value.
func (t *TemporalInput) Value() *temporal.Value { return t.value }

// ZoneSearch is the live zone query, or nil when the search is not open.
func (t *TemporalInput) ZoneSearch() *string { return t.zoneSearch }

// CalendarDate is the day-picker's highlighted day, or nil.
func (t *TemporalInput) CalendarDate() *temporal.Date { return t.calendar }

// CursorFor hides the text caret while either overlay owns the keyboard.
func (t *TemporalInput) CursorFor(value any, context termform.Context) *int {
	if t.open {
		return nil
	}
	return t.DateInput.CursorFor(value, context)
}

// HandleEvent routes one event: the control first when it is open, then the
// base field's typed and calendar behavior.
func (t *TemporalInput) HandleEvent(event termform.Event, value any, context termform.Context) *termform.Result {
	if t.open {
		return t.handleControl(event, value)
	}
	if termform.Command(event, termform.EventCommit, "\r", "\n") {
		t.open = true
		t.value = t.workingValue(value)
		t.row = 0
		t.parseError = ""
		return termform.HandledResult(value)
	}
	return t.DateInput.HandleEvent(event, value, context)
}

// handleControl is the control's own key table.
func (t *TemporalInput) handleControl(event termform.Event, value any) *termform.Result {
	if t.zoneSearch != nil {
		return t.handleZoneSearch(event, value)
	}
	if t.calendar != nil {
		return t.handleCalendar(event, value)
	}
	if termform.Command(event, termform.EventCancel, "\x1b") {
		// The control is a LIVE editor: arrow adjustments have already changed
		// the field value, so Escape only closes the overlay. Discarding the
		// whole field draft is the editor-level Escape's job.
		t.open = false
		return termform.HandledResult(value)
	}
	rows := t.visibleRows(t.value)
	switch termform.DecodedKey(event) {
	case "\x1b[A":
		t.row = ((t.row-1)%len(rows) + len(rows)) % len(rows)
	case "\x1b[B":
		t.row = (t.row + 1) % len(rows)
	case "\x1b[D":
		return t.adjust(rows[clamp(t.row, 0, len(rows)-1)], -1, value)
	case "\x1b[C":
		return t.adjust(rows[clamp(t.row, 0, len(rows)-1)], 1, value)
	default:
		if termform.Command(event, termform.EventCommit, "\r", "\n") {
			return t.activate(rows[clamp(t.row, 0, len(rows)-1)], value)
		}
		return nil
	}
	return termform.HandledResult(value)
}

// activate is Return on a row: the date row opens the calendar, the zone row
// opens the identifier search, and every other row steps forward.
func (t *TemporalInput) activate(row temporalRow, value any) *termform.Result {
	switch row {
	case temporalRowDate:
		date := t.value.Date
		t.calendar = &date
		return termform.HandledResult(value)
	case temporalRowZone:
		if t.modeOf(t.value) == temporalFixed {
			empty := ""
			t.zoneSearch = &empty
			t.zoneIndex = 0
		}
		return termform.HandledResult(value)
	}
	return t.adjust(row, 1, value)
}

// adjust steps one row.
func (t *TemporalInput) adjust(row temporalRow, direction int, value any) *termform.Result {
	var updated *temporal.Value
	switch row {
	case temporalRowDate:
		updated = t.rebuild(t.value, t.value.Date.AddDays(direction), t.value.LocalTime,
			t.value.Timezone, t.value.Fold)
	case temporalRowTime:
		stepped, err := t.adjustTime(t.value, direction)
		if err != nil {
			t.parseError = err.Error()
			return termform.HandledResult(value)
		}
		updated = stepped
	case temporalRowMode:
		updated = t.adjustMode(t.value, direction)
	case temporalRowZone:
		return t.activate(temporalRowZone, value)
	case temporalRowFold:
		fold := 1
		if t.value.Fold == 1 {
			fold = 0
		}
		updated = t.rebuild(t.value, t.value.Date, t.value.LocalTime, t.value.Timezone, fold)
	}
	if updated == nil {
		return termform.HandledResult(value)
	}
	return t.commitWorking(updated, value)
}

// adjustTime steps the wall time by fifteen minutes, wrapping the day.
//
// The DST gap is the interesting case. Stepping FORWARD into an hour that does
// not exist in the chosen zone lands on the first valid local time after the
// gap rather than refusing — because the user is walking the clock, and the
// clock in that zone genuinely skips. Stepping BACKWARD into the gap refuses,
// since there is no "previous valid" reading a backward walk should jump to.
func (t *TemporalInput) adjustTime(current *temporal.Value, direction int) (*temporal.Value, error) {
	if current.AllDay() {
		return &temporal.Value{Date: current.Date, LocalTime: "09:00"}, nil
	}
	hour, minute := splitLocalTime(current.LocalTime)
	total := ((hour*60+minute+direction*15)%1440 + 1440) % 1440
	candidate := fmt.Sprintf("%02d:%02d", total/60, total%60)

	rebuilt := t.rebuild(current, current.Date, candidate, current.Timezone, current.Fold)
	if err := t.resolvable(rebuilt); err == nil {
		return rebuilt, nil
	} else if direction < 0 {
		return nil, err
	}
	zone := current.Timezone
	if zone == "" {
		zone = t.context.TimezoneID
	}
	if zone == "" {
		return nil, fmt.Errorf("%s does not exist on %s", candidate, current.Date.ISO())
	}
	location, err := timezones.Load(zone)
	if err != nil {
		return nil, err
	}
	next, ok := timezones.FirstValidLocalAfter(current.Date.Year, current.Date.Month,
		current.Date.Day, total/60, total%60, location)
	if !ok {
		return nil, fmt.Errorf("%s does not exist on %s in %s", candidate, current.Date.ISO(), zone)
	}
	return t.rebuild(current, current.Date, next, current.Timezone, current.Fold), nil
}

// adjustMode cycles all-day → floating → fixed.
func (t *TemporalInput) adjustMode(current *temporal.Value, direction int) *temporal.Value {
	index := 0
	for position, mode := range temporalModes {
		if mode == t.modeOf(current) {
			index = position
			break
		}
	}
	next := temporalModes[((index+direction)%len(temporalModes)+len(temporalModes))%len(temporalModes)]
	switch next {
	case temporalAllDay:
		return &temporal.Value{Date: current.Date}
	case temporalFloating:
		local := current.LocalTime
		if local == "" {
			local = "09:00"
		}
		return &temporal.Value{Date: current.Date, LocalTime: local, Fold: current.Fold}
	}
	local := current.LocalTime
	if local == "" {
		local = "09:00"
	}
	zone := current.Timezone
	if zone == "" {
		zone = t.context.TimezoneID
	}
	if zone == "" {
		zone = "Etc/UTC"
	}
	return &temporal.Value{Date: current.Date, LocalTime: local, Timezone: zone, Fold: current.Fold}
}

// commitWorking adopts an adjusted value: it becomes the working copy, the
// field's text buffer is rewritten to match, and the row is clamped because a
// mode change can remove the row the cursor was on.
func (t *TemporalInput) commitWorking(updated *temporal.Value, previous any) *termform.Result {
	t.value = updated
	t.parseError = ""
	t.SetText(FormatTemporal(updated))
	t.row = min(t.row, len(t.visibleRows(updated))-1)
	if existing, ok := previous.(*temporal.Value); ok && existing != nil && *existing == *updated {
		return termform.HandledResult(updated)
	}
	return termform.ChangedResult(updated)
}

// -- the calendar opened from the date row ----------------------------------------

func (t *TemporalInput) handleCalendar(event termform.Event, value any) *termform.Result {
	if termform.Command(event, termform.EventCancel, "\x1b") {
		t.calendar = nil
		return termform.HandledResult(value)
	}
	if termform.Command(event, termform.EventCommit, "\r", "\n") {
		updated := t.rebuild(t.value, *t.calendar, t.value.LocalTime, t.value.Timezone, t.value.Fold)
		t.calendar = nil
		return t.commitWorking(updated, value)
	}
	moved := *t.calendar
	switch termform.DecodedKey(event) {
	case "\x1b[D":
		moved = moved.AddDays(-1)
	case "\x1b[C":
		moved = moved.AddDays(1)
	case "\x1b[A":
		moved = moved.AddDays(-7)
	case "\x1b[B":
		moved = moved.AddDays(7)
	case "\x1b[5~":
		moved = shiftMonths(moved, -1)
	case "\x1b[6~":
		moved = shiftMonths(moved, 1)
	case "t", "T":
		moved = t.today()
	default:
		return nil
	}
	t.calendar = &moved
	return termform.HandledResult(value)
}

// -- the zone search ---------------------------------------------------------------

func (t *TemporalInput) handleZoneSearch(event termform.Event, value any) *termform.Result {
	matches := t.zoneMatches()
	if termform.Command(event, termform.EventCancel, "\x1b") {
		t.zoneSearch = nil
		t.zoneIndex = 0
		return termform.HandledResult(value)
	}
	if termform.Command(event, termform.EventCommit, "\r", "\n") {
		if len(matches) == 0 {
			return termform.HandledResult(value)
		}
		updated := t.rebuild(t.value, t.value.Date, t.value.LocalTime,
			matches[clamp(t.zoneIndex, 0, len(matches)-1)], t.value.Fold)
		t.zoneSearch = nil
		t.zoneIndex = 0
		return t.commitWorking(updated, value)
	}
	key := termform.DecodedKey(event)
	switch key {
	case "\x1b[A":
		t.zoneIndex = max(t.zoneIndex-1, 0)
	case "\x1b[B":
		t.zoneIndex = min(t.zoneIndex+1, max(len(matches)-1, 0))
	case "\x7f", "\b":
		query := *t.zoneSearch
		if runes := []rune(query); len(runes) > 0 {
			query = string(runes[:len(runes)-1])
		}
		t.zoneSearch = &query
		t.zoneIndex = 0
	default:
		text := key
		if event.Type == termform.EventPaste {
			text = event.Text
		}
		if text == "" || !zoneQueryRune(text) {
			return nil
		}
		query := *t.zoneSearch + text
		t.zoneSearch = &query
		t.zoneIndex = 0
	}
	return termform.HandledResult(value)
}

// zoneQueryRune restricts the search to the characters an identifier can hold,
// so an arrow sequence that fell through cannot end up in the query.
func zoneQueryRune(text string) bool {
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '+' || r == '-' || r == '/' || r == '.':
		default:
			return false
		}
	}
	return true
}

// zoneMatches offers only identifiers the store will accept — Area/Location or
// UTC. A slashless link like "Japan" would fail validation on save, so offering
// it would be offering a value that cannot be kept.
func (t *TemporalInput) zoneMatches() []string {
	query := strings.ToLower(*t.zoneSearch)
	matches := []string{}
	for _, identifier := range ZoneIdentifiers() {
		if strings.Contains(strings.ToLower(identifier), query) {
			matches = append(matches, identifier)
		}
	}
	return matches
}

var (
	zoneOnce sync.Once
	zoneList []string
)

// ZoneIdentifiers is every IANA zone this host can load, Area/Location or UTC.
//
// Go has no equivalent of TZInfo.all_identifiers, so the zoneinfo tree is
// walked. A host with no tree at all still gets the handful of zones the
// runtime always resolves, so the search is never empty.
func ZoneIdentifiers() []string {
	zoneOnce.Do(func() {
		seen := map[string]bool{}
		for _, root := range zoneRoots() {
			_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return nil
				}
				identifier := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
				if !strings.Contains(identifier, "/") || strings.Contains(identifier, ".") {
					return nil
				}
				if _, err := time.LoadLocation(identifier); err != nil {
					return nil
				}
				seen[identifier] = true
				return nil
			})
		}
		seen["UTC"] = true
		for _, fallback := range []string{"Etc/UTC", "America/New_York", "Europe/London",
			"Europe/Berlin", "Asia/Tokyo", "Australia/Sydney"} {
			if _, err := time.LoadLocation(fallback); err == nil {
				seen[fallback] = true
			}
		}
		zoneList = make([]string, 0, len(seen))
		for identifier := range seen {
			zoneList = append(zoneList, identifier)
		}
		sort.Strings(zoneList)
	})
	return zoneList
}

func zoneRoots() []string {
	if custom := os.Getenv("ZONEINFO"); custom != "" {
		return []string{custom}
	}
	return []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ"}
}

// -- the value model ------------------------------------------------------------------

func (t *TemporalInput) workingValue(value any) *temporal.Value {
	if parsed, ok := value.(*temporal.Value); ok && parsed != nil {
		held := *parsed
		return &held
	}
	if parsed, err := ParseTemporal(t.Text(), t.today(), t.context); err == nil {
		if typed, ok := parsed.(*temporal.Value); ok && typed != nil {
			held := *typed
			return &held
		}
	}
	return &temporal.Value{Date: t.today()}
}

func (t *TemporalInput) rebuild(current *temporal.Value, date temporal.Date,
	local, zone string, fold int) *temporal.Value {

	if local == "" {
		return &temporal.Value{Date: date}
	}
	return &temporal.Value{Date: date, LocalTime: local, Timezone: zone, Fold: fold}
}

// resolvable reports whether a value names an instant that exists.
func (t *TemporalInput) resolvable(value *temporal.Value) error {
	if value.LocalTime == "" {
		return nil
	}
	zone := value.Timezone
	if zone == "" {
		zone = t.context.TimezoneID
	}
	if zone == "" {
		return nil
	}
	location, err := timezones.Load(zone)
	if err != nil {
		return err
	}
	hour, minute := splitLocalTime(value.LocalTime)
	_, err = timezones.UTCFor(value.Date.Year, value.Date.Month, value.Date.Day,
		hour, minute, location, value.Fold)
	return err
}

func (t *TemporalInput) modeOf(value *temporal.Value) temporalMode {
	switch {
	case value == nil || value.AllDay():
		return temporalAllDay
	case value.Timezone != "":
		return temporalFixed
	}
	return temporalFloating
}

// visibleRows is the row set for a value. The zone row appears only for a fixed
// value, and the fold row only when the local time is genuinely AMBIGUOUS —
// showing it otherwise would offer a choice that has no second option.
func (t *TemporalInput) visibleRows(value *temporal.Value) []temporalRow {
	rows := []temporalRow{temporalRowDate, temporalRowTime, temporalRowMode}
	if t.modeOf(value) == temporalFixed {
		rows = append(rows, temporalRowZone)
	}
	if value != nil && value.LocalTime != "" && t.ambiguous(value) {
		rows = append(rows, temporalRowFold)
	}
	return rows
}

func (t *TemporalInput) ambiguous(value *temporal.Value) bool {
	zone := value.Timezone
	if zone == "" {
		zone = t.context.TimezoneID
	}
	if zone == "" {
		return false
	}
	location, err := timezones.Load(zone)
	if err != nil {
		return false
	}
	hour, minute := splitLocalTime(value.LocalTime)
	return timezones.Ambiguous(value.Date.Year, value.Date.Month, value.Date.Day,
		hour, minute, location)
}

// -- rendering -------------------------------------------------------------------------

// MetadataFor publishes the control for the form renderer.
func (t *TemporalInput) MetadataFor(value any, context termform.Context) map[string]any {
	if !t.open {
		return t.DateInput.MetadataFor(value, context)
	}
	out := map[string]any{}
	for key, entry := range t.Metadata() {
		out[key] = entry
	}
	out["text"] = t.Text()
	out["preview"] = FormatTemporal(t.value)
	out["picker_open"] = true
	out["suggestions"] = append([]string{}, t.suggestions...)
	out["picker"] = TemporalControl{
		Row:        t.Row(),
		Mode:       t.modeOf(t.value),
		ZoneSearch: t.zoneSearch,
		Lines:      t.ControlLines(200),
	}
	return out
}

// TemporalControl is the control as the renderer sees it.
type TemporalControl struct {
	Row        temporalRow
	Mode       temporalMode
	ZoneSearch *string
	Lines      []string
}

// ControlLines renders the control at a width.
func (t *TemporalInput) ControlLines(width int) []string {
	if t.calendar != nil {
		lines := []string{"Date picker · arrows move · Enter selects · Esc returns"}
		return append(lines, calendarLines(*t.calendar, width)...)
	}
	if t.zoneSearch != nil {
		matches := t.zoneMatches()
		lines := []string{
			"Zone search: " + *t.zoneSearch,
			"type to filter · ↑/↓ choose · Enter selects · Esc returns",
		}
		for index, zone := range matches[:min(len(matches), 6)] {
			marker := " "
			if index == t.zoneIndex {
				marker = "›"
			}
			lines = append(lines, marker+" "+zone)
		}
		if len(matches) == 0 {
			lines = append(lines, "  no matching IANA zones")
		}
		return lines
	}
	rows := t.visibleRows(t.value)
	lines := []string{"Temporal value · ↑/↓ field · ←/→ change · Enter opens · Esc closes"}
	for index, row := range rows {
		marker := " "
		if index == clamp(t.row, 0, len(rows)-1) {
			marker = "›"
		}
		lines = append(lines, marker+" "+t.rowLabel(row))
	}
	if t.parseError != "" {
		lines = append(lines, "Error: "+t.parseError)
	}
	return lines
}

func (t *TemporalInput) rowLabel(row temporalRow) string {
	switch row {
	case temporalRowDate:
		return "Date: " + t.value.Date.ISO()
	case temporalRowTime:
		if t.value.LocalTime == "" {
			return "Time: All day"
		}
		return "Time: " + t.value.LocalTime
	case temporalRowMode:
		name := strings.ReplaceAll(string(t.modeOf(t.value)), "_", " ")
		return "Mode: " + strings.ToUpper(name[:1]) + name[1:]
	case temporalRowZone:
		return "Zone: " + t.value.Timezone + " (Enter to search)"
	case temporalRowFold:
		if t.value.Fold == 1 {
			return "Fold: Later"
		}
		return "Fold: Earlier"
	}
	return ""
}

func calendarLines(date temporal.Date, width int) []string {
	calendar := termform.CalendarFor(date)
	if width < 20 {
		return []string{date.ISO()[:7], date.ISO(), "arrows move; enter picks"}
	}
	lines := []string{monthYear(calendar.Month), strings.Join(calendar.WeekdayLabels, " ")}
	for _, week := range calendar.Weeks {
		cells := []string{}
		for _, day := range week {
			if day.Month == calendar.Month.Month {
				cells = append(cells, rightAlign(itoa(day.Day), 2))
			} else {
				cells = append(cells, "  ")
			}
		}
		lines = append(lines, strings.Join(cells, " "))
	}
	return lines
}

func splitLocalTime(value string) (int, int) {
	parts := strings.SplitN(value, ":", 2)
	hour, _ := strconv.Atoi(parts[0])
	minute := 0
	if len(parts) > 1 {
		minute, _ = strconv.Atoi(parts[1])
	}
	return hour, minute
}

func shiftMonths(date temporal.Date, offset int) temporal.Date {
	index := date.Year*12 + int(date.Month) - 1 + offset
	year, month := index/12, index%12
	if month < 0 {
		month += 12
		year--
	}
	day := min(date.Day, temporal.DaysIn(year, time.Month(month+1)))
	shifted, _ := temporal.NewDate(year, time.Month(month+1), day)
	return shifted
}
