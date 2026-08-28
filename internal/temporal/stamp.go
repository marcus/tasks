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
// literal Z. It is what record.DelegationStamp writes and what JSON emits.
const StampLayout = "2006-01-02T15:04:05Z"

// ParseStamp reads a stored instant. The canonical spelling is StampLayout;
// RFC 3339 with an explicit offset is accepted too, because a hand-edited file
// is still a file we would rather render than refuse. Anything else reports
// false, and callers print what the record said rather than dropping the field.
func ParseStamp(stored string) (time.Time, bool) {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		return time.Time{}, false
	}
	if instant, err := time.Parse(StampLayout, trimmed); err == nil {
		return instant.UTC(), true
	}
	if instant, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return instant.UTC(), true
	}
	return time.Time{}, false
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
// A stamp this build cannot parse comes back UNCHANGED. A field the reader can
// see and question is worth more than a field that quietly disappeared because
// something hand-edited it into a shape the parser did not know.
func (c Context) StampLabel(stored string) string {
	instant, ok := ParseStamp(stored)
	if !ok || c.Timezone == nil {
		return stored
	}
	local := instant.In(c.Timezone)
	return fmt.Sprintf("%s %02d-%02d %s",
		strings.ToLower(local.Weekday().String()[:3]),
		int(local.Month()), local.Day(), c.Clock(local.Hour(), local.Minute()))
}
