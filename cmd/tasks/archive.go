package main

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
)

// archive sweeps every fully closed subtree into archive.jsonl.
//
// The sweep returns a count; only the pre-sweep preview knows WHICH records
// moved, which is what a --json caller needs. Pinning the sweep to that preview
// is what makes the reported ids true: a store that changed in between refuses
// rather than reporting a stale list. The day stamp on a moved record is part of
// that fingerprint, so a sweep prepared either side of local midnight also
// refuses — and retrying is always the right answer.
//
// `--dry-run` prints that preview instead of sweeping: counts, the ids that
// would move, every blocked root with its open children, and the fingerprint.
// `--fingerprint FP` then sweeps only while the preview still carries FP — the
// same two-step contract as GET /archive-preview and POST /archive-sweeps, for
// a caller that reviews the preview before committing to it.
func (s *surfaceContext) archive(args []string) int {
	fingerprint, args, pinnedByCaller, status := takeFlagValue(args, "--fingerprint")
	if status != 0 {
		return status
	}
	flags, rest, err := takeFlags(args, "--json", "--dry-run")
	if err != nil {
		return abort(err.Error())
	}
	usage := "usage: tasks archive [--dry-run | --fingerprint FP] [--json]"
	if len(rest) > 0 || (pinnedByCaller && (fingerprint == "" || flags["--dry-run"])) {
		return abort(usage)
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
	if flags["--dry-run"] {
		preview := writer.ArchivePreviewFor(today)
		if preview.Unavailable != "" {
			return archiveUnavailable(preview.Unavailable, asJSON)
		}
		return archiveDryRun(preview, asJSON)
	}

	var pinned *store.ArchivePreview
	var result store.ArchiveResult
	if pinnedByCaller {
		app, _, status := s.projectApplication()
		if status != 0 {
			return status
		}
		outcome, _ := app.ArchiveSweepMatching(fingerprint, nil)
		result = outcome.ArchiveResult
		pinned = &result.Preview
	} else {
		if asJSON {
			preview := writer.ArchivePreviewFor(today)
			if preview.Unavailable != "" {
				return archiveUnavailable(preview.Unavailable, asJSON)
			}
			pinned = &preview
		}
		result = writer.ArchiveSweep(today, pinned)
	}

	if result.Refusal != store.ArchiveNotRefused {
		return archiveRefused(result, asJSON, pinnedByCaller)
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

// archiveDryRun prints the preview a sweep would act on. It exits 0 even when
// roots are blocked: a preview that shows the blockage has answered the
// question it was asked, and the sweep itself is what refuses.
func archiveDryRun(preview store.ArchivePreview, asJSON bool) int {
	if asJSON {
		out(archivePreviewDocument(preview))
		return 0
	}
	if preview.Roots == 0 {
		out("Nothing to archive (no DONE/CANCELLED items).")
		return 0
	}
	out(fmt.Sprintf("Would archive %d item%s and %d descendant%s to archive.jsonl.",
		preview.Roots, plural(preview.Roots), preview.Descendants, plural(preview.Descendants)))
	if preview.Blocked() {
		has := "s have"
		if preview.BlockedRoots() == 1 {
			has = " has"
		}
		out(fmt.Sprintf("Blocked: %d closed root%s %d open descendant%s, so `tasks archive` would refuse.",
			preview.BlockedRoots(), has, preview.OpenDescendants(), plural(preview.OpenDescendants())))
		for _, block := range preview.Blocks {
			out("  " + rubyInspectQuote(block.RootTitle) + ": " + strings.Join(block.OpenTitles, ", "))
		}
	}
	return 0
}

// archivePreviewDocument is the preview in the same members GET
// /api/v1/archive-preview answers with: `records` counts `candidate_ids`, and
// `blocked` rows are the ones the open_descendants refusal carries.
func archivePreviewDocument(preview store.ArchivePreview) string {
	candidates := preview.CandidateIDs
	if candidates == nil {
		candidates = []string{}
	}
	w := jsonWriter()
	w.BeginObject()
	w.KeyInt("roots", preview.Roots)
	w.KeyInt("descendants", preview.Descendants)
	w.KeyInt("records", len(candidates))
	w.Key("candidate_ids")
	w.Strings(candidates)
	w.KeyInt("open_descendants", preview.OpenDescendants())
	w.Key("blocked")
	writeArchiveBlocks(w, preview.Blocks)
	w.KeyStr("fingerprint", preview.Fingerprint)
	w.EndObject()
	return w.String()
}

func writeArchiveBlocks(w *jsonout.Writer, blocks []store.ArchiveBlock) {
	w.BeginArray()
	for _, block := range blocks {
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
}

// archiveRefused reports the sweep's safety gates in both dialects. Each refusal
// names the same fix in prose and carries the CLI's `conflict` error code plus a
// stable `reason`, so a caller branches on one shape across every surface.
func archiveRefused(result store.ArchiveResult, asJSON, pinnedByCaller bool) int {
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
		// Only a pinned sweep can see this: --json pins its own preview, and
		// --fingerprint pins the one the caller reviewed.
		message := "Archive refused: tasks.jsonl changed while the sweep was being prepared. Retry."
		if pinnedByCaller {
			message = "Archive refused: the archive preview changed since that fingerprint. " +
				"Review `tasks archive --dry-run` again, then retry with its fingerprint."
		}
		if asJSON {
			var extra func(*jsonout.Writer)
			if pinnedByCaller {
				extra = func(w *jsonout.Writer) { w.KeyStr("fingerprint", preview.Fingerprint) }
			}
			out(archiveErrorDocument("preview_changed", message, extra))
		}
		return abort(message)

	case store.ArchiveUnavailable:
		message := "task store unavailable"
		if len(result.Details) > 0 && result.Details[0] != "" {
			message = result.Details[0]
		}
		return archiveUnavailable(message, asJSON)

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
				writeArchiveBlocks(w, preview.Blocks)
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
func archiveUnavailable(message string, asJSON bool) int {
	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("error", "unavailable")
		w.KeyStr("action", "archive")
		w.KeyStr("message", message)
		w.EndObject()
		out(w.String())
	}
	return abort(message)
}

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
