package main

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// deleteTask hard-deletes a task's subtree from the live file (not the archive).
//
// A task with descendants is refused unless --cascade removes the whole subtree;
// deletion never reparents children. It is undoable through `tasks undo`.
//
// The removed tasks are gone after the write, so their headlines are captured
// BEFORE the mutation and the report is printed from that snapshot — there is
// nothing left to read them from afterwards.
func (s *surfaceContext) deleteTask(args []string) int {
	flags, rest, err := takeFlags(args, "--cascade", "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks delete <ref> [--cascade] [--dry-run] [--json] [--include-done]")
	}
	ref := rest[0]

	queries, status := s.readQueries(args, "delete")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{
		includeDone: flags["--include-done"], includeProposed: true,
	})
	if code != 0 {
		return code
	}
	if !item.HasID {
		return abort("task has no id")
	}

	removed := subtreeItems(queries, item)
	descendants := len(removed) - 1
	expectedRevision := queries.Snapshot().RevisionFor(item)

	if flags["--dry-run"] {
		if descendants > 0 {
			out(fmt.Sprintf("would delete %d tasks (%s): %s",
				len(removed), pluralize(descendants, "descendant"), taskquery.Headline(item)))
		} else {
			out("would delete: " + taskquery.Headline(item))
		}
		return 0
	}

	result := s.writeStore().DeleteTask(item.ID, flags["--cascade"], expectedRevision, "")
	if result.Status == store.MutationConflict {
		return abort(fmt.Sprintf(
			"refusing to delete: %s has %s (%d open). Re-run with --cascade to remove the whole subtree.",
			rubyInspectQuote(item.Title),
			pluralize(result.Summary.Descendants, "descendant"), result.Summary.OpenDescendants))
	}
	if status := mutationResultFailed(result, args, "delete", "failed to delete"); status != 0 {
		return status
	}

	if flags["--json"] {
		w := jsonWriter()
		w.BeginObject()
		w.Key("deleted")
		w.BeginArray()
		for _, gone := range removed {
			writeItemJSON(w, queries, gone)
		}
		w.EndArray()
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	for _, gone := range removed {
		out(taskquery.Headline(gone))
	}
	return 0
}

// subtreeItems is the root and every task beneath it, in file order — the set
// the delete removes. It reads from the PRE-delete snapshot for the reason the
// command captures anything at all beforehand: afterwards there is nothing left
// to describe.
func subtreeItems(queries *taskquery.Queries, root store.Item) []store.Item {
	node := queries.NodeFor(root)
	if node == nil {
		return []store.Item{root}
	}
	items := []store.Item{}
	var walk func(*taskquery.Node)
	walk = func(current *taskquery.Node) {
		if current.Item != nil {
			items = append(items, *current.Item)
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
	if len(items) == 0 {
		return []store.Item{root}
	}
	return items
}

func init() {
	register("delete", (*surfaceContext).deleteTask)
}
