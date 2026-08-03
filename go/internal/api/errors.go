package api

import "fmt"

// httpError is lib/tasks/api/errors.rb's HttpError: a status, a stable machine
// code, a human sentence, and the structured details a client branches on.
//
// It is a Go error so a handler can `return` one from anywhere in a request and
// have the one place that writes responses shape it, which is what keeps the
// error envelope identical for every endpoint.
type httpError struct {
	Status  int
	Code    string
	Message string
	Details detailWriter
	Headers map[string]string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// errorMessages is Errors::MESSAGES, verbatim. A code with no sentence here is
// a code no surface may raise: the message is part of the contract.
var errorMessages = map[string]string{
	"malformed_request":          "The request is malformed.",
	"forbidden_origin":           "This origin is not allowed to mutate tasks.",
	"not_found":                  "No task with that id.",
	"conflict":                   "The requested change conflicts with the current task.",
	"claim_conflict":             "The task's claim is held by another worker.",
	"cycle":                      "A task cannot be moved under itself or a descendant.",
	"too_deep":                   "Nesting the task would exceed the maximum depth.",
	"stale_revision":             "The task changed after it was loaded.",
	"payload_too_large":          "The request body is too large.",
	"unsupported_media_type":     "Request bodies must be application/json.",
	"validation_failed":          "One or more fields are invalid.",
	"missing_precondition":       "This write requires an If-Match header.",
	"unsupported_schema_version": "The task store declares a schema version this build does not implement.",
	"store_invalid":              "The task list failed structural validation.",
	"unavailable":                "The task store is unavailable; retry.",
	// not_implemented has no Ruby counterpart. It is this build's honest
	// refusal for a route the Go store cannot perform at all — see
	// porting/intentional-differences.md, `go-api-refuses-unbuilt-writes`.
	"not_implemented": "This build does not implement that operation yet.",
}

func message(code string) string {
	text, found := errorMessages[code]
	if !found {
		// A code with no sentence is a programming error, and a server that
		// answers with an empty message hides it. Naming it is the cheapest
		// way to make the omission visible without crashing a request.
		return "Unspecified error: " + code + "."
	}
	return text
}

// errorOf is the common constructor: the canonical message for the code.
func errorOf(status int, code string) *httpError {
	return &httpError{Status: status, Code: code, Message: message(code)}
}

// errorWith is the constructor for a refusal whose message is more useful than
// the canonical one — a domain sentence, or a field-specific reason.
func errorWith(status int, code, text string) *httpError {
	return &httpError{Status: status, Code: code, Message: text}
}

// withDetails attaches the structured half of a refusal.
func (e *httpError) withDetails(details detailWriter) *httpError {
	e.Details = details
	return e
}

// withHeader attaches a response header that survives the error path — today
// only the fresh `etag` a 412 carries.
func (e *httpError) withHeader(name, value string) *httpError {
	if e.Headers == nil {
		e.Headers = map[string]string{}
	}
	e.Headers[name] = value
	return e
}

// validationError is App#validation!: a 422 whose details name each refused
// field and the reasons for it, in the order the adapter checked them.
func validationError(fields ...fieldError) *httpError {
	return errorOf(422, "validation_failed").withDetails(fieldDetails(fields))
}

// fieldError is one `field => [reasons]` pair. A slice of these is used rather
// than a map because the response is compared byte for byte against Ruby, whose
// Hash preserves insertion order.
type fieldError struct {
	Field   string
	Reasons []string
}

func reason(field, text string) fieldError {
	return fieldError{Field: field, Reasons: []string{text}}
}
