package main

import (
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

const tagUsage = "usage: tasks tag <ref> +tag -tag @ctx -@ctx"

// tag edits a task's tags and contexts in one write.
//
// The whole ordered tag sequence is the write's baseline, not the slice the
// delta touches: an add and a remove are expressed against the list as it
// stood, so a concurrent context edit must refuse rather than be silently
// overwritten by a rewrite of the same list.
func (s *surfaceContext) tag(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "tag"); refusal != 0 {
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
	specs := []string{}
	if len(rest) > 1 {
		specs = rest[1:]
	}
	if strings.TrimSpace(ref) == "" || len(specs) == 0 {
		return abort(tagUsage)
	}

	add := []string{}
	remove := []string{}
	for _, spec := range specs {
		switch {
		case strings.HasPrefix(spec, "-") && len(spec) > 1:
			remove = append(remove, spec[1:])
		case strings.HasPrefix(spec, "+") && len(spec) > 1:
			add = append(add, spec[1:])
		case strings.HasPrefix(spec, "@"):
			add = append(add, spec)
		default:
			return abort("tag spec must start with +, -, or @: " + spec)
		}
	}

	queries, status := s.readQueries(args, "tag")
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
		changes := []string{}
		for _, value := range add {
			changes = append(changes, "+"+value)
		}
		for _, value := range remove {
			changes = append(changes, "-"+value)
		}
		out("would apply " + strings.Join(changes, " ") + " to: " + taskquery.Headline(item))
		return 0
	}
	return s.patchValueAndReport(args, item, store.FieldTagDelta,
		store.TagDeltaValue(add, remove), "tags: "+item.Title,
		"tag", "failed to update tags", flags["--json"])
}

func init() {
	register("tag", (*surfaceContext).tag)
}
