package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

const leadUsage = `usage: tasks lead <ref> <span|off> [--dry-run] [--json]
       tasks lead <ref>                              read-only: the window it opens`

// leadHint is what to type when the parser cannot read a span: one example per
// shape. A refusal that only says "unreadable" leaves the user guessing at a
// grammar they cannot see.
const leadHint = `try: 3w · 2d · 1m · "3 weeks" · "a week" · "10 days" · off`

// leadCommand attaches, replaces, or clears the lead-time window: how long
// before its date a task becomes visible. With no span it is READ-ONLY, which
// is what makes it safe for an agent to call before committing to a change.
func (s *surfaceContext) leadCommand(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "lead"); refusal != 0 {
		return refusal
	}
	flags, rest, err := takeFlags(args, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	ref := ""
	if len(rest) > 0 {
		ref = rest[0]
	}
	input := ""
	if len(rest) > 1 {
		input = joinPositional(rest[1:])
	}
	if strings.TrimSpace(ref) == "" {
		return abort(leadUsage)
	}

	queries, status := s.readQueries(args, "lead")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{
		includeDone: flags["--include-done"], includeProposed: true,
	})
	if code != 0 {
		return code
	}
	if input == "" {
		return s.leadPreview(queries, item, flags["--json"])
	}

	parsed := lead.Parse(input)
	if parsed.Error != "" {
		return abort(parsed.Error + "\n" + leadHint)
	}
	off := parsed.IsOff()
	span := parsed.Canonical

	// The store owns the five rules. These two refusals exist only because the
	// CLI can name the exact flag combination that caused them, and a sentence
	// naming the fix is worth more than the store's general form of the same no.
	_, hasAnchor := queries.LeadAnchorValue(item)
	if !off && !hasAnchor {
		return abort("“" + item.Title + "” has no date to hide before — " +
			"add a deadline (`tasks due`) or an available-from date (`tasks schedule`) first")
	}
	if !off && item.Deadline != "" && item.Scheduled != "" {
		human, _ := lead.Humanize(span)
		return abort("a lead of " + human + " measures from the deadline, so an " +
			"available-from date would be a second, ignored gate — pick one")
	}

	ownText := `clear the lead time on "` + item.Title + `"`
	if !off {
		window := ""
		if anchor, ok := queries.LeadAnchorValue(item); ok {
			window = " (" + span + " before " + anchor.Date.ISO() + ")"
		}
		// The mutation clause names the window; the availability clause that
		// follows carries the resulting date, so stating it twice would only
		// invite the two to disagree.
		ownText = `lead time ` + span + ` on "` + item.Title + `"` + window
	}

	override := taskquery.Override{Lead: leadOverride(off, span)}
	if flags["--dry-run"] {
		snapshot, status := s.readSnapshotFor()
		if status != 0 {
			return status
		}
		return s.printAvailabilityChange(snapshot, item.ID, ownText, true, override)
	}

	label := "lead off: " + item.Title
	value := store.NoValue()
	if !off {
		label = "lead " + span + ": " + item.Title
		value = store.TextValue(span)
	}
	return s.patchValueReporting(args, item, store.FieldLead, value, label,
		"lead", "failed to set the lead time", flags["--json"],
		func(result store.MutationResult) int {
			if flags["--json"] {
				touched := result.TouchedIDs
				if len(touched) == 0 {
					touched = []string{item.ID}
				}
				return s.reportTouched(result, touched, true)
			}
			return s.printAvailabilityChange(result.ReadSnapshot, item.ID, ownText, false,
				taskquery.Override{})
		})
}

// leadOverride is the span a preview substitutes: the empty string clears the
// window, which is what `off` asks for.
func leadOverride(off bool, span string) *string {
	if off {
		cleared := ""
		return &cleared
	}
	return &span
}

// leadPreview is `tasks lead <ref>` — read-only: the stored span and the date
// its window opens.
func (s *surfaceContext) leadPreview(queries *taskquery.Queries, item store.Item, asJSON bool) int {
	span := strings.TrimSpace(item.Lead)
	anchor, hasAnchor := queries.LeadAnchorValue(item)
	gate, hasGate := temporal.Value{}, false
	if lead.Span(span) {
		gate, hasGate = queries.LeadWindowValue(item)
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("id", item.ID)
		w.KeyInt("line", item.Line)
		w.KeyStr("title", item.Title)
		w.KeyStrOrNull("lead", span)
		human := ""
		if span != "" {
			if described, ok := lead.Humanize(span); ok {
				human = described
			} else {
				human = span
			}
		}
		w.KeyStrOrNull("lead_human", human)
		w.Key("anchor")
		if hasAnchor {
			w.Str(anchor.Date.ISO())
		} else {
			w.Null()
		}
		w.Key("opens")
		if hasGate {
			w.Str(gate.Date.ISO())
		} else {
			w.Null()
		}
		w.Key("opens_at")
		if instant, err := gate.ReleaseInstant(queries.Context()); hasGate && err == nil {
			w.Str(instant.UTC().Format("2006-01-02T15:04:05Z"))
		} else {
			w.Null()
		}
		w.KeyStrOrNull("lead_skip", item.LeadSkip)
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	out(taskquery.Headline(item))
	if span == "" {
		out("  no lead time — set one with `tasks lead " + rubyInspectQuote(item.Title) + " 3w`")
		return 0
	}
	described, _ := lead.Describe(span)
	out("  ⏳ " + described + " " + dim("("+span+")"))
	if line, ok := leadGateLine(queries, item); ok {
		out("  opens " + line)
	} else {
		fmt.Fprintln(os.Stderr, "no date to hide before — add a deadline or an available-from date")
	}
	if hasAnchor && item.LeadSkip == anchor.Date.ISO() {
		out("  " + dim("this occurrence was released early (activate)"))
	}
	return 0
}

func init() {
	register("lead", (*surfaceContext).leadCommand)
}
