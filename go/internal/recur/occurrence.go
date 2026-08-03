package recur

// Occurrence math: given a parsed calendar schedule, the stored stamp it is
// anchored on, and a date to look past, the next date it fires on.
//
// The anchor matters as much as the schedule. `2w:mon` does not mean "every
// other Monday" in the abstract — it means every other Monday counting from the
// ISO week of the stored stamp, so two tasks one week apart run on opposite
// weeks. The same is true of `3m:15` and `2y:07-04`. That is why every
// projection takes both an anchor and an "after".

import (
	"fmt"
	"math/big"
)

// cycleLimit is how many month or year cycles the search walks before
// declaring a schedule unreachable from its anchor. Generous: an `m:5fri` skip
// needs a handful, a `y:02:5fri` one can need about thirty.
const cycleLimit = 500

// occurrenceAfter is the first date matching `parsed` strictly after `after`.
func occurrenceAfter(parsed schedule, anchor, after CivilDate) (CivilDate, error) {
	switch parsed.kind {
	case weekly:
		return weeklyAfter(parsed, anchor, after)
	case monthly:
		return monthlyAfter(parsed, anchor, after)
	default:
		return yearlyAfter(parsed, anchor, after)
	}
}

func weeklyAfter(parsed schedule, anchor, after CivilDate) (CivilDate, error) {
	block := big.NewInt(int64(7 * parsed.interval))
	anchorMonday := anchor.addDays(big.NewInt(int64(-(anchor.cwday() - 1))))
	cycle := floorQuotient(new(big.Int).Sub(daysFromCivil(after), daysFromCivil(anchorMonday)), block)

	for attempt := 0; attempt < 3; attempt++ {
		monday := anchorMonday.addDays(new(big.Int).Mul(block, cycle))
		for _, day := range parsed.days {
			date := monday.addDays(big.NewInt(int64(dayIndex[day])))
			if after.Before(date) {
				return date, nil
			}
		}
		cycle = new(big.Int).Add(cycle, big.NewInt(1))
	}
	return CivilDate{}, noOccurrence(parsed, anchor, fmt.Sprintf("%d weeks", 3*parsed.interval))
}

func monthlyAfter(parsed schedule, anchor, after CivilDate) (CivilDate, error) {
	interval := big.NewInt(int64(parsed.interval))
	anchorMonth := monthIndexOf(anchor)
	cycle := floorQuotient(new(big.Int).Sub(monthIndexOf(after), anchorMonth), interval)

	for attempt := 0; attempt < cycleLimit; attempt++ {
		months := new(big.Int).Add(anchorMonth, new(big.Int).Mul(interval, cycle))
		year, index := floorDiv(months, 12)
		month := int(index.Int64()) + 1

		var best *CivilDate
		for _, spec := range parsed.specs {
			date, ok := monthSpecDate(spec, year, month)
			if !ok || !after.Before(date) {
				continue
			}
			if best == nil || date.Before(*best) {
				candidate := date
				best = &candidate
			}
		}
		if best != nil {
			return *best, nil
		}
		cycle = new(big.Int).Add(cycle, big.NewInt(1))
	}
	return CivilDate{}, noOccurrence(parsed, anchor, fmt.Sprintf("%d months", cycleLimit*parsed.interval))
}

func yearlyAfter(parsed schedule, anchor, after CivilDate) (CivilDate, error) {
	interval := big.NewInt(int64(parsed.interval))
	cycle := floorQuotient(new(big.Int).Sub(after.Year, anchor.Year), interval)

	for attempt := 0; attempt < cycleLimit; attempt++ {
		year := new(big.Int).Add(anchor.Year, new(big.Int).Mul(interval, cycle))
		var date CivilDate
		var ok bool
		if parsed.ordinal != nil {
			date, ok = nthWeekday(year, parsed.month, parsed.ordinal.ordinal, parsed.ordinal.weekday)
		} else {
			date, ok = clampedDay(year, parsed.month, parsed.day), true
		}
		if ok && after.Before(date) {
			return date, nil
		}
		cycle = new(big.Int).Add(cycle, big.NewInt(1))
	}
	return CivilDate{}, noOccurrence(parsed, anchor, fmt.Sprintf("%d years", cycleLimit*parsed.interval))
}

// noOccurrence names the schedule AND the anchor that made it unreachable. Some
// schedules are satisfiable only for some anchors — `2y:02:5fri` needs a
// February with five Fridays, which only a leap year has, so an odd anchor year
// never fires — and that cannot be decided at parse time.
func noOccurrence(parsed schedule, anchor CivilDate, span string) error {
	return fmt.Errorf("no occurrence of %s within %s from %s — the schedule may never fire for this anchor",
		rubyInspect(canonicalString(parsed)), span, anchor)
}

// monthSpecDate resolves one monthly rule inside one month, reporting ok=false
// when that month has no such date — a 5th Friday that does not exist is
// SKIPPED, not clamped.
func monthSpecDate(spec monthSpec, year *big.Int, month int) (CivilDate, bool) {
	switch {
	case spec.ordinalY:
		return nthWeekday(year, month, spec.ordinal, spec.weekday)
	case spec.last:
		return lastDayOf(year, month), true
	default:
		return clampedDay(year, month, spec.day), true
	}
}

// clampedDay is a numeric day clamped to the month's length (April 31 becomes
// April 30, February 29 becomes the 28th in a common year), matching the clamp
// month intervals use. It is why `m:31` reads as `m:last` in short months.
func clampedDay(year *big.Int, month, day int) CivilDate {
	if length := daysInMonth(year, month); day > length {
		day = length
	}
	return normalizeReformGap(CivilDate{Year: new(big.Int).Set(year), Month: month, Day: day})
}

func lastDayOf(year *big.Int, month int) CivilDate {
	return CivilDate{Year: new(big.Int).Set(year), Month: month, Day: daysInMonth(year, month)}
}

// nthWeekday is the Nth (or last) named weekday of a month. A month with only
// four Fridays has no fifth one, which is reported rather than clamped.
func nthWeekday(year *big.Int, month, ordinal int, day string) (CivilDate, bool) {
	if ordinal == lastOrdinal {
		last := lastDayOf(year, month)
		back := (last.cwday() - 1 - dayIndex[day]) % 7
		if back < 0 {
			back += 7
		}
		return last.addDays(big.NewInt(int64(-back))), true
	}
	first := CivilDate{Year: new(big.Int).Set(year), Month: month, Day: 1}
	forward := (dayIndex[day] - (first.cwday() - 1)) % 7
	if forward < 0 {
		forward += 7
	}
	date := first.addDays(big.NewInt(int64(forward + 7*(ordinal-1))))
	if date.Month != month {
		return CivilDate{}, false
	}
	return date, true
}

// cwday is Ruby Date#cwday: the ISO weekday, Monday being 1. Day zero of the
// civil-date arithmetic is 1970-01-01, a Thursday.
func (d CivilDate) cwday() int {
	shifted := new(big.Int).Add(daysFromCivil(d), big.NewInt(3))
	return int(new(big.Int).Mod(shifted, big.NewInt(7)).Int64()) + 1
}

func monthIndexOf(d CivilDate) *big.Int {
	index := new(big.Int).Mul(d.Year, big.NewInt(12))
	return index.Add(index, big.NewInt(int64(d.Month-1)))
}

// floorQuotient is Ruby Integer#div: division that rounds toward negative
// infinity, which is what keeps an every-Nth cycle index correct for an `after`
// that sits BEFORE the anchor.
func floorQuotient(value, divisor *big.Int) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value, divisor, remainder)
	if remainder.Sign() != 0 && (remainder.Sign() < 0) != (divisor.Sign() < 0) {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}
