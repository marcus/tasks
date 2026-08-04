package determinism

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The seams test/test_determinism.rb covers, minus the halves that belong to
// the store and the CLI (a pinned run's identical bytes, the store's own id
// mint). Two properties matter equally here: a pin actually reaches the value
// an adapter injects, and an UNSET pin is invisible — "adding a seam is in
// scope, changing behavior is not" is only true if the default path is
// untouched.

// -- clock -------------------------------------------------------------------

// test_now_is_nil_when_unpinned
func TestNowIsUnsetWhenUnpinned(t *testing.T) {
	if _, ok, err := Now(Env{}); ok || err != nil {
		t.Fatalf("Now = %v/%v, want unset and no error", ok, err)
	}
	if Clock(Env{}) != nil {
		t.Fatal("Clock should be nil when nothing is pinned")
	}
}

// test_now_parses_an_iso8601_instant_as_utc
func TestNowParsesAnISO8601InstantAsUTC(t *testing.T) {
	instant, ok, err := Now(Env{NameNow: "2026-03-14T15:09:26Z"})
	if err != nil || !ok {
		t.Fatalf("Now errored: %v", err)
	}
	if want := time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC); !instant.Equal(want) {
		t.Fatalf("Now = %s, want %s", instant, want)
	}
	if instant.Location() != time.UTC {
		t.Fatalf("Now location = %s, want UTC", instant.Location())
	}
}

// test_now_converts_an_offset_instant_to_utc
func TestNowConvertsAnOffsetInstantToUTC(t *testing.T) {
	instant, _, err := Now(Env{NameNow: "2026-03-14T15:09:26-07:00"})
	if err != nil {
		t.Fatalf("Now errored: %v", err)
	}
	if want := time.Date(2026, 3, 14, 22, 9, 26, 0, time.UTC); !instant.Equal(want) {
		t.Fatalf("Now = %s, want %s", instant, want)
	}
}

// test_clock_returns_the_same_instant_every_call
func TestClockReturnsTheSameInstantEveryCall(t *testing.T) {
	clock := Clock(Env{NameNow: "2026-03-14T15:09:26Z"})
	if clock == nil {
		t.Fatal("Clock should be present when the pin is set")
	}
	if first, second := clock(), clock(); !first.Equal(second) {
		t.Fatalf("clock drifted: %s then %s", first, second)
	}
}

// test_now_raises_rather_than_falling_back_to_the_wall_clock. A silent fallback
// would produce a run that LOOKS reproducible and is not — the harness's worst
// failure mode, because every downstream comparison would still pass.
func TestNowErrorsRatherThanFallingBackToTheWallClock(t *testing.T) {
	_, ok, err := Now(Env{NameNow: "yesterday"})
	if err == nil {
		t.Fatal("an unparseable pin must be an error, not a wall-clock fallback")
	}
	if ok {
		t.Fatal("an unparseable pin must not report itself as applied")
	}
	if !strings.Contains(err.Error(), NameNow) {
		t.Fatalf("error %q should name the pin the operator set", err)
	}
	// NowForAdapter propagates it rather than quietly taking a later branch.
	if _, err := NowForAdapter(Env{NameNow: "yesterday", NameTestTodaySequence: ""}); err == nil {
		t.Fatal("NowForAdapter swallowed a malformed pin")
	}
}

// test_blank_pin_is_treated_as_unset
func TestBlankPinIsTreatedAsUnset(t *testing.T) {
	if _, ok, err := Now(Env{NameNow: "  "}); ok || err != nil {
		t.Fatalf("blank pin = %v/%v, want unset", ok, err)
	}
}

// -- the clock precedence, in one place --------------------------------------
//
// Written so an INVERSION fails rather than merely producing a different
// instant: the pinned instant is years away from today, so the two branches can
// never be confused for each other.

// test_the_pinned_instant_dominates_the_test_today_sequence
func TestThePinnedInstantDominatesTheTestTodaySequence(t *testing.T) {
	resolved, err := NowForAdapter(Env{NameNow: "2026-03-14T15:09:26Z", NameTestTodaySequence: "2020-01-02"})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC); !resolved.Equal(want) {
		t.Fatalf("resolved = %s, want %s — TASKS_PIN_NOW must out-rank the sequence", resolved, want)
	}
}

// test_the_test_today_sequence_applies_only_when_nothing_is_pinned, and
// test_an_empty_test_today_sequence_still_takes_the_branch: PRESENCE, not
// non-emptiness, is the trigger, because `if ENV[name]` is what the Ruby seam
// reads and an empty string is truthy there.
func TestTheTestTodaySequenceIsKeyedOnPresenceAlone(t *testing.T) {
	for _, value := range []string{"2020-01-02", ""} {
		resolved, err := NowForAdapter(Env{NameTestTodaySequence: value})
		if err != nil {
			t.Fatal(err)
		}
		today := time.Now()
		want := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.UTC)
		if !resolved.Equal(want) {
			t.Fatalf("resolved = %s, want noon UTC of today (%s)", resolved, want)
		}
	}
}

// test_unset_everything_falls_through_to_the_wall_clock
func TestUnsetEverythingFallsThroughToTheWallClock(t *testing.T) {
	before := time.Now().UTC()
	resolved, err := NowForAdapter(Env{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Before(before.Add(-5*time.Second)) || resolved.After(time.Now().UTC().Add(5*time.Second)) {
		t.Fatalf("resolved = %s, want the wall clock", resolved)
	}
	if resolved.Location() != time.UTC {
		t.Fatalf("location = %s, want UTC", resolved.Location())
	}
}

// -- id sequence -------------------------------------------------------------

// test_id_source_is_nil_when_unpinned
func TestIDSourceIsNilWhenUnpinned(t *testing.T) {
	for _, env := range []Env{{}, {NameIDs: "   "}} {
		sequence, err := IDSource(env)
		if sequence != nil || err != nil {
			t.Fatalf("IDSource = %v/%v, want nil", sequence, err)
		}
	}
}

// test_id_source_mints_the_listed_ids_in_order,
// test_id_source_continues_by_incrementing_the_last_token,
// test_seq_token_starts_at_zero, test_id_source_wraps_at_32_bits
func TestIDSequenceMintsTokensThenCounts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  string
		draws int
		want  []string
	}{
		{"listed ids in order", "bbbb0001,bbbb0002", 2, []string{"bbbb0001", "bbbb0002"}},
		{"continues past the list", "bbbb0001", 3, []string{"bbbb0001", "bbbb0002", "bbbb0003"}},
		{"seq starts at zero", "seq", 2, []string{"00000000", "00000001"}},
		{"wraps at 32 bits", "ffffffff", 2, []string{"ffffffff", "00000000"}},
		{"tokens are case-folded and trimmed", " BBBB0001 , seq ", 3, []string{"bbbb0001", "00000000", "00000001"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sequence, err := IDSource(Env{NameIDs: tc.spec})
			if err != nil {
				t.Fatal(err)
			}
			for index, want := range tc.want {
				if got := sequence.Call(); got != want {
					t.Fatalf("draw %d = %q, want %q", index, got, want)
				}
			}
		})
	}
}

// test_id_source_rejects_a_token_that_is_not_eight_hex
func TestIDSourceRejectsATokenThatIsNotEightHex(t *testing.T) {
	for _, spec := range []string{"nope", "bbb1", "bbbb00001", "BBBB000G", ",,", ""} {
		env := Env{NameIDs: spec}
		if spec == "" {
			// An empty spec is UNSET, not malformed.
			if sequence, err := IDSource(env); sequence != nil || err != nil {
				t.Fatalf("empty spec = %v/%v, want unset", sequence, err)
			}
			continue
		}
		if _, err := IDSource(env); err == nil {
			t.Fatalf("spec %q was accepted", spec)
		}
	}
	// The diagnostic names the pin the operator actually set, not a generic one.
	_, err := DelegationKeySource(Env{NameDelegationKeys: "bbbb0001"})
	if err == nil || !strings.Contains(err.Error(), NameDelegationKeys) {
		t.Fatalf("delegation-width error = %v, want one naming %s", err, NameDelegationKeys)
	}
}

func TestDelegationKeysAreSixteenHexWide(t *testing.T) {
	sequence, err := DelegationKeySource(Env{NameDelegationKeys: "seq,00000000ffffffff"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0000000000000000", "00000000ffffffff", "0000000100000000"}
	for index, expected := range want {
		if got := sequence.Call(); got != expected {
			t.Fatalf("draw %d = %q, want %q", index, got, expected)
		}
	}
	if sequence.Width() != 16 {
		t.Fatalf("width = %d, want 16", sequence.Width())
	}
}

// test_id_source_is_memoized_per_spec / test_id_source_rebuilds_when_the_spec_changes.
// One invocation can build several stores; they must draw from ONE sequence
// rather than each restarting it, or two records would be minted the same id.
func TestSharedIDSourceIsMemoizedPerSpec(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	env := Env{NameIDs: "bbbb0001"}
	first, err := SharedIDSource(env)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := SharedIDSource(env)
	if first != second {
		t.Fatal("the same spec must return the same sequence")
	}
	first.Call()
	if got, _ := SharedIDSource(env); got.Call() != "bbbb0002" {
		t.Fatal("a second caller restarted the sequence instead of continuing it")
	}
	// A different spec is a different sequence.
	changed, _ := SharedIDSource(Env{NameIDs: "cccc0001"})
	if got := changed.Call(); got != "cccc0001" {
		t.Fatalf("changed spec drew %q, want cccc0001", got)
	}
	// Unset is still unset, and does not resurrect the memo.
	if sequence, err := SharedIDSource(Env{}); sequence != nil || err != nil {
		t.Fatalf("unset shared source = %v/%v", sequence, err)
	}
	Reset()
	restarted, _ := SharedIDSource(env)
	if got := restarted.Call(); got != "bbbb0001" {
		t.Fatalf("after Reset the sequence drew %q, want a fresh bbbb0001", got)
	}
}

func TestSharedDelegationKeySourceIsMemoizedPerSpec(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	env := Env{NameDelegationKeys: "aaaaaaaaaaaaaaa1"}
	first, err := SharedDelegationKeySource(env)
	if err != nil {
		t.Fatal(err)
	}
	first.Call()
	second, _ := SharedDelegationKeySource(env)
	if got := second.Call(); got != "aaaaaaaaaaaaaaa2" {
		t.Fatalf("draw = %q, want the sequence to continue", got)
	}
}

// A shared sequence is drawn from more than one place in a process, so the draw
// itself has to be safe. Ruby's GVL hides this; Go's does not.
func TestIDSequenceDrawsAreRaceFree(t *testing.T) {
	sequence, err := NewIDSequence("seq", 8, NameIDs)
	if err != nil {
		t.Fatal(err)
	}
	const draws = 200
	tokens := make([]string, draws)
	var wait sync.WaitGroup
	for index := 0; index < draws; index++ {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			tokens[slot] = sequence.Call()
		}(index)
	}
	wait.Wait()
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			t.Fatalf("token %q was minted twice", token)
		}
		seen[token] = true
	}
}

// -- hostname, coalesce scope, winsize ---------------------------------------

// test_hostname_honors_the_pin, and the unpinned provider is the real host.
func TestHostnameHonorsThePin(t *testing.T) {
	if got := Hostname(Env{NameHostname: "fixture-host"})(); got != "fixture-host" {
		t.Fatalf("Hostname = %q", got)
	}
	if got := Hostname(Env{NameHostname: "  "})(); got == "" {
		t.Fatal("a blank pin should fall back to the real hostname, not to empty")
	}
}

// test_coalesce_scope_pin
func TestCoalesceScopePin(t *testing.T) {
	if _, ok := CoalesceScope(Env{}); ok {
		t.Fatal("unset coalesce scope reported as pinned")
	}
	value, ok := CoalesceScope(Env{NameCoalesceScope: "pinned-scope"})
	if !ok || value != "pinned-scope" {
		t.Fatalf("CoalesceScope = %q/%v", value, ok)
	}
}

// test_winsize_requires_both_dimensions / test_winsize_ignores_nonsense. Half a
// geometry is not a geometry: one dimension would otherwise silently pair with
// the real terminal's other one.
func TestWinsizeRequiresBothDimensions(t *testing.T) {
	for _, env := range []Env{
		{NameColumns: "100"}, {NameLines: "40"},
		{NameLines: "0", NameColumns: "100"}, {NameLines: "wide", NameColumns: "100"},
		{NameLines: "-4", NameColumns: "100"}, {NameLines: "40", NameColumns: ""},
	} {
		if _, _, ok := Winsize(env); ok {
			t.Fatalf("Winsize(%v) reported a geometry", env)
		}
	}
	rows, columns, ok := Winsize(Env{NameLines: "40", NameColumns: "100"})
	if !ok || rows != 40 || columns != 100 {
		t.Fatalf("Winsize = %d/%d/%v, want 40/100/true", rows, columns, ok)
	}
}

// -- the Env value itself ----------------------------------------------------

// Unset and set-but-empty are different facts, and at least one pin keys off
// the difference, so the distinction has to survive the Env type.
func TestEnvDistinguishesUnsetFromEmpty(t *testing.T) {
	env := Env{"SET_EMPTY": ""}
	if value, present := env.Lookup("SET_EMPTY"); !present || value != "" {
		t.Fatalf("Lookup = %q/%v, want present and empty", value, present)
	}
	if _, present := env.Lookup("ABSENT"); present {
		t.Fatal("an absent name reported as present")
	}
	if env.Get("ABSENT") != "" {
		t.Fatal("Get should read an unset name as the empty string")
	}
	if _, ok := env.Requested("SET_EMPTY"); ok {
		t.Fatal("a blank value is not a request")
	}
	raw := Env{"P": " x "}
	if value, ok := raw.Requested("P"); !ok || value != " x " {
		t.Fatalf("Requested = %q/%v, want the RAW value", value, ok)
	}
}

// Keys is what a report accounts for. An unset pin still has to be listed —
// "no pin was applied" is a fact, and omitting it would be indistinguishable
// from a harness that never looked.
func TestKeysCoverEveryPinName(t *testing.T) {
	want := []string{
		NameNow, NameIDs, NameCoalesceScope, NameDelegationKeys, NameHostname,
		NameLines, NameColumns, NameTestTodaySequence, NameDevice, NameTZ,
		NameLang, NameLCAll,
	}
	if len(Keys) != len(want) {
		t.Fatalf("Keys = %v, want %v", Keys, want)
	}
	for index, name := range want {
		if Keys[index] != name {
			t.Fatalf("Keys[%d] = %q, want %q", index, Keys[index], name)
		}
	}
}
