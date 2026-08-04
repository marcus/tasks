package main

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/taskquery"
)

// projects lists the projects and areas with their rollups. It is the weekly
// review's read: not "what should I do next" but "is anything I committed to
// quietly stalled", which is what the `(stuck)` marker answers.
func (s *surfaceContext) projects(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	// `projects` takes no arguments at all: a stray word is more likely a ref
	// meant for `project show` than something to ignore.
	if len(rest) > 0 {
		return abort("usage: tasks projects [--json]")
	}
	queries, status := s.readQueries(args, "projects")
	if status != 0 {
		return status
	}
	// A rollup over an invalid file is worse than no rollup: the counts would be
	// computed from whatever records survived parsing, and "2 open" from a file
	// that actually holds five is the kind of wrong answer a weekly review acts
	// on. Ruby reaches this refusal through the application's checked read; the
	// bytes and exit status are the same either way.
	if checked, err := s.store.CheckedReadSnapshot(); err != nil || !checked.OK() {
		return abort("task file is invalid — run `tasks check`")
	}
	views := queries.Projects()

	if flags["--json"] {
		w := jsonout.New()
		w.BeginArray()
		for _, view := range views {
			writeProjectJSON(w, view)
		}
		w.EndArray()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	if len(views) == 0 {
		out("No projects or areas.")
		return 0
	}
	width := 0
	for _, view := range views {
		if length := len([]rune(view.Title)); length > width {
			width = length
		}
	}
	for _, group := range [][2]string{{"project", "Projects"}, {"area", "Areas"}} {
		matched := []taskquery.ProjectView{}
		for _, view := range views {
			if view.Kind == group[0] {
				matched = append(matched, view)
			}
		}
		if len(matched) == 0 {
			continue
		}
		out(bold(group[1]))
		for _, view := range matched {
			out("  " + projectRow(view, width))
		}
		out("")
	}
	return 0
}

func projectRow(view taskquery.ProjectView, width int) string {
	title := view.Title
	if pad := width - len([]rune(title)); pad > 0 {
		title += strings.Repeat(" ", pad)
	}
	return title + "  " + projectSummary(view)
}

// projectSummary is one project or area's rollup: "3 open · 1 next · next 7/25"
// plus the stuck marker. A timed next date shows its WALL time instead of the
// day, because a rollup whose soonest item is at 17:00 today says more than one
// that only says "today".
func projectSummary(view taskquery.ProjectView) string {
	parts := []string{
		fmt.Sprintf("%d open", view.OpenCount),
		fmt.Sprintf("%d next", view.NextCount),
	}
	if view.HasNextDate {
		when := fmt.Sprintf("%d/%d", int(view.NextDate.Month), view.NextDate.Day)
		if view.HasNextTime && view.NextTime.Local != "" {
			when = view.NextTime.Local
		}
		parts = append(parts, "next "+when)
	}
	summary := dim(strings.Join(parts, " · "))
	if view.Stuck {
		summary += "  " + yellow("(stuck)")
	}
	return summary
}

// writeProjectJSON is the canonical project resource. Absent fields are OMITTED
// rather than emitted as null, so the document stays as lean as the record it
// rolls up — an area has no parent, and saying so with a null would invent a
// field the section never had. `stuck` and `held_count` are always present:
// false and 0 are answers, not absences.
func writeProjectJSON(w *jsonout.Writer, view taskquery.ProjectView) {
	w.BeginObject()
	w.KeyStr("id", view.ID)
	w.KeyStr("title", view.Title)
	if view.HasParentID {
		w.KeyStr("parent_id", view.ParentID)
	}
	w.KeyStr("kind", view.Kind)
	w.KeyInt("open_count", view.OpenCount)
	w.KeyInt("next_count", view.NextCount)
	if view.HasNextDate {
		w.KeyStr("next_date", view.NextDate.ISO())
	}
	if view.HasNextTime {
		w.Key("next_time")
		writeAPITimeValue(w, view.NextTime)
	}
	if !view.NextAt.IsZero() {
		w.KeyStr("next_at", view.NextAt.Format("2006-01-02T15:04:05Z"))
	}
	w.KeyBool("stuck", view.Stuck)
	w.KeyInt("held_count", view.HeldCount)
	if view.HasBody {
		w.KeyStr("body", view.Body)
	}
	w.Key("task_ids")
	w.Strings(view.TaskIDs)
	w.EndObject()
}

func init() {
	register("projects", (*surfaceContext).projects)
}
