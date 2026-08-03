package recur

// The exact civil-time counterpart of NextDate, used by recurrence completion
// and by previews that have to agree with it.
//
// NextDate answers in DATES. That is the right answer for a projection, but not
// for a write: a candidate date can fail to exist as a local time (02:30 on a
// spring-forward morning), and whether a candidate is "still in the future"
// depends on the stamp's own boundary rather than on the calendar. So this path
// walks candidates, resolves each one against the reader's clock, and skips the
// ones that cannot be written.

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"tasks-go/internal/temporal"
)

// rollLimit bounds the candidate walk. Its iterations are for genuine skips —
// DST gaps and vetoes — because a stale catch-up series is fast-forwarded by
// plain date arithmetic before the walk begins.
const rollLimit = 1000

// Kind says which boundary a candidate is judged by, because an all-day
// deadline and an all-day available-from date do not release at the same
// instant.
type Kind int

const (
	// Deadline judges a candidate by when it stops being on time.
	Deadline Kind = iota
	// Scheduled judges a candidate by when it becomes available.
	Scheduled
)

// ErrNoValidOccurrence is the exhaustion of the candidate walk: every date the
// schedule offered was unwritable or vetoed.
var ErrNoValidOccurrence = errors.New("recurrence could not find a valid local date/time")

// NextTemporalDate is the date a stored recurrence rolls a stamp to. `veto` may
// be nil; when supplied it rejects a candidate the caller cannot accept (a
// paired field that would land in a DST gap, say) and the walk continues.
func NextTemporalDate(value string, stamp temporal.Value, kind Kind, context temporal.Context,
	veto func(temporal.Date) bool) (temporal.Date, error) {

	advance, candidate, requireFuture, err := temporalPlan(rubyStrip(value), stamp, context)
	if err != nil {
		return temporal.Date{}, err
	}

	for attempt := 0; attempt < rollLimit; attempt++ {
		if accepted, err := acceptable(stamp, candidate, kind, context, requireFuture, veto); err == nil && accepted {
			return candidate, nil
		}
		// A candidate naming no real local time advances to the following
		// occurrence rather than failing the roll.
		next, err := advance(candidate)
		if err != nil {
			return temporal.Date{}, err
		}
		candidate = next
	}
	return temporal.Date{}, ErrNoValidOccurrence
}

// acceptable reports whether a candidate can be written and is far enough
// ahead. An error means the candidate does not exist as a local time.
func acceptable(stamp temporal.Value, candidate temporal.Date, kind Kind, context temporal.Context,
	requireFuture bool, veto func(temporal.Date) bool) (bool, error) {

	candidateValue, err := stamp.WithDate(candidate)
	if err != nil {
		return false, err
	}
	instant, err := boundaryOf(candidateValue, kind, context)
	if err != nil {
		return false, err
	}
	if veto != nil && !veto(candidate) {
		return false, nil
	}
	if requireFuture && !instant.After(context.Now) {
		return false, nil
	}
	return true, nil
}

func boundaryOf(value temporal.Value, kind Kind, context temporal.Context) (time.Time, error) {
	if kind == Deadline {
		return value.DueBoundary(context)
	}
	return value.ReleaseInstant(context)
}

// temporalPlan is the first candidate, how to step past a rejected one, and
// whether the result must land strictly in the future. Both stored shapes share
// it so the walk above has one body.
func temporalPlan(s string, stamp temporal.Value, context temporal.Context) (
	func(temporal.Date) (temporal.Date, error), temporal.Date, bool, error) {

	if match := cookie.FindStringSubmatch(s); match != nil {
		count, _ := new(big.Int).SetString(match[2], 10)
		unit := match[3]
		advance := func(date temporal.Date) (temporal.Date, error) {
			return fromCivil(Step(toCivil(date), count, unit))
		}
		base := stamp.Date
		if match[1] == ".+" {
			base = localToday(stamp, context)
		}
		candidate, err := advance(base)
		if err != nil {
			return nil, temporal.Date{}, false, err
		}
		if match[1] == "++" {
			// Walk a stale catch-up series up to the current day with plain
			// date math first. The bounded loop above is for genuine skips; a
			// stamp years behind would otherwise exhaust it on hops that only
			// ever fail the future test. Stopping AT today rather than past it
			// is deliberate: a candidate landing on today can still be future
			// by its local time, which is the boundary comparison's call.
			today := localToday(stamp, context)
			for candidate.Before(today) {
				candidate, err = advance(candidate)
				if err != nil {
					return nil, temporal.Date{}, false, err
				}
			}
		}
		return advance, candidate, match[1] == "++", nil
	}

	parsed, ok := parseSchedule(s)
	if !ok {
		return nil, temporal.Date{}, false, fmt.Errorf("not a repeater cookie: %s", rubyInspect(s))
	}
	anchor := toCivil(stamp.Date)
	catchUp := parsed.prefix != "+"
	after := anchor
	if catchUp {
		if today := toCivil(localToday(stamp, context)); after.Before(today) {
			after = today
		}
	}
	advance := func(date temporal.Date) (temporal.Date, error) {
		next, err := occurrenceAfter(parsed, anchor, toCivil(date))
		if err != nil {
			return temporal.Date{}, err
		}
		return fromCivil(next)
	}
	first, err := occurrenceAfter(parsed, anchor, after)
	if err != nil {
		return nil, temporal.Date{}, false, err
	}
	candidate, err := fromCivil(first)
	if err != nil {
		return nil, temporal.Date{}, false, err
	}
	return advance, candidate, catchUp, nil
}

// localToday is the calendar day the stamp's OWN zone is on. A fixed stamp
// rolls by its own zone's clock, not by the reader's.
func localToday(stamp temporal.Value, context temporal.Context) temporal.Date {
	return temporal.DateOf(context.Now.In(stamp.EffectiveZone(context)))
}

func toCivil(date temporal.Date) CivilDate {
	return CivilDate{Year: big.NewInt(int64(date.Year)), Month: int(date.Month), Day: date.Day}
}

// fromCivil narrows a projection back to a storable date. A year outside the
// four-digit range is not a date this store can hold, and saying so here keeps
// the refusal at the boundary rather than in the file.
func fromCivil(date CivilDate) (temporal.Date, error) {
	if !date.Year.IsInt64() {
		return temporal.Date{}, fmt.Errorf("recurrence left the storable year range at %s", date)
	}
	year := date.Year.Int64()
	if year < 1 || year > 9999 {
		return temporal.Date{}, fmt.Errorf("recurrence left the storable year range at %s", date)
	}
	built, ok := temporal.NewDate(int(year), time.Month(date.Month), date.Day)
	if !ok {
		return temporal.Date{}, fmt.Errorf("recurrence produced the impossible date %s", date)
	}
	return built, nil
}
