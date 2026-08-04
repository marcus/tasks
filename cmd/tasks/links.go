package main

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// linkEntry is one task and the links it points at. The pairing survives all
// the way to the output because the answer to "where is this URL from" is the
// task, not the URL.
type linkEntry struct {
	item  store.Item
	links []links.Link
}

// linksCommand lists the links found in task titles and bodies, classified by
// system (slack, jira, github, …). With a ref it lists one task's; otherwise
// every open task's. `--system` filters, `--all` widens to done and archived.
func (s *surfaceContext) linksCommand(args []string) int {
	asJSON, all := false, false
	system := ""
	rest := []string{}
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "--json":
			asJSON = true
		case "--all", "-a":
			all = true
		case "--system", "-s":
			value, consumed, status := takeSystemValue(args, index)
			if status != 0 {
				return status
			}
			system, index = value, consumed
		default:
			if strings.HasPrefix(argument, "-") {
				return abort("unknown flag: " + argument + " (links accepts: --json, --all, --system)")
			}
			rest = append(rest, argument)
		}
	}

	queries, status := s.readQueries(args, "links")
	if status != 0 {
		return status
	}

	entries := []linkEntry{}
	if len(rest) > 0 {
		item, refStatus := resolveRef(queries, rest[0], refScope{includeDone: true})
		if refStatus != 0 {
			return refStatus
		}
		entries = append(entries, linkEntry{item: item, links: queries.Links(item)})
	} else {
		candidates := append([]store.Item{}, queries.LiveItems()...)
		if all {
			candidates = append(candidates, queries.ArchiveItems()...)
		}
		for _, item := range candidates {
			if !all && !contains(taskquery.OpenStates(), item.State) {
				continue
			}
			entries = append(entries, linkEntry{item: item, links: queries.Links(item)})
		}
	}

	kept := []linkEntry{}
	for _, entry := range entries {
		selected := entry.links
		if system != "" {
			selected = []links.Link{}
			for _, link := range entry.links {
				if link.System == system {
					selected = append(selected, link)
				}
			}
		}
		if len(selected) == 0 {
			continue
		}
		kept = append(kept, linkEntry{item: entry.item, links: selected})
	}

	if asJSON {
		w := jsonout.New()
		w.BeginObject()
		w.Key("links")
		w.BeginArray()
		for _, entry := range kept {
			for _, link := range entry.links {
				w.BeginObject()
				writeLinkMembers(w, link)
				w.KeyStr("task", entry.item.Title)
				w.KeyStrOrNull("id", entry.item.ID)
				w.KeyInt("line", entry.item.Line)
				w.KeyStr("source", string(entry.item.Source))
				w.EndObject()
			}
		}
		w.EndArray()
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}

	if len(kept) == 0 {
		out("No links found.")
		return 0
	}
	width := 0
	for _, entry := range kept {
		for _, link := range entry.links {
			if length := len([]rune(link.System)); length > width {
				width = length
			}
		}
	}
	for _, entry := range kept {
		out(format(entry.item))
		for _, link := range entry.links {
			out(linkLine(link, width))
		}
	}
	return 0
}

// takeSystemValue is the shared `--system` reader for `links` and `open`. The
// two MUST agree on how a system name is read — downcased, because the built-in
// names and the host fallback are lowercase — or "open what links showed me"
// breaks between them.
func takeSystemValue(args []string, index int) (string, int, int) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", index, abort("--system needs a value")
	}
	return strings.ToLower(args[index+1]), index + 1, 0
}

// linkLine is one rendered link row, shared by `links` and `show` so the two
// surfaces agree.
func linkLine(link links.Link, width int) string {
	label := ""
	if link.Label != nil {
		label = dim(fmt.Sprintf("  (%s)", *link.Label))
	}
	system := link.System
	if pad := width - len([]rune(system)); pad > 0 {
		system += strings.Repeat(" ", pad)
	}
	return fmt.Sprintf("  %s %s%s", cyan(system), link.URL, label)
}

// writeLinkMembers is Links::Link#to_h: the three members in struct order. An
// absent label is null, not "" — a bare URL and one labelled with an empty
// string are different things in the file.
func writeLinkMembers(w *jsonout.Writer, link links.Link) {
	w.KeyStr("url", link.URL)
	w.Key("label")
	if link.Label == nil {
		w.Null()
	} else {
		w.Str(*link.Label)
	}
	w.KeyStr("system", link.System)
}

func init() {
	register("links", (*surfaceContext).linksCommand)
}
