package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// show prints one task in full: its headline, where it sits, when it is
// workable, and everything the record carries that a list row has no room for.
//
// It is a pure READ. A concurrent write that removes the task between resolving
// the ref and rendering it falls back to the held item rather than aborting
// with mutation-flavoured error text and a misleading rollback hint — nothing
// was being written, so nothing can have been rolled back.
func (s *surfaceContext) show(args []string) int {
	flags, rest, err := takeFlags(args, "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks show <ref>")
	}
	queries, status := s.readQueries(args, "show")
	if status != 0 {
		return status
	}
	item, refStatus := resolveRef(queries, rest[0],
		refScope{includeDone: flags["--include-done"], includeProposed: true})
	if refStatus != 0 {
		return refStatus
	}

	found := queries.Links(item)
	notes := taskNotes(queries, item)
	project, hasProject := queries.Project(item)

	if flags["--json"] {
		w := jsonout.New()
		writeItemJSONWith(w, queries, item, func(w *jsonout.Writer) {
			w.KeyStrOrNull("closed", item.Closed)
			w.Key("notes")
			w.Strings(notes)
			// `project` is deliberately NOT written again: the standard row
			// already carries it, and Ruby's merge overwrites in place rather
			// than appending, so a second member here would be a shape the
			// oracle never emits.
			w.Key("links")
			w.BeginArray()
			for _, link := range found {
				w.BeginObject()
				writeLinkMembers(w, link)
				w.EndObject()
			}
			w.EndArray()
		})
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	out(taskquery.Headline(item))
	if item.ID != "" {
		out("  id:        " + item.ID)
	}
	if hasProject {
		out("  project:   " + project)
	}
	if marker := delegationFields(item.Delegation); marker != nil {
		// The transition stamp is stored UTC and READ locally: every other
		// instant on this screen is already projected into the configured zone,
		// and "since 18:03Z" would be the one line asking the reader to convert.
		//
		// A marker missing its stamp drops the parenthetical rather than
		// printing an empty one: "(since )" answers nothing and looks like a
		// rendering fault rather than the malformed record it is reporting.
		line := "  delegation: " + delegationSummary(marker)
		if at := marker["at"]; at != "" {
			line += " " + dim(fmt.Sprintf("(since %s)", queries.Context().StampLabel(at)))
		}
		out(line)
		if ref := marker["work_ref"]; ref != "" {
			out("  work ref:  " + ref)
		}
		// The briefing prints IN FULL and keeps its paragraphs. It is the
		// instruction the receiver is meant to act on, so truncating it here
		// would make `show` the one place that cannot answer what was asked.
		if note := marker["note"]; note != "" {
			lines := strings.Split(note, "\n")
			out("  note:      " + lines[0])
			for _, line := range lines[1:] {
				out("             " + line)
			}
		}
	}
	if item.Scheduled != "" {
		value, ok := queries.ScheduledValue(item)
		label := item.Scheduled
		if ok {
			label = temporalLabel(&value)
		}
		out("  available from: " + label)
	}
	if item.Deadline != "" {
		value, ok := queries.DeadlineValue(item)
		label := item.Deadline
		if ok {
			label = temporalLabel(&value)
		}
		out("  deadline:  " + label)
	}
	out("  availability: " + availabilitySummary(queries, queries.AvailabilityFor(item)))
	if item.Recur != "" {
		out(fmt.Sprintf("  recur:     %s %s", item.Recur, dim("("+recurSummary(item.Recur)+")")))
	}
	if item.Lead != "" {
		human, known := lead.Humanize(item.Lead)
		if !known || !lead.Span(item.Lead) {
			human = item.Lead
		}
		out(fmt.Sprintf("  lead:      %s %s", item.Lead, dim("("+human+")")))
		if opens, ok := leadGateLine(queries, item); ok {
			out("  opens:     " + opens)
		}
	}
	if item.Closed != "" {
		out("  closed:    " + item.Closed)
	}
	for _, note := range notes {
		out("  " + note)
	}
	if len(found) > 0 {
		width := 0
		for _, link := range found {
			if length := len([]rune(link.System)); length > width {
				width = length
			}
		}
		for _, link := range found {
			out(linkLine(link, width))
		}
	}
	return 0
}

// taskNotes is the body as `show` prints it: each line trimmed, blank lines
// dropped. The stored body keeps its own whitespace; this is presentation.
func taskNotes(queries *taskquery.Queries, item store.Item) []string {
	notes := []string{}
	for _, line := range queries.Body(item) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			notes = append(notes, trimmed)
		}
	}
	return notes
}

// delegationSummary is the delegation line without the title, for show's field
// list. It is the same vocabulary the list headline uses, so a task reads the
// same way in both places.
func delegationSummary(marker map[string]string) string {
	switch marker["status"] {
	case "delegated":
		// A mode is optional on a human delegation, so it appears only when the
		// owner stated one — "→ pat@example.com" and "→ pat@example.com
		// (review)" are both complete answers.
		if mode := marker["mode"]; mode != "" {
			return "→ " + marker["assignee"] + " (" + mode + ")"
		}
		return "→ " + marker["assignee"]
	case "ready":
		return fmt.Sprintf("agent-ready (%s)", marker["mode"])
	case "claimed":
		return fmt.Sprintf("claimed by %s (%s)", marker["assignee"], marker["mode"])
	default:
		return marker["status"]
	}
}

// availabilitySummary is the prose answer to "can I work on this now", naming
// the blocker when one exists. An id alone would not be actionable, so the
// blocker's title comes with it.
func availabilitySummary(queries *taskquery.Queries, availability taskquery.Availability) string {
	via := ""
	if blocker, found := queries.FindLive(availability.BlockerID); found {
		via = fmt.Sprintf(" via %q [%s]", blocker.Title, blocker.ID)
	}
	switch availability.Reason {
	case taskquery.ReasonAvailable:
		return "available now"
	case taskquery.ReasonScheduled:
		return "unavailable until " + temporalLabel(availability.Value)
	case taskquery.ReasonAncestorScheduled:
		return "unavailable until " + temporalLabel(availability.Value) + via
	case taskquery.ReasonOnHold:
		return "on hold indefinitely"
	case taskquery.ReasonAncestorOnHold:
		return "still on hold indefinitely" + via
	case taskquery.ReasonProposed:
		return "awaiting approval"
	case taskquery.ReasonClosed:
		return "closed"
	default:
		return "unavailable"
	}
}

// leadGateLine is "2026-10-11 (Sun) — 3 weeks before 2026-11-01", or, with a
// clock lead, the time it opens as well. The window comes from the read model's
// own derivation, so what `show` prints is the instant availability was decided
// on rather than a second, separately-computed answer.
func leadGateLine(queries *taskquery.Queries, item store.Item) (string, bool) {
	anchor, hasAnchor := queries.LeadAnchorValue(item)
	// The WINDOW, not the gate: a released occurrence still has a window for its
	// current anchor, and `show` is describing the record rather than deciding
	// what is workable now.
	opens, hasOpens := queries.LeadWindowValue(item)
	if !hasAnchor || !hasOpens {
		return "", false
	}
	human, known := lead.Humanize(item.Lead)
	if !known {
		return "", false
	}
	stamp := fmt.Sprintf("%s (%s)", opens.Date.ISO(), weekdayAbbrev(opens.Date))
	if opens.LocalTime != "" {
		stamp = temporalLabel(&opens)
	}
	return fmt.Sprintf("%s — %s before %s", stamp, human, anchor.Date.ISO()), true
}

// weekdayAbbrev is Date#strftime("%a"). The lead line names the day because
// "2026-10-11" does not tell you it is a Sunday, and whether a window opens on
// a weekend is exactly what a reader is checking.
func weekdayAbbrev(date temporal.Date) string {
	return time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC).Format("Mon")
}

func init() {
	register("show", (*surfaceContext).show)
}
