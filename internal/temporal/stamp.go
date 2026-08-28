package temporal

// Stored instants — the `delegation.at` transition stamp and everything else a
// record writes as an absolute moment — are UTC RFC 3339 with second
// precision. That is the right shape on disk and in JSON: one spelling, no
// reader's zone baked into the file.
//
// It is the wrong shape to READ. "2026-08-27T18:03:08Z" makes "when did I hand
// this off?" a timezone conversion the person has to do in their head, while
// every other date on the same screen is already projected. So the projection
// lives here, beside the one that dates and deadlines use, and it takes the
// same Context: the reader's zone and clock format decide the rendering, the
// stored value never moves.

import (
	"fmt"
	"strings"
	"time"
)

// StampLayout is the stored spelling of an instant: UTC, second precision,
// literal Z — the shape `check` requires of `delegation.at` and the one every
// writer in the tree emits. (record spells the same literal itself rather than
// importing this package: the schema layer has no other reason to depend on
// projection, and one string constant is not a reason to give it one.)
const StampLayout = "2006-01-02T15:04:05Z"

// ParseStamp reads a stored instant, and it accepts ONLY the stored spelling.
//
// Being lenient here — taking RFC 3339 with an explicit offset, say — would
// launder a record `check` calls invalid: the field would render as a healthy
// local time on every human surface while validation refused the file, which is
// the worst of both answers. Nothing in the tree writes those forms, so the
// leniency bought nothing and hid something. Refusing means "print as stored"
// fires exactly when the record is wrong, which is when a person should see it.
func ParseStamp(stored string) (time.Time, bool) {
	instant, err := time.Parse(StampLayout, stored)
	// The ROUND TRIP is the real test. Go's parser tolerates a fractional second
	// the layout never named, so "…:44.500Z" would slip through a plain parse —
	// and that is one of the shapes `check` refuses. Formatting back and
	// comparing is precisely the predicate record.DelegationTimestamp applies.
	if err != nil || instant.UTC().Format(StampLayout) != stored {
		return time.Time{}, false
	}
	return instant.UTC(), true
}

// ClockLabel renders a wall time in the configured format: 24-hour zero-padded,
// or 12-hour with an a/p suffix and no leading zero. It is the codebase's one
// answer to "what does time_format mean", so a clock reads the same on every
// human surface.
func ClockLabel(hour, minute, timeFormat int) string {
	if timeFormat == 24 {
		return fmt.Sprintf("%02d:%02d", hour, minute)
	}
	display := hour % 12
	if display == 0 {
		display = 12
	}
	suffix := "p"
	if hour < 12 {
		suffix = "a"
	}
	return fmt.Sprintf("%d:%02d%s", display, minute, suffix)
}

// Clock is ClockLabel in the reader's own format.
func (c Context) Clock(hour, minute int) string { return ClockLabel(hour, minute, c.TimeFormat) }

// StampLabel is a stored instant as the reader sees it: "thu 08-27 11:03a", or
// "thu 08-27 11:03" under time_format 24. The weekday leads because a bare
// month-day does not say whether the handoff was a working day, which is the
// first thing anyone asks of a delegation clock.
//
// A stamp from ANOTHER YEAR carries that year — "sun 2023-08-27 11:03a". Terse
// is right for the common case, where everything on screen is within weeks; it
// is wrong the moment a two-year-old handoff renders byte-identical to
// yesterday's, which is precisely when the reader is being misled rather than
// merely under-informed.
//
// It does NOT disambiguate a DST fall-back fold: the two 01:30s of a repeated
// hour render the same, without the "(later fold)" marker a stored value gets.
// A stored value's fold is a CHOICE the user made and a renderer must not eat;
// an instant's fold is an accident of the calendar, and one second of ambiguity
// once a year is not worth a marker on every delegation line. Anyone who needs
// the exact instant has it in `--json`.
//
// A stamp this build cannot parse comes back UNCHANGED. A field the reader can
// see and question is worth more than a field that quietly disappeared because
// something hand-edited it into a shape the parser did not know.
func (c Context) StampLabel(stored string) string {
	instant, ok := ParseStamp(stored)
	if !ok || c.Timezone == nil {
		return stored
	}
	local := instant.In(c.Timezone)
	date := fmt.Sprintf("%02d-%02d", int(local.Month()), local.Day())
	if local.Year() != c.LocalNow().Year() {
		date = fmt.Sprintf("%04d-%s", local.Year(), date)
	}
	return fmt.Sprintf("%s %s %s", strings.ToLower(local.Weekday().String()[:3]),
		date, c.Clock(local.Hour(), local.Minute()))
}
