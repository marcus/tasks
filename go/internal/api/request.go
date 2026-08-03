package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"tasks-go/internal/record"
)

// This file is the transport half of lib/tasks/api/app.rb: query parsing, body
// reading and limits, media-type enforcement, and the If-Match precondition.

// jsonObject is a parsed request body that remembers its key ORDER.
//
// Ruby's Hash preserves insertion order, and two refusals depend on it: the
// unknown-field list is `body.keys - allowed`, and the fields object of a 422
// is compared byte for byte. A Go map cannot answer either question, so the
// ordered keys are kept alongside. A repeated key keeps its FIRST position and
// its LAST value, which is what `Hash#[]=` does.
type jsonObject struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseJSONObject(raw []byte) (*jsonObject, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		var probe any
		if json.Unmarshal(raw, &probe) != nil {
			return nil, errorWith(400, "malformed_request", "The request body is not valid JSON.")
		}
		return nil, errorWith(400, "malformed_request", "The request body must be a JSON object.")
	}
	fields, err := record.Fields(json.RawMessage(raw))
	if err != nil {
		return nil, errorWith(400, "malformed_request", "The request body is not valid JSON.")
	}
	body := &jsonObject{values: map[string]json.RawMessage{}}
	for _, field := range fields {
		if _, seen := body.values[field.Key]; !seen {
			body.keys = append(body.keys, field.Key)
		}
		body.values[field.Key] = field.Value
	}
	return body, nil
}

func (o *jsonObject) empty() bool { return o == nil || len(o.keys) == 0 }

func (o *jsonObject) has(key string) bool {
	if o == nil {
		return false
	}
	_, found := o.values[key]
	return found
}

func (o *jsonObject) raw(key string) json.RawMessage {
	if o == nil {
		return nil
	}
	return o.values[key]
}

func (o *jsonObject) isNull(key string) bool {
	return strings.TrimSpace(string(o.raw(key))) == "null"
}

// text reports the string value and whether the member IS a string. A caller
// distinguishes "absent", "not a string", and "the empty string".
func (o *jsonObject) text(key string) (string, bool) {
	var value string
	if json.Unmarshal(o.raw(key), &value) != nil {
		return "", false
	}
	return value, true
}

func (o *jsonObject) boolean(key string) (bool, bool) {
	var value bool
	if json.Unmarshal(o.raw(key), &value) != nil {
		return false, false
	}
	return value, true
}

// strings reports a list of strings, and false when the member is not one.
func (o *jsonObject) stringList(key string) ([]string, bool) {
	var elements []json.RawMessage
	if json.Unmarshal(o.raw(key), &elements) != nil {
		return nil, false
	}
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		var text string
		if json.Unmarshal(element, &text) != nil {
			return nil, false
		}
		values = append(values, text)
	}
	return values, true
}

// object reports a nested object, and false when the member is not one.
func (o *jsonObject) object(key string) (*jsonObject, bool) {
	raw := o.raw(key)
	if len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw))[0] != '{' {
		return nil, false
	}
	nested, err := parseJSONObject(raw)
	if err != nil {
		return nil, false
	}
	return nested, true
}

// rejectUnknownFields is App#reject_unknown_fields!.
func rejectUnknownFields(body *jsonObject, allowed []string) error {
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[name] = true
	}
	unknown := []string{}
	for _, key := range body.keys {
		if !permitted[key] {
			unknown = append(unknown, "unknown request field "+key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return validationError(fieldError{Field: "unknown", Reasons: unknown})
}

// queryParams is App#query_params: unknown keys are a 422, a repeated key is a
// 400, and a malformed escape is a 400.
func queryParams(request *http.Request, allowed ...string) (url.Values, error) {
	parsed, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, errorWith(400, "malformed_request", "The query string is malformed.")
	}
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[name] = true
	}
	// Ordered by the query string so the refusal is stable, which url.Values
	// (a map) cannot be on its own.
	unknown := []string{}
	seen := map[string]bool{}
	for _, pair := range strings.Split(request.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}
		name := pair
		if index := strings.Index(pair, "="); index >= 0 {
			name = pair[:index]
		}
		decoded, decodeErr := url.QueryUnescape(name)
		if decodeErr != nil {
			return nil, errorWith(400, "malformed_request", "The query string is malformed.")
		}
		if permitted[decoded] || seen[decoded] {
			continue
		}
		seen[decoded] = true
		unknown = append(unknown, "unknown query field "+decoded)
	}
	if len(unknown) > 0 {
		return nil, validationError(fieldError{Field: "query", Reasons: unknown})
	}
	for _, values := range parsed {
		if len(values) > 1 {
			return nil, errorWith(400, "malformed_request", "Query parameters may be supplied only once.")
		}
	}
	return parsed, nil
}

// booleanQuery is App#boolean_query: exactly "true" or "false", or the default.
func booleanQuery(query url.Values, key string, fallback *bool) (*bool, error) {
	if !query.Has(key) {
		return fallback, nil
	}
	yes, no := true, false
	switch query.Get(key) {
	case "true":
		return &yes, nil
	case "false":
		return &no, nil
	}
	return nil, validationError(reason(key, "must be true or false"))
}

// jsonBody is App#json_body: the media type, the two size gates, and the
// object requirement.
func jsonBody(request *http.Request) (*jsonObject, error) {
	if mediaType(request) != "application/json" {
		return nil, errorOf(415, "unsupported_media_type")
	}
	raw, err := readLimitedBody(request)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errorWith(400, "malformed_request", "The request body is not valid JSON.")
	}
	return parseJSONObject(raw)
}

// readLimitedBody enforces Content-Length and the streamed byte cap. Both are
// checked because a client may lie about the former or omit it entirely.
func readLimitedBody(request *http.Request) ([]byte, error) {
	if declared := request.Header.Get("Content-Length"); declared != "" {
		length, err := strconv.Atoi(declared)
		if err != nil {
			return nil, errorWith(400, "malformed_request", "Content-Length is malformed.")
		}
		if length > bodyLimit {
			return nil, errorOf(413, "payload_too_large")
		}
	}
	if request.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, bodyLimit+1))
	if err != nil {
		return nil, errorWith(400, "malformed_request", "The request body could not be read.")
	}
	if len(raw) > bodyLimit {
		return nil, errorOf(413, "payload_too_large")
	}
	return raw, nil
}

// mediaType is Rack::Request#media_type: the type without parameters, folded to
// lower case.
func mediaType(request *http.Request) string {
	value := request.Header.Get("Content-Type")
	if index := strings.Index(value, ";"); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// rejectBody is App#reject_delete_body!: a parameterless DELETE or action POST
// accepts no body, and says so rather than ignoring one.
func rejectBody(request *http.Request, subject string) error {
	raw, err := readLimitedBody(request)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if mediaType(request) != "application/json" {
		return errorOf(415, "unsupported_media_type")
	}
	return errorWith(400, "malformed_request", subject+" do not accept a body.")
}

// optionalBody is the reject-notes case: an absent body is fine, a present one
// must be JSON.
func optionalBody(request *http.Request) (*jsonObject, error) {
	raw, err := readLimitedBody(request)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if mediaType(request) != "application/json" {
		return nil, errorOf(415, "unsupported_media_type")
	}
	return parseJSONObject(raw)
}

var etagPattern = regexp.MustCompile(`^"([^"\\]+)"$`)

// ifMatch is App#if_match!: the precondition is REQUIRED on every task write,
// and a malformed one is a validation failure rather than a missing one.
func ifMatch(request *http.Request) (string, error) {
	raw := request.Header.Get("If-Match")
	if raw == "" {
		return "", errorOf(428, "missing_precondition")
	}
	match := etagPattern.FindStringSubmatch(raw)
	if match == nil {
		return "", errorWith(422, "validation_failed", "If-Match is not a well-formed task revision.")
	}
	return match[1], nil
}

func etag(revision string) string { return `"` + revision + `"` }
