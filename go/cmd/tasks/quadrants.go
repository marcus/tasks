package main

import (
	"tasks-go/internal/jsonout"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// quadrants prints the available open work in Covey's Important/Urgent 2x2.
// Every quadrant prints, empty ones included: the empty Q1 is the reassuring
// answer, and a view that hid it would only ever show you what is on fire.
func (s *surfaceContext) quadrants(args []string) int {
	queries, status := s.readQueries(args, "quadrants")
	if status != 0 {
		return status
	}
	items := queries.QuadrantItems()
	urgentDays := s.paths.UrgentDays

	if hasFlag(args, "--json") {
		w := jsonout.New()
		w.BeginArray()
		for _, item := range items {
			// The quadrant rides along as one more member of the standard row.
			// Ruby merges it into the same object rather than nesting it, so a
			// consumer reads one flat shape whichever read command produced it.
			writeItemJSONWith(w, queries, item, func(w *jsonout.Writer) {
				w.KeyStr("quadrant", queries.QuadrantOf(item, urgentDays))
			})
		}
		w.EndArray()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	byQuadrant := map[string][]store.Item{}
	for _, item := range items {
		key := queries.QuadrantOf(item, urgentDays)
		byQuadrant[key] = append(byQuadrant[key], item)
	}
	for _, entry := range taskquery.QuadrantLabels {
		out(bold(entry[1]))
		matched := byQuadrant[entry[0]]
		if len(matched) == 0 {
			out("  —")
		}
		for _, item := range matched {
			out("  " + format(item))
		}
		out("")
	}
	return 0
}

func init() {
	register("quadrants", (*surfaceContext).quadrants)
}
