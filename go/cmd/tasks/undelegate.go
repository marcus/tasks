package main

import (
	"strings"

	"tasks-go/internal/store"
)

// undelegate clears the delegation marker, revoking any live claim.
//
// Lifecycle state is deliberately untouched: undelegating does not automatically
// leave WAITING. The owner decided to wait on someone; only the owner decides
// they are done waiting.
func (s *surfaceContext) undelegate(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks undelegate <ref> [--json]")
	}

	queries, status := s.readQueries(args, "undelegate")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, rest[0], refScope{includeDone: true})
	if code != 0 {
		return code
	}

	result := s.writeStore().Undelegate(item.ID, s.delegationCoalesceKey("undelegate"))
	if status := s.delegationFailed(result, args, "undelegate"); status != 0 {
		return status
	}
	if flags["--json"] {
		return s.reportTouched(result, []string{item.ID}, true)
	}
	// An already-undelegated task is a no_change, and saying "undelegated" for
	// it would claim a write that did not happen.
	verb := "undelegated: "
	if result.Status == store.MutationNoChange {
		verb = "not delegated: "
	}
	out(verb + s.delegationTitle(result, item))
	return 0
}

// delegationTitle is the post-write title, falling back to the resolved item's
// when the write produced no snapshot — which an idempotent repeat does, because
// it deliberately wrote nothing.
func (s *surfaceContext) delegationTitle(result store.MutationResult, item store.Item) string {
	if found, ok := s.delegationItem(result, item.ID); ok {
		return found.Title
	}
	return item.Title
}

func (s *surfaceContext) delegationItem(result store.MutationResult, id string) (store.Item, bool) {
	snapshot := result.ReadSnapshot
	if snapshot == nil {
		fresh, err := s.store.ReadSnapshot(false)
		if err != nil {
			return store.Item{}, false
		}
		snapshot = fresh
	}
	for _, item := range snapshot.Items {
		if item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}

func init() {
	register("undelegate", (*surfaceContext).undelegate)
}
