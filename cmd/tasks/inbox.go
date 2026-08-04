package main

// inbox prints the unfiled captures waiting to be triaged. It is deliberately
// flat and unranked: an inbox is a queue you empty, not a list you choose from,
// and any ordering would suggest a priority the capture never claimed.
func (s *surfaceContext) inbox(args []string) int {
	queries, status := s.readQueries(args, "inbox")
	if status != 0 {
		return status
	}
	items := queries.InboxItems()
	if hasFlag(args, "--json") {
		return s.emitItemsJSON(queries, items)
	}
	if len(items) == 0 {
		out("Inbox empty. ✨")
		return 0
	}
	for _, item := range items {
		out("  " + format(item))
	}
	return 0
}

func init() {
	register("inbox", (*surfaceContext).inbox)
}
