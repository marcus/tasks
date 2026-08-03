package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tasks-go/internal/query"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// refExit is the status a ref that resolved to nothing, or to more than one
// task, exits with. It is NOT 1, and the distinction is load-bearing: exit 2
// means "your ref was wrong, refine it and try again", which an agent can act
// on, while exit 1 means the command itself failed. A port that collapsed the
// two would pass almost every other assertion in the corpus and quietly break
// the one loop agents depend on.
const refExit = 2

var linePattern = regexp.MustCompile(`(?i)\AL(\d+)\z`)

// done marks a matching open task DONE. Ref resolution runs FIRST and refuses
// before any write is contemplated — which is the whole of what this build can
// honestly do, since completing a task is a mutation.
func (s *surfaceContext) done(args []string) int {
	flags, rest, _ := takeFlags(args, "--dry-run", "--json", "--include-done")
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort(`usage: tasks done "<ref>"`)
	}
	ref := rest[0]

	queries, status := s.readQueries()
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, flags["--include-done"])
	if code != 0 {
		return code
	}
	return abort(fmt.Sprintf("done: not implemented in the Go port — %q resolved to %q, but "+
		"completing it would rewrite the task file, and this build has no write path",
		ref, taskquery.Headline(item)))
}

// resolveRef resolves a ref to exactly one in-scope item, or refuses with exit
// 2. The precedence is exact before fuzzy: `L<line>` names a physical headline,
// then an exact (case-insensitive) id, then a case-insensitive title substring.
// A ref that names a live task OUTSIDE the requested scope says so explicitly
// rather than pretending the task does not exist — "no match" for a task you
// can see in the file is the least actionable answer there is.
func resolveRef(queries *taskquery.Queries, ref string, includeDone bool) (store.Item, int) {
	all := queries.LiveItems()
	items := all
	if !includeDone {
		items = []store.Item{}
		for _, item := range all {
			if contains(taskquery.OpenStates(), item.State) {
				items = append(items, item)
			}
		}
	}

	if match := linePattern.FindStringSubmatch(strings.TrimSpace(ref)); match != nil {
		line, _ := strconv.Atoi(match[1])
		for _, item := range items {
			if item.Line == line {
				return item, 0
			}
		}
		for _, item := range all {
			if item.Line == line {
				return store.Item{}, refOutsideScope(ref, item, includeDone)
			}
		}
		qualifier := "open "
		if includeDone {
			qualifier = ""
		}
		return store.Item{}, refFailed(fmt.Sprintf("no %stask with a headline on line %s", qualifier, match[1]))
	}

	// An exact id match is unambiguous, so it wins over fuzzy title matching.
	needle := query.Downcase(strings.TrimSpace(ref))
	for _, item := range items {
		if item.HasID && query.Downcase(item.ID) == needle {
			return item, 0
		}
	}
	for _, item := range all {
		if item.HasID && query.Downcase(item.ID) == needle {
			return store.Item{}, refOutsideScope(ref, item, includeDone)
		}
	}

	matches := []store.Item{}
	for _, item := range items {
		if strings.Contains(query.Downcase(item.Title), needle) {
			matches = append(matches, item)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], 0
	case len(matches) > 1:
		lines := []string{fmt.Sprintf("ambiguous: %s — matches %d tasks:", ref, len(matches))}
		for _, item := range matches {
			lines = append(lines, fmt.Sprintf("  L%d: %s", item.Line, taskquery.Headline(item)))
		}
		return store.Item{}, refFailed(strings.Join(lines, "\n"))
	}

	outside := []store.Item{}
	for _, item := range all {
		if strings.Contains(query.Downcase(item.Title), needle) {
			outside = append(outside, item)
		}
	}
	if len(outside) == 1 {
		return store.Item{}, refOutsideScope(ref, outside[0], includeDone)
	}
	if len(outside) > 1 {
		lines := []string{fmt.Sprintf("ref outside scope: %s — %d title matches, none is %s:",
			ref, len(outside), refScopeDescription(includeDone))}
		for _, item := range outside {
			lines = append(lines, fmt.Sprintf("  L%d: %s", item.Line, taskquery.Headline(item)))
		}
		return store.Item{}, refFailed(strings.Join(lines, "\n"))
	}
	return store.Item{}, refFailed("no match: " + ref)
}

func refOutsideScope(ref string, item store.Item, includeDone bool) int {
	return refFailed(fmt.Sprintf("ref outside scope: %s — task is %s; expected %s",
		ref, item.State, refScopeDescription(includeDone)))
}

func refScopeDescription(includeDone bool) string {
	if includeDone {
		return "a live task"
	}
	return "an open task"
}

// refFailed prints the diagnostic and returns exit 2. The bytes are contract:
// the candidate list is what an agent reads back to refine its ref.
func refFailed(message string) int {
	fmt.Fprintln(os.Stderr, message)
	return refExit
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
