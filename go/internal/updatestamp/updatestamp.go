// Package updatestamp carries the device half of the per-record last-write
// stamp. It is the Go counterpart of lib/tasks/update_stamp.rb, ported here
// only as far as the read path and the probe need it: the device slug, which
// is one of the two consumers TASKS_PIN_HOSTNAME has to reach.
package updatestamp

import (
	"regexp"
	"strings"
	"time"

	"tasks-go/internal/determinism"
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
