package api

import (
	"net/http"
	"strings"

	"github.com/marcus/tasks/internal/store"
)

// Delta members on PATCH.
//
// The replacement fields (`tags`, `contexts`, `body`) make a client read the
// task, modify a list, and send the whole thing back — a read-modify-write the
// CLI never does, because `tasks tag` and `tasks note` are deltas in the store
// itself. These members send the same deltas: `tag_delta` for the four tag
// lists and `body_append` for the note, applied to whatever the store holds
// under its lock.
//
// That is also what makes the precondition negotiable for them. A delta does
// not depend on the value it lands on, so a PATCH that carries ONLY delta
// members may send `If-Match: *` — "the task exists" — and land alongside a
// concurrent edit of some other field instead of failing 412 and retrying. A
// real revision is still honoured exactly as on any other PATCH, and anything
// that is not a delta still needs one.

// deltaFields are the delta members, in the order a refusal names them.
var deltaFields = []string{"add_tags", "remove_tags", "add_contexts", "remove_contexts", "append_body"}

// tagDeltaFields are the four members that fold into one `tag_delta`.
var tagDeltaFields = []string{"add_tags", "remove_tags", "add_contexts", "remove_contexts"}

// ifMatchAny is the wildcard precondition a delta-only PATCH may send.
const ifMatchAny = "*"

// patchPrecondition reads PATCH's If-Match. It is ifMatch plus one spelling:
// `*`, reported separately because only the body can say whether it is
// allowed, and the header is read before the body so a request missing it
// gets the same 428 whatever else is wrong.
func patchPrecondition(request *http.Request) (string, bool, error) {
	if strings.TrimSpace(request.Header.Get("If-Match")) == ifMatchAny {
		return "", true, nil
	}
	expected, err := ifMatch(request)
	return expected, false, err
}

// deltaOnly reports a body whose every member is a delta.
func deltaOnly(body *jsonObject) bool {
	if body.empty() {
		return false
	}
	for _, key := range body.keys {
		if !containsString(deltaFields, key) {
			return false
		}
	}
	return true
}

// wildcardRefusal is the 428 for `If-Match: *` on a PATCH that replaces
// something: the wildcard asserts only that the task exists, which is not a
// precondition a replacement can be decided against.
func wildcardRefusal() error {
	return errorWith(428, "missing_precondition",
		"If-Match: * is accepted only on a PATCH whose members are all deltas "+
			"(add_tags, remove_tags, add_contexts, remove_contexts, append_body); "+
			"send the task's revision.")
}

// validatePatchDeltas checks the delta members' shapes and the combinations
// that would make the result depend on field order.
//
// A tag delta rewrites the WHOLE ordered tag sequence — contexts, ordinary
// tags, and the hold marker are one list in the store — so it cannot be
// combined with `tags`, `contexts`, or `deferred`, each of which replaces a
// slice of that same list. `append_body` likewise cannot share a request with
// the `body` it would append to.
func validatePatchDeltas(body *jsonObject) error {
	for _, field := range tagDeltaFields {
		if !body.has(field) {
			continue
		}
		values, ok := body.stringList(field)
		if !ok || body.isNull(field) {
			return validationError(reason(field, "must be a list of text values"))
		}
		contexts := strings.HasSuffix(field, "_contexts")
		for _, value := range values {
			if contexts && (!strings.HasPrefix(value, "@") || len(value) == 1) {
				return validationError(reason(field, "each context must start with @"))
			}
			if !contexts && (value == "" || strings.HasPrefix(value, "@") || value == store.DeferTag) {
				return validationError(reason(field, "must contain ordinary tags only"))
			}
		}
	}
	for _, pair := range [][2]string{{"add_tags", "remove_tags"}, {"add_contexts", "remove_contexts"}} {
		added, _ := body.stringList(pair[0])
		removed, _ := body.stringList(pair[1])
		for _, value := range removed {
			if containsString(added, value) {
				return validationError(reason(pair[1], "cannot remove "+value+", which "+pair[0]+" adds"))
			}
		}
	}
	if body.has("append_body") {
		if text, ok := appendedText(body); !ok || text == "" {
			return validationError(reason("append_body", "must be non-empty text or a non-empty list of text lines"))
		}
	}

	conflicts := []fieldError{}
	for _, field := range tagDeltaFields {
		if !body.has(field) {
			continue
		}
		for _, replaced := range []string{"tags", "contexts", "deferred"} {
			if body.has(replaced) {
				conflicts = append(conflicts, reason(field, "cannot be combined with "+replaced))
			}
		}
	}
	if body.has("append_body") && body.has("body") {
		conflicts = append(conflicts, reason("append_body", "cannot be combined with body"))
	}
	if len(conflicts) > 0 {
		return validationError(conflicts...)
	}
	return nil
}

// appendedText is `append_body` as the one string the store appends: text as
// sent, or a list of lines joined the way `body` joins them.
func appendedText(body *jsonObject) (string, bool) {
	if body.isNull("append_body") {
		return "", false
	}
	if text, ok := body.text("append_body"); ok {
		return text, true
	}
	if lines, ok := body.stringList("append_body"); ok {
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

// deltaChanges maps the validated delta members onto the store's delta ops.
// The four tag lists become ONE `tag_delta`, which is what `tasks tag` sends
// for `+foo -bar @ctx -@old`.
func deltaChanges(body *jsonObject) []store.Change {
	changes := []store.Change{}
	tagged := false
	add, remove := []string{}, []string{}
	for _, field := range tagDeltaFields {
		if !body.has(field) {
			continue
		}
		tagged = true
		values, _ := body.stringList(field)
		if strings.HasPrefix(field, "add_") {
			add = append(add, values...)
		} else {
			remove = append(remove, values...)
		}
	}
	if tagged {
		changes = append(changes, store.Change{Field: store.FieldTagDelta, Value: store.TagDeltaValue(add, remove)})
	}
	if body.has("append_body") {
		text, _ := appendedText(body)
		changes = append(changes, store.Change{Field: store.FieldBodyAppend, Value: store.TextValue(text)})
	}
	return changes
}
