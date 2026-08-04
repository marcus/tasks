package promptfacts

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"tasks-go/internal/config"
)

// pinned is the clock every rendering test reads from, so the block is a fixed
// document rather than whatever the wall clock said.
func pinned() time.Time { return time.Date(2026, 7, 15, 8, 41, 0, 0, time.UTC) }

func testSources(hostname string) Sources {
	return Sources{
		Clock:    pinned,
		Hostname: func() (string, error) { return hostname, nil },
	}
}

func TestResolveDefaultsDatetimeAndHostnameOn(t *testing.T) {
	want := map[string]bool{"datetime": true, "hostname": true}
	if got := Resolve(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve(nil) = %v, want %v", got, want)
	}
}

func TestResolveHonorsOverrides(t *testing.T) {
	got := Resolve(map[string]bool{"datetime": false, "hostname": true})
	want := map[string]bool{"datetime": false, "hostname": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve = %v, want %v", got, want)
	}
}

func TestResolveIgnoresUnknownOverrideKeys(t *testing.T) {
	got := Resolve(map[string]bool{"weather": true, "datetime": false})
	if _, present := got["weather"]; present {
		t.Fatalf("an unregistered fact must not reach the resolved map: %v", got)
	}
	if got["datetime"] {
		t.Fatalf("datetime override lost: %v", got)
	}
}

func TestFormatDatetimeIsAgentFriendly(t *testing.T) {
	out := FormatDatetime(pinned())
	if !strings.HasPrefix(out, "2026-07-15 Wed 08:41 ") {
		t.Fatalf("FormatDatetime = %q", out)
	}
	fields := strings.Fields(out)
	if fields[len(fields)-1] == "" {
		t.Fatalf("no timezone abbreviation in %q", out)
	}
}

// A zone whose offset has no abbreviation must still render a last field: Ruby's
// %Z prints the numeric offset there, and an agent reading "09:00" needs to know
// which 09:00 it is.
func TestFormatDatetimeCarriesAZoneEvenWithoutAnAbbreviation(t *testing.T) {
	zone := time.FixedZone("", 5*3600+45*60)
	out := FormatDatetime(time.Date(2026, 7, 15, 8, 41, 0, 0, zone))
	if !strings.HasPrefix(out, "2026-07-15 Wed 08:41 ") {
		t.Fatalf("FormatDatetime = %q", out)
	}
	if strings.TrimSpace(strings.TrimPrefix(out, "2026-07-15 Wed 08:41")) == "" {
		t.Fatalf("no zone field in %q", out)
	}
}

func TestRenderIncludesEnabledFacts(t *testing.T) {
	block := Render(map[string]bool{"datetime": true, "hostname": true}, testSources("test-host.local"))
	if !strings.HasPrefix(block, "Current environment:\n") {
		t.Fatalf("block = %q", block)
	}
	if !strings.Contains(block, "- datetime: 2026-07-15 Wed 08:41") {
		t.Fatalf("datetime missing from %q", block)
	}
	if !strings.Contains(block, "- hostname: test-host.local") {
		t.Fatalf("hostname missing from %q", block)
	}
	// datetime before hostname — registry order, not map order.
	if strings.Index(block, "datetime") > strings.Index(block, "hostname") {
		t.Fatalf("registry order lost: %q", block)
	}
}

func TestRenderOmitsDisabledFacts(t *testing.T) {
	block := Render(map[string]bool{"datetime": false, "hostname": true}, Sources{
		Clock:    func() time.Time { panic("a disabled fact must not be rendered") },
		Hostname: func() (string, error) { return "only-host", nil },
	})
	if block != "Current environment:\n- hostname: only-host" {
		t.Fatalf("block = %q", block)
	}
}

func TestRenderEmptyWhenAllOff(t *testing.T) {
	if block := Render(map[string]bool{"datetime": false, "hostname": false}, testSources("h")); block != "" {
		t.Fatalf("block = %q, want empty", block)
	}
}

// A flaky future source (weather, a calendar) must cost its own line and never
// the run.
func TestProviderExceptionOmitsThatLineOnly(t *testing.T) {
	block := Render(map[string]bool{"datetime": true, "hostname": true}, Sources{
		Clock:    pinned,
		Hostname: func() (string, error) { return "", errors.New("boom") },
	})
	if block != "Current environment:\n- datetime: 2026-07-15 Wed 08:41 UTC" {
		t.Fatalf("block = %q", block)
	}
}

func TestBlankProviderValueOmitsThatLine(t *testing.T) {
	block := Render(map[string]bool{"datetime": true, "hostname": true}, testSources("  "))
	if strings.Contains(block, "hostname") {
		t.Fatalf("blank hostname reached the block: %q", block)
	}
	if !strings.Contains(block, "datetime") {
		t.Fatalf("datetime lost: %q", block)
	}
}

// A hostname with surrounding whitespace renders trimmed, not padded — the line
// is `- name: value` with one space, always.
func TestProviderValueIsStripped(t *testing.T) {
	block := Render(map[string]bool{"hostname": true}, testSources("  padded-host \n"))
	if !strings.Contains(block, "- hostname: padded-host\n") && !strings.HasSuffix(block, "- hostname: padded-host") {
		t.Fatalf("block = %q", block)
	}
}

// A nil map is "nothing resolved", which is `paths.prompt_facts || resolve`.
func TestRenderWithNoResolvedMapFallsBackToTheDefaults(t *testing.T) {
	block := Render(nil, testSources("fallback-host"))
	if !strings.Contains(block, "- datetime: ") || !strings.Contains(block, "- hostname: fallback-host") {
		t.Fatalf("block = %q", block)
	}
}

func TestParseToggle(t *testing.T) {
	for _, spelling := range []string{"on", "TRUE", "1", " on "} {
		value, ok := ParseToggle(spelling)
		if !ok || !value {
			t.Fatalf("ParseToggle(%q) = %v, %v; want true, true", spelling, value, ok)
		}
	}
	for _, spelling := range []string{"off", "False", "0"} {
		value, ok := ParseToggle(spelling)
		if !ok || value {
			t.Fatalf("ParseToggle(%q) = %v, %v; want false, true", spelling, value, ok)
		}
	}
	for _, spelling := range []string{"maybe", "", "  "} {
		if _, ok := ParseToggle(spelling); ok {
			t.Fatalf("ParseToggle(%q) claimed to name a state", spelling)
		}
	}
}

func TestFactNameVocabulary(t *testing.T) {
	for _, name := range []string{"datetime", "host-name", "weather_2"} {
		if !FactName.MatchString(name) {
			t.Fatalf("%q should be a legal fact name", name)
		}
	}
	for _, name := range []string{"", "2fast", "Datetime", "a.b", "a b"} {
		if FactName.MatchString(name) {
			t.Fatalf("%q should not be a legal fact name", name)
		}
	}
}

// internal/config owns the resolution `tasks config` reports and carries its own
// copy of the fact set. Two copies that disagree would mean `tasks config` says
// a fact is on that the prompt never renders — pin them together rather than
// leave the duplication to care.
func TestConfigAndRegistryAgreeOnTheFactSet(t *testing.T) {
	if !reflect.DeepEqual(Defaults(), config.PromptFactDefaults) {
		t.Fatalf("promptfacts.Defaults() = %v, config.PromptFactDefaults = %v",
			Defaults(), config.PromptFactDefaults)
	}
	// And the resolution agrees for every override shape, not only the default.
	for _, overrides := range []map[string]bool{
		nil,
		{"datetime": false},
		{"hostname": false, "datetime": false},
		{"weather": true},
	} {
		if !reflect.DeepEqual(Resolve(overrides), config.ResolvePromptFacts(overrides)) {
			t.Fatalf("resolution disagrees for %v: %v vs %v",
				overrides, Resolve(overrides), config.ResolvePromptFacts(overrides))
		}
	}
}

// The default providers are the real ones: production must not silently render a
// zero clock or an empty hostname because a caller passed no Sources.
func TestZeroSourcesUseTheRealClockAndHostname(t *testing.T) {
	block := Render(nil, Sources{})
	if !strings.Contains(block, "- datetime: ") {
		t.Fatalf("no datetime from the default clock: %q", block)
	}
	// A machine with no hostname is possible; a machine with a zero-valued clock
	// is not, so only the datetime line is asserted unconditionally.
	if strings.Contains(block, "- datetime: 0001-01-01") {
		t.Fatalf("the default clock is not the real one: %q", block)
	}
}
