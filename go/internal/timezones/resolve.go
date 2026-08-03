package timezones

import (
	"fmt"
	"sort"
	"time"
)

// NonexistentLocalTime is Tasks::Timezones::NonexistentLocalTime: a wall time
// the zone skips over, such as 02:30 on a spring-forward morning.
type NonexistentLocalTime struct{ Message string }

func (e *NonexistentLocalTime) Error() string { return e.Message }

// Load resolves an identifier to a location. It goes through Get first, so the
// narrow IANA gate applies here too and a POSIX TZ string cannot slip past.
func Load(identifier string) (*time.Location, error) {
	id, err := Get(identifier)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(id)
	if err != nil {
		return nil, &Error{fmt.Sprintf("unknown IANA time zone %q", id)}
	}
	return location, nil
}

// InstantsFor is TZInfo's periods_for_local, expressed as the UTC instants a
// wall time maps to: none across a spring-forward gap, one ordinarily, two
// across a fall-back overlap.
//
// Go's time.Date cannot answer this — it silently picks one instant for an
// ambiguous wall time and an unspecified one for a nonexistent one — so the
// candidate offsets are probed either side of the transition and each is kept
// only when it round-trips back to the wall time that was asked for.
func InstantsFor(year int, month time.Month, day, hour, minute int, location *time.Location) []time.Time {
	wall := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	seen := map[int64]time.Time{}
	for _, probe := range []time.Time{wall.Add(-26 * time.Hour), wall, wall.Add(26 * time.Hour)} {
		_, offset := probe.In(location).Zone()
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if local.Year() == year && local.Month() == month && local.Day() == day &&
			local.Hour() == hour && local.Minute() == minute {
			seen[candidate.Unix()] = candidate
		}
	}
	instants := make([]time.Time, 0, len(seen))
	for _, instant := range seen {
		instants = append(instants, instant)
	}
	sort.Slice(instants, func(left, right int) bool { return instants[left].Before(instants[right]) })
	return instants
}

// UTCFor resolves a local wall time in a zone to its UTC instant. `fold` picks
// the SECOND instant of an overlap, matching the stored `fold: 1` marker.
func UTCFor(year int, month time.Month, day, hour, minute int, location *time.Location, fold int) (time.Time, error) {
	instants := InstantsFor(year, month, day, hour, minute, location)
	if len(instants) == 0 {
		date := fmt.Sprintf("%04d-%02d-%02d", year, int(month), day)
		local := fmt.Sprintf("%02d:%02d", hour, minute)
		hint := ""
		if next, ok := FirstValidLocalAfter(year, month, day, hour, minute, location); ok {
			hint = "; first valid time is " + next
		}
		return time.Time{}, &NonexistentLocalTime{
			fmt.Sprintf("%s %s does not exist in %s%s", date, local, location.String(), hint),
		}
	}
	if fold == 1 {
		return instants[len(instants)-1].UTC(), nil
	}
	return instants[0].UTC(), nil
}

// EarliestOn is the first instant of a calendar date in a zone. It scans
// forward a minute at a time because midnight itself can be skipped: some
// zones have moved their clock forward at 00:00 local.
func EarliestOn(year int, month time.Month, day int, location *time.Location) (time.Time, error) {
	for minute := 0; minute < 1440; minute++ {
		instant, err := UTCFor(year, month, day, minute/60, minute%60, location, 0)
		if err == nil {
			return instant, nil
		}
	}
	return time.Time{}, &NonexistentLocalTime{
		fmt.Sprintf("calendar date %04d-%02d-%02d does not exist in %s", year, int(month), day, location.String()),
	}
}

// Ambiguous reports a wall time that names TWO instants — the hour a fall-back
// transition repeats. It is the question `fold` answers, and the only way a
// caller can know that answering it matters.
func Ambiguous(year int, month time.Month, day, hour, minute int, location *time.Location) bool {
	return len(InstantsFor(year, month, day, hour, minute, location)) > 1
}

// LocalTime projects an instant into a zone.
func LocalTime(instant time.Time, location *time.Location) time.Time {
	return instant.UTC().In(location)
}

// FirstValidLocalAfter is the hint a nonexistent-time diagnostic carries: the
// next wall time on the same date that the zone does observe.
func FirstValidLocalAfter(year int, month time.Month, day, hour, minute int, location *time.Location) (string, bool) {
	for candidate := hour*60 + minute + 1; candidate < 1440; candidate++ {
		if len(InstantsFor(year, month, day, candidate/60, candidate%60, location)) > 0 {
			return fmt.Sprintf("%02d:%02d", candidate/60, candidate%60), true
		}
	}
	return "", false
}
