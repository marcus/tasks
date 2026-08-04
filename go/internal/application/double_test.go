package application

import (
	"encoding/json"
	"os"
	"sync"

	"tasks-go/internal/record"
	"tasks-go/internal/store"
)

// capableStore is the test double for the capabilities the Go store has not
// grown yet: undelegate, release, work_ref, a coalescing patch, and the body
// field.
//
// It is NOT a second implementation of the store's rules. It embeds the real
// store, so every ported capability — the transaction, validation, the claim
// compare-and-set, the journal — is the real thing, and it adds only enough
// file editing to make the composed half of an application operation
// observable: the marker really changes, so the canonical task the application
// reports really reflects it.
//
// What these tests then assert is the APPLICATION's job — which store calls
// happened, in what order, with which coalescing key, and how the outcomes were
// folded into one result — and never a rule the store owns.
type capableStore struct {
	*store.Store

	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	verb        string
	id          string
	coalesceKey string
	detail      string
}

func (c *capableStore) record(call recordedCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *capableStore) log() []recordedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedCall{}, c.calls...)
}

// capableFactory wraps every freshly built store in ONE shared double, so the
// call log spans an application operation's several stores the way a journal
// would.
func capableFactory() (func(*store.Store) Store, *capableStore) {
	double := &capableStore{}
	return func(built *store.Store) Store {
		double.mu.Lock()
		defer double.mu.Unlock()
		double.Store = built
		return double
	}, double
}

// -- capabilities -------------------------------------------------------------

func (c *capableStore) PatchesField(field store.PatchField) bool {
	return field == store.FieldPriority || field == store.FieldState || field == FieldBody ||
		field == store.FieldDeferred || field == store.FieldContexts || field == store.FieldTags
}

// Patch is the typed capability. It records the call and then delegates to the
// real store's own typed entry point, so a test asserting on the log is
// asserting about a write that actually happened.
func (c *capableStore) Patch(request store.PatchRequest) store.MutationResult {
	c.record(recordedCall{verb: "typed_patch:" + string(request.Field), id: request.ID,
		coalesceKey: request.CoalesceKey})
	return c.Store.Patch(request)
}

func (c *capableStore) PatchTaskCoalesced(id string, field store.PatchField, value, expected, label, today, coalesceKey string) store.MutationResult {
	c.record(recordedCall{verb: "patch:" + string(field), id: id, coalesceKey: coalesceKey, detail: value})
	if field == FieldBody {
		return c.edit(id, func(target *record.Record) bool {
			target.SetString("body", value)
			return true
		})
	}
	return c.PatchTask(id, field, value, expected, label, today)
}

func (c *capableStore) Undelegate(id, coalesceKey string) store.MutationResult {
	c.record(recordedCall{verb: "undelegate", id: id, coalesceKey: coalesceKey})
	marker := c.marker(id)
	if marker == nil {
		return c.snapshotResult(store.MutationNoChange)
	}
	return c.edit(id, func(target *record.Record) bool {
		target.Delete(store.DelegationField)
		return true
	})
}

func (c *capableStore) Release(id, worker string, force bool, coalesceKey string) store.MutationResult {
	c.record(recordedCall{verb: "release", id: id, coalesceKey: coalesceKey, detail: worker})
	marker := c.marker(id)
	if marker == nil || marker["status"] != store.DelegationClaimed {
		return store.MutationResult{Status: store.MutationInvalid,
			Errors: []string{"task is not claimed"}}
	}
	if !force && marker["assignee"] != worker {
		return store.MutationResult{
			Status:  store.MutationConflict,
			Summary: store.MutationSummary{Holder: marker["assignee"], At: marker["at"]},
		}
	}
	return c.edit(id, func(target *record.Record) bool {
		next := map[string]string{"kind": marker["kind"], "mode": marker["mode"],
			"status": store.DelegationReady, "at": marker["at"]}
		if ref := marker["work_ref"]; ref != "" {
			next["work_ref"] = ref
		}
		writeMarker(target, next)
		return true
	})
}

func (c *capableStore) SetWorkRef(id, workRef, worker, coalesceKey string) store.MutationResult {
	c.record(recordedCall{verb: "work_ref", id: id, coalesceKey: coalesceKey, detail: workRef})
	marker := c.marker(id)
	if marker == nil {
		return store.MutationResult{Status: store.MutationInvalid,
			Errors: []string{"task is not delegated"}}
	}
	if worker != "" && marker["assignee"] != worker {
		return store.MutationResult{
			Status:  store.MutationConflict,
			Summary: store.MutationSummary{Holder: marker["assignee"], At: marker["at"]},
		}
	}
	return c.edit(id, func(target *record.Record) bool {
		next := map[string]string{}
		for key, value := range marker {
			next[key] = value
		}
		if workRef == "" {
			delete(next, "work_ref")
		} else {
			next["work_ref"] = workRef
		}
		writeMarker(target, next)
		return true
	})
}

// -- file editing -------------------------------------------------------------

// markerOrder is the stored key order the schema expects. Writing the members
// in a fixed order keeps the double's output stable enough to compare.
var markerOrder = []string{"kind", "mode", "status", "assignee", "at", "work_ref"}

func writeMarker(target *record.Record, marker map[string]string) {
	ordered := map[string]string{}
	keys := []string{}
	for _, key := range markerOrder {
		if value, ok := marker[key]; ok && value != "" {
			ordered[key] = value
			keys = append(keys, key)
		}
	}
	buffer := []byte("{")
	for index, key := range keys {
		if index > 0 {
			buffer = append(buffer, ',')
		}
		name, _ := json.Marshal(key)
		value, _ := json.Marshal(ordered[key])
		buffer = append(buffer, name...)
		buffer = append(buffer, ':')
		buffer = append(buffer, value...)
	}
	buffer = append(buffer, '}')
	target.Set(store.DelegationField, buffer)
}

func (c *capableStore) marker(id string) map[string]string {
	snapshot, err := c.ReadSnapshot(false)
	if err != nil {
		return nil
	}
	for _, item := range snapshot.Items {
		if item.HasID && item.ID == id {
			return decodeMarker(item.Delegation)
		}
	}
	return nil
}

// edit rewrites one record in place and returns the result shape the real store
// returns: the post-write snapshot and the global revision from those bytes.
func (c *capableStore) edit(id string, apply func(*record.Record) bool) store.MutationResult {
	raw, err := os.ReadFile(c.Org())
	if err != nil {
		return store.MutationResult{Status: store.MutationUnavailable}
	}
	parsed := record.Parse(raw)
	working := record.CloneAll(parsed.Records)
	index := -1
	for position, candidate := range working {
		if candidate.String("id") == id {
			index = position
			break
		}
	}
	if index < 0 {
		return store.MutationResult{Status: store.MutationNotFound}
	}
	if !apply(&working[index]) {
		return c.snapshotResult(store.MutationNoChange)
	}
	dumped, err := record.Dump(working)
	if err != nil {
		return store.MutationResult{Status: store.MutationInvalid, Errors: []string{err.Error()}}
	}
	if err := os.WriteFile(c.Org(), []byte(dumped), 0o644); err != nil {
		return store.MutationResult{Status: store.MutationUnavailable}
	}
	result := c.snapshotResult(store.MutationOK)
	result.TouchedIDs = []string{id}
	return result
}

func (c *capableStore) snapshotResult(status store.MutationStatus) store.MutationResult {
	snapshot, err := c.ReadSnapshot(false)
	if err != nil {
		return store.MutationResult{Status: status}
	}
	live, _ := os.ReadFile(c.Org())
	archive, _ := os.ReadFile(c.Archive())
	return store.MutationResult{
		Status: status, ReadSnapshot: snapshot,
		StoreRevision: store.StoreRevisionForContents(live, archive),
	}
}

// -- project, delete and proposal doubles -------------------------------------

// scriptedStore answers the capabilities whose store half is entirely absent,
// so the application's own composition around them can still be proved: the
// duplicate-title refusal, the schema gate, the rollback folding, and the
// count-versus-rollback ambiguity a bare integer return creates.
type scriptedStore struct {
	*store.Store

	projects        map[string]string // id -> title, for rename/complete/archive
	createResult    store.MutationResult
	renameFound     bool
	completeClosed  int
	completeFound   bool
	archiveMoved    []string
	archiveProposed bool
	archiveFound    bool
	rollbackReason  string
	rollbackStage   store.RollbackStage
	deleteResult    store.MutationResult
	proposalResult  store.MutationResult

	calls []recordedCall
}

func (s *scriptedStore) CreateProject(title string) store.MutationResult {
	s.calls = append(s.calls, recordedCall{verb: "create_project", detail: title})
	return s.createResult
}

func (s *scriptedStore) RenameSection(id, title string) (string, bool) {
	s.calls = append(s.calls, recordedCall{verb: "rename_section", id: id, detail: title})
	return id, s.renameFound
}

func (s *scriptedStore) CompleteProject(id, today string) (int, bool) {
	s.calls = append(s.calls, recordedCall{verb: "complete_project", id: id, detail: today})
	return s.completeClosed, s.completeFound
}

func (s *scriptedStore) ArchiveProject(id, today string) ([]string, bool, bool) {
	s.calls = append(s.calls, recordedCall{verb: "archive_project", id: id})
	return s.archiveMoved, s.archiveProposed, s.archiveFound
}

func (s *scriptedStore) LastRollback() (string, store.RollbackStage) {
	return s.rollbackReason, s.rollbackStage
}

func (s *scriptedStore) DeleteTask(id string, cascade bool, expectedRevision, historyLabel string) store.MutationResult {
	detail := "cascade=false"
	if cascade {
		detail = "cascade=true"
	}
	s.calls = append(s.calls, recordedCall{verb: "delete", id: id, detail: detail})
	return s.deleteResult
}

func (s *scriptedStore) DecideProposal(id, action string, notes []string, expectedRevision, today string) store.MutationResult {
	s.calls = append(s.calls, recordedCall{verb: "decide:" + action, id: id, detail: today})
	return s.proposalResult
}

// -- the store that grew nothing ----------------------------------------------

// bareStore exposes ONLY the base Store interface, hiding whatever optional
// capabilities the real store has grown.
//
// It exists because the fallback paths must stay covered as the store catches
// up. The optional-capability design means a Store may legitimately lack
// PatchTaskCoalesced or Undelegate — that is the whole reason they are separate
// interfaces — so the branches that report degraded granularity or refuse
// outright are contract, not scaffolding, and must not lose their tests the
// moment *store.Store happens to implement the capability.
//
// It delegates by explicit method rather than by embedding, because an embedded
// *store.Store would re-expose every optional capability through the type
// assertion and defeat the point.
type bareStore struct{ inner Store }

func bareFactory() func(*store.Store) Store {
	return func(built *store.Store) Store { return bareStore{inner: built} }
}

func (b bareStore) Org() string     { return b.inner.Org() }
func (b bareStore) Archive() string { return b.inner.Archive() }

func (b bareStore) ReadSnapshot(includeArchive bool) (*store.Snapshot, error) {
	return b.inner.ReadSnapshot(includeArchive)
}

func (b bareStore) CheckedReadSnapshot() (store.CheckedRead, error) {
	return b.inner.CheckedReadSnapshot()
}

func (b bareStore) CreateTask(command store.CreateCommand, today string) store.MutationResult {
	return b.inner.CreateTask(command, today)
}

func (b bareStore) PatchTask(id string, field store.PatchField, value, expected, label, today string) store.MutationResult {
	return b.inner.PatchTask(id, field, value, expected, label, today)
}

func (b bareStore) ExpectedFor(id string, field store.PatchField) (string, bool) {
	return b.inner.ExpectedFor(id, field)
}

func (b bareStore) Delegate(id, kind, mode, assignee, coalesceKey string) store.MutationResult {
	return b.inner.Delegate(id, kind, mode, assignee, coalesceKey)
}

func (b bareStore) Claim(id, worker, coalesceKey string) store.MutationResult {
	return b.inner.Claim(id, worker, coalesceKey)
}
