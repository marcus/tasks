package store

import (
	"regexp"
	"strconv"
	"time"
)

// Ruby's `Store#to_date` is `Date.iso8601(str)`, whose accepted grammar is far
// wider than YYYY-MM-DD: basic, ordinal, week, truncated, and datetime-prefixed
// forms all parse, and the truncated forms complete their missing components
// from the current date. The patterns below are transcribed verbatim from the
// ones the date extension compiles (`iso8601_ext_datetime` and
// `iso8601_bas_datetime` in date_parse.c), with two mechanical adjustments:
// Ruby applies them case-insensitively, and Ruby's `\s` includes a vertical tab
// while Go's does not. Ruby's remaining two patterns are time-only, and a
// time with no date component always raises, so they are absent here — an
// input only they would match returns no date either way.
//
// The oracle for every row is
// porting/evidence/store-snapshot-items/ruby/date-iso8601-grammar.json.
const rubySpace = `[ \t\r\n\f\v]`

var (
	iso8601Extended = regexp.MustCompile(`(?i)\A` + rubySpace + `*(?:([-+]?\d{2,}|-)-(\d{2})?(?:-(\d{2}))?|([-+]?\d{2,})?-(\d{3})|(\d{4}|\d{2})?-w(\d{2})-(\d)|-w-(\d))(?:t(\d{2}):(\d{2})(?::(\d{2})(?:[,.](\d+))?)?(z|[-+]\d{2}(?::?\d{2})?)?)?` + rubySpace + `*\z`)
	iso8601Basic    = regexp.MustCompile(`(?i)\A` + rubySpace + `*(?:([-+]?(?:\d{4}|\d{2})|--)(\d{2}|-)(\d{2})|([-+]?(?:\d{4}|\d{2}))(\d{3})|-(\d{3})|(\d{4}|\d{2})w(\d{2})(\d)|-w(\d{2})(\d)|-w-(\d))(?:t?(\d{2})(\d{2})(?:(\d{2})(?:[,.](\d+))?)?(z|[-+]\d{2}(?:\d{2})?)?)?` + rubySpace + `*\z`)
)

// italyReformJD is the Julian Day Number of 1582-10-15, the first Gregorian
// day of Date::ITALY — the default calendar reform Date.iso8601 uses. Dates
// before it are Julian, which is why Ruby accepts 1500-02-29 and rejects
// 1582-10-05 through 1582-10-14.
const italyReformJD = 2299161

// dateParts holds the date elements Date._iso8601 can produce. Time elements
// are parsed by the grammar and then discarded, exactly as Date.iso8601
// discards them.
type dateParts struct {
	year, mon, mday      int
	yday                 int
	cwyear, cweek, cwday int

	hasYear, hasMon, hasMday      bool
	hasYday                       bool
	hasCwyear, hasCweek, hasCwday bool
}

func (p dateParts) empty() bool {
	return !(p.hasYear || p.hasMon || p.hasMday || p.hasYday ||
		p.hasCwyear || p.hasCweek || p.hasCwday)
}

// parseISO8601Date is `Date.iso8601` rescued to nil: it returns the parsed date
// or false. today supplies the components a truncated form omits, which is the
// one place this read model depends on the wall clock.
func parseISO8601Date(text string, today time.Time) (time.Time, bool) {
	parts, ok := iso8601Parts(text)
	if !ok || parts.empty() {
		return time.Time{}, false
	}
	jd, ok := completeAndResolve(parts, today)
	if !ok {
		return time.Time{}, false
	}
	year, month, day := jdToCivil(jd)
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}

func iso8601Parts(text string) (dateParts, bool) {
	if groups := iso8601Extended.FindStringSubmatch(text); groups != nil {
		return extendedParts(groups), true
	}
	if groups := iso8601Basic.FindStringSubmatch(text); groups != nil {
		return basicParts(groups), true
	}
	return dateParts{}, false
}

func extendedParts(g []string) dateParts {
	var parts dateParts
	switch {
	case g[1] != "":
		if g[3] != "" {
			parts.mday, parts.hasMday = number(g[3]), true
		}
		if g[2] != "" {
			parts.mon, parts.hasMon = number(g[2]), true
		}
		if g[1] != "-" {
			parts.year, parts.hasYear = completedYear(g[1]), true
		}
	case g[5] != "":
		parts.yday, parts.hasYday = number(g[5]), true
		if g[4] != "" {
			parts.year, parts.hasYear = completedYear(g[4]), true
		}
	case g[7] != "":
		parts.cweek, parts.hasCweek = number(g[7]), true
		parts.cwday, parts.hasCwday = number(g[8]), true
		if g[6] != "" {
			parts.cwyear, parts.hasCwyear = completedYear(g[6]), true
		}
	case g[9] != "":
		parts.cwday, parts.hasCwday = number(g[9]), true
	}
	return parts
}

func basicParts(g []string) dateParts {
	var parts dateParts
	switch {
	case g[1] != "":
		parts.mday, parts.hasMday = number(g[3]), true
		if g[2] != "-" {
			parts.mon, parts.hasMon = number(g[2]), true
		}
		if g[1] != "--" {
			parts.year, parts.hasYear = completedYear(g[1]), true
		}
	case g[4] != "":
		parts.year, parts.hasYear = completedYear(g[4]), true
		parts.yday, parts.hasYday = number(g[5]), true
	case g[6] != "":
		parts.yday, parts.hasYday = number(g[6]), true
	case g[7] != "":
		parts.cwyear, parts.hasCwyear = completedYear(g[7]), true
		parts.cweek, parts.hasCweek = number(g[8]), true
		parts.cwday, parts.hasCwday = number(g[9]), true
	case g[10] != "":
		parts.cweek, parts.hasCweek = number(g[10]), true
		parts.cwday, parts.hasCwday = number(g[11]), true
	case g[12] != "":
		parts.cwday, parts.hasCwday = number(g[12]), true
	}
	return parts
}

func number(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}

// completedYear applies Ruby's two-digit completion, which the date extension
// triggers on the matched year *text* being shorter than four characters — so
// "202" completes to 2102 and "-08" to 1992, both observed in the oracle.
func completedYear(text string) int {
	year := number(text)
	if len(text) < 4 {
		if year >= 69 {
			return year + 1900
		}
		return year + 2000
	}
	return year
}

// completeAndResolve is Ruby's `complete_frags` followed by validation: within
// the element group the parsed parts belong to, elements more significant than
// the first supplied one come from today, and less significant ones default to
// 1.
func completeAndResolve(parts dateParts, today time.Time) (int, bool) {
	switch {
	case parts.hasCwyear || parts.hasCweek || parts.hasCwday:
		cwyear, cweek, cwday := jdToCommercial(civilJD(today))
		supplied := []bool{parts.hasCwyear, parts.hasCweek, parts.hasCwday}
		values := []int{parts.cwyear, parts.cweek, parts.cwday}
		defaults := []int{cwyear, cweek, cwday}
		complete(supplied, values, defaults)
		return commercialToJD(values[0], values[1], values[2])
	case parts.hasYday:
		supplied := []bool{parts.hasYear, parts.hasYday}
		values := []int{parts.year, parts.yday}
		defaults := []int{today.Year(), today.YearDay()}
		complete(supplied, values, defaults)
		return ordinalToJD(values[0], values[1])
	default:
		supplied := []bool{parts.hasYear, parts.hasMon, parts.hasMday}
		values := []int{parts.year, parts.mon, parts.mday}
		defaults := []int{today.Year(), int(today.Month()), today.Day()}
		complete(supplied, values, defaults)
		return civilToJD(values[0], values[1], values[2])
	}
}

func complete(supplied []bool, values, defaults []int) {
	first := len(supplied)
	for index, present := range supplied {
		if present {
			first = index
			break
		}
	}
	for index, present := range supplied {
		if present {
			continue
		}
		if index < first {
			values[index] = defaults[index]
		} else {
			values[index] = 1
		}
	}
}

// civilToJD is Ruby's valid_civil? under Date::ITALY: a date is Gregorian when
// that reading falls on or after the reform, Julian when its Julian reading
// falls before it, and invalid otherwise — which is how the ten skipped days of
// October 1582 are rejected.
func civilToJD(year, month, day int) (int, bool) {
	if jd := gregorianToJD(year, month, day); jd >= italyReformJD {
		if y, m, d := jdToGregorian(jd); y == year && m == month && d == day {
			return jd, true
		}
		return 0, false
	}
	jd := julianToJD(year, month, day)
	if jd >= italyReformJD {
		return 0, false
	}
	if y, m, d := jdToJulian(jd); y == year && m == month && d == day {
		return jd, true
	}
	return 0, false
}

func ordinalToJD(year, yday int) (int, bool) {
	start, ok := civilToJD(year, 1, 1)
	if !ok {
		return 0, false
	}
	jd := start + yday - 1
	if resolvedYear, resolvedDay := jdToOrdinal(jd); resolvedYear != year || resolvedDay != yday {
		return 0, false
	}
	return jd, true
}

func commercialToJD(cwyear, cweek, cwday int) (int, bool) {
	start, ok := commercialYearStart(cwyear)
	if !ok {
		return 0, false
	}
	jd := start + (cweek-1)*7 + (cwday - 1)
	if y, w, d := jdToCommercial(jd); y != cwyear || w != cweek || d != cwday {
		return 0, false
	}
	return jd, true
}

// commercialYearStart is the Monday of the ISO week containing January 4th.
func commercialYearStart(cwyear int) (int, bool) {
	jan4, ok := civilToJD(cwyear, 1, 4)
	if !ok {
		return 0, false
	}
	return jan4 - (isoWeekday(jan4) - 1), true
}

func jdToCommercial(jd int) (cwyear, cweek, cwday int) {
	year, _, _ := jdToCivil(jd)
	if next, ok := commercialYearStart(year + 1); ok && jd >= next {
		year++
	} else if current, ok := commercialYearStart(year); !ok || jd < current {
		year--
	}
	start, ok := commercialYearStart(year)
	if !ok {
		return year, 0, 0
	}
	return year, floorDiv(jd-start, 7) + 1, isoWeekday(jd)
}

func jdToOrdinal(jd int) (year, yday int) {
	year, _, _ = jdToCivil(jd)
	start, ok := civilToJD(year, 1, 1)
	if !ok {
		return year, 0
	}
	return year, jd - start + 1
}

// isoWeekday numbers Monday 1 through Sunday 7. JD 0 was a Monday.
func isoWeekday(jd int) int { return floorMod(jd, 7) + 1 }

// jdToCivil renders a Julian Day Number in the calendar Date::ITALY has in
// effect for it, so a pre-reform date keeps the Julian civil fields Ruby prints.
func jdToCivil(jd int) (year, month, day int) {
	if jd < italyReformJD {
		return jdToJulian(jd)
	}
	return jdToGregorian(jd)
}

func gregorianToJD(year, month, day int) int {
	a := floorDiv(14-month, 12)
	y := year + 4800 - a
	m := month + 12*a - 3
	return day + floorDiv(153*m+2, 5) + 365*y + floorDiv(y, 4) - floorDiv(y, 100) + floorDiv(y, 400) - 32045
}

func julianToJD(year, month, day int) int {
	a := floorDiv(14-month, 12)
	y := year + 4800 - a
	m := month + 12*a - 3
	return day + floorDiv(153*m+2, 5) + 365*y + floorDiv(y, 4) - 32083
}

func jdToGregorian(jd int) (year, month, day int) {
	a := jd + 32044
	b := floorDiv(4*a+3, 146097)
	c := a - floorDiv(146097*b, 4)
	return fromJulianDayRemainder(100*b-4800, c)
}

func jdToJulian(jd int) (year, month, day int) {
	c := jd + 32082
	return fromJulianDayRemainder(-4800, c)
}

// fromJulianDayRemainder finishes both calendars' inverse conversions, which
// share everything after the century step.
func fromJulianDayRemainder(yearBase, c int) (year, month, day int) {
	d := floorDiv(4*c+3, 1461)
	e := c - floorDiv(1461*d, 4)
	m := floorDiv(5*e+2, 153)
	day = e - floorDiv(153*m+2, 5) + 1
	month = m + 3 - 12*floorDiv(m, 10)
	year = yearBase + d + floorDiv(m, 10)
	return year, month, day
}

// civilJD converts an ordinary calendar date — today's — to a Julian Day
// Number. Today is always post-reform, so the Gregorian reading is the right one.
func civilJD(value time.Time) int {
	return gregorianToJD(value.Year(), int(value.Month()), value.Day())
}

func floorDiv(a, b int) int {
	quotient := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		quotient--
	}
	return quotient
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }
