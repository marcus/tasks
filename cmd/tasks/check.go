package main

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
)

// check validates the store's structure. Exit 0 clean, 1 when there are errors.
//
// Two reports, not one. Plain `check` lints the LIVE file plus the archive's
// schema-version header; `--all-files` lints both files and the cross-file id
// invariant. The narrow default is deliberate — the archive's own records are
// what `--all-files` is for, and folding them in would make the everyday check
// noisy about a file the everyday commands never read. The version gate is the
// one exception, because it is store-wide: a v1 archive under a v2 live file
// makes every read and every mutation refuse the whole store, and a `check`
// that could not see it would answer "ok" to a user who had just been told, by
// that refusal, to run `tasks check`.
func (s *surfaceContext) check(args []string) int {
	flags, rest, err := takeFlags(args, "--json", "--all-files")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort("usage: tasks check [--json] [--all-files]")
	}
	allFiles := flags["--all-files"]

	var result check.Result
	if allFiles {
		result = s.store.CheckFiles()
	} else {
		result = s.store.CheckLive()
	}

	if flags["--json"] {
		out(checkJSON(result))
		return exitFor(result)
	}

	for _, entry := range result.Errors {
		out(fmt.Sprintf("%s  line %d: %s", red("error"), entry.Line, entry.Message))
	}
	for _, entry := range result.Warnings {
		out(fmt.Sprintf("%s   line %d: %s", yellow("warn"), entry.Line, entry.Message))
	}
	if result.OK() {
		// The count comes from a fresh snapshot rather than from the lint: the
		// lint counts DIAGNOSTICS, and what a reader wants to be told is how
		// many records this build could actually interpret.
		count := 0
		if snapshot, err := s.store.ReadSnapshot(allFiles); err == nil {
			count = len(snapshot.Items())
			if allFiles {
				count += len(snapshot.ArchiveItems())
			}
		}
		noun := "task"
		if allFiles {
			noun = "record"
		}
		suffix := ""
		if len(result.Warnings) > 0 {
			suffix = fmt.Sprintf(" (%s)", pluralize(len(result.Warnings), "warning"))
		}
		out(fmt.Sprintf("ok — %s parsed, no structural errors%s", pluralize(count, noun), suffix))
	} else {
		out(fmt.Sprintf("%d error(s), %d warning(s)", len(result.Errors), len(result.Warnings)))
	}
	return exitFor(result)
}

func exitFor(result check.Result) int {
	if result.OK() {
		return 0
	}
	return 1
}

// checkJSON is Check::Result#to_h: the ok flag, then the two diagnostic lists
// as {line, message} objects in report order.
func checkJSON(result check.Result) string {
	w := jsonout.New()
	w.BeginObject()
	w.KeyBool("ok", result.OK())
	for _, group := range []struct {
		key     string
		entries []check.Entry
	}{{"errors", result.Errors}, {"warnings", result.Warnings}} {
		w.Key(group.key)
		w.BeginArray()
		for _, entry := range group.entries {
			w.BeginObject()
			w.KeyInt("line", entry.Line)
			w.KeyStr("message", entry.Message)
			w.EndObject()
		}
		w.EndArray()
	}
	w.EndObject()
	return w.String()
}

// takeFlags is tasks' take_flags: the recognized flags as a set, everything
// else in argv order.
//
// An unrecognized `--flag` is an ERROR, not a positional. That distinction is
// the whole point: a misspelled `--dry-run` accepted as a title word would
// perform the mutation it was meant to preview. A single-dash argument is left
// alone, because that is where a ref like `-A` or a negative index lives, and
// the caller decides whether leftovers are an error.
func takeFlags(args []string, names ...string) (map[string]bool, []string, error) {
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	flags := map[string]bool{}
	rest := []string{}
	for _, arg := range args {
		switch {
		case known[arg]:
			flags[arg] = true
		case strings.HasPrefix(arg, "--"):
			return nil, nil, fmt.Errorf("unknown flag: %s (this command accepts: %s)",
				arg, strings.Join(names, ", "))
		default:
			rest = append(rest, arg)
		}
	}
	return flags, rest, nil
}

var _ = store.New

func init() {
	register("check", (*surfaceContext).check)
}
