package api

import (
	"net/http"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
)

// The manager endpoints: the shared undo journal and the list-wide archive
// sweep. Both are the TUI's `u` / ctrl-r and `x`, and both follow the same
// two-step shape a remote client needs — read what WOULD happen, then act only
// while that reading is still true.
//
// The pins differ because the two operations are guarded by different things.
// History is pinned to the global store revision: any write anywhere changes
// which step is next. The sweep is pinned to its preview fingerprint: only a
// change to the set it would move matters, and the fingerprint already covers
// that set, its content, and the day stamp it would carry.
//
// Neither route asks the store to be VALID first. An undo is one of the ways
// back from a bad write, so the history routes gate only on the schema version
// (which the store checks itself); the sweep, which interprets records, does
// require a readable store, exactly as the CLI's `archive` does.

// historyStepFields is the one member an undo or redo body carries.
var historyStepFields = []string{"store_revision"}

// archiveSweepFields is the one member a sweep body carries.
var archiveSweepFields = []string{"fingerprint"}

func (s *Server) getHistory(request *http.Request) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	peek, supported := s.options.App.HistoryPeek()
	if !supported {
		return response{}, notImplemented("read the undo history", "the store has no journal planner")
	}
	if peek.UnsupportedSchema {
		return response{}, unsupportedSchemaError()
	}
	if peek.Unavailable != "" {
		return response{}, unavailableError(peek.Unavailable)
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { writeHistoryPeek(w, peek) }, peek.StoreRevision)
	return response{status: 200, body: w.Bytes()}, nil
}

// writeHistoryPeek is `{undo, redo, store_revision}` — the same document
// `tasks history --json` prints.
func writeHistoryPeek(w *jsonout.Writer, peek store.HistoryPeek) {
	w.BeginObject()
	w.Key("undo")
	writeOptionalLabel(w, peek.Undo)
	w.Key("redo")
	writeOptionalLabel(w, peek.Redo)
	w.KeyStr("store_revision", peek.StoreRevision)
	w.EndObject()
}

func writeOptionalLabel(w *jsonout.Writer, label *string) {
	if label == nil {
		w.Null()
		return
	}
	w.Str(*label)
}

// historyStep is POST /history/undo and /history/redo.
func (s *Server) historyStep(request *http.Request, delta int, verb string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	if err := rejectUnknownFields(body, historyStepFields); err != nil {
		return response{}, err
	}
	if !body.has("store_revision") {
		return response{}, validationError(reason("store_revision", "is required"))
	}
	expected, isText := body.text("store_revision")
	if !isText || expected == "" {
		return response{}, validationError(reason("store_revision", "must be a non-empty string"))
	}

	result, supported := s.options.App.GuardedHistoryStep(delta, expected)
	if !supported {
		return response{}, notImplemented(verb, "the store has no journal planner")
	}
	switch result.Outcome {
	case store.HistoryOK:
		w := jsonout.New()
		writeSuccess(w, func(w *jsonout.Writer) {
			w.BeginObject()
			w.KeyStr("label", result.Label)
			w.KeyStr("store_revision", result.StoreRevision)
			w.EndObject()
		}, result.StoreRevision)
		return response{status: 200, body: w.Bytes()}, nil
	case store.HistoryStale:
		return response{}, errorWith(409, "conflict",
			"The store changed since that revision; refetch and retry.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: string(store.HistoryStale)},
				detailPair{Key: "store_revision", Value: result.StoreRevision},
			))
	case store.HistoryEmpty:
		return response{}, errorWith(409, "conflict", "Nothing to "+verb+".").
			withDetails(pairDetails(detailPair{Key: "reason", Value: "empty"}))
	case store.HistoryConflict:
		return response{}, errorWith(409, "conflict",
			"The task files changed outside the journal since that edit; refusing to "+verb+" it.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: "journal_conflict"},
				detailPair{Key: "label", Value: result.Label},
			))
	case store.HistoryUnsupportedSchema:
		return response{}, unsupportedSchemaError()
	}
	// HistoryUnavailable carries the lock diagnostic in the label slot.
	return response{}, unavailableError(result.Label)
}

func (s *Server) archivePreview(request *http.Request, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	preview, supported := s.options.App.ArchivePreview(operation)
	if !supported {
		return response{}, notImplemented("preview an archive sweep", "the store has no archive sweep")
	}
	if preview.Unavailable != "" {
		return response{}, unavailableError(preview.Unavailable)
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { writeArchivePreview(w, preview) }, preview.StoreRevision)
	return response{status: 200, body: w.Bytes()}, nil
}

// writeArchivePreview is the preview document `tasks archive --dry-run --json`
// prints too. `records` counts `candidate_ids` — every record the sweep would
// move — which is the same number a completed sweep reports as `records`.
func writeArchivePreview(w *jsonout.Writer, preview store.ArchivePreview) {
	candidates := preview.CandidateIDs
	if candidates == nil {
		candidates = []string{}
	}
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
}

// writeArchiveBlocks is the blocked-root list: the same members the CLI's
// `open_descendants` refusal carries, so the TUI's "Archive blocked" modal can
// be drawn from either surface.
func writeArchiveBlocks(w *jsonout.Writer, blocks []store.ArchiveBlock) {
	w.BeginArray()
	for _, block := range blocks {
		w.BeginObject()
		w.KeyStr("root_id", block.RootID)
		w.KeyStr("root_title", block.RootTitle)
		w.Key("open_ids")
		w.Strings(nonNil(block.OpenIDs))
		w.Key("open_titles")
		w.Strings(nonNil(block.OpenTitles))
		w.EndObject()
	}
	w.EndArray()
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (s *Server) archiveSweep(request *http.Request, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	if err := rejectUnknownFields(body, archiveSweepFields); err != nil {
		return response{}, err
	}
	if !body.has("fingerprint") {
		return response{}, validationError(reason("fingerprint", "is required"))
	}
	fingerprint, isText := body.text("fingerprint")
	if !isText || fingerprint == "" {
		return response{}, validationError(reason("fingerprint", "must be a non-empty string"))
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	outcome, supported := s.options.App.ArchiveSweepMatching(fingerprint, operation)
	if !supported {
		return response{}, notImplemented("sweep the archive", "the store has no archive sweep")
	}
	if err := archiveSweepFailure(outcome.ArchiveResult, outcome.RollbackStage); err != nil {
		return response{}, err
	}

	moved := []string{}
	if outcome.Roots > 0 {
		moved = nonNil(outcome.Preview.CandidateIDs)
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		// The CLI's `archive --json` document, member for member.
		w.BeginObject()
		w.KeyInt("roots", outcome.Roots)
		w.KeyInt("records", len(moved))
		w.Key("moved_ids")
		w.Strings(moved)
		w.EndObject()
	}, outcome.StoreRevision)
	return response{status: 200, body: w.Bytes()}, nil
}

// archiveSweepFailure maps the sweep's refusal vocabulary onto HTTP. Every
// safety gate is a 409 `conflict` whose `details.reason` is the CLI's `reason`
// spelled the same way, so a client branches on one word across both surfaces.
func archiveSweepFailure(result store.ArchiveResult, stage store.RollbackStage) error {
	preview := result.Preview
	switch result.Refusal {
	case store.ArchiveNotRefused:
	case store.ArchiveUnsupportedSchema:
		return unsupportedSchemaError()
	case store.ArchivePreviewChanged:
		return errorWith(409, "conflict", "The archive preview changed; refetch it and retry.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: string(store.ArchivePreviewChanged)},
				detailPair{Key: "fingerprint", Value: preview.Fingerprint},
			))
	case store.ArchiveOpenDescendants:
		return errorWith(409, "conflict",
			"Closed roots still contain open work; complete, cancel, move, or unnest it, then retry.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: string(store.ArchiveOpenDescendants)},
				detailPair{Key: "open_descendants", Value: preview.OpenDescendants()},
				detailPair{Key: "blocked", Value: func(w *jsonout.Writer) { writeArchiveBlocks(w, preview.Blocks) }},
			))
	case store.ArchiveConflict:
		return errorWith(409, "conflict",
			"The archive already holds partial or conflicting copies of the candidate ids; live tasks were preserved.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: string(store.ArchiveConflict)},
				detailPair{Key: "conflicting_ids", Value: func(w *jsonout.Writer) { w.Strings(nonNil(result.Details)) }},
			))
	case store.ArchiveUnavailable:
		text := ""
		if len(result.Details) > 0 {
			text = result.Details[0]
		}
		return unavailableError(text)
	default:
		return errorOf(503, "unavailable")
	}
	if result.Failed {
		// The sweep wrote and was rolled back; live tasks were preserved. A
		// validation rollback is the store refusing its own result, which is
		// the store_invalid answer every other write gives it.
		if stage == store.RollbackValidation {
			return errorOf(503, "store_invalid").
				withDetails(pairDetails(detailPair{Key: "reason", Value: "write_failed"}))
		}
		return errorOf(503, "unavailable").
			withDetails(pairDetails(detailPair{Key: "reason", Value: "write_failed"}))
	}
	return nil
}

func unsupportedSchemaError() error {
	return errorOf(503, "unsupported_schema_version").
		withDetails(pairDetails(detailPair{Key: "supported_version", Value: schemaVersion}))
}
