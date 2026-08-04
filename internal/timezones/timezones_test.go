package timezones

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, identifier string) *time.Location {
	t.Helper()
	location, err := Load(identifier)
	if err != nil {
		t.Fatalf("Load(%q): %v", identifier, err)
	}
	return location
}

// The identifier gate is deliberately narrow: an abbreviation and a POSIX TZ
// string are not zones, however willing the platform is to accept them.
func TestGetAcceptsOnlyIANAIdentifiers(t *testing.T) {
	for _, identifier := range []string{"UTC", "Etc/UTC", "America/Los_Angeles", " Europe/London "} {
		if _, err := Get(identifier); err != nil {
			t.Fatalf("Get(%q): %v", identifier, err)
		}
	}
	rejected := map[string]string{
		"":    "time zone is required",
		"   ": "time zone is required",
		"PST": "not an IANA time-zone identifier",
		"GMT": "not an IANA time-zone identifier",
		// A POSIX TZ string carrying a slash clears the shape gate and is then
		// refused for naming no zone, which is the same answer by a longer road.
		"EST5EDT4,M3.2.0/02":  "unknown IANA time zone",
		"EST5EDT":             "not an IANA time-zone identifier",
		"Mars/Olympus":        "unknown IANA time zone",
		"America/Nowhere":     "unknown IANA time zone",
		"Europe/London/extra": "unknown IANA time zone",
	}
	for identifier, want := range rejected {
		_, err := Get(identifier)
		if err == nil {
			t.Fatalf("Get(%q) must be refused", identifier)
		}
		var zoneError *Error
		if !errors.As(err, &zoneError) {
			t.Fatalf("Get(%q) error type = %T, want *Error", identifier, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Get(%q) = %q, want it to mention %q", identifier, err, want)
		}
	}
}

// Get returns the canonical spelling with surrounding whitespace gone, which is
// what a stored value must be compared against.
func TestGetTrimsButDoesNotRewriteTheIdentifier(t *testing.T) {
	id, err := Get("  America/Denver  ")
	if err != nil || id != "America/Denver" {
		t.Fatalf("Get = %q (%v), want America/Denver", id, err)
	}
}

func TestUTCForResolvesAWallTimeInItsZone(t *testing.T) {
	cases := []struct {
		zone                           string
		year                           int
		month                          time.Month
		day, hour, minute, fold        int
		want                           time.Time
		wantAmbiguous, wantNonexistent bool
	}{
		{zone: "Etc/UTC", year: 2026, month: 7, day: 20, hour: 9,
			want: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
		{zone: "America/Los_Angeles", year: 2026, month: 7, day: 20, hour: 9,
			want: time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)},
		// A zone whose offset is not a whole number of hours.
		{zone: "Asia/Kathmandu", year: 2026, month: 7, day: 20, hour: 9,
			want: time.Date(2026, 7, 20, 3, 15, 0, 0, time.UTC)},
		// The repeated hour of a fall-back: fold picks which instant is meant.
		{zone: "America/Los_Angeles", year: 2026, month: 11, day: 1, hour: 1, minute: 30,
			want: time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC), wantAmbiguous: true},
		{zone: "America/Los_Angeles", year: 2026, month: 11, day: 1, hour: 1, minute: 30, fold: 1,
			want: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC), wantAmbiguous: true},
		// The hour a spring-forward skips exists nowhere.
		{zone: "America/Los_Angeles", year: 2026, month: 3, day: 8, hour: 2, minute: 30,
			wantNonexistent: true},
		// A southern-hemisphere transition, so the test is not only US-shaped.
		{zone: "Australia/Sydney", year: 2026, month: 10, day: 4, hour: 2, minute: 30,
			wantNonexistent: true},
		{zone: "Australia/Sydney", year: 2026, month: 4, day: 5, hour: 2, minute: 30,
			want: time.Date(2026, 4, 4, 15, 30, 0, 0, time.UTC), wantAmbiguous: true},
	}
	for _, tc := range cases {
		t.Run(tc.zone+"/"+tc.want.String(), func(t *testing.T) {
			location := load(t, tc.zone)
			got, err := UTCFor(tc.year, tc.month, tc.day, tc.hour, tc.minute, location, tc.fold)
			if tc.wantNonexistent {
				var gap *NonexistentLocalTime
				if !errors.As(err, &gap) {
					t.Fatalf("UTCFor = %s (%v), want a nonexistent-local-time refusal", got, err)
				}
				if !strings.Contains(err.Error(), "does not exist in") {
					t.Fatalf("refusal = %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("UTCFor: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("UTCFor = %s, want %s", got, tc.want)
			}
			if Ambiguous(tc.year, tc.month, tc.day, tc.hour, tc.minute, location) != tc.wantAmbiguous {
				t.Fatalf("Ambiguous = %v, want %v", !tc.wantAmbiguous, tc.wantAmbiguous)
			}
		})
	}
}

// The diagnostic names the first wall time the zone does observe, so a refusal
// tells the user what to type instead of only what not to.
func TestNonexistentLocalTimeCarriesTheFirstValidTime(t *testing.T) {
	location := load(t, "America/Los_Angeles")
	_, err := UTCFor(2026, 3, 8, 2, 30, location, 0)
	if err == nil || !strings.Contains(err.Error(), "first valid time is 03:00") {
		t.Fatalf("refusal = %v, want it to point at 03:00", err)
	}
	next, ok := FirstValidLocalAfter(2026, 3, 8, 2, 30, location)
	if !ok || next != "03:00" {
		t.Fatalf("FirstValidLocalAfter = %q (%v), want 03:00", next, ok)
	}
	// Past the last minute of the day there is nothing left to suggest.
	if _, ok := FirstValidLocalAfter(2026, 3, 8, 23, 59, location); ok {
		t.Fatal("there is no valid time after the last minute of a day")
	}
}

// A fold marker on a wall time that is NOT ambiguous is inert: there is only
// one instant to pick.
func TestFoldIsInertOnAnUnambiguousWallTime(t *testing.T) {
	location := load(t, "America/Los_Angeles")
	plain, err := UTCFor(2026, 7, 20, 9, 0, location, 0)
	if err != nil {
		t.Fatalf("fold 0: %v", err)
	}
	folded, err := UTCFor(2026, 7, 20, 9, 0, location, 1)
	if err != nil {
		t.Fatalf("fold 1: %v", err)
	}
	if !plain.Equal(folded) {
		t.Fatalf("fold moved an unambiguous time: %s vs %s", plain, folded)
	}
}

// EarliestOn is midnight in most zones and on most days — and is NOT midnight
// on a day whose midnight the zone skipped, which is the case it exists for.
func TestEarliestOnScansPastASkippedMidnight(t *testing.T) {
	cases := []struct {
		zone  string
		year  int
		month time.Month
		day   int
		want  time.Time
	}{
		{"Etc/UTC", 2026, 7, 20, time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)},
		{"America/Los_Angeles", 2026, 7, 20, time.Date(2026, 7, 20, 7, 0, 0, 0, time.UTC)},
		// A spring-forward day whose transition is at 02:00 still starts at
		// local midnight.
		{"America/Los_Angeles", 2026, 3, 8, time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)},
		// Santiago moves its clock forward AT midnight, so 2026-09-06 has no
		// 00:00 at all and the day begins at 01:00 local (04:00Z).
		{"America/Santiago", 2026, 9, 6, time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)},
		// Havana does the same on its spring-forward Sunday.
		{"America/Havana", 2026, 3, 8, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			got, err := EarliestOn(tc.year, tc.month, tc.day, load(t, tc.zone))
			if err != nil {
				t.Fatalf("EarliestOn: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("EarliestOn = %s, want %s", got, tc.want)
			}
		})
	}
}

// InstantsFor is the primitive the rest of the package rests on: zero instants
// across a gap, one ordinarily, two across an overlap — and always sorted.
func TestInstantsForCountsTheInstantsAWallTimeNames(t *testing.T) {
	location := load(t, "America/Los_Angeles")
	cases := []struct {
		month             time.Month
		day, hour, minute int
		want              int
	}{
		{7, 20, 9, 0, 1},
		{3, 8, 2, 30, 0},
		{3, 8, 3, 30, 1},
		{11, 1, 1, 30, 2},
		{11, 1, 2, 30, 1},
	}
	for _, tc := range cases {
		instants := InstantsFor(2026, tc.month, tc.day, tc.hour, tc.minute, location)
		if len(instants) != tc.want {
			t.Fatalf("%02d-%02d %02d:%02d named %d instants, want %d",
				tc.month, tc.day, tc.hour, tc.minute, len(instants), tc.want)
		}
		for index := 1; index < len(instants); index++ {
			if !instants[index-1].Before(instants[index]) {
				t.Fatal("InstantsFor must return its instants in order")
			}
		}
	}
}

func TestLocalTimeProjectsAnInstant(t *testing.T) {
	instant := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	local := LocalTime(instant, load(t, "America/Los_Angeles"))
	if local.Hour() != 9 || local.Day() != 20 {
		t.Fatalf("LocalTime = %s, want 09:00 on the 20th", local)
	}
}

// Detection precedence: the TZ variable, then the /etc/localtime symlink, then
// the UTC fallback — whose third return value is the warning Config carries.
func TestDetectPrecedenceAndFallbackWarning(t *testing.T) {
	zoneinfo := t.TempDir()
	denver := filepath.Join(zoneinfo, "zoneinfo", "America")
	if err := os.MkdirAll(denver, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(denver, "Denver")
	if err := os.WriteFile(real, []byte("not really zone data"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(zoneinfo, "localtime")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	unmarked := filepath.Join(zoneinfo, "elsewhere")
	if err := os.WriteFile(unmarked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unmarkedLink := filepath.Join(zoneinfo, "localtime-unmarked")
	if err := os.Symlink(unmarked, unmarkedLink); err != nil {
		t.Fatal(err)
	}

	plainFile := filepath.Join(zoneinfo, "localtime-plain")
	if err := os.WriteFile(plainFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		tz         string
		localtime  string
		wantZone   string
		wantSource string
		wantWarn   bool
	}{
		{"TZ wins", "Europe/London", link, "Europe/London", "TZ env", false},
		{"an unusable TZ falls back rather than reading the host", "PST", link, Fallback, "UTC fallback", true},
		{"an unknown TZ falls back", "Mars/Olympus", link, Fallback, "UTC fallback", true},
		{"the host symlink is second", "", link, "America/Denver", "host /etc/localtime", false},
		{"a symlink with no zoneinfo marker falls back", "", unmarkedLink, Fallback, "UTC fallback", true},
		{"a plain file is not a zone", "", plainFile, Fallback, "UTC fallback", true},
		{"a missing file falls back", "", filepath.Join(zoneinfo, "absent"), Fallback, "UTC fallback", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			zone, source, warned := Detect(tc.tz, tc.localtime)
			if zone != tc.wantZone || source != tc.wantSource || warned != tc.wantWarn {
				t.Fatalf("Detect = (%q, %q, %v), want (%q, %q, %v)",
					zone, source, warned, tc.wantZone, tc.wantSource, tc.wantWarn)
			}
		})
	}
}

// Whatever Detect answers must itself be loadable, or the fallback warning
// would be the only thing standing between a bad host and a crash.
func TestDetectAlwaysAnswersALoadableZone(t *testing.T) {
	for _, tz := range []string{"", "PST", "Europe/London", "Mars/Olympus", "  "} {
		zone, _, _ := Detect(tz, filepath.Join(t.TempDir(), "absent"))
		if _, err := Load(zone); err != nil {
			t.Fatalf("Detect(%q) answered %q, which does not load: %v", tz, zone, err)
		}
	}
}

func TestTZDBVersionSelfReports(t *testing.T) {
	if version := TZDBVersion(); !strings.Contains(version, "Zoneinfo DataSource") {
		t.Fatalf("TZDBVersion = %q", version)
	}
}
