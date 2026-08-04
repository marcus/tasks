package main

import (
	"strings"
	"unicode/utf8"
)

// workRefClearWords are the two spellings of "there is no reference any more".
// They normalize HERE, once, rather than at each surface: when the CLI kept its
// own list it stored the literal string "none" while the TUI cleared.
var workRefClearWords = []string{"off", "none"}

// workref records where the work lives — a ticket, a PR, a brief, a session.
//
// One reference: setting overwrites, and `off` clears it. The owner may always
// write it; an agent passes --worker to prove its claim still matches. That flag
// is deliberately NOT defaulted from TASKS_WORKER_ID, so an exported worker id
// cannot silently change who the store thinks is writing.
func (s *surfaceContext) workref(args []string) int {
	worker, rest, _ := extractValue(args, "--worker")
	flags, rest, err := takeFlags(rest, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks workref <ref> <url-or-id|off> [--worker <id>]")
	}
	ref := rest[0]
	reference := strings.TrimSpace(strings.Join(rest[1:], " "))
	if reference == "" {
		return abort("usage: tasks workref <ref> <url-or-id|off> [--worker <id>]")
	}
	// Go strings can hold bytes argv supplied that are not valid UTF-8. There is
	// nothing to re-tag, so the refusal is the whole rule.
	if !utf8.ValidString(reference) {
		return abort("work reference is not valid UTF-8 text")
	}
	for _, word := range workRefClearWords {
		if strings.EqualFold(reference, word) {
			reference = ""
			break
		}
	}

	queries, status := s.readQueries(args, "workref")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: true})
	if code != 0 {
		return code
	}

	result := s.writeStore().SetWorkRef(item.ID, reference, strings.TrimSpace(worker),
		s.delegationCoalesceKey("work_ref"))
	if status := s.delegationFailed(result, args, "work_ref"); status != 0 {
		return status
	}
	if flags["--json"] {
		return s.reportTouchedSnapshot(s.delegationSnapshot(result), []string{item.ID}, true, nil)
	}
	title := s.delegationTitle(result, item)
	// The stored reference is read back rather than echoed: a store that
	// normalized what it was given must not be described by what it was sent.
	stored := ""
	if written, ok := s.delegationItem(result, item.ID); ok {
		stored = delegationFields(written.Delegation)["work_ref"]
	}
	if stored == "" {
		out("work ref cleared: " + title)
		return 0
	}
	out("work ref → " + stored + ": " + title)
	return 0
}

func init() {
	register("workref", (*surfaceContext).workref)
}
