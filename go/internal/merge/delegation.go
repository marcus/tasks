package merge

import (
	"encoding/json"
	"strconv"
	"strings"

	"tasks-go/internal/record"
)

// mergeDelegation resolves the delegation marker as ONE atomic value: the
// merged record takes exactly one side's whole object, so a claim can never be
// spliced together from two devices.
//
// The winner is picked by a SINGLE total order over the two values, never by
// asking which side is "ours" or which record was written last. That matters
// because the merge is applied pairwise: an earlier rule that ordered claims by
// `at` but everything else by record-level last-write-wins is non-associative,
// and three devices could converge on DIFFERENT holders depending on the order
// their clones happened to sync. A maximum over one total order is associative
// and commutative, which is what makes "exactly one worker holds a claim" hold
// across multi-device merges. The order is:
//
//   - removal absorbs everything: if either side dropped a marker the base
//     carried, the merge drops it. Owner revocation (undelegate) is the
//     always-wins override, and the escape hatch for every rule below;
//   - a `claimed` marker beats any non-claimed one, so a live claim is never
//     silently downgraded by a concurrent edit that did not go through
//     revocation;
//   - two claims: earlier `at`, then the lexicographically smaller assignee,
//     then canonical bytes — first claim wins;
//   - two non-claimed markers: later `at`, then canonical bytes — the most
//     recent owner intent.
func mergeDelegation(merged *entry, base, ours, theirs *entry, event *Event) {
	baseValue := valueOf(base, delegationField)
	oursValue := valueOf(ours, delegationField)
	theirsValue := valueOf(theirs, delegationField)
	if sameValue(oursValue, theirsValue) {
		merged.assign(delegationField, oursValue)
		return
	}

	removed := !nilValue(baseValue) && (nilValue(oursValue) || nilValue(theirsValue))
	var winner json.RawMessage
	if !removed {
		winner = delegationMax(oursValue, theirsValue)
	}
	// A change only one side made is reported as a conflict only when the order
	// OVERRULES it — a released claim the other device still holds, say.
	// Confirming a one-sided edit is the ordinary field rule, and silent.
	other := oursValue
	if sameValue(oursValue, baseValue) {
		other = theirsValue
	}
	if (sameValue(oursValue, baseValue) || sameValue(theirsValue, baseValue)) && sameValue(winner, other) {
		merged.assign(delegationField, winner)
		return
	}

	event.Conflicts = append(event.Conflicts, delegationField)
	reason := ReasonRemovalWins
	if !removed {
		reason = delegationReason(oursValue, theirsValue)
	}
	event.Delegation = &DelegationEvent{Reason: reason, Holder: delegationAssignee(winner)}
	merged.assign(delegationField, winner)
}

// delegationMax is the greater of two markers under the total order documented
// above. nil — a side with no marker, with none in the base to have removed —
// ranks below every object, so a concurrent add still wins over "absent".
func delegationMax(left, right json.RawMessage) json.RawMessage {
	if nilValue(right) {
		return left
	}
	if nilValue(left) {
		return right
	}
	leftClaimed := delegationClaimed(left)
	rightClaimed := delegationClaimed(right)
	if leftClaimed != rightClaimed {
		if leftClaimed {
			return left
		}
		return right
	}

	byAt := strings.Compare(delegationString(left, "at"), delegationString(right, "at"))
	if byAt != 0 {
		// A claim is ranked by when it was TAKEN (first wins); any other marker
		// by when the owner last stated it (most recent wins).
		preferLeft := byAt > 0
		if leftClaimed {
			preferLeft = byAt < 0
		}
		if preferLeft {
			return left
		}
		return right
	}
	if leftClaimed {
		if byAssignee := strings.Compare(delegationString(left, "assignee"), delegationString(right, "assignee")); byAssignee != 0 {
			if byAssignee < 0 {
				return left
			}
			return right
		}
	}
	// Canonical bytes make the order total: two objects that tie on every
	// meaningful key still resolve the same way on both devices.
	if delegationBytes(left) <= delegationBytes(right) {
		return left
	}
	return right
}

func delegationReason(oursValue, theirsValue json.RawMessage) string {
	oursClaimed := delegationClaimed(oursValue)
	if oursClaimed != delegationClaimed(theirsValue) {
		return ReasonClaimHolds
	}
	if oursClaimed {
		return ReasonEarlierClaim
	}
	return ReasonLaterIntent
}

// settleDelegationState reconciles the resolved marker with the rest of the
// resolved record, because the parts were decided independently and only some
// combinations are legal:
//
//   - only a task can carry a delegation; a record whose `type` resolved to
//     something else drops it (defensive — no CLI or API operation turns a
//     delegated task into a section — but the alternative is aborting the whole
//     merge, which would block device sync until a hand repair);
//   - closed keeps a claim or a human delegation verbatim (who did it, and
//     where), but drops a marker still merely `ready` — nothing happened, the
//     same normalization a local close performs;
//   - a task the other side turned back into a proposal carries no delegation.
//
// Without this the merge could emit a record Check rejects, failing the whole
// merge over a pair of individually legal sides.
func settleDelegationState(merged *entry, event *Event) {
	delegation := merged.get(delegationField)
	if !delegationObject(delegation) {
		return
	}
	reason := ""
	switch {
	case merged.str("type") != "task":
		reason = ClearedOnNonTask
	case proposedStates[merged.str("state")]:
		reason = ClearedOnProposal
	case terminalStates[merged.str("state")] && delegationReady(delegation):
		reason = ClearedOnClose
	}
	if reason == "" {
		return
	}
	merged.delete(delegationField)
	if event.Delegation == nil {
		event.Delegation = &DelegationEvent{Reason: reason}
		return
	}
	event.Delegation.Cleared = reason
}

// delegationObject is Delegation.object?: a Hash that is not empty.
func delegationObject(raw json.RawMessage) bool {
	value, ok := delegationDecode(raw)
	return ok && len(value) > 0
}

func delegationClaimed(raw json.RawMessage) bool {
	value, ok := delegationDecode(raw)
	return ok && len(value) > 0 && record.DelegationClaimed(value)
}

func delegationReady(raw json.RawMessage) bool {
	value, ok := delegationDecode(raw)
	return ok && len(value) > 0 && record.DelegationReady(value)
}

func delegationDecode(raw json.RawMessage) (map[string]any, bool) {
	if nilValue(raw) {
		return nil, false
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	return value, true
}

func delegationAssignee(raw json.RawMessage) string {
	value, ok := delegationDecode(raw)
	if !ok {
		return ""
	}
	assignee, isText := value["assignee"].(string)
	if !isText {
		return ""
	}
	return assignee
}

// delegationString is Ruby's `marker[key].to_s`: a missing key is the empty
// string, and a value that is not a string still renders rather than aborting
// the comparison.
func delegationString(raw json.RawMessage, key string) string {
	value, ok := delegationDecode(raw)
	if !ok {
		return ""
	}
	switch typed := value[key].(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return rubyNumber(typed)
	default:
		return canonical(mustJSON(typed))
	}
}

func rubyNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return encoded
}

// delegationBytes is JSON.generate(Delegation.ordered(value)) — the canonical
// spelling of a marker, which is what makes the order total.
func delegationBytes(raw json.RawMessage) string {
	canonicalBytes, err := record.NestedCanonical(delegationField, raw)
	if err != nil {
		return string(raw)
	}
	return canonicalBytes
}
