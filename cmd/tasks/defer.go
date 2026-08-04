package main

import (
	"strings"

	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// deferTask sets the available-from date and clears an indefinite hold in one
// write, or — with no date — puts the task on hold indefinitely.
//
// It is a CHANGESET rather than two patches because those two fields are one
// decision. A `defer <ref> <date>` that landed the date and then failed to clear
// the hold would leave a task both scheduled and on hold, which is a state the
// user never asked for and no single undo takes back.
func (s *surfaceContext) deferTask(args []string, someday bool) int {
	action := "defer"
	usage := "usage: tasks defer <ref> [date]"
	if someday {
		action, usage = "someday", "usage: tasks someday <ref>"
	}
	if refusal := s.refuseUnsupportedSchema(args, action); refusal != 0 {
		return refusal
	}
	options, remaining, status := takeTemporalOptions(args)
	if status != 0 {
		return status
	}
	flags, rest, err := takeFlags(remaining, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	ref := ""
	if len(rest) > 0 {
		ref = rest[0]
	}
	expression := ""
	if len(rest) > 1 {
		expression = joinPositional(rest[1:])
	}
	if strings.TrimSpace(ref) == "" || (someday && expression != "") {
		return abort(usage)
	}
	// A modifier says how to read a wall time. With no date to read, it is a
	// silent no-op — so it is refused rather than accepted and ignored.
	if expression == "" && options.modified {
		return abort("temporal modifiers require a date")
	}

	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	var date temporal.Value
	hasDate := expression != ""
	if hasDate {
		date, status = parseTemporalArg(expression, context, options, s.dateOrder())
		if status != 0 {
			return status
		}
	}

	queries, status := s.readQueries(args, action)
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	// The lead already owns "hide until". A one-off defer date would either
	// fight it or be silently ignored, so name the two ways out instead.
	if hasDate && lead.Span(item.Lead) {
		anchor := "available-from date"
		if item.Deadline != "" {
			anchor = "deadline"
		}
		described, _ := lead.Describe(item.Lead)
		return abort("“" + item.Title + "” already hides until " + described + " its " + anchor +
			" — change the window with `tasks lead`, or clear it with `tasks lead <ref> off` first")
	}

	deferred := !hasDate
	changes := []store.Change{{Field: store.FieldDeferred, Value: store.BoolValue(deferred)}}
	ownText := `put "` + item.Title + `" on hold (Someday/Maybe)`
	label := "someday: " + item.Title
	override := taskquery.Override{Deferred: &deferred}
	if hasDate {
		changes = append(changes, store.Change{Field: store.FieldScheduled, Value: store.TemporalValue(date)})
		ownText = `defer "` + item.Title + `" until ` + temporalValueLabel(date)
		label = "defer until " + temporalValueLabel(date) + ": " + item.Title
		override.Scheduled, override.ScheduledSet = &date, true
	}

	if flags["--dry-run"] {
		snapshot, status := s.readSnapshotFor()
		if status != 0 {
			return status
		}
		return s.printAvailabilityChange(snapshot, item.ID, ownText, true, override)
	}
	return s.changesetAndReport(args, item, changes, label, action, "failed to defer",
		flags["--json"], func(result store.MutationResult) int {
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

// activate makes a task available now: its own indefinite marker goes, and a
// FUTURE available-from date goes with it. A past or present date stays, because
// it is history rather than a gate.
func (s *surfaceContext) activate(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "activate"); refusal != 0 {
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
	if strings.TrimSpace(ref) == "" || len(rest) > 1 {
		return abort("usage: tasks activate <ref>")
	}

	queries, status := s.readQueries(args, "activate")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	ownText := `activate "` + item.Title + `"`

	if flags["--dry-run"] {
		snapshot, status := s.readSnapshotFor()
		if status != 0 {
			return status
		}
		context, status := s.temporalContext()
		if status != 0 {
			return status
		}
		// The preview has to model what the write DOES, and the write drops only
		// a future available-from date. A preview that dropped a past one too
		// would report "available now" for a task whose ancestor still gates it.
		override := taskquery.Override{Deferred: boolPointer(false), ScheduledSet: true}
		if scheduled, ok := queries.ScheduledValue(item); ok {
			instant, err := scheduled.ReleaseInstant(context)
			if err != nil || !instant.After(context.Now) {
				override.Scheduled = &scheduled
			}
		}
		return s.printAvailabilityChange(snapshot, item.ID, ownText, true, override)
	}
	return s.changesetAndReport(args, item,
		[]store.Change{{Field: store.FieldActivate, Value: store.BoolValue(true)}},
		"activate: "+item.Title, "activate", "failed to activate", flags["--json"],
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

func boolPointer(value bool) *bool { return &value }

func init() {
	register("defer", func(s *surfaceContext, args []string) int {
		return s.deferTask(args, false)
	})
	register("someday", func(s *surfaceContext, args []string) int {
		return s.deferTask(args, true)
	})
	register("activate", (*surfaceContext).activate)
}
