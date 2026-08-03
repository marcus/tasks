package main

import (
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// setDate is the shared body of `due` (DEADLINE) and `schedule` (SCHEDULED).
//
// The two are one implementation because the only thing that differs is which
// stamp is written; every rule that matters — how the expression is read, what
// a lead refuses, what the history entry says — is identical, and two copies
// would be two chances to let them drift.
func (s *surfaceContext) setDate(args []string, field store.PatchField, key, usage, action string) int {
	if refusal := s.refuseUnsupportedSchema(args, action); refusal != 0 {
		return refusal
	}
	// The temporal modifiers come out FIRST, so their values can never be
	// mistaken for part of the date expression that follows.
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
		expression = strings.Join(rest[1:], " ")
	}
	if ref == "" || strings.TrimSpace(expression) == "" {
		return abort("usage: " + usage)
	}

	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	value, status := parseTemporalArg(expression, context, options, s.dateOrder())
	if status != 0 {
		return status
	}

	queries, status := s.readQueries(args, action)
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	label := temporalValueLabel(value)
	if flags["--dry-run"] {
		out("would set " + key + " <" + label + "> on: " + taskquery.Headline(item))
		return 0
	}
	return s.patchValueAndReport(args, item, field, store.TemporalValue(value),
		strings.ToLower(key)+" → "+label+": "+item.Title,
		action, "failed to set "+strings.ToLower(key), flags["--json"])
}

func init() {
	register("due", func(s *surfaceContext, args []string) int {
		return s.setDate(args, store.FieldDeadline, "DEADLINE",
			"tasks due <ref> <date-or-date-time>", "due")
	})
	register("schedule", func(s *surfaceContext, args []string) int {
		return s.setDate(args, store.FieldScheduled, "SCHEDULED",
			"tasks schedule <ref> <date-or-date-time>", "schedule")
	})
}
