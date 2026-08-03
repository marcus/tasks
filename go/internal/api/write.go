package api

import (
	"net/http"
	"strings"

	"tasks-go/internal/application"
	"tasks-go/internal/jsonout"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// The write surface.
//
// Two things are worth stating up front, because they explain most of what is
// here and every refusal that is not.
//
// First, an adapter validates SHAPE and nothing else. Whether a state
// transition is legal, whether a claim may be taken, whether a schedule can
// ever fire — all of that belongs to the store and reaches this file as a
// status in the shared vocabulary. What this file owns is the mapping from that
// vocabulary onto HTTP, and the message a client reads.
//
// Second, where the Go store cannot perform an operation AT ALL, the route
// answers 501 with a stated reason rather than approximating it. A half-working
// write against a task store is the one failure mode the user cannot recover
// from, and an endpoint that silently ignored an If-Match or dropped a field
// would be exactly that. Every such refusal names the missing capability.

// createFields is App::CREATE_FIELDS.
var createFields = []string{
	"title", "priority", "tags", "contexts", "deferred", "scheduled", "scheduled_time",
	"deadline", "deadline_time", "state", "project", "parent_id", "recurrence", "lead",
	"body", "apply_host_context",
}

// patchFields is App::PATCH_FIELDS.
var patchFields = []string{
	"title", "priority", "body", "contexts", "tags", "deferred", "scheduled", "scheduled_time",
	"deadline", "deadline_time", "recurrence", "lead", "parent_id", "placement", "state",
}

var placementFields = []string{"parent_id", "before_id"}

func (s *Server) createTask(request *http.Request, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	if err := rejectUnknownFields(body, createFields); err != nil {
		return response{}, err
	}
	if err := validateCreateBody(body); err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}

	command, err := s.createCommand(body)
	if err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	outcome := s.options.App.CreateTask(command, operation)
	parentID, _ := body.text("parent_id")
	if err := s.mutationFailure(outcome, mutationRefusal{ParentID: parentID}); err != nil {
		return response{}, err
	}
	if len(outcome.TouchedIDs) == 0 {
		return response{}, errorOf(503, "unavailable")
	}
	id := outcome.TouchedIDs[0]
	item, resources, revision, err := s.resourceAfter(outcome, id)
	if err != nil {
		return response{}, err
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { resources.writeTask(w, item) }, revision)
	return response{
		status: 201,
		headers: map[string]string{
			"location": "/api/v1/tasks/" + id,
			"etag":     etag(resources.revisionFor(item)),
		},
		body: w.Bytes(),
	}, nil
}

// createCommand maps a validated body onto the application's typed command.
func (s *Server) createCommand(body *jsonObject) (application.CreateCommand, error) {
	title, _ := body.text("title")
	priority := ""
	if body.has("priority") && !body.isNull("priority") {
		priority, _ = body.text("priority")
	}
	// Ruby's order, and it reaches the file: `Array(body["tags"]) +
	// Array(body["contexts"])`. The store preserves stored tag order, so
	// swapping these two writes different bytes for the same request.
	tags := []string{}
	if ordinary, ok := body.stringList("tags"); ok {
		tags = append(tags, ordinary...)
	}
	if contexts, ok := body.stringList("contexts"); ok {
		tags = append(tags, contexts...)
	}
	deferred := false
	if value, ok := body.boolean("deferred"); ok {
		deferred = value
	}
	applyHost := true
	if value, ok := body.boolean("apply_host_context"); ok {
		applyHost = value
	}
	command := application.CreateCommand{
		Title: title, Priority: priority, Tags: tags, Deferred: deferred,
		SkipHostContext: !applyHost,
	}
	if body.has("state") {
		command.State, _ = body.text("state")
	}
	if body.has("project") && !body.isNull("project") {
		command.Project, _ = body.text("project")
	}
	if body.has("parent_id") && !body.isNull("parent_id") {
		command.ParentID, _ = body.text("parent_id")
	}
	if body.has("scheduled") && !body.isNull("scheduled") {
		command.Scheduled, _ = body.text("scheduled")
	}
	if body.has("deadline") && !body.isNull("deadline") {
		command.Deadline, _ = body.text("deadline")
	}
	if body.has("recurrence") && !body.isNull("recurrence") {
		canonical, err := normalizeRecurrence(body, false)
		if err != nil {
			return application.CreateCommand{}, err
		}
		command.Recurrence = canonical
	}
	if body.has("lead") && !body.isNull("lead") {
		canonical, err := normalizeLead(body, false)
		if err != nil {
			return application.CreateCommand{}, err
		}
		command.Lead = canonical
	}
	if body.has("body") && !body.isNull("body") {
		if lines, ok := body.stringList("body"); ok {
			command.Notes = lines
		} else {
			text, _ := body.text("body")
			command.Body = text
		}
	}
	return command, nil
}

// -- PATCH --------------------------------------------------------------------

func (s *Server) updateTask(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	expected, err := ifMatch(request)
	if err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	if err := rejectUnknownFields(body, patchFields); err != nil {
		return response{}, err
	}
	if body.empty() {
		return response{}, validationError(reason("changes", "must contain at least one field"))
	}

	read, readErr := s.options.Read()
	if readErr != nil || !read.OK() {
		return response{}, readFailure(read, readErr)
	}
	current, found := findInSource(read.Queries, id, store.SourceLive)
	if !found {
		return response{}, errorWith(404, "not_found", "No live task with that id.").
			withDetails(pairDetails(detailPair{Key: "field", Value: "id"}, detailPair{Key: "id", Value: id}))
	}
	if err := validatePatchBody(body, read.Queries, current); err != nil {
		return response{}, err
	}

	applier := s.changesets()
	if applier == nil {
		return response{}, errorWith(501, "not_implemented",
			"This build cannot apply a task changeset: the application layer exposes no "+
				"multi-field update and no changeset store was supplied.")
	}
	changes, err := s.patchChanges(body, read.Queries, current)
	if err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	context := s.options.TemporalContext()
	result := applier.ApplyChangeset(store.Changeset{
		ID: id, Changes: changes, ExpectedRevision: expected,
		Today: context.LocalDate().ISO(), Context: context,
	})
	outcome := application.Outcome{MutationResult: result}
	if err := s.mutationFailure(outcome, mutationRefusal{
		ID: id, SemanticInvalid: true, Placement: placementOf(changes),
	}); err != nil {
		return response{}, err
	}
	item, resources, revision, err := s.resourceAfter(outcome, id)
	if err != nil {
		return response{}, err
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { resources.writeTask(w, item) }, revision)
	return response{
		status:  200,
		headers: map[string]string{"etag": etag(resources.revisionFor(item))},
		body:    w.Bytes(),
	}, nil
}

func placementOf(changes []store.Change) bool {
	for _, change := range changes {
		if change.Field == store.FieldLocation {
			return true
		}
	}
	return false
}

// -- routes this build refuses ------------------------------------------------
//
// Each of these exists in the contract and in the Ruby server, and the Go store
// has no operation behind it. Answering 501 with the missing capability named is
// the only response that cannot mislead a client into believing a write landed.

func (s *Server) deleteTask(request *http.Request, id, requestID string) (response, error) {
	if err := rejectBody(request, "DELETE requests"); err != nil {
		return response{}, err
	}
	if _, err := ifMatch(request); err != nil {
		return response{}, err
	}
	if _, err := queryParams(request, "cascade"); err != nil {
		return response{}, err
	}
	return response{}, notImplemented("delete a task",
		"the Go store implements no undoable hard delete yet")
}

func (s *Server) decideProposal(request *http.Request, id, action, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if action == "reject" {
		if _, err := optionalBody(request); err != nil {
			return response{}, err
		}
	} else if err := rejectBody(request, "Task proposal action requests"); err != nil {
		return response{}, err
	}
	if _, err := ifMatch(request); err != nil {
		return response{}, err
	}
	return response{}, notImplemented(action+" a proposal",
		"the Go store implements no proposal decision yet")
}

func (s *Server) delegationAction(request *http.Request, id, action, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if action == "undelegate" {
		if err := rejectBody(request, "Undelegate requests"); err != nil {
			return response{}, err
		}
	}
	if _, err := ifMatch(request); err != nil {
		return response{}, err
	}
	return response{}, delegationPreconditionRefusal(action)
}

func (s *Server) putWorkRef(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if _, err := ifMatch(request); err != nil {
		return response{}, err
	}
	return response{}, delegationPreconditionRefusal("work_ref")
}

// delegationPreconditionRefusal is the one refusal shared by all five
// delegation routes.
//
// The application layer performs every delegation verb, and the store performs
// the claim compare-and-set. What neither does yet is honour an
// `expected_revision`: `application.runDelegation` refuses a non-empty one
// outright, on the grounds that a guard it cannot enforce inside the write
// transaction would be a race dressed as protection. The HTTP contract makes
// If-Match MANDATORY on these routes, so accepting the request would mean
// dropping a precondition the client believes it set — the one thing worse than
// refusing.
func delegationPreconditionRefusal(action string) error {
	return notImplemented(action+" over HTTP",
		"the store cannot honour the mandatory If-Match precondition on a delegation, "+
			"and dropping a precondition a client set is not an option")
}

func (s *Server) createProject(request *http.Request, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if _, err := jsonBody(request); err != nil {
		return response{}, err
	}
	return response{}, notImplemented("create a project",
		"the Go store implements no project lifecycle write yet")
}

func (s *Server) renameProject(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if _, err := jsonBody(request); err != nil {
		return response{}, err
	}
	return response{}, notImplemented("rename a project",
		"the Go store implements no project lifecycle write yet")
}

func (s *Server) completeProject(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if err := rejectBody(request, "Project action requests"); err != nil {
		return response{}, err
	}
	return response{}, notImplemented("complete a project",
		"the Go store implements no project lifecycle write yet")
}

func (s *Server) archiveProject(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request, "force"); err != nil {
		return response{}, err
	}
	if err := rejectBody(request, "Project action requests"); err != nil {
		return response{}, err
	}
	return response{}, notImplemented("archive a project",
		"the Go store implements no project lifecycle write yet")
}

// notImplemented is the honest refusal: 501, the operation, and the reason.
func notImplemented(operation, why string) error {
	return errorWith(501, "not_implemented",
		"This build cannot "+operation+" — "+why+".")
}

// -- shared write plumbing ----------------------------------------------------

func (s *Server) changesets() Changesets {
	if s.options.Changesets == nil {
		return nil
	}
	return s.options.Changesets()
}

func (s *Server) operationContext(requestID string) (*application.OperationContext, error) {
	context, err := application.NewOperationContext(requestID, application.SourceAPI, "")
	if err != nil {
		return nil, errorOf(503, "unavailable")
	}
	return context.WithTemporalContext(s.options.TemporalContext()), nil
}

// ensureStoreReady is App#ensure_store_ready!: a multi-step write asks the
// store's health BEFORE it starts, so an unreadable schema is refused with the
// diagnostic that names it rather than by whichever step happens to fail first.
func (s *Server) ensureStoreReady() error {
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return readFailure(read, err)
	}
	return nil
}

// resourceAfter builds the canonical post-write resource from the snapshot the
// write ITSELF produced, so the response cannot show a neighbouring writer's
// later state and its ETag is the revision this request created.
func (s *Server) resourceAfter(outcome application.Outcome, id string) (store.Item, *resourceContext, string, error) {
	if outcome.ReadSnapshot == nil || outcome.StoreRevision == "" {
		// An idempotent repeat writes nothing and so carries no snapshot. A
		// fresh read is the only way to answer, and it is correct precisely
		// because nothing was written.
		read, err := s.options.Read()
		if err != nil || !read.OK() {
			return store.Item{}, nil, "", readFailure(read, err)
		}
		item, found := findInSource(read.Queries, id, store.SourceLive)
		if !found {
			return store.Item{}, nil, "", errorOf(404, "not_found")
		}
		return item, newResourceContext(read.Queries), read.Revision, nil
	}
	queries := taskquery.New(outcome.ReadSnapshot, s.options.TemporalContext(), s.options.QueryOptions...)
	item, found := findInSource(queries, id, store.SourceLive)
	if !found {
		return store.Item{}, nil, "", errorOf(404, "not_found")
	}
	return item, newResourceContext(queries), outcome.StoreRevision, nil
}

// mutationRefusal is the context mutationFailure needs to shape a refusal.
type mutationRefusal struct {
	ID              string
	ParentID        string
	Placement       bool
	SemanticInvalid bool
}

// mutationFailure is App#mutation_failure!: the shared outcome vocabulary
// mapped onto HTTP, with the domain's own sentence preserved where it is the
// only useful information.
func (s *Server) mutationFailure(outcome application.Outcome, refusal mutationRefusal) error {
	if outcome.OK() {
		return nil
	}
	// A capability this build does not have is a 501 whatever status the
	// application dressed it in, because retrying cannot help and a 503 invites
	// exactly that.
	if capabilityRefusal(outcome) {
		return errorWith(501, "not_implemented", outcome.FirstError())
	}
	switch outcome.Status {
	case store.MutationNotFound:
		if refusal.ParentID != "" {
			return errorWith(404, "not_found", "parent_id does not identify a live task.").
				withDetails(pairDetails(detailPair{Key: "parent_id", Value: refusal.ParentID}))
		}
		return errorOf(404, "not_found")
	case store.MutationStale:
		return s.staleRefusal(refusal.ID)
	case store.MutationInvalid:
		message := message("validation_failed")
		if refusal.SemanticInvalid && outcome.FirstError() != "" {
			message = outcome.FirstError()
		}
		return errorWith(422, "validation_failed", message)
	case store.MutationConflict:
		return errorOf(409, "conflict")
	case store.MutationCycle:
		return errorOf(409, "cycle")
	case store.MutationTooDeep:
		return errorOf(409, "too_deep").
			withDetails(pairDetails(detailPair{Key: "max_depth", Value: s.options.MaxDepth}))
	case store.MutationUnsupportedSchema:
		return errorOf(503, "unsupported_schema_version").
			withDetails(pairDetails(detailPair{Key: "supported_version", Value: schemaVersion}))
	case store.MutationStoreInvalid:
		return errorOf(503, "store_invalid")
	}
	return errorOf(503, "unavailable")
}

// staleRefusal is the 412 with the CURRENT resource attached, which is what
// lets a client decide again without a second round trip.
func (s *Server) staleRefusal(id string) error {
	failure := errorOf(412, "stale_revision")
	if id == "" {
		return failure
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return failure
	}
	item, found := findInSource(read.Queries, id, store.SourceLive)
	if !found {
		return failure
	}
	resources := newResourceContext(read.Queries)
	return failure.
		withDetails(pairDetails(detailPair{Key: "current", Value: func(w *jsonout.Writer) {
			resources.writeTask(w, item)
		}})).
		withHeader("etag", etag(resources.revisionFor(item)))
}

// capabilityRefusal recognizes the application layer's own "this build cannot …"
// refusals, which are statements about the BUILD rather than about the request.
func capabilityRefusal(outcome application.Outcome) bool {
	for _, message := range outcome.Errors {
		if strings.HasPrefix(message, "this build cannot") ||
			strings.Contains(message, "is not implemented in the Go port") {
			return true
		}
	}
	return false
}
