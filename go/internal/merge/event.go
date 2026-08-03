package merge

import (
	"fmt"
	"strings"
)

// The decisions a merge can record. They are the audit log's vocabulary: a
// merge that silently loses or duplicates a task is the failure nobody notices
// until the data is already wrong, so every record the merge touched says what
// happened to it and why.
const (
	DecisionDeleted                      = "deleted"
	DecisionDeletedByOurs                = "deleted_by_ours"
	DecisionDeletedByTheirs              = "deleted_by_theirs"
	DecisionKeptTheirsEditOverOursDelete = "kept_theirs_edit_over_ours_delete"
	DecisionKeptOursEditOverTheirsDelete = "kept_ours_edit_over_theirs_delete"
	DecisionAddedOurs                    = "added_ours"
	DecisionAddedTheirs                  = "added_theirs"
	DecisionMergedConcurrentAdd          = "merged_concurrent_add"
	DecisionMergedFields                 = "merged_fields"
	DecisionRestoredAncestor             = "restored_ancestor_for_edited_descendant"
	DecisionOursOrderingConflict         = "ours_ordering_conflict"
)

// Why one delegation marker outranked the other, and why a resolved marker was
// then cleared.
const (
	ReasonRemovalWins  = "removal_wins"
	ReasonClaimHolds   = "claim_holds"
	ReasonEarlierClaim = "earlier_claim"
	ReasonLaterIntent  = "later_intent"

	ClearedOnNonTask  = "cleared_on_non_task"
	ClearedOnProposal = "cleared_on_proposal"
	ClearedOnClose    = "cleared_on_close"
)

// DelegationEvent is the delegation half of one record's decision: which rule
// decided the marker, who holds it afterwards, and whether the resolved record
// then dropped it.
type DelegationEvent struct {
	Reason  string
	Holder  string
	Cleared string
}

// Event is one record's decision.
type Event struct {
	ID            string
	Decision      string
	Conflicts     []string
	LowConfidence []string
	Delegation    *DelegationEvent
}

type eventLog struct{ entries []Event }

func (l *eventLog) add(event Event) { l.entries = append(l.entries, event) }

// LogLines is the audit trail appended beside the store. A failed merge writes
// its reason and nothing else — there are no decisions to report, because none
// were applied.
func (r Result) LogLines(pathname string) []string {
	name := pathname
	if name == "" {
		name = "tasks JSONL"
	}
	status := "ok"
	if !r.OK() {
		status = "failed"
	}
	heading := fmt.Sprintf("merge %s: %s", name, status)
	if !r.OK() {
		return []string{heading, "  error: " + r.Error}
	}
	lines := []string{fmt.Sprintf("%s (%d decisions)", heading, len(r.Events))}
	for _, event := range r.Events {
		lines = append(lines, FormatEvent(event))
	}
	return lines
}

// FormatEvent renders one decision the way the log carries it.
func FormatEvent(event Event) string {
	details := make([]string, 0, 3)
	if conflicts := uniqueStrings(event.Conflicts); len(conflicts) > 0 {
		details = append(details, "conflicts="+strings.Join(conflicts, ","))
	}
	if low := uniqueStrings(event.LowConfidence); len(low) > 0 {
		details = append(details, "low-confidence="+strings.Join(low, ","))
	}
	if event.Delegation != nil {
		parts := make([]string, 0, 2)
		if event.Delegation.Reason != "" {
			parts = append(parts, event.Delegation.Reason)
		}
		if event.Delegation.Cleared != "" {
			parts = append(parts, event.Delegation.Cleared)
		}
		details = append(details, "delegation="+strings.Join(parts, "+"))
		if event.Delegation.Holder != "" {
			details = append(details, "holder="+event.Delegation.Holder)
		}
	}
	line := fmt.Sprintf("  %s %s", event.ID, event.Decision)
	if len(details) > 0 {
		line += " " + strings.Join(details, " ")
	}
	return line
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
