package api

import (
	"net/http"
	"strings"
	"testing"
)

// test/api/test_app.rb's transport half:
// test_transport_validation_body_limit_host_origin_and_forwarded_headers,
// split into the questions it actually asks.

func TestUnsupportedMediaTypeAndMalformedBodies(t *testing.T) {
	h := newHarness(t)

	unsupported := h.do(request{method: "POST", path: "/api/v1/tasks", body: "{}", contentType: "text/plain"})
	assertError(t, unsupported, 415, "unsupported_media_type")

	malformed := h.json("POST", "/api/v1/tasks", "{", nil)
	assertError(t, malformed, 400, "malformed_request")
	if malformed.message() != "The request body is not valid JSON." {
		t.Errorf("message = %q", malformed.message())
	}

	notAnObject := h.json("POST", "/api/v1/tasks", "[]", nil)
	assertError(t, notAnObject, 400, "malformed_request")
	if notAnObject.message() != "The request body must be a JSON object." {
		t.Errorf("message = %q", notAnObject.message())
	}

	unknown := h.json("POST", "/api/v1/tasks", `{"title":"ok","unknown":true}`, nil)
	assertError(t, unknown, 422, "validation_failed")

	badContext := h.json("POST", "/api/v1/tasks", `{"title":"ok","contexts":["desk"]}`, nil)
	assertError(t, badContext, 422, "validation_failed")
}

func TestBodyLimitAppliesToEveryVerbThatReadsOne(t *testing.T) {
	h := newHarness(t)
	huge := `{"title":"` + strings.Repeat("x", bodyLimit) + `"}`

	assertError(t, h.json("POST", "/api/v1/tasks", huge, nil), 413, "payload_too_large")

	current := h.etagOf(fixPR)
	assertError(t, h.json("PATCH", "/api/v1/tasks/"+fixPR, huge, h.withIfMatch(current)),
		413, "payload_too_large")

	// A DELETE accepts no body at all, but the size gate runs first: a client
	// that streamed a megabyte must be told it was too large, not that DELETE
	// takes no body.
	oversized := strings.Repeat("x", bodyLimit+1)
	assertError(t, h.do(request{
		method: "DELETE", path: "/api/v1/tasks/" + fixPR, body: oversized,
		headers: h.withIfMatch(current),
	}), 413, "payload_too_large")

	if !strings.Contains(string(h.storeBytes()), "Review PR backlog") {
		t.Error("an oversized request changed the store")
	}
}

func TestParameterlessVerbsRefuseABody(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixPR)

	withBody := h.json("DELETE", "/api/v1/tasks/"+fixPR, `{"a":1}`, h.withIfMatch(current))
	assertError(t, withBody, 400, "malformed_request")
	if !strings.Contains(withBody.message(), "do not accept a body") {
		t.Errorf("message = %q", withBody.message())
	}

	textBody := h.do(request{
		method: "DELETE", path: "/api/v1/tasks/" + fixPR, body: "not json",
		contentType: "text/plain", headers: h.withIfMatch(current),
	})
	assertError(t, textBody, 415, "unsupported_media_type")

	action := h.json("POST", "/api/v1/tasks/"+fixPR+"/undelegate", `{"a":1}`, h.withIfMatch(current))
	assertError(t, action, 400, "malformed_request")
}

func TestHostOriginAndForwardedHeaders(t *testing.T) {
	h := newHarness(t)

	badHost := h.do(request{method: "GET", path: "/healthz", headers: map[string]string{"Host": "evil.example"}})
	assertError(t, badHost, 400, "malformed_request")

	for _, header := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port"} {
		forwarded := h.do(request{
			method: "GET", path: "/healthz",
			headers: map[string]string{header: "127.0.0.1:4747"},
		})
		assertError(t, forwarded, 400, "malformed_request")
		if !strings.Contains(forwarded.message(), "Forwarded host headers") {
			t.Errorf("%s: message = %q", header, forwarded.message())
		}
	}

	// localhost is the other allowed spelling of the same loopback server.
	allowedHost := h.do(request{
		method: "GET", path: "/healthz",
		headers: map[string]string{"Host": "localhost:4747"},
	})
	assertStatus(t, allowedHost, 200)

	blocked := h.json("POST", "/api/v1/tasks", `{"title":"blocked"}`,
		map[string]string{"Origin": "https://evil.example"})
	assertError(t, blocked, 403, "forbidden_origin")

	allowed := h.json("POST", "/api/v1/tasks", `{"title":"allowed"}`,
		map[string]string{"Origin": "http://127.0.0.1:4747"})
	assertStatus(t, allowed, 201)

	// An Origin on a READ is never checked: the guard exists to stop a browser
	// from mutating, and refusing reads would break every local page.
	read := h.do(request{
		method: "GET", path: "/api/v1/tasks",
		headers: map[string]string{"Origin": "https://evil.example"},
	})
	assertStatus(t, read, 200)

	for _, answered := range []answer{badHost, blocked, allowed, read} {
		if answered.Header.Get("access-control-allow-origin") == "*" {
			t.Error("the server advertises a wildcard CORS origin")
		}
	}
}

func TestIfMatchMustBeAWellFormedQuotedRevision(t *testing.T) {
	h := newHarness(t)
	assertError(t, h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"x"}`, nil),
		428, "missing_precondition")

	for _, tag := range []string{"notquoted", `W/"weak"`, `""`, `"has\\backslash"`, `"unterminated`} {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"x"}`, h.withIfMatch(tag))
		assertError(t, answered, 422, "validation_failed")
		if answered.message() != "If-Match is not a well-formed task revision." {
			t.Errorf("%s: message = %q", tag, answered.message())
		}
	}
}

// A well-formed token this store never produced is a REFUSAL, not a write. It
// is the case a client hits after a rollback or a restore from backup.
func TestAnUnrecognizedRevisionTokenIsRefusedWithoutWriting(t *testing.T) {
	h := newHarness(t)
	before := string(h.storeBytes())
	answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"x"}`,
		h.withIfMatch(`"v1.deadbeef.deadbeef.deadbeef"`))
	if answered.Status != 412 && answered.Status != 422 {
		t.Fatalf("status = %d, want 412 or 422 (body %s)", answered.Status, answered.Body)
	}
	if string(h.storeBytes()) != before {
		t.Error("a refused precondition wrote to the store")
	}
}

func TestUnknownRoutesAndMethodsAreNotFound(t *testing.T) {
	h := newHarness(t)
	assertError(t, h.get("/nope"), 404, "not_found")
	assertError(t, h.get("/api/v1/nope"), 404, "not_found")
	assertError(t, h.json("PUT", "/api/v1/tasks/"+fixPR, `{"title":"x"}`, nil), 404, "not_found")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixPR, `{"title":"x"}`, nil), 404, "not_found")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/nope", "", nil), 404, "not_found")
	assertError(t, h.get("/api/v1/tasks/"+fixPR+"/extra/segment"), 404, "not_found")
}

// The 204 has no body and no content-type, and says so with content-length: 0.
func TestNoBodyResponsesCarryNoContentType(t *testing.T) {
	h := newHarness(t)
	// The one 204 route this build has is refused, so the shape is asserted on
	// the writer directly rather than through a route that cannot produce it.
	writer := &recordingWriter{header: http.Header{}}
	h.server.write(writer, response{status: 204, body: nil}, "req_1")
	if writer.status != 204 {
		t.Errorf("status = %d", writer.status)
	}
	if writer.header.Get("content-type") != "" {
		t.Errorf("content-type = %q", writer.header.Get("content-type"))
	}
	if writer.header.Get("content-length") != "0" {
		t.Errorf("content-length = %q", writer.header.Get("content-length"))
	}
	if len(writer.body) != 0 {
		t.Errorf("body = %q", writer.body)
	}
}

// An unexpected failure must never reach a client: its text could name a path,
// a task title, or a token.
func TestUnexpectedFailuresAreSafe(t *testing.T) {
	h := newHarness(t)
	h.server.options.Read = func() (CheckedRead, error) {
		return CheckedRead{}, errSecret
	}
	answered := h.get("/readyz")
	assertError(t, answered, 503, "unavailable")
	if strings.Contains(answered.Body, "secret") || strings.Contains(answered.Body, "tasks.jsonl") {
		t.Errorf("the refusal leaked the failure: %s", answered.Body)
	}
	if strings.Contains(h.logs.String(), "secret") {
		t.Errorf("the log leaked the failure: %s", h.logs.String())
	}
}

type secretError struct{}

func (secretError) Error() string { return "secret title at /private/tasks.jsonl?token=abc" }

var errSecret = secretError{}

type recordingWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) Write(data []byte) (int, error) {
	w.body = append(w.body, data...)
	return len(data), nil
}
func (w *recordingWriter) WriteHeader(status int) { w.status = status }
