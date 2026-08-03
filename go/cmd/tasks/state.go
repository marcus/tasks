package main

import "strings"

// state is any lifecycle transition, named by argument.
//
// It resolves CLOSED tasks unconditionally — `state <ref> TODO` reopening a
// finished task is the point of the verb, and a scope that refused to find the
// task would make reopening impossible.
func (s *surfaceContext) state(args []string) int {
	return s.changeState(args, stateOptions{
		usage: "tasks state <ref> <" + strings.Join(allStates, "|") + ">",
		// recurAware for the same reason `done` is: DONE on a repeating task
		// rolls it forward here too, so the preview and the report must say so
		// rather than claiming it closed.
		action: "state", resolveClosed: true, recurAware: true,
	})
}

// cancel withdraws a task, optionally recording why.
//
// `--note` is extracted BEFORE the flag scan, because it is a repeatable valued
// flag and takeFlags only knows booleans — an unextracted `--note` would abort
// as an unknown flag and its value would become part of the ref.
func (s *surfaceContext) cancel(args []string) int {
	notes, rest, status := takeRepeatableValue(args, "--note")
	if status != 0 {
		return status
	}
	return s.changeState(rest, stateOptions{
		target: "CANCELLED", usage: `tasks cancel <ref> [--note "reason"]`,
		action: "cancel", notes: notes,
	})
}

func init() {
	register("state", (*surfaceContext).state)
	register("cancel", (*surfaceContext).cancel)
}
