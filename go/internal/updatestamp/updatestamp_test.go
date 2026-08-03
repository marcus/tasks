package updatestamp

import (
	"testing"
	"time"

	"tasks-go/internal/determinism"
)

// The invariants test/test_update_stamp.rb states about the value object
// itself. The store-level halves of that file (patch stamps only the touched
// record, undo restores bytes without re-stamping) belong to the write wave;
// what lives here is the one definition of "which stamp is newer" that
// last-write-wins will be built on.

const (
	stamp    = "2026-07-16T14:03:11Z#home"
	oldStamp = "2026-07-15T09:00:00Z#work"
)

// test_clock_device_and_hostname_slug_are_deterministic
func TestClockDeviceAndHostnameSlugAreDeterministic(t *testing.T) {
	if got := Slug("Marcus-MBP.local"); got != "marcus" {
		t.Fatalf("Slug = %q, want %q", got, "marcus")
	}
	env := determinism.Env{"TASKS_DEVICE": "Home2", "TASKS_PIN_HOSTNAME": "ignored"}
	if got := Device(env); got != "home2" {
		t.Fatalf("Device = %q, want %q", got, "home2")
	}
	if got := Format(time.Date(2026, 7, 16, 14, 3, 11, 0, time.UTC), "HOME"); got != stamp {
		t.Fatalf("Format = %q, want %q", got, stamp)
	}
	if Compare("2026-07-16T14:03:11Z#home", "2026-07-16T14:03:11Z#work") >= 0 {
		t.Fatal("the device half must break a timestamp tie, home before work")
	}
}

func TestSlugBoundaries(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Marcus-MBP.local", "marcus"},
		{"home2", "home2"},
		{"HOME2", "home2"},
		{"---.local", "device"},   // no alphanumeric token at all
		{"", "device"},            // nothing to slug
		{".leading", "device"},    // the first DNS label is empty
		{"a1-b2.example", "a1"},   // stops at the first non-alphanumeric
		{"-lead9ing", "lead9ing"}, // a leading separator is skipped, not fatal
	} {
		if got := Slug(tc.in); got != tc.want {
			t.Fatalf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TASKS_DEVICE outranks the hostname, and a blank override is not an override —
// otherwise an exported-but-empty variable would silently rename the device.
func TestDevicePrefersTheOverrideThenTheHostnamePin(t *testing.T) {
	if got := Device(determinism.Env{"TASKS_PIN_HOSTNAME": "Fixture-Host.local"}); got != "fixture" {
		t.Fatalf("Device = %q, want the pinned hostname's slug", got)
	}
	env := determinism.Env{"TASKS_DEVICE": "   ", "TASKS_PIN_HOSTNAME": "fixture-host"}
	if got := Device(env); got != "fixture" {
		t.Fatalf("Device = %q, want a blank override to be ignored", got)
	}
}

func TestFormatIsUTCWhateverZoneTheClockReads(t *testing.T) {
	zone := time.FixedZone("plus7", 7*3600)
	instant := time.Date(2026, 7, 16, 21, 3, 11, 0, zone)
	if got := Format(instant, "home"); got != stamp {
		t.Fatalf("Format = %q, want %q — the stamp is always UTC", got, stamp)
	}
	// Sub-second precision is dropped rather than rounded: the stored spelling
	// has second resolution, and a rounded stamp could name a future instant.
	precise := time.Date(2026, 7, 16, 14, 3, 11, 999_999_999, time.UTC)
	if got := Format(precise, "home"); got != stamp {
		t.Fatalf("Format = %q, want %q", got, stamp)
	}
}

func TestKeySplitsOnlyValidStamps(t *testing.T) {
	timestamp, device, ok := Key(stamp)
	if !ok || timestamp != "2026-07-16T14:03:11Z" || device != "home" {
		t.Fatalf("Key = %q/%q/%v", timestamp, device, ok)
	}
	for _, invalid := range []string{"yesterday#home", "", "2026-07-16T14:03:11Z", "2026-07-16T14:03:11Z#HOME"} {
		if _, _, ok := Key(invalid); ok {
			t.Fatalf("Key(%q) reported a key for a value Valid rejects", invalid)
		}
	}
}

// The ordering an eventual last-write-wins merge rests on. An unparseable stamp
// losing to a real one is the load-bearing case: a hand edit that destroyed a
// stamp must not be able to defeat a record that still carries one.
func TestCompareAndMaxOrderTimestampThenDevice(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right string
		want        int
	}{
		{"newer timestamp wins", stamp, oldStamp, 1},
		{"older timestamp loses", oldStamp, stamp, -1},
		{"identical stamps tie", stamp, stamp, 0},
		{"device breaks a timestamp tie", "2026-07-16T14:03:11Z#home", "2026-07-16T14:03:11Z#work", -1},
		{"invalid loses to valid", "yesterday#home", oldStamp, -1},
		{"valid beats invalid", oldStamp, "yesterday#home", 1},
		{"two invalid stamps tie", "yesterday#home", "", 0},
		{"absent ties absent", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.left, tc.right); got != tc.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
			}
		})
	}

	if got := Max(oldStamp, stamp); got != stamp {
		t.Fatalf("Max = %q, want the later stamp", got)
	}
	if got := Max(stamp, oldStamp); got != stamp {
		t.Fatalf("Max = %q, want the later stamp whichever side it arrives on", got)
	}
	// A tie keeps the LEFT value, so a merge that finds two equal stamps holds
	// on to the record it already had rather than churning bytes.
	if got := Max(stamp, "2026-07-16T14:03:11Z#home"); got != stamp {
		t.Fatalf("Max on a tie = %q, want the left value", got)
	}
	if got := Max("", oldStamp); got != oldStamp {
		t.Fatalf("Max = %q, want a real stamp to beat an absent one", got)
	}
}

// Compare must be a total order over whatever a file can hold: sorting a slice
// of stamps can never depend on the order it arrived in.
func TestCompareIsAntisymmetricAndTransitive(t *testing.T) {
	values := []string{
		"", "yesterday#home", oldStamp, stamp,
		"2026-07-16T14:03:11Z#home", "2026-07-16T14:03:11Z#work", "2026-07-16T14:03:12Z#a",
	}
	for _, left := range values {
		for _, right := range values {
			if Compare(left, right) != -Compare(right, left) {
				t.Fatalf("Compare(%q,%q) is not antisymmetric", left, right)
			}
			for _, third := range values {
				if Compare(left, right) <= 0 && Compare(right, third) <= 0 && Compare(left, third) > 0 {
					t.Fatalf("Compare is not transitive across %q, %q, %q", left, right, third)
				}
			}
		}
	}
}

// A stamp this package formats is always a stamp it accepts. If that ever
// stopped holding, every write would fail its own post-write check.
func TestFormattedStampsAlwaysValidate(t *testing.T) {
	instant := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, device := range []string{"HOME", "Marcus-MBP.local", "---", "", "9", "über"} {
		value := Format(instant, device)
		if !Valid(value) {
			t.Fatalf("Format(%q) produced %q, which Valid rejects", device, value)
		}
	}
}

func TestValidPinsTheStoredGrammar(t *testing.T) {
	for _, value := range []string{stamp, "0001-01-01T00:00:00Z#a", "2026-06-01T00:00:00Z#device9"} {
		if !Valid(value) {
			t.Fatalf("Valid(%q) = false", value)
		}
	}
	for _, value := range []string{
		"2026-07-16T14:03:11Z#HOME",  // the slug is lowercase-only
		"2026-07-16T14:03:11Z#ho me", // and has no whitespace
		"2026-07-16T14:03:11+00:00#home",
		"2026-07-16 14:03:11Z#home",
		"2026-07-16T14:03:11Z#",
		"2026-07-16T14:03:11Z",
		" 2026-07-16T14:03:11Z#home",
		"",
	} {
		if Valid(value) {
			t.Fatalf("Valid(%q) = true", value)
		}
	}
}
