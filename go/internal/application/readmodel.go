package application

import (
	"os"

	"tasks-go/internal/record"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// ReadModel is ONE coherent live read for presentation adapters — the Go
// counterpart of Tasks::TaskReadModel.
//
// It is not a store wrapper. It is built from a single snapshot and exposes no
// store and no persistence operation, so a TUI that holds one across a paint
// cannot accidentally write through it or re-read half of it.
type ReadModel struct {
	snapshot *store.Snapshot
	queries  *taskquery.Queries
	path     string
	stat     statKey
}

// statKey is Store.stat_key: the identity of the bytes a model was built over.
// A zero value means the file was not there, which is a real state and not an
// error — an archive that does not exist yet is not an empty one.
type statKey struct {
	present bool
	modTime int64
	size    int64
}

func statKeyOf(path string) statKey {
	info, err := os.Stat(path)
	if err != nil {
		return statKey{}
	}
	return statKey{present: true, modTime: info.ModTime().UnixNano(), size: info.Size()}
}

// ReadTasks captures one live read. It deliberately gets a fresh store like
// every other application read, so a long-lived presenter cannot retain
// anything the next read would have to invalidate.
func (a *Application) ReadTasks(operation *OperationContext) (*ReadModel, error) {
	target := a.store()
	snapshot, err := target.ReadSnapshot(false)
	if err != nil {
		return nil, err
	}
	path := target.Org()
	return &ReadModel{
		snapshot: snapshot,
		queries:  taskquery.New(snapshot, a.contextFor(operation), a.queryOptions...),
		path:     path,
		stat:     statKeyOf(path),
	}, nil
}

// Items is the live tasks this model was built over, in file order.
func (m *ReadModel) Items() []store.Item { return m.snapshot.Items }

// Queries is the read model over the same snapshot, for every derived answer —
// availability, links, the tree, body lines.
func (m *ReadModel) Queries() *taskquery.Queries { return m.queries }

// Snapshot is the exact read behind every answer this model gives.
func (m *ReadModel) Snapshot() *store.Snapshot { return m.snapshot }

// TaskFor is the canonical task for a stable id.
func (m *ReadModel) TaskFor(id string) (store.Item, bool) {
	return findInSource(m.queries, id, store.SourceLive)
}

// NodeFor is where an item sits in the structural tree.
func (m *ReadModel) NodeFor(item store.Item) *taskquery.Node { return m.queries.NodeFor(item) }

// Sections is every live section record, in file order.
func (m *ReadModel) Sections() []record.Record { return sectionsOf(m.queries) }

// ViewTasks answers a named selection from the SAME snapshot, so a multi-view
// adapter never parses a second read to render one screen.
func (m *ReadModel) ViewTasks(name string) ([]store.Item, error) {
	return viewItems(m.queries, name)
}

// Stale reports that the live file no longer matches the snapshot this model
// was built from.
//
// A long-lived presenter must gate refreshes on THIS rather than on a mutating
// store's own change detection: any read that lets a store self-reload consumes
// its change signal, and a model kept until the next tick would then stay stale
// forever.
func (m *ReadModel) Stale() bool { return statKeyOf(m.path) != m.stat }
