package main

import (
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

const linkUsage = "usage: tasks link add <ref> <url> [--label TEXT] | tasks link rm <ref> <n|url> | tasks link set <ref> <n> --label TEXT"

func (s *surfaceContext) link(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "link"); refusal != 0 {
		return refusal
	}
	label, rest, labelGiven, status := takeFlagValue(args, "--label")
	if status != 0 {
		return status
	}
	flags, rest, err := takeFlags(rest, "--json", "--dry-run", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 3 || (rest[0] != "add" && rest[0] != "rm" && rest[0] != "set") {
		return abort(linkUsage)
	}
	if rest[0] == "rm" && labelGiven {
		return abort("--label is only valid with link add or link set")
	}
	if rest[0] == "set" && !labelGiven {
		return abort("link set requires --label TEXT")
	}
	queries, readStatus := s.readQueries(args, "link")
	if readStatus != 0 {
		return readStatus
	}
	item, code := resolveRef(queries, rest[1], refScope{includeDone: flags["--include-done"], includeProposed: true})
	if code != 0 {
		return code
	}
	formal := append([]links.FormalLink(nil), item.FormalLinks...)
	if rest[0] == "add" {
		linkURL, defaultLabel, ok := s.expandFormalLink(rest[2])
		if !ok {
			return abort("link URL must be http://, https://, or a configured shorthand")
		}
		for _, existing := range formal {
			if existing.URL == linkURL {
				return abort("formal link already exists: " + linkURL)
			}
		}
		if !labelGiven {
			label = defaultLabel
		} else if !links.ValidFormalLabel(label) {
			return abort("link label must be non-empty trimmed single-line text")
		}
		formal = append(formal, links.FormalLink{URL: linkURL, Label: label})
	} else if rest[0] == "rm" {
		index := -1
		if number, parseErr := strconv.Atoi(rest[2]); parseErr == nil {
			if number < 1 || number > len(formal) {
				return abort("formal link index out of range")
			}
			index = number - 1
		} else {
			for candidate, existing := range formal {
				if existing.URL == rest[2] {
					index = candidate
					break
				}
			}
			if index < 0 {
				return abort("formal link not found: " + rest[2])
			}
		}
		formal = append(formal[:index:index], formal[index+1:]...)
	} else {
		number, parseErr := strconv.Atoi(rest[2])
		if parseErr != nil || number < 1 || number > len(formal) {
			return abort("formal link index out of range")
		}
		if !links.ValidFormalLabel(label) {
			return abort("link label must be non-empty trimmed single-line text")
		}
		formal[number-1].Label = label
	}
	if flags["--dry-run"] {
		out("would update formal links on: " + taskquery.Headline(item))
		return 0
	}
	return s.patchValueAndReport(args, item, store.FieldLinks, store.LinksValue(formal),
		"links: "+item.Title, "link", "failed to update links", flags["--json"])
}

func (s *surfaceContext) expandFormalLink(raw string) (string, string, bool) {
	if links.ValidFormalURL(raw) {
		return raw, "", true
	}
	name, value, found := strings.Cut(raw, ":")
	template, configured := s.paths.Links[name]
	if !found || !configured || value == "" {
		return "", "", false
	}
	expanded := links.Expand(value, template)
	if !links.ValidFormalURL(expanded) {
		return "", "", false
	}
	return expanded, raw, true
}

func init() { register("link", (*surfaceContext).link) }
