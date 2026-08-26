package main

import (
	"fmt"
	"sort"

	"github.com/marcus/tasks/internal/store"
)

// next prints the available NEXT actions grouped by context, because a NEXT
// list is answered by where you are: @errands is useless at a desk and
// @computer is useless in the car. A task with several contexts appears under
// each of them — it is genuinely doable in each place.
func (s *surfaceContext) next(args []string) int {
	queries, status := s.readQueries(args, "next")
	if status != 0 {
		return status
	}
	items := queries.NextItems()
	if hasFlag(args, "--json") {
		return s.emitItemsJSON(queries, items)
	}
	// Silence is the one thing an empty NEXT list must not be. Nothing marked
	// NEXT is not the same fact as nothing to do — dated work usually still sits
	// on the agenda, unmarked — and a blank screen cannot tell the two apart or
	// say which command fixes it.
	if len(items) == 0 {
		out("No next actions. Dated work is on the agenda; mark one with: tasks state <ref> NEXT")
		return 0
	}

	const noContext = "(no context)"
	byContext := map[string][]store.Item{}
	names := []string{}
	for _, item := range items {
		contexts := item.Contexts
		if len(contexts) == 0 {
			contexts = []string{noContext}
		}
		for _, context := range contexts {
			if _, seen := byContext[context]; !seen {
				names = append(names, context)
			}
			byContext[context] = append(byContext[context], item)
		}
	}
	// Ruby sorts the grouped hash, which orders by the context STRING: "(no
	// context)" sorts ahead of every "@name" because "(" precedes "@".
	sort.Strings(names)

	for _, context := range names {
		out(bold(context))
		// The canonical NEXT order is already priority-sorted with file-order
		// ties; a per-group re-sort here would re-break those ties.
		for _, item := range byContext[context] {
			due := shortDue(queries, item)
			if due != "" {
				due = "  " + due
			}
			out(fmt.Sprintf("  %s%s", format(item), due))
		}
		out("")
	}
	return 0
}

func init() {
	register("next", (*surfaceContext).next)
}
