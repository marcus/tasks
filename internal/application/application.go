// Package application is the persistence-neutral facade shared by the CLI, the
// TUI, and the HTTP API — the Go counterpart of lib/tasks/application.rb and
// the operation objects it composes.
//
// It accepts typed Go inputs and returns typed results. Adapter concerns —
// argv, terminal rendering, HTTP status mapping — deliberately stay outside it,
// and so does everything the store owns: validation, transactions, rollback,
// eligibility, the claim compare-and-set. What lives here is COMPOSITION: the
// operation objects, argument normalization, the creation defaults that belong
// to the runtime rather than to persistence, the WAITING default behind a human
// delegation, the blocker note behind a release, and the project commands that
// run several store calls and must report one outcome.
//
// The rule that keeps the boundary honest: if a behavior can be expressed as a
// rule about the FILE, it belongs to the store. If it can only be expressed as
// a rule about two store calls, or about what a caller asked for, it belongs
// here.
package application

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// StoreFactory builds a FRESH store for every application operation.
//
// Ruby's StoreFactory exists because Store keeps read caches for interactive
// clients, so one long-lived instance in an HTTP or CLI application object
// would let request-local reads leak into later calls. The Go store has no such
// cache today, and the factory is kept anyway for two reasons that outlive
// that: an operation that gets its own store cannot be handed a stale one by a
// neighbour, and a future concurrent API surface gets per-request isolation by
// construction rather than by review.
type StoreFactory func() Store

// Options is the immutable construction settings of one application.
type Options struct {
	// Factory is required.
	Factory StoreFactory

	// TemporalContext is the clock every operation reads. nil means "now, UTC",
	// which is the same default Ruby falls back to when no factory is injected.
	TemporalContext func() temporal.Context

	// HostContext is the machine's own @context, applied to a capture unless
	// the command opts out. It must be an @-prefixed word or empty.
	HostContext string

	// DelegationKeySource mints the per-operation journal coalescing token.
	// It is injectable for exactly the reason the store's IDSource is: the
	// token is persisted into journal bytes, so an unpinnable one makes two
	// identical runs produce different journals.
	DelegationKeySource func() string

	// QueryOptions are passed to every taskquery built here — the link
	// shorthand and system configuration a read surface needs.
	QueryOptions []taskquery.Option
}

var hostContextPattern = regexp.MustCompile(`^@\S+$`)

// Application is the facade. It holds only immutable construction settings; a
// mutable store is built per operation and never retained.
type Application struct {
	factory             StoreFactory
	temporalContext     func() temporal.Context
	hostContext         string
	delegationKeySource func() string
	queryOptions        []taskquery.Option
}

// New validates the options and builds an application.
func New(options Options) (*Application, error) {
	if options.Factory == nil {
		return nil, fmt.Errorf("store factory is required")
	}
	if options.HostContext != "" && !hostContextPattern.MatchString(options.HostContext) {
		return nil, fmt.Errorf("host_context must be an @context or empty")
	}
	return &Application{
		factory:             options.Factory,
		temporalContext:     options.TemporalContext,
		hostContext:         options.HostContext,
		delegationKeySource: options.DelegationKeySource,
		queryOptions:        append([]taskquery.Option{}, options.QueryOptions...),
	}, nil
}

// NewWithStore is the convenience for the common case: one live/archive pair,
// one clock, no injected seams.
func NewWithStore(built func() *store.Store, context func() temporal.Context) (*Application, error) {
	return New(Options{
		Factory:         func() Store { return built() },
		TemporalContext: context,
	})
}

// HostContext is the configured machine context, or "" when there is none.
func (a *Application) HostContext() string { return a.hostContext }

// -- clocks and stores --------------------------------------------------------

// contextFor resolves the clock one operation reads: the operation's own pinned
// context wins, then the injected factory, then now in UTC.
func (a *Application) contextFor(operation *OperationContext) temporal.Context {
	if pinned, ok := operation.TemporalContext(); ok {
		return pinned
	}
	if a.temporalContext != nil {
		return a.temporalContext()
	}
	return temporal.Context{Now: time.Now().UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
}

// today is the ISO local date the store's date-sensitive writes are measured
// against.
func (a *Application) today(operation *OperationContext) string {
	return a.contextFor(operation).LocalDate().ISO()
}

func (a *Application) store() Store { return a.factory() }

// DelegationModes is the delegation mode vocabulary the store behind this
// application enforces. Surfaces ask for it AT USE TIME — a package-level var
// built during init would freeze the built-in set before the store the process
// actually writes through was ever constructed, and a user's configured modes
// would then be silently missing from the prompts and refusals that quote them.
//
// It is an optional store capability rather than a method on the Store
// interface so a caller's own store double is not forced to answer it.
func (a *Application) DelegationModes() record.ModeVocabulary {
	if source, ok := a.store().(interface {
		Modes() record.ModeVocabulary
	}); ok {
		return record.Modes(source.Modes())
	}
	return record.BuiltinModes()
}

// Queries builds a read model over ONE fresh snapshot. Every read method here
// goes through it, so no application read can mix fields from two reads.
func (a *Application) Queries(includeArchive bool, operation *OperationContext) (*taskquery.Queries, error) {
	snapshot, err := a.store().ReadSnapshot(includeArchive)
	if err != nil {
		return nil, err
	}
	return taskquery.New(snapshot, a.contextFor(operation), a.queryOptions...), nil
}

// -- reads --------------------------------------------------------------------

// ListTasks answers a parsed filter. The archive half is captured exactly when
// the filter's scope reaches it, so an ordinary read never pays for it.
func (a *Application) ListTasks(filter query.Filter, operation *OperationContext) ([]store.Item, error) {
	queries, err := a.Queries(filter.IncludeArchive(), operation)
	if err != nil {
		return nil, err
	}
	return queries.List(filter), nil
}

// NamedViews is TaskQueries::NAMED_VIEWS.
var NamedViews = []string{"agenda", "next", "quadrants", "inbox"}

// DefaultUrgentDays is Quadrants::DEFAULT_URGENT_DAYS, passed through so a
// caller that does not care can supply 0.
const DefaultUrgentDays = 3

// ViewTasks answers one named selection. The names live here rather than in
// each adapter so a CLI, an HTTP handler, and the TUI cannot each invent their
// own idea of what "next" means.
func (a *Application) ViewTasks(name string, operation *OperationContext) ([]store.Item, error) {
	queries, err := a.Queries(false, operation)
	if err != nil {
		return nil, err
	}
	return viewItems(queries, name)
}

func viewItems(queries *taskquery.Queries, name string) ([]store.Item, error) {
	switch name {
	case "agenda":
		return queries.AgendaItems(), nil
	case "next":
		return queries.NextItems(), nil
	case "quadrants":
		return queries.QuadrantItems(), nil
	case "inbox":
		return queries.InboxItems(), nil
	}
	return nil, fmt.Errorf("unknown task view: %s", name)
}

// GetTask resolves a STABLE ID. Fuzzy title and L<line> resolution are CLI-only
// conveniences and deliberately absent: a missing id is an ordinary "not found"
// so a transport maps it to its own response rather than to an error.
func (a *Application) GetTask(id string, includeArchive bool, operation *OperationContext) (store.Item, bool, error) {
	queries, err := a.Queries(includeArchive, operation)
	if err != nil {
		return store.Item{}, false, err
	}
	item, found := findByID(queries, id, includeArchive)
	return item, found, nil
}

// findByID searches live first and the archive only when it was captured, which
// is the precedence `TaskQueries#find` uses.
func findByID(queries *taskquery.Queries, id string, includeArchive bool) (store.Item, bool) {
	for _, item := range queries.LiveItems() {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	if !includeArchive {
		return store.Item{}, false
	}
	for _, item := range queries.ArchiveItems() {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}

// findInSource is the exact-source lookup the API-grade read uses: `live` and
// `archive` are different resources that may legitimately share an id.
func findInSource(queries *taskquery.Queries, id string, source store.Source) (store.Item, bool) {
	items := queries.LiveItems()
	if source == store.SourceArchive {
		items = queries.ArchiveItems()
	}
	for _, item := range items {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}

// ListSections is every live section record, in file order.
func (a *Application) ListSections(operation *OperationContext) ([]record.Record, error) {
	queries, err := a.Queries(false, operation)
	if err != nil {
		return nil, err
	}
	return sectionsOf(queries), nil
}

func sectionsOf(queries *taskquery.Queries) []record.Record {
	sections := []record.Record{}
	for _, parsed := range queries.Snapshot().LiveRecords() {
		if parsed.String("type") == "section" {
			sections = append(sections, parsed)
		}
	}
	return sections
}

// ListProjects is projects and areas rolled up over their open tasks. It lives
// here so the CLI and a transport share one definition of what a project is and
// how the list is ordered.
func (a *Application) ListProjects(operation *OperationContext) ([]taskquery.ProjectView, error) {
	queries, err := a.Queries(false, operation)
	if err != nil {
		return nil, err
	}
	return queries.Projects(), nil
}

// GetProject is one project or area section, and false when the id is neither.
func (a *Application) GetProject(id string, operation *OperationContext) (taskquery.ProjectView, bool, error) {
	queries, err := a.Queries(false, operation)
	if err != nil {
		return taskquery.ProjectView{}, false, err
	}
	view, found := queries.ProjectView(id)
	return view, found, nil
}

// -- API-grade reads ----------------------------------------------------------
//
// These return canonical data PLUS the global live+archive revision produced by
// the exact checked snapshot behind that data. The direct methods above stay
// available for CLI and TUI callers that do not need a change token.

// checkedQuery runs one checked read and shapes the answer. It is a package
// function rather than a method because Go methods cannot take type parameters,
// and the data shape is precisely what varies.
func checkedQuery[T any](a *Application, operation *OperationContext, body func(*taskquery.Queries) (T, bool)) ReadResult[T] {
	var zero T
	checked, err := a.store().CheckedReadSnapshot()
	if err != nil {
		return ReadResult[T]{
			Status: ReadUnavailable, Data: zero,
			Errors: []store.Entry{{Message: store.UnavailableMessage(err)}},
		}
	}
	if !checked.OK() {
		return ReadResult[T]{
			Status: readStatusOf(checked.Status), Data: zero,
			StoreRevision: checked.StoreRevision,
			Errors:        checked.Errors, Warnings: checked.Warnings,
		}
	}
	queries := taskquery.New(checked.Snapshot, a.contextFor(operation), a.queryOptions...)
	data, found := body(queries)
	status := ReadOK
	if !found {
		status, data = ReadNotFound, zero
	}
	return ReadResult[T]{
		Status: status, Data: data,
		StoreRevision: checked.StoreRevision, Warnings: checked.Warnings,
	}
}

// ListTasksResult is ListTasks with the change token. An empty list is `ok`,
// not `not_found`: the question had an answer.
func (a *Application) ListTasksResult(filter query.Filter, operation *OperationContext) ReadResult[[]store.Item] {
	return checkedQuery(a, operation, func(queries *taskquery.Queries) ([]store.Item, bool) {
		return queries.List(filter), true
	})
}

// GetTaskResult is the exact-source resource read.
func (a *Application) GetTaskResult(id string, source store.Source, operation *OperationContext) ReadResult[store.Item] {
	if source != store.SourceLive && source != store.SourceArchive {
		return ReadResult[store.Item]{
			Status: ReadNotFound,
			Errors: []store.Entry{{Message: fmt.Sprintf("unknown source: %s", source)}},
		}
	}
	return checkedQuery(a, operation, func(queries *taskquery.Queries) (store.Item, bool) {
		return findInSource(queries, id, source)
	})
}

// ListSectionsResult is ListSections with the change token.
func (a *Application) ListSectionsResult(operation *OperationContext) ReadResult[[]record.Record] {
	return checkedQuery(a, operation, func(queries *taskquery.Queries) ([]record.Record, bool) {
		return sectionsOf(queries), true
	})
}

// ListProjectsResult is ListProjects with the change token.
func (a *Application) ListProjectsResult(operation *OperationContext) ReadResult[[]taskquery.ProjectView] {
	return checkedQuery(a, operation, func(queries *taskquery.Queries) ([]taskquery.ProjectView, bool) {
		return queries.Projects(), true
	})
}

// ProjectResult is GetProject with the change token, mapped to not_found when
// the id is not a project or area.
func (a *Application) ProjectResult(id string, operation *OperationContext) ReadResult[taskquery.ProjectView] {
	return checkedQuery(a, operation, func(queries *taskquery.Queries) (taskquery.ProjectView, bool) {
		return queries.ProjectView(id)
	})
}

// ReadStatusResult is the safe foundation for /meta and readiness: store health
// and its change token, and nothing a transport would have to be told about.
func (a *Application) ReadStatusResult(operation *OperationContext) ReadResult[struct{}] {
	return checkedQuery(a, operation, func(*taskquery.Queries) (struct{}, bool) {
		return struct{}{}, true
	})
}

// TaskResultFromMutation rebuilds a canonical resource from the immutable
// post-mutation snapshot a write produced.
//
// This is what keeps a response and its global revision tied to the exact write
// instead of racing a second read after the lock is released.
func (a *Application) TaskResultFromMutation(outcome Outcome, id string, operation *OperationContext) (ReadResult[store.Item], error) {
	if !outcome.OK() || outcome.ReadSnapshot == nil || outcome.StoreRevision == "" {
		return ReadResult[store.Item]{}, fmt.Errorf("mutation result has no coherent task snapshot")
	}
	queries := taskquery.New(outcome.ReadSnapshot, a.contextFor(operation), a.queryOptions...)
	item, found := findInSource(queries, id, store.SourceLive)
	status := ReadOK
	if !found {
		status = ReadNotFound
	}
	return ReadResult[store.Item]{
		Status: status, Data: item, StoreRevision: outcome.StoreRevision,
	}, nil
}

// -- shared write plumbing ----------------------------------------------------

// mintDelegationKey is sixteen hex characters, from the injected mint when
// there is one.
func (a *Application) mintDelegationKey() string {
	if a.delegationKeySource != nil {
		return a.delegationKeySource()
	}
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		panic("tasks: secure random unavailable for delegation key: " + err.Error())
	}
	return hex.EncodeToString(buffer)
}

// unsupportedSchemaRefusal is the gate the MULTI-CALL commands run first.
//
// A single-transaction store mutation refuses an unreadable schema itself. A
// project command runs several store calls, so it has to ask before any of them
// runs — otherwise the first call refuses and the later ones report a state
// derived from a file this build must not interpret.
func unsupportedSchemaRefusal(target Store) *Outcome {
	checked, err := target.CheckedReadSnapshot()
	if err != nil {
		return &Outcome{MutationResult: store.MutationResult{
			Status: store.MutationUnavailable, Errors: []string{store.UnavailableMessage(err)},
		}}
	}
	if checked.Status == store.StatusUnavailable {
		messages := []string{}
		for _, entry := range checked.Errors {
			messages = append(messages, entry.Message)
		}
		if len(messages) == 0 {
			messages = []string{"task store unavailable"}
		}
		return &Outcome{MutationResult: store.MutationResult{
			Status: store.MutationUnavailable, Errors: messages,
		}}
	}
	if checked.Status != store.StatusUnsupportedSchema {
		return nil
	}
	errors := []string{}
	for _, entry := range checked.Errors {
		message := entry.Message
		if entry.Source == store.SourceArchive {
			message = "archive: " + message
		}
		errors = append(errors, message)
	}
	return &Outcome{MutationResult: store.MutationResult{
		Status: store.MutationUnsupportedSchema, Errors: errors,
	}}
}

// rollbackOutcome folds a store's recorded rollback into the same result shape
// single-transaction mutations return.
//
// The STAGE is preserved deliberately. A failed write is `unavailable` and a
// failed post-write check is `store_invalid`; collapsing both to `store_invalid`
// is what once made a failed write claim the file failed validation.
func rollbackOutcome(target Store) *Outcome {
	writer, ok := target.(ProjectWriter)
	if !ok {
		return nil
	}
	reason, stage := writer.LastRollback()
	if reason == "" {
		return nil
	}
	status := store.MutationStoreInvalid
	if stage == store.RollbackWrite {
		status = store.MutationUnavailable
	}
	if stage == "" {
		stage = store.RollbackValidation
	}
	return &Outcome{MutationResult: store.MutationResult{
		Status: status, Errors: []string{reason},
		RolledBack: true, RollbackStage: stage,
	}}
}

// unsupported is the refusal an application operation returns when the store
// build in front of it cannot perform the store half at all.
//
// It is deliberately NOT silence and deliberately not a different behavior. A
// caller that asked to release a claim and got `ok` because the release was
// skipped would be worse than one that got a refusal naming what is missing.
func unsupported(capability string) Outcome {
	return Outcome{MutationResult: store.MutationResult{
		Status: store.MutationUnavailable,
		Errors: []string{"this build cannot " + capability + " — the store does not implement it yet"},
	}}
}

func trimmed(value string) string { return strings.TrimSpace(value) }
