package api

import (
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
)

// The two seams this adapter needs that `internal/application` does not publish
// today. Both are named here, in one file, rather than spread through the
// handlers, so the gap is a thing a reader can find and close rather than a
// habit that spreads.
//
//   - Reader: ONE checked read that yields the read model AND the global store
//     revision. application.ReadResult[T] carries the revision and the data but
//     discards the *taskquery.Queries the snapshot was read through, and a task
//     resource cannot be rendered without it (availability, links, body,
//     project). Re-reading to get it would tag a resource with a revision from
//     different bytes, which is precisely what the checked read exists to
//     prevent. The fix belongs in application — a `CheckedReadModel` — and then
//     this seam becomes a one-line adapter over it.
//
//   - Changesets: the multi-field atomic PATCH. `store.ApplyChangeset` exists
//     and implements the whole of it, including the three-part revision
//     precondition an If-Match needs; `application` exposes only the
//     single-field `PatchTask`, so an HTTP PATCH of title+priority would be two
//     undo steps and two conflict windows. The fix belongs in application — an
//     `UpdateTask(id, []Change, expectedRevision)` — and then this seam
//     disappears.
//
// Everything else the API does goes through application.Application.

// CheckedRead is one coherent checked read: the read model over the snapshot,
// the global revision of the exact bytes behind it, and the store's verdict on
// those bytes.
type CheckedRead struct {
	Queries  *taskquery.Queries
	Revision string
	Status   store.Status
	Errors   []store.Entry
}

// OK reports bytes this build may interpret.
func (r CheckedRead) OK() bool { return r.Status == store.StatusOK }

// Reader performs one checked read. Every request that answers from the store
// calls it exactly once, so no response can mix two reads.
type Reader func() (CheckedRead, error)

// Changesets applies one atomic multi-field change under the store's lock.
type Changesets interface {
	ApplyChangeset(changeset store.Changeset) store.MutationResult
}

// NewStoreReader builds the production Reader over a store factory. The factory
// yields a FRESH store per call for the same reason application.StoreFactory
// does: a concurrent request must not be handed a neighbour's store.
func NewStoreReader(factory func() *store.Store, context func() temporal.Context,
	options ...taskquery.Option) Reader {
	return func() (CheckedRead, error) {
		checked, err := factory().CheckedReadSnapshot()
		if err != nil {
			return CheckedRead{Status: store.StatusUnavailable}, err
		}
		read := CheckedRead{
			Revision: checked.StoreRevision, Status: checked.Status, Errors: checked.Errors,
		}
		if checked.Status == store.StatusOK {
			read.Queries = taskquery.New(checked.Snapshot, context(), options...)
		}
		return read, nil
	}
}
