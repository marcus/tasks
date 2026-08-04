package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"

	"fmt"
)

const recurUsage = `usage: tasks recur <ref> [<schedule>] [--from schedule|completion] [--on <date>]
       tasks recur <ref> [--count N] [--json]              preview the next occurrences
       tasks recur --explain "<schedule>" [--count N] [--json]`

// recurHint is what to type when the parser cannot read a schedule: one example
// per shape the grammar accepts.
const recurHint = "try: weekly · 2w · every 3 days · .+1m · every mon,wed · m:15 · " +
	"2nd tuesday · last day of the month · every july 4 · off"

// recurCommand attaches, replaces, or clears a repeat schedule — or, with no
// schedule argument, previews when the task fires next.
//
// The three modes are one command because they answer one question at three
// levels of commitment: what would this schedule do (`--explain`), what does
// this task's schedule do (bare ref), and make it so. Splitting them would make
// the safe read a different command from the write it precedes.
func (s *surfaceContext) recurCommand(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "recur"); refusal != 0 {
		return refusal
	}
	from, remaining, _, status := takeFlagValue(args, "--from")
	if status != 0 {
		return status
	}
	if from != "" && from != "schedule" && from != "completion" {
		return abort("--from must be schedule or completion")
	}
	on, remaining, hasOn, status := takeFlagValue(remaining, "--on")
	if status != 0 {
		return status
	}
	count, remaining, hasCount, status := takeRecurCount(remaining)
	if status != 0 {
		return status
	}

	flags, rest, err := takeFlags(remaining, "--dry-run", "--json", "--include-done", "--explain")
	if err != nil {
		return abort(err.Error())
	}

	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	today := context.LocalDate()

	var onDate temporal.Date
	if hasOn {
		parsed, ok := temporal.ParseWhen(on, today, s.dateOrder())
		if !ok {
			return abort("unrecognized date: " + on)
		}
		onDate = parsed
	}

	// `--explain` never touches the store: it parses, renders, and projects.
	if flags["--explain"] {
		if from != "" || hasOn || flags["--dry-run"] {
			return abort("--explain previews a schedule, not a task — drop --from/--on/--dry-run")
		}
		input := joinPositional(rest)
		if input == "" {
			return abort(recurUsage)
		}
		return recurExplain(input, defaultCount(count, hasCount), flags["--json"], today)
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
		return abort(recurUsage)
	}

	queries, status := s.readQueries(args, "recur")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}

	// No schedule argument: a read-only preview of the schedule already stored.
	if input == "" {
		if from != "" || hasOn || flags["--dry-run"] {
			return abort("`tasks recur <ref>` with no schedule is read-only — pass a schedule to set one")
		}
		return recurPreview(queries, item, defaultCount(count, hasCount), flags["--json"], today)
	}
	if hasCount {
		return abort("--count previews occurrences; it does not apply when setting a schedule")
	}

	defaultPrefix := ".+"
	if from == "schedule" {
		defaultPrefix = "+"
	}
	parsed := recur.Parse(input, defaultPrefix)
	if parsed.Error != "" {
		return abort(parsed.Error + "\n" + recurHint)
	}
	cookie := parsed.Canonical
	off := cookie == "off"

	// --from is interval-only: a calendar schedule carries its own advance
	// semantics in the `+` prefix, so point at the prefix the input does NOT
	// have rather than repeating that the flag does not apply.
	if from != "" && !off && recur.Calendar(cookie) {
		advice := cookie + " advances to the next match after today; " +
			"write +" + cookie + " to advance one occurrence past the stored date instead"
		if strings.HasPrefix(cookie, "+") {
			advice = cookie + " already advances one occurrence past the stored date; " +
				"drop the + to advance to the next match after today instead"
		}
		return abort("--from " + from + " applies to intervals, not calendar schedules — " + advice)
	}

	// A closed task with a live repeater is dead recurrence: `done` can never
	// reach it to roll it forward. Refuse to create one; clearing is always fine.
	if !off && contains(taskquery.ClosedStates(), item.State) {
		return abort("can't set recurrence on a " + item.State + " task — reopen it first")
	}
	hasDate := item.Scheduled != "" || item.Deadline != ""
	if !off && !hasDate && !hasOn {
		return abort("“" + item.Title + "” has no date to repeat — " +
			"add --on <date>, or schedule/`due` it first")
	}
	// Clearing recurrence from a task that has no date (so cannot be recurring)
	// is a harmless no-op, not an error.
	noopOff := off && !hasDate

	if flags["--dry-run"] {
		description := "set recurrence " + cookie + " (" + recurSummary(cookie) + ")"
		switch {
		case noopOff:
			description = "leave unchanged (no recurrence set)"
		case off:
			description = "clear recurrence"
		}
		if hasOn && !hasDate {
			description += ", seeding DEADLINE <" + onDate.ISO() + ">"
		}
		out("would " + description + " on: " + taskquery.Headline(item))
		return 0
	}

	if noopOff {
		snapshot, status := s.readSnapshotFor()
		if status != 0 {
			return status
		}
		return s.reportTouchedSnapshot(snapshot, []string{item.ID}, flags["--json"], nil)
	}

	label := "recur " + cookie + ": " + item.Title
	if off {
		label = "recur off: " + item.Title
	}
	report := func(result store.MutationResult) int {
		// The stamp on the task IS its next occurrence, so that is what gets
		// reported — the same value, and the same phrasing, `done` prints after
		// a roll.
		next := ""
		if !off {
			for _, fresh := range result.ReadSnapshot.Items() {
				if fresh.ID != item.ID {
					continue
				}
				next = fresh.Deadline
				if next == "" {
					next = fresh.Scheduled
				}
				break
			}
		}
		if !off && !flags["--json"] {
			line := "↻ " + recurSummary(cookie) + " " + dim("("+cookie+")")
			if date, ok := temporal.ParseDate(next); ok {
				line += " → next " + date.ISO() + " (" + weekdayAbbrev(date) + ")"
			}
			out(line)
		}
		touched := result.TouchedIDs
		if len(touched) == 0 {
			touched = []string{item.ID}
		}
		var extra func(*jsonout.Writer)
		if next != "" {
			extra = func(w *jsonout.Writer) { w.KeyStr("next", next) }
		}
		return s.reportTouchedSnapshot(result.ReadSnapshot, touched, flags["--json"], extra)
	}

	summary := "failed to set recurrence (no date stamp?)"
	value := store.NoValue()
	if !off {
		value = store.TextValue(cookie)
	}
	if hasOn && !hasDate {
		// Seed and schedule land in ONE checked transaction: the store applies
		// dates before recurrence, so the satisfiability guard anchors on the
		// new deadline, a refusal leaves the file untouched, and success is one
		// undo rather than two.
		return s.changesetAndReport(args, item, []store.Change{
			{Field: store.FieldDeadline, Value: store.TemporalValue(temporal.Value{Date: onDate})},
			{Field: store.FieldRecurrence, Value: value},
		}, "recur "+cookie+" + deadline "+onDate.ISO()+": "+item.Title,
			"recur", summary, flags["--json"], report)
	}
	return s.patchValueReporting(args, item, store.FieldRecurrence, value, label,
		"recur", summary, flags["--json"], report)
}

// takeRecurCount reads --count for the two preview modes. It returns "absent"
// distinctly from a value, so the WRITE path can reject the flag rather than
// silently ignoring a projection the user asked for and will not get.
func takeRecurCount(args []string) (int, []string, bool, int) {
	raw, rest, present, status := takeFlagValue(args, "--count")
	if status != 0 || !present {
		return 0, rest, false, status
	}
	value, err := strconv.Atoi(raw)
	if err != nil || !isDigits(raw) || value <= 0 {
		return 0, rest, false, abort("--count must be a positive whole number")
	}
	if value > 50 {
		return 0, rest, false, abort("--count is capped at 50")
	}
	return value, rest, true, 0
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func defaultCount(count int, present bool) int {
	if present {
		return count
	}
	return 5
}

// recurPreview is `tasks recur <ref>` — read-only: what this task's schedule
// means and when it fires next. The stamp IS the next occurrence by definition,
// so the list starts with it and projects forward from there.
func recurPreview(queries *taskquery.Queries, item store.Item, count int, asJSON bool,
	today temporal.Date) int {

	cookie := strings.TrimSpace(item.Recur)
	anchorText := item.Deadline
	if anchorText == "" {
		anchorText = item.Scheduled
	}
	anchor, hasAnchor := temporal.ParseDate(anchorText)
	dates := []string{}
	failure := ""
	if cookie != "" {
		if !hasAnchor {
			failure = "no scheduled date or deadline to project from"
		} else {
			dates = append(dates, anchor.ISO())
			if count > 1 {
				projected, err := recur.Occurrences(cookie, civil(anchor), civil(today), count-1)
				if err != nil {
					failure = err.Error()
				} else {
					for _, date := range projected {
						dates = append(dates, date.String())
					}
				}
			}
		}
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("id", item.ID)
		w.KeyInt("line", item.Line)
		w.KeyStr("title", item.Title)
		w.KeyStrOrNull("recur", cookie)
		human := ""
		if cookie != "" {
			human = recurSummary(cookie)
		}
		w.KeyStrOrNull("recur_human", human)
		w.Key("anchor")
		if hasAnchor {
			w.Str(anchor.ISO())
		} else {
			w.Null()
		}
		w.Key("next")
		w.Strings(dates)
		if failure != "" {
			w.KeyStr("error", failure)
		}
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	out(taskquery.Headline(item))
	if cookie == "" {
		out("  no recurrence — set one with `tasks recur " + rubyInspectQuote(item.Title) + " <schedule>`")
		return 0
	}
	out("  ↻ " + recurSummary(cookie) + " " + dim("("+cookie+")"))
	if failure != "" {
		fmt.Fprintln(os.Stderr, failure)
	}
	for _, iso := range dates {
		printProjectedDate(iso)
	}
	return 0
}

// recurExplain is `tasks recur --explain "<schedule>"` — a taskless
// parse/preview. Both failing shapes exit 1 so a script can branch on the
// status alone rather than parsing the prose.
func recurExplain(input string, count int, asJSON bool, today temporal.Date) int {
	payload := recur.Explain(input, civil(today), count, "")

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("input", input)
		if payload.Error != "" && !payload.HasCanonical {
			w.KeyStr("error", payload.Error)
			w.EndObject()
			out(w.String())
			return 1
		}
		w.KeyStrOrNull("canonical", payload.Canonical)
		w.KeyStr("human", payload.Human)
		w.Key("next")
		dates := []string{}
		for _, date := range payload.Next {
			dates = append(dates, date.String())
		}
		w.Strings(dates)
		if payload.Error != "" {
			w.KeyStr("error", payload.Error)
		}
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		if payload.Error != "" {
			return 1
		}
		return 0
	}

	if payload.Canonical == "" {
		// Either an unreadable input, or the explicit "off" words, which parse
		// cleanly and mean "clear the schedule".
		if payload.Error != "" {
			return abort(payload.Error + "\n" + recurHint)
		}
		out("off — clears any schedule on the task")
		return 0
	}
	out(payload.Canonical + " — " + payload.Human)
	for _, date := range payload.Next {
		printProjectedDate(date.String())
	}
	if payload.Error != "" {
		fmt.Fprintln(os.Stderr, payload.Error)
		return 1
	}
	return 0
}

// printProjectedDate is one row of a projection: the ISO date and the weekday
// that makes it readable. A year the calendar cannot narrow to a storable date
// still prints, because the projection is information even when the store could
// not hold it.
func printProjectedDate(iso string) {
	if date, ok := temporal.ParseDate(iso); ok {
		out("  " + iso + " " + dim(weekdayAbbrev(date)))
		return
	}
	out("  " + iso)
}

func civil(date temporal.Date) recur.CivilDate {
	return recur.NewCivilDate(int64(date.Year), int(date.Month), date.Day)
}

func init() {
	register("recur", (*surfaceContext).recurCommand)
}
