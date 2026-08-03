package main

import (
	"fmt"
	"strings"

	"tasks-go/internal/jsonout"
	"tasks-go/internal/store"
)

// archive sweeps every fully closed subtree into archive.jsonl.
//
// The sweep returns a count; only the pre-sweep preview knows WHICH records
// moved, which is what a --json caller needs. Pinning the sweep to that preview
// is what makes the reported ids true: a store that changed in between refuses
// rather than reporting a stale list. The day stamp on a moved record is part of
// that fingerprint, so a sweep prepared either side of local midnight also
// refuses — and retrying is always the right answer.
func (s *surfaceContext) archive(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort("usage: tasks archive [--json]")
	}
	asJSON := flags["--json"]
	if message := s.store.UnsupportedSchemaError(); message != "" {
		return s.historyFailed(asJSON, "unsupported_schema_version", "archive", "",
			unsupportedSchemaMessage(message))
	}
	today, status := s.today()
	if status != 0 {
		return status
	}

	writer := s.writeStore()
	var pinned *store.ArchivePreview
	if asJSON {
		preview := writer.ArchivePreviewFor(today)
		pinned = &preview
	}
	result := writer.ArchiveSweep(today, pinned)

	if result.Refusal != store.ArchiveNotRefused {
		return archiveRefused(result, asJSON)
	}
	if result.Failed {
		message := "archive failed; live tasks were preserved"
		if asJSON {
			out(archiveErrorDocument("write_failed", message, nil))
		}
		return abort(message)
	}

	if asJSON {
		moved := []string{}
		if result.Roots > 0 && pinned != nil {
			moved = pinned.CandidateIDs
		}
		w := jsonWriter()
		w.BeginObject()
		// `roots` is what the human line counts; `records` is the whole swept
		// subtree, which is what `moved_ids` lists. Two names because they are
		// two numbers — the sibling `project archive --json` calls its record
		// count `archived`, which is why this one deliberately does not reuse
		// that word.
		w.KeyInt("roots", result.Roots)
		w.KeyInt("records", len(moved))
		w.Key("moved_ids")
		w.Strings(moved)
		w.EndObject()
		out(w.String())
		return 0
	}
	if result.Roots == 0 {
		out("Nothing to archive (no DONE/CANCELLED items).")
		return 0
	}
	out(fmt.Sprintf("Archived %d item%s to archive.jsonl.", result.Roots, plural(result.Roots)))
	return 0
}

// archiveRefused reports the sweep's safety gates in both dialects. Each refusal
// names the same fix in prose and carries the CLI's `conflict` error code plus a
// stable `reason`, so a caller branches on one shape across every surface.
func archiveRefused(result store.ArchiveResult, asJSON bool) int {
	preview := result.Preview
	switch result.Refusal {
	case store.ArchiveConflict:
		message := "Archive refused: archive.jsonl has partial or conflicting copies of candidate IDs " +
			strings.Join(result.Details, ", ") + ".\n" +
			"Live tasks were preserved. Compare `tasks list --done --json` with " +
			"`tasks list --archived --json`, reconcile the conflicting records, then retry."
		if asJSON {
			out(archiveErrorDocument("archive_conflict", message, func(w *jsonout.Writer) {
				w.Key("conflicting_ids")
				w.Strings(result.Details)
			}))
		}
		return abort(message)

	case store.ArchivePreviewChanged:
		// Only the --json path pins a preview, so only it can see this.
		message := "Archive refused: tasks.jsonl changed while the sweep was being prepared. Retry."
		if asJSON {
			out(archiveErrorDocument("preview_changed", message, nil))
		}
		return abort(message)

	case store.ArchiveOpenDescendants:
		blocked := []string{}
		for _, block := range preview.Blocks {
			blocked = append(blocked, rubyInspectQuote(block.RootTitle)+": "+
				strings.Join(block.OpenTitles, ", "))
		}
		has := "s have"
		if preview.BlockedRoots() == 1 {
			has = " has"
		}
		message := fmt.Sprintf(
			"Archive refused: %d closed root%s %d open descendant%s.\nBlocked subtree%s: %s\n"+
				"Complete, cancel, move, or unnest the open work, then retry `tasks archive`.",
			preview.BlockedRoots(), has, preview.OpenDescendants(),
			plural(preview.OpenDescendants()), plural(len(blocked)), strings.Join(blocked, "; "))
		if asJSON {
			out(archiveErrorDocument("open_descendants", message, func(w *jsonout.Writer) {
				w.KeyInt("open_descendants", preview.OpenDescendants())
				w.Key("blocked")
				w.BeginArray()
				for _, block := range preview.Blocks {
					w.BeginObject()
					w.KeyStr("root_id", block.RootID)
					w.KeyStr("root_title", block.RootTitle)
					w.Key("open_ids")
					w.Strings(block.OpenIDs)
					w.Key("open_titles")
					w.Strings(block.OpenTitles)
					w.EndObject()
				}
				w.EndArray()
			}))
		}
		return abort(message)

	default:
		// The schema gate is handled above; anything else is a refusal reason
		// this adapter has not been taught. Say so rather than dereferencing a
		// preview that may not be there.
		message := "Archive refused: " + string(result.Refusal) + "."
		if asJSON {
			out(archiveErrorDocument(string(result.Refusal), message, nil))
		}
		return abort(message)
	}
}

// archiveErrorDocument is the CLI's error envelope with the sweep's `reason`.
// Extra payload is written FIRST, so a payload key can never shadow one of the
// envelope's own discriminators.
func archiveErrorDocument(reason, message string, extra func(*jsonout.Writer)) string {
	w := jsonWriter()
	w.BeginObject()
	w.KeyStr("reason", reason)
	if extra != nil {
		extra(w)
	}
	w.KeyStr("error", "conflict")
	w.KeyStr("action", "archive")
	w.KeyStr("message", message)
	w.EndObject()
	return w.String()
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func init() {
	register("archive", (*surfaceContext).archive)
}
