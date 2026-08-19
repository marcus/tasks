package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The delegation write routes.
//
// They answered 501 until the store learned to compare a task revision inside
// its own delegation transaction. The contract makes If-Match MANDATORY on
// these routes, and the whole reason for the refusal was that honouring it was
// impossible without that comparison. Every test here is therefore about one of
// two things: the three-part delegation reaching the store intact, and the
// precondition being real rather than accepted and dropped.

// delegationOf digs the marker out of a task resource.
func delegationOf(t *testing.T, answered answer) map[string]any {
	t.Helper()
	marker, _ := answered.data()["delegation"].(map[string]any)
	return marker
}

// A three-part delegation — who, in what mode, and the briefing — is ONE
// request, and every part reaches the marker.
func TestDelegateCarriesModeAndNoteInOneWrite(t *testing.T) {
	h := newHarness(t)
	answered := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement","note":"Start with the failing test."}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)

	marker := delegationOf(t, answered)
	if marker["kind"] != "agent" || marker["mode"] != "implement" || marker["status"] != "ready" {
		t.Fatalf("marker = %v", marker)
	}
	if marker["note"] != "Start with the failing test." {
		t.Fatalf("note = %v", marker["note"])
	}
	// The response carries the whole canonical task and a fresh ETag, so a
	// client writes and reads its result without a second round trip.
	if answered.etag() == "" || answered.data()["id"] != fixPR {
		t.Fatalf("etag %q, id %v", answered.etag(), answered.data()["id"])
	}
}

// A note survives HTTP unchanged, including the two things a transport is most
// likely to mangle: paragraph breaks and non-ASCII text.
func TestDelegationNoteRoundTripsNewlinesAndMultibyte(t *testing.T) {
	h := newHarness(t)
	note := "Ligne un — attention aux accents.\n\nDeuxième paragraphe: 日本語とemoji 🚀.\nFin."
	answered := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"research","note":`+jsonString(note)+`}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)
	if got := delegationOf(t, answered)["note"]; got != note {
		t.Fatalf("note = %q, want %q", got, note)
	}

	// And it is still that text on a fresh READ, which is what an agent
	// polling the queue actually sees.
	reread := h.get("/api/v1/tasks/" + fixPR)
	assertStatus(t, reread, 200)
	if got := delegationOf(t, reread)["note"]; got != note {
		t.Fatalf("reread note = %q", got)
	}
}

// The note's bound is the schema's, and the refusal quotes it with the actual
// length so a caller can trim rather than guess.
func TestOverlongDelegationNoteIsRefusedWithTheLimit(t *testing.T) {
	h := newHarness(t)
	note := strings.Repeat("é", 2001)
	if utf8.RuneCountInString(note) != 2001 {
		t.Fatalf("fixture is %d runes", utf8.RuneCountInString(note))
	}
	answered := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine","note":`+jsonString(note)+`}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertError(t, answered, 422, "validation_failed")
	if !strings.Contains(answered.message(), "2000") ||
		!strings.Contains(answered.message(), "2001") {
		t.Fatalf("message does not quote the limit and the length: %q", answered.message())
	}
	// The bound is counted in RUNES, so 2000 multibyte characters fit.
	fits := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine","note":`+jsonString(strings.Repeat("é", 2000))+`}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, fits, 200)
}

// Three cases a single string cannot express: absent leaves the briefing alone,
// null clears it, and text replaces it. The first is the one that matters —
// re-stating the mode must not silently erase a briefing.
func TestDelegateNoteAbsentKeepsItAndNullClearsIt(t *testing.T) {
	h := newHarness(t)
	first := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine","note":"Read the RFC first."}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, first, 200)

	kept := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, kept, 200)
	if got := delegationOf(t, kept)["note"]; got != "Read the RFC first." {
		t.Fatalf("an omitted note changed the briefing: %v", got)
	}

	cleared := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement","note":null}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, cleared, 200)
	if _, present := delegationOf(t, cleared)["note"]; present {
		t.Fatalf("null did not clear the note: %v", delegationOf(t, cleared))
	}
}

// The briefing has its own route, so an owner correcting instructions does not
// have to restate who holds the work.
func TestDelegationNoteRouteRewritesAndClears(t *testing.T) {
	h := newHarness(t)
	assertStatus(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine"}`, h.withIfMatch(h.etagOf(fixPR))), 200)

	set := h.json("PUT", "/api/v1/tasks/"+fixPR+"/delegation_note",
		`{"note":"Land it behind the flag."}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, set, 200)
	if got := delegationOf(t, set)["note"]; got != "Land it behind the flag." {
		t.Fatalf("note = %v", got)
	}
	if got := delegationOf(t, set)["mode"]; got != "refine" {
		t.Fatalf("the note route changed the mode: %v", got)
	}

	cleared := h.json("PUT", "/api/v1/tasks/"+fixPR+"/delegation_note",
		`{"note":null}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, cleared, 200)
	if _, present := delegationOf(t, cleared)["note"]; present {
		t.Fatalf("note survived a clear: %v", delegationOf(t, cleared))
	}

	// An undelegated task has no briefing to write, and saying so is more
	// useful than inventing a marker.
	orphan := h.json("PUT", "/api/v1/tasks/"+fixEval+"/delegation_note",
		`{"note":"hello"}`, h.withIfMatch(h.etagOf(fixEval)))
	assertError(t, orphan, 422, "validation_failed")
	if !strings.Contains(orphan.message(), "not delegated") {
		t.Fatalf("message = %q", orphan.message())
	}
}

// A mode is valid on a HUMAN delegation too: who holds the work and what kind
// of delegation it is are orthogonal facts.
func TestHumanDelegationCarriesModeAndNote(t *testing.T) {
	h := newHarness(t)
	answered := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"assignee":"pat@example.com","mode":"refine","note":"Ping me when it lands."}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)
	marker := delegationOf(t, answered)
	if marker["kind"] != "human" || marker["assignee"] != "pat@example.com" {
		t.Fatalf("marker = %v", marker)
	}
	if marker["mode"] != "refine" || marker["note"] != "Ping me when it lands." {
		t.Fatalf("marker = %v", marker)
	}
	// A human delegation moves the task to WAITING, and keep_state opts out.
	if answered.data()["state"] != "WAITING" {
		t.Fatalf("state = %v", answered.data()["state"])
	}
}

// The precondition is REAL. It is compared inside the store's own write
// transaction, which is the whole reason these routes stopped answering 501.
func TestDelegationHonoursIfMatch(t *testing.T) {
	h := newHarness(t)
	tag := h.etagOf(fixPR)

	missing := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine"}`, nil)
	assertError(t, missing, 428, "missing_precondition")

	malformed := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine"}`, map[string]string{"If-Match": "not-an-etag"})
	assertError(t, malformed, 422, "validation_failed")

	// Spend the tag, then reuse it: the second write is deciding against facts
	// that no longer hold.
	assertStatus(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"refine"}`, h.withIfMatch(tag)), 200)
	stale := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement"}`, h.withIfMatch(tag))
	assertError(t, stale, 412, "stale_revision")
	if delegationOf(t, h.get("/api/v1/tasks/"+fixPR))["mode"] != "refine" {
		t.Fatal("a stale delegation wrote anyway")
	}
}

// Claim, release and work_ref complete the surface, and a lost claim race names
// the holder and the instant as their own fields rather than as prose.
func TestClaimReleaseAndWorkRefOverHTTP(t *testing.T) {
	h := newHarness(t)
	assertStatus(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"implement","note":"Keep the diff small."}`,
		h.withIfMatch(h.etagOf(fixPR))), 200)

	claimed := h.json("POST", "/api/v1/tasks/"+fixPR+"/claim", `{"worker":"w-1"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, claimed, 200)
	marker := delegationOf(t, claimed)
	if marker["status"] != "claimed" || marker["assignee"] != "w-1" {
		t.Fatalf("marker = %v", marker)
	}
	// The briefing survives the pickup — it is the instruction the claiming
	// worker is meant to read.
	if marker["note"] != "Keep the diff small." {
		t.Fatalf("claim dropped the note: %v", marker)
	}

	lost := h.json("POST", "/api/v1/tasks/"+fixPR+"/claim", `{"worker":"w-2"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertError(t, lost, 409, "conflict")
	details, _ := lost.dig("error", "details").(map[string]any)
	if details["holder"] != "w-1" || details["at"] == "" {
		t.Fatalf("conflict details = %v", details)
	}

	ref := h.json("PUT", "/api/v1/tasks/"+fixPR+"/work_ref",
		`{"work_ref":"https://example.com/pr/7"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, ref, 200)
	if delegationOf(t, ref)["work_ref"] != "https://example.com/pr/7" {
		t.Fatalf("marker = %v", delegationOf(t, ref))
	}

	released := h.json("POST", "/api/v1/tasks/"+fixPR+"/release",
		`{"worker":"w-1","note":"blocked: need repo access"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, released, 200)
	if delegationOf(t, released)["status"] != "ready" {
		t.Fatalf("marker = %v", delegationOf(t, released))
	}
	// A release note is the BLOCKER line on the body, not the marker's
	// briefing, and the briefing is untouched by it.
	body := stringsOf(released.data()["body"])
	if len(body) == 0 || !strings.Contains(strings.Join(body, "\n"), "need repo access") {
		t.Fatalf("body = %v", body)
	}
	if delegationOf(t, released)["note"] != "Keep the diff small." {
		t.Fatalf("a release rewrote the briefing: %v", delegationOf(t, released))
	}

	cleared := h.json("POST", "/api/v1/tasks/"+fixPR+"/undelegate", "",
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, cleared, 200)
	if cleared.data()["delegation"] != nil {
		t.Fatalf("delegation = %v", cleared.data()["delegation"])
	}
}

// A refusal quotes the vocabulary that was actually ENFORCED. Naming the
// built-in set to a user who configured another one would send them to fix a
// word that is not the problem.
func TestDelegationRefusalQuotesTheConfiguredVocabulary(t *testing.T) {
	h := newHarnessModes(t, "triage", "ship")

	accepted := h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
		`{"kind":"agent","mode":"triage"}`, h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, accepted, 200)

	refused := h.json("POST", "/api/v1/tasks/"+fixEval+"/delegate",
		`{"kind":"agent","mode":"research"}`, h.withIfMatch(h.etagOf(fixEval)))
	assertError(t, refused, 422, "validation_failed")
	if !strings.Contains(refused.message(), "triage/ship") {
		t.Fatalf("message does not quote the configured set: %q", refused.message())
	}
	if strings.Contains(refused.message(), "refine/research/implement") {
		t.Fatalf("message quotes the built-in set: %q", refused.message())
	}

	// And the vocabulary a client discovers is the same one.
	meta := h.get("/api/v1/meta")
	assertStatus(t, meta, 200)
	assertStrings(t, stringsOf(meta.data()["delegation_modes"]), []string{"triage", "ship"},
		"meta delegation_modes")
}

// A member the route does not know is refused by name rather than dropped: a
// body that says `catgory` meant something, and silently ignoring it would ship
// a delegation the caller did not ask for.
func TestDelegationBodiesRefuseUnknownFields(t *testing.T) {
	h := newHarness(t)
	for _, testCase := range []struct{ method, path, body string }{
		{"POST", "/api/v1/tasks/" + fixPR + "/delegate", `{"kind":"agent","mode":"refine","nope":1}`},
		{"POST", "/api/v1/tasks/" + fixPR + "/claim", `{"worker":"w-1","nope":1}`},
		{"PUT", "/api/v1/tasks/" + fixPR + "/work_ref", `{"work_ref":"x","nope":1}`},
		{"PUT", "/api/v1/tasks/" + fixPR + "/delegation_note", `{"note":"x","nope":1}`},
	} {
		answered := h.json(testCase.method, testCase.path, testCase.body, h.withIfMatch(h.etagOf(fixPR)))
		assertStatus(t, answered, 422)
	}
}

// PARITY. Every three-part delegation the CLI can express, HTTP can express
// too, and both leave the SAME marker behind.
//
// The inputs below are the ones
// cmd/tasks/delegationnotecli_test.go drives through argv; this test drives the
// same intents through the routes and asserts the same stored result. A
// capability reachable from only one surface is the failure this catches, and
// it is the failure the whole packet exists to prevent.
func TestDelegationSurfaceParityWithTheCLI(t *testing.T) {
	cases := []struct {
		name   string
		method string
		suffix string
		body   string
		want   map[string]string
	}{
		{
			name:   "tasks delegate <ref> implement --note …",
			method: "POST", suffix: "/delegate",
			body: `{"kind":"agent","mode":"implement","note":"Start with the failing test."}`,
			want: map[string]string{"kind": "agent", "mode": "implement", "status": "ready",
				"note": "Start with the failing test."},
		},
		{
			name:   "tasks delegate <ref> --to pat@example.com refine --note-file -",
			method: "POST", suffix: "/delegate",
			body: `{"assignee":"pat@example.com","mode":"refine","note":"Please review the API shape."}`,
			want: map[string]string{"kind": "human", "mode": "refine", "status": "delegated",
				"assignee": "pat@example.com", "note": "Please review the API shape."},
		},
		{
			name:   "tasks delegate <ref> --note off",
			method: "PUT", suffix: "/delegation_note",
			body: `{"note":null}`,
			want: map[string]string{"kind": "agent", "mode": "research", "status": "ready"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			// A shared starting point with a briefing already on it, so the
			// clearing case has something to clear and the others have
			// something they must overwrite rather than merge with.
			assertStatus(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/delegate",
				`{"kind":"agent","mode":"research","note":"prior briefing"}`,
				h.withIfMatch(h.etagOf(fixPR))), 200)

			answered := h.json(testCase.method, "/api/v1/tasks/"+fixPR+testCase.suffix,
				testCase.body, h.withIfMatch(h.etagOf(fixPR)))
			assertStatus(t, answered, 200)

			marker := delegationOf(t, answered)
			for key, want := range testCase.want {
				if marker[key] != want {
					t.Fatalf("%s = %v, want %q (marker %v)", key, marker[key], want, marker)
				}
			}
			if _, present := marker["note"]; !present && testCase.want["note"] != "" {
				t.Fatalf("marker lost the note: %v", marker)
			}
			if testCase.want["note"] == "" {
				if _, present := marker["note"]; present {
					t.Fatalf("marker kept a note it should have cleared: %v", marker)
				}
			}
		})
	}
}

// jsonString quotes text the way a request body carries it.
func jsonString(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
