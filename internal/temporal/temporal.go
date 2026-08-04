// Package temporal is the Go counterpart of lib/tasks/temporal_value.rb and
// lib/tasks/temporal_context.rb: a stored date with an optional wall time and
// zone, and the reader's own "now" that such a value is resolved against.
//
// The split matters. A stored value is what the user wrote down — 2026-06-18
// 17:00 Europe/London — and never moves. The context is who is looking and
// when, and it is what turns that value into an instant, a local rendering, or
// the answer to "is this released yet?". Keeping them apart is what lets one
// snapshot render identically for two readers in different zones.
package temporal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/timezones"
)

// localPattern is TemporalValue::LOCAL_RE: minute precision, 24-hour clock.
var localPattern = regexp.MustCompile(`\A(?:[01]\d|2[0-3]):[0-5]\d\z`)

// datePattern is Check::DATE_RE: the stored spelling, before the date is
// checked for being real.
var datePattern = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}\z`)

// Date is a civil date with no zone: the shape every stored date stamp has.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate accepts exactly the stored spelling and only real dates.
func ParseDate(value string) (Date, bool) {
	if !datePattern.MatchString(value) {
		return Date{}, false
	}
	year, _ := strconv.Atoi(value[0:4])
	month, _ := strconv.Atoi(value[5:7])
	day, _ := strconv.Atoi(value[8:10])
	return NewDate(year, time.Month(month), day)
}

// NewDate reports false for a day the calendar does not have, which is what
// Date.valid_date? answers.
func NewDate(year int, month time.Month, day int) (Date, bool) {
	if month < 1 || month > 12 || day < 1 {
		return Date{}, false
	}
	candidate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if candidate.Year() != year || candidate.Month() != month || candidate.Day() != day {
		return Date{}, false
	}
	return Date{Year: year, Month: month, Day: day}, true
}

// DateOf is the civil date of an instant already projected into a zone.
func DateOf(instant time.Time) Date {
	return Date{Year: instant.Year(), Month: instant.Month(), Day: instant.Day()}
}

// ISO is the stored spelling.
func (d Date) ISO() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }

// Zero reports the never-set date.
func (d Date) Zero() bool { return d.Year == 0 && d.Month == 0 && d.Day == 0 }

func (d Date) time() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

// Sub is Ruby's `(a - b).to_i` over two Dates: whole days, signed.
func (d Date) Sub(other Date) int {
	return int(d.time().Sub(other.time()).Hours() / 24)
}

// AddDays is Date#+.
func (d Date) AddDays(days int) Date {
	shifted := d.time().AddDate(0, 0, days)
	return Date{Year: shifted.Year(), Month: shifted.Month(), Day: shifted.Day()}
}

// AddMonths is Ruby Date#>>: a calendar step whose day of month CLAMPS to the
// target month's length rather than overflowing into the next month, so
// January 31 plus one month is February 28 (29 in a leap year). Go's
// time.Time.AddDate normalizes instead, which would say March 3.
func (d Date) AddMonths(months int) Date {
	total := int(d.Month) - 1 + months
	year := d.Year + floorDiv(total, 12)
	month := time.Month(floorMod(total, 12) + 1)
	day := d.Day
	if length := DaysIn(year, month); day > length {
		day = length
	}
	return Date{Year: year, Month: month, Day: day}
}

// DaysIn is the length of a calendar month.
func DaysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(value, divisor int) int {
	quotient := value / divisor
	if value%divisor != 0 && (value < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}

func floorMod(value, divisor int) int { return value - floorDiv(value, divisor)*divisor }

// Weekday is the day of week, Sunday being zero, as Ruby's Date#wday reports.
func (d Date) Weekday() time.Weekday { return d.time().Weekday() }

// Before reports chronological order.
func (d Date) Before(other Date) bool { return d.time().Before(other.time()) }

// After reports chronological order the other way round.
func (d Date) After(other Date) bool { return other.Before(d) }

// Equal reports identity.
func (d Date) Equal(other Date) bool { return d == other }

// Context is the reader: the instant "now", the zone dates are projected into,
// and the clock format renderings use.
type Context struct {
	Now        time.Time
	Timezone   *time.Location
	TimezoneID string
	TimeFormat int
}

// NewContext builds a context from a resolved zone identifier.
func NewContext(now time.Time, timezone string, timeFormat int) (Context, error) {
	location, err := timezones.Load(timezone)
	if err != nil {
		return Context{}, err
	}
	id, _ := timezones.Get(timezone)
	return Context{Now: now.UTC(), Timezone: location, TimezoneID: id, TimeFormat: timeFormat}, nil
}

// LocalNow is `now` in the reader's zone.
func (c Context) LocalNow() time.Time { return c.Now.In(c.Timezone) }

// LocalDate is the reader's own calendar day — the "today" every date-sensitive
// read is measured against.
func (c Context) LocalDate() Date { return DateOf(c.LocalNow()) }

// Value is a stored date with an optional local time, zone and DST fold.
//
// Three shapes, and the difference between them is the whole point: an all-day
// value is a calendar day (it releases at local midnight wherever the reader
// is); a floating value carries a wall time but no zone (09:30 means 09:30
// where you are); a fixed value carries a zone (17:00 in London is one instant
// for everybody).
type Value struct {
	Date      Date
	LocalTime string
	Timezone  string
	Fold      int
}

// NewValue validates the combination the way TemporalValue#initialize does.
func NewValue(date Date, localTime, timezone string, fold int, validate bool) (Value, error) {
	if localTime != "" && !localPattern.MatchString(localTime) {
		return Value{}, fmt.Errorf("local time must use HH:MM minute precision")
	}
	if timezone != "" && localTime == "" {
		return Value{}, fmt.Errorf("time zone requires a local time")
	}
	if fold != 0 && fold != 1 {
		return Value{}, fmt.Errorf("fold must be 0 or 1")
	}
	value := Value{Date: date, LocalTime: localTime, Timezone: timezone, Fold: fold}
	if timezone != "" {
		if _, err := timezones.Load(timezone); err != nil {
			return Value{}, err
		}
		if validate {
			if _, err := value.resolvedInstant(); err != nil {
				return Value{}, err
			}
		}
	}
	return value, nil
}

// AllDay reports a value with no wall time.
func (v Value) AllDay() bool { return v.LocalTime == "" }

// Floating reports a wall time with no zone.
func (v Value) Floating() bool { return !v.AllDay() && v.Timezone == "" }

// Fixed reports a value that names its own zone.
func (v Value) Fixed() bool { return v.Timezone != "" }

// EffectiveZone is the value's own zone when it has one, else the reader's.
func (v Value) EffectiveZone(context Context) *time.Location {
	if v.Fixed() {
		if location, err := timezones.Load(v.Timezone); err == nil {
			return location
		}
	}
	return context.Timezone
}

// Instant is the UTC instant this value names for this reader. An all-day
// value is the first instant of its date in the reader's zone; a timed value
// resolves its wall time in its effective zone.
func (v Value) Instant(context Context) (time.Time, error) {
	if v.AllDay() {
		return timezones.EarliestOn(v.Date.Year, v.Date.Month, v.Date.Day, context.Timezone)
	}
	return v.instantIn(v.EffectiveZone(context))
}

// ReleaseInstant is Instant under the name the availability rules use it by.
func (v Value) ReleaseInstant(context Context) (time.Time, error) { return v.Instant(context) }

func (v Value) resolvedInstant() (time.Time, error) {
	location, err := timezones.Load(v.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	return v.instantIn(location)
}

func (v Value) instantIn(location *time.Location) (time.Time, error) {
	hour, minute, ok := splitLocal(v.LocalTime)
	if !ok {
		return time.Time{}, fmt.Errorf("local time must use HH:MM minute precision")
	}
	return timezones.UTCFor(v.Date.Year, v.Date.Month, v.Date.Day, hour, minute, location, v.Fold)
}

// Projection is what a renderer needs: the date and wall time as the reader
// sees them, plus the zone that projection was made in.
type Projection struct {
	Date       Date
	Local      string
	TimezoneID string
}

// Projected renders this value in the reader's zone. An all-day value has no
// wall time to project, so it reports its stored date unchanged.
func (v Value) Projected(context Context) (Projection, error) {
	if v.AllDay() {
		return Projection{Date: v.Date}, nil
	}
	instant, err := v.Instant(context)
	if err != nil {
		return Projection{}, err
	}
	local := instant.In(context.Timezone)
	return Projection{
		Date:       DateOf(local),
		Local:      fmt.Sprintf("%02d:%02d", local.Hour(), local.Minute()),
		TimezoneID: context.TimezoneID,
	}, nil
}

// DueBoundary is the instant a value stops being on time. An ALL-DAY value is
// due at the end of its day, so its boundary is the first instant of the NEXT
// date — not the stored date's own midnight.
func (v Value) DueBoundary(context Context) (time.Time, error) {
	if v.AllDay() {
		next := v.Date.AddDays(1)
		return timezones.EarliestOn(next.Year, next.Month, next.Day, context.Timezone)
	}
	return v.Instant(context)
}

// Overdue reports whether this value has passed. Date-only: overdue the moment
// the local date passes. Timed: on time AT the exact instant, overdue strictly
// after it.
func (v Value) Overdue(context Context) (bool, error) {
	boundary, err := v.DueBoundary(context)
	if err != nil {
		return false, err
	}
	if v.AllDay() {
		return !context.Now.Before(boundary), nil
	}
	return context.Now.After(boundary), nil
}

// Released reports whether an available-from value has arrived: true at its
// instant, not only after it.
func (v Value) Released(context Context) (bool, error) {
	instant, err := v.ReleaseInstant(context)
	if err != nil {
		return false, err
	}
	return !context.Now.Before(instant), nil
}

// TimeMetadata is the stored `<field>_time` object, or ok=false for an all-day
// value which has none. `fold` is written only when it is set, and `timezone`
// only when the value carries one, so a floating value round-trips as one.
func (v Value) TimeMetadata() (local, timezone string, fold int, ok bool) {
	if v.AllDay() {
		return "", "", 0, false
	}
	return v.LocalTime, v.Timezone, v.Fold, true
}

// Shift moves a value by whole days, keeping its wall time, zone and fold.
func (v Value) Shift(days int) (Value, error) {
	return NewValue(v.Date.AddDays(days), v.LocalTime, v.Timezone, v.Fold, true)
}

// WithDate re-dates a value, keeping everything else. It is how a recurrence
// roll proposes its next occurrence.
func (v Value) WithDate(date Date) (Value, error) {
	return NewValue(date, v.LocalTime, v.Timezone, v.Fold, true)
}

// Equal compares the four stored fields, which is what identity means for a
// value: two values naming the same instant through different zones are not
// the same stored value.
func (v Value) Equal(other Value) bool { return v == other }

// FromRecord reads a stored date field and its paired `<field>_time` object.
// A time object that does not survive validation degrades to the date alone
// rather than losing the record: a hand-edited time must not make a task
// disappear from a read, and Check is what reports it.
func FromRecord(date string, local, timezone string, fold int, validate bool) (Value, bool) {
	parsed, ok := ParseDate(date)
	if !ok {
		return Value{}, false
	}
	value, err := NewValue(parsed, local, timezone, fold, validate)
	if err != nil {
		return Value{Date: parsed}, true
	}
	return value, true
}

func splitLocal(value string) (int, int, bool) {
	hourText, minuteText, found := strings.Cut(value, ":")
	if !found {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return 0, 0, false
	}
	minute, err := strconv.Atoi(minuteText)
	if err != nil {
		return 0, 0, false
	}
	return hour, minute, true
}

// ValidLocal reports whether a stored `local` string is well formed. Check
// needs the predicate without building a value.
func ValidLocal(value string) bool { return localPattern.MatchString(value) }
