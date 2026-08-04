package main

import (
	"fmt"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
)

// repair converges a store `check` refuses.
//
// `check` reports; every mutation refuses or rolls back on a file that fails it;
// and the per-record repairs the codebase documents each fix ONE record while
// being validated against the whole file — so with two or more instances nothing
// converges. This is the one command that fixes every instance it knows about
// and writes once.
//
// Deliberately additive: no existing command's behavior or diagnostics change.
// The two refusal wordings a mutation can earn — the preflight's "task file is
// already invalid … (nothing was written)" and the rollback's "file failed
// validation after the edit" — still say exactly what they said, on exactly the
// stores they said it on. This command is a new door, not a change to the locks.
func (s *surfaceContext) repair(args []string) int {
	flags, rest, err := takeFlags(args, "--json", "--dry-run")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort("usage: tasks repair [--dry-run] [--json]")
	}
	asJSON := flags["--json"]
	// The schema gate fires before the command body, exactly as tasks
	// applies it once for the whole CLI. `repair` is not an exception: a store
	// at a version this build cannot read needs the matching binary, and a
	// repair pass over records it cannot interpret is the last thing it should
	// attempt. The store re-checks under its own lock, so the RepairResult's
	// unsupported_schema status stays reachable from the library surface.
	if status := s.refuseUnsupportedSchema(args, "repair"); status != 0 {
		return status
	}
	result := s.writeStore().Repair(flags["--dry-run"])

	if asJSON {
		if !result.OK() {
			w := jsonWriter()
			w.BeginObject()
			writeRepairPayload(w, result)
			w.KeyStr("error", string(result.Status))
			w.KeyStr("action", "repair")
			w.KeyStr("message", repairRefusalMessage(result))
			w.EndObject()
			out(w.String())
			return abort(repairRefusalMessage(result))
		}
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("action", "repair")
		writeRepairPayload(w, result)
		w.EndObject()
		out(w.String())
		return 0
	}

	// The verb names what happened, not what was planned: a refused pass wrote
	// nothing, so its known defects are ones this command *can* fix, not ones it
	// did. Each line restates the `check` error it answers, so the two reports
	// read side by side.
	verb := "fixed"
	switch {
	case !result.OK():
		verb = "can fix"
	case result.DryRun:
		verb = "would fix"
	}
	for _, fix := range result.Fixes {
		minted := ""
		if result.Written && fix.ID != "" {
			minted = " → minted " + fix.ID
		}
		out(fmt.Sprintf("%s  %s line %d: %s%s", green(verb), fix.File, fix.Line, fix.Message, minted))
	}
	if !result.OK() {
		for _, blocker := range result.Blockers {
			location := ""
			if blocker.File != "" {
				location = fmt.Sprintf("%s line %d: ", blocker.File, blocker.Line)
			}
			out(fmt.Sprintf("%s  %s%s", red("error"), location, blocker.Message))
		}
		return abort(repairRefusalMessage(result))
	}
	out(repairSuccessMessage(result))
	return 0
}

// writeRepairPayload is RepairResult#to_h, in its declared key order. A minted
// id is reported ONLY when it was actually written: a dry run and a refused pass
// both mint one to prove the file would validate, then discard it, and
// publishing that id would invite a caller to record one no record will carry.
func writeRepairPayload(w *jsonout.Writer, result store.RepairResult) {
	w.KeyBool("ok", result.OK())
	w.KeyStr("status", string(result.Status))
	w.KeyBool("dry_run", result.DryRun)
	w.KeyBool("written", result.Written)
	w.Key("fixes")
	w.BeginArray()
	for _, fix := range result.Fixes {
		w.BeginObject()
		w.KeyStr("file", fix.File)
		w.KeyInt("line", fix.Line)
		w.KeyStr("kind", string(fix.Kind))
		w.KeyStr("message", fix.Message)
		if result.Written && fix.ID != "" {
			w.KeyStr("id", fix.ID)
		}
		w.EndObject()
	}
	w.EndArray()
	w.Key("blockers")
	w.BeginArray()
	for _, blocker := range result.Blockers {
		w.BeginObject()
		w.KeyStrOrNull("file", blocker.File)
		w.KeyInt("line", blocker.Line)
		w.KeyStr("message", blocker.Message)
		w.EndObject()
	}
	w.EndArray()
}

// repairRefusalMessage is shared by the human and --json paths so a scripted
// caller and a person are told the same thing. It always states that nothing was
// written: a repair that left the file half-converged would be a worse dead end
// than the one it exists to open.
func repairRefusalMessage(result store.RepairResult) string {
	if result.Status == store.RepairUnsupportedSchema {
		detail := ""
		if len(result.Blockers) > 0 {
			detail = result.Blockers[0].Message
		}
		return "cannot repair: " + detail +
			" — this build cannot read this task file (nothing was written)"
	}
	return fmt.Sprintf("cannot repair: %s this command does not know how to fix "+
		"(nothing was written) — see `tasks check`", pluralize(len(result.Blockers), "error"))
}

func repairSuccessMessage(result store.RepairResult) string {
	if len(result.Fixes) == 0 {
		return "nothing to repair — the store is already valid"
	}
	noun := pluralize(len(result.Fixes), "repair")
	if result.DryRun {
		return noun + " available — nothing was written (--dry-run)"
	}
	return noun + " written — the store validates; `undo` restores the previous bytes"
}

func init() {
	register("repair", (*surfaceContext).repair)
}
