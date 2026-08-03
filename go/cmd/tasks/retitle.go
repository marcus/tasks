package main

import (
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

const retitleUsage = `usage: tasks retitle <ref> "new title"`

// retitle replaces a task's title.
//
// It resolves PROPOSED items as well as open ones, because a proposal's whole
// purpose is to be edited before it is accepted — refusing to rename one would
// leave reject-and-recapture as the only way to fix a typo.
func (s *surfaceContext) retitle(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "retitle"); refusal != 0 {
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
	title := ""
	if len(rest) > 1 {
		title = joinPositional(rest[1:])
	}
	if strings.TrimSpace(ref) == "" || title == "" {
		return abort(retitleUsage)
	}

	queries, status := s.readQueries(args, "retitle")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{
		includeDone: flags["--include-done"], includeProposed: true,
	})
	if code != 0 {
		return code
	}
	if flags["--dry-run"] {
		out("would retitle to " + rubyInspectQuote(title) + ": " + taskquery.Headline(item))
		return 0
	}
	return s.patchAndReport(args, item, store.FieldTitle, title,
		"retitle → "+title+": "+item.Title, "retitle", "failed to retitle", flags["--json"])
}

func init() {
	register("retitle", (*surfaceContext).retitle)
}
