package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The delegation TRANSITION STAMP as a person reads it.
//
// The file keeps one spelling — UTC RFC 3339 — and every human surface projects
// it into the configured zone and clock format, exactly as deadlines and
// scheduled dates already do. These are black-box assertions because the claim
// is a cross-layer one: config has to reach the read model, and the read model
// has to reach the sentence `show` prints.

// 2026-08-28T00:46:44Z is 2026-08-27 17:46 in America/Los_Angeles, which is a
// different DAY as well as a different clock — the case a rendering that only
// shifted the time would get wrong.
const delegationStampFixture = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"2026-08-28T00:46:44Z"}}
{"type":"task","id":"dddd0003","parent":"dddd0001","state":"NEXT","title":"Held thing","delegation":{"kind":"agent","mode":"research","status":"claimed","assignee":"worker-1","at":"2026-08-28T00:46:44Z"}}
`

func TestShowRendersTheDelegationStampInTheConfiguredZone(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		config string
		want   string
	}{
		{"local 12-hour", "timezone = America/Los_Angeles\ntime_format = 12\n",
			"(since thu 08-27 5:46p)"},
		{"local 24-hour", "timezone = America/Los_Angeles\ntime_format = 24\n",
			"(since thu 08-27 17:46)"},
		{"another zone entirely", "timezone = Asia/Tokyo\ntime_format = 12\n",
			"(since fri 08-28 9:46a)"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := seedStore(t, delegationStampFixture)
			seedConfig(t, dir, testCase.config)
			result := runCLI(t, dir, "show", "dddd0002")
			if result.status != 0 {
				t.Fatalf("show: exit %d, stderr %q", result.status, result.stderr)
			}
			if !strings.Contains(result.stdout, testCase.want) {
				t.Errorf("show did not print %q:\n%s", testCase.want, result.stdout)
			}
			if strings.Contains(result.stdout, "2026-08-28T00:46:44Z") {
				t.Errorf("show leaked the stored UTC stamp to a person:\n%s", result.stdout)
			}
		})
	}
}

// The machine surface is deliberately NOT localized: a reader that has to guess
// the viewer's zone cannot compare two agents' claims.
func TestShowJSONKeepsTheStoredUTCStamp(t *testing.T) {
	dir := seedStore(t, delegationStampFixture)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	marker := markerOf(t, dir, "dddd0002")
	if marker["at"] != "2026-08-28T00:46:44Z" {
		t.Errorf("--json at = %v, want the stored UTC instant", marker["at"])
	}
}

// A stamp nothing can parse is still the record's answer to "when": it prints
// as stored rather than vanishing from the line.
func TestShowPrintsAnUnparseableStampAsStored(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"whenever"}}
`)
	seedConfig(t, dir, "timezone = America/Los_Angeles\n")
	result := runCLI(t, dir, "show", "dddd0002")
	if result.status != 0 {
		t.Fatalf("show: exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, "(since whenever)") {
		t.Errorf("show dropped or rewrote an unparseable stamp:\n%s", result.stdout)
	}
}

// A handoff from another year says which year. Terse is right for last week and
// misleading for 2023.
func TestShowNamesTheYearOnAnOlderDelegation(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"2023-08-28T00:46:44Z"}}
`)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	result := runCLI(t, dir, "show", "dddd0002")
	if result.status != 0 {
		t.Fatalf("show: exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, "(since sun 2023-08-27 5:46p)") {
		t.Errorf("an older delegation did not name its year:\n%s", result.stdout)
	}
}

// A marker with no stamp at all drops the parenthetical: "(since )" reads as a
// broken renderer rather than as the broken record it is describing.
func TestShowOmitsTheSinceParentheticalWhenTheStampIsMissing(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready"}}
`)
	seedConfig(t, dir, "timezone = America/Los_Angeles\n")
	result := runCLI(t, dir, "show", "dddd0002")
	if result.status != 0 {
		t.Fatalf("show: exit %d, stderr %q", result.status, result.stderr)
	}
	if strings.Contains(result.stdout, "since") {
		t.Errorf("show printed an empty since-parenthetical:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "delegation: agent-ready (research)") {
		t.Errorf("show dropped the delegation line entirely:\n%s", result.stdout)
	}
}

// A worker id is free-form enough to be SPELLED like a timestamp. The conflict
// sentence must translate the instant and leave the holder's name alone.
func TestClaimConflictDoesNotTranslateAHolderSpelledLikeAStamp(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0003","parent":"dddd0001","state":"NEXT","title":"Held thing","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"2026-08-28T00:46:44Z","at":"2026-08-28T00:46:44Z"}}
`)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	result := runCLI(t, dir, "claim", "dddd0003", "--worker", "worker-2")
	if result.status != 1 {
		t.Fatalf("claim: exit %d, want 1 (stderr %q)", result.status, result.stderr)
	}
	want := "already claimed by 2026-08-28T00:46:44Z at thu 08-27 5:46p"
	if !strings.Contains(result.stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", result.stderr, want)
	}
}

// The same trap on the path that has no structured fields — the sentence the
// STORE wrote, where only the stamp may be rewritten.
func TestDelegateConflictTranslatesTheStampNotTheHolderSpelledLikeOne(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0003","parent":"dddd0001","state":"NEXT","title":"Held thing","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"2026-08-28T00:46:44Z","at":"2026-08-28T00:46:44Z"}}
`)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	result := runCLI(t, dir, "delegate", "dddd0003", "implement")
	if result.status != 1 {
		t.Fatalf("delegate: exit %d, want 1", result.status)
	}
	want := "already claimed by 2026-08-28T00:46:44Z at thu 08-27 5:46p; " +
		"undelegate to revoke the claim first"
	if !strings.Contains(result.stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", result.stderr, want)
	}
}

// The lost claim race, both channels at once: stderr is the person's copy and
// speaks their clock, stdout is the agent's and keeps the stored instant.
func TestClaimConflictSpeaksTheReadersClockOnStderrAndUTCInJSON(t *testing.T) {
	dir := seedStore(t, delegationStampFixture)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	result := runCLI(t, dir, "claim", "dddd0003", "--worker", "worker-2", "--json")
	if result.status != 1 {
		t.Fatalf("claim: exit %d, want 1 (stdout %q)", result.status, result.stdout)
	}
	if !strings.Contains(result.stderr, "already claimed by worker-1 at thu 08-27 5:46p") {
		t.Errorf("the conflict did not name the instant locally:\n%s", result.stderr)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &decoded); err != nil {
		t.Fatalf("conflict --json is not JSON (%v): %s", err, result.stdout)
	}
	if decoded["at"] != "2026-08-28T00:46:44Z" {
		t.Errorf("conflict at = %v, want the stored UTC instant", decoded["at"])
	}
	message, _ := decoded["message"].(string)
	if !strings.Contains(message, "2026-08-28T00:46:44Z") {
		t.Errorf("the JSON message stopped naming the stored instant: %q", message)
	}
}

// A conflict the store worded itself keeps its words; only the stamp inside it
// is translated for the person reading it.
func TestDelegateOverAClaimTranslatesOnlyTheStampInTheStoresSentence(t *testing.T) {
	dir := seedStore(t, delegationStampFixture)
	seedConfig(t, dir, "timezone = America/Los_Angeles\ntime_format = 12\n")
	result := runCLI(t, dir, "delegate", "dddd0003", "implement")
	if result.status != 1 {
		t.Fatalf("delegate: exit %d, want 1", result.status)
	}
	want := "already claimed by worker-1 at thu 08-27 5:46p; undelegate to revoke the claim first"
	if !strings.Contains(result.stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", result.stderr, want)
	}
}
