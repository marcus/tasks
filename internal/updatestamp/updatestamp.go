// Package updatestamp is the per-record last-write stamp: one sortable token
// that keeps the timestamp and the device tiebreaker from ever drifting apart.
// It is the Go counterpart of lib/tasks/update_stamp.rb.
//
// The whole value object lives here — validation, the ordering key, comparison,
// and formatting — because the stamp is what decides last-write-wins when two
// devices edited the same record. A second spelling of "which stamp is newer"
// living in the merge path would be a second answer waiting to diverge.
package updatestamp

import (
	"regexp"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/determinism"
)

// Slug reduces a hostname to its first alphanumeric token. Hostnames commonly
// look like Marcus-MBP.local; the first token is stable and short while still
// letting an explicit TASKS_DEVICE disambiguate similar machines.
func Slug(value string) string {
	head, _, _ := strings.Cut(strings.ToLower(value), ".")
	token := strings.Builder{}
	for index := 0; index < len(head); index++ {
		char := head[index]
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			token.WriteByte(char)
			continue
		}
		if token.Len() > 0 {
			break
		}
	}
	if token.Len() == 0 {
		return "device"
	}
	return token.String()
}

// Device is the device slug for this process: TASKS_DEVICE when it is set to
// anything but blank, otherwise the hostname — resolved through Determinism so
// that one pin covers both hostname consumers.
func Device(env determinism.Env) string {
	if override := env.Get(determinism.NameDevice); strings.TrimSpace(override) != "" {
		return Slug(override)
	}
	return Slug(determinism.Hostname(env)())
}

// valuePattern is UpdateStamp::VALUE_RE: one sortable token that keeps the
// timestamp and the device tiebreaker from drifting apart.
var valuePattern = regexp.MustCompile(`\A(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)#([a-z0-9]+)\z`)

// Valid reports whether a stored `updated` value is a well-formed stamp: an
// RFC3339 UTC timestamp naming a real instant, then the device slug.
func Valid(value string) bool {
	match := valuePattern.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	_, err := time.Parse("2006-01-02T15:04:05Z", match[1])
	return err == nil
}

// Key is the ordering key of a stamp: its timestamp half and its device half,
// or ok=false when the value is not a stamp at all. The two are compared in
// that order, which is why the stored spelling is one token — sorting the file
// by `updated` and sorting it by (timestamp, device) are the same sort.
func Key(value string) (timestamp, device string, ok bool) {
	if !Valid(value) {
		return "", "", false
	}
	timestamp, device, _ = strings.Cut(value, "#")
	return timestamp, device, true
}

// Compare orders two stamps the way UpdateStamp.compare does: -1, 0, or 1.
//
// An unparseable or absent stamp sorts BEFORE every real one, and two of them
// compare equal. That is the rule last-write-wins depends on: a record whose
// stamp a hand edit destroyed must never win against a record that carries a
// real one, and it must not oscillate when neither side has one.
func Compare(left, right string) int {
	leftTime, leftDevice, leftOK := Key(left)
	rightTime, rightDevice, rightOK := Key(right)
	switch {
	case !leftOK && !rightOK:
		return 0
	case !leftOK:
		return -1
	case !rightOK:
		return 1
	}
	if leftTime != rightTime {
		return strings.Compare(leftTime, rightTime)
	}
	return strings.Compare(leftDevice, rightDevice)
}

// Max is the later of two stamps, preferring the LEFT one on a tie — the same
// bias Ruby's `compare(left, right).negative? ? right : left` has, so a merge
// that finds two equal stamps keeps the value it already held.
func Max(left, right string) string {
	if Compare(left, right) < 0 {
		return right
	}
	return left
}

// Format renders the stamp a writer stores: the instant in UTC, then the device
// slug. Ruby additionally raises on an empty slug; Slug cannot return one (it
// falls back to "device"), so the guard is unreachable in both languages and is
// not reproduced as an error return.
func Format(instant time.Time, device string) string {
	return instant.UTC().Format("2006-01-02T15:04:05Z") + "#" + Slug(device)
}
