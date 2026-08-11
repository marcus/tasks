package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
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
	"title", "priority", "body", "formal_links", "contexts", "tags", "deferred", "scheduled", "scheduled_time",
	"deadline", "deadline_time", "recurrence", "lead", "parent_id", "placement", "state",
}

var placementFields = []string{"parent_id", "before_id"}

// rejectFields is App::REJECT_FIELDS: the one member a reject body may carry.
var rejectFields = []string{"notes"}

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
	// The date, and then the COMPLETE value when the request named a time of day.
	// Ruby's `temporal_input` does exactly this — build the value, and resolve a
	// floating one against the reader's zone so an unresolvable wall time is a
	// client error rather than a stored one.
	for _, field := range []string{"scheduled", "deadline"} {
		if !body.has(field) || body.isNull(field) {
			continue
		}
		text, _ := body.text(field)
		if field == "scheduled" {
			command.Scheduled = text
		} else {
			command.Deadline = text
		}
		timeKey := field + "_time"
		if !body.has(timeKey) || body.isNull(timeKey) {
			continue
		}
		date, ok := temporal.ParseDate(text)
		if !ok {
			// validateCommonBody already refused an unparseable date, so this is
			// unreachable; refusing again rather than silently dropping the time
			// is the cheaper of the two ways to be wrong here.
			return application.CreateCommand{}, validationError(reason(field, "must be a real calendar date"))
		}
		value, err := s.createTemporalValue(body, field, date)
		if err != nil {
			return application.CreateCommand{}, err
		}
		if field == "scheduled" {
			command.ScheduledValue = &value
		} else {
			command.DeadlineValue = &value
		}
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

// createTemporalValue builds the complete value behind a create's
// `{ local, timezone, fold }`, and is App#temporal_input for the create half.
//
// It is the twin of validate.go's `temporalInput`, which serves PATCH. The two
// are not one function because that one answers in `store.PatchValue`, whose
// temporal half has no accessor — a create needs the `temporal.Value` itself, to
// hand to the application command. `TestCreateAndPatchAgreeOnATimedValue` is
// what keeps the pair from drifting apart, because a one-line disagreement here
// would store a different instant depending on which verb a client used.
func (s *Server) createTemporalValue(body *jsonObject, field string, date temporal.Date) (temporal.Value, error) {
	timeKey := field + "_time"
	object, ok := body.object(timeKey)
	if !ok {
		return temporal.Value{}, validationError(reason(timeKey, "must be an object or null"))
	}
	local, _ := object.text("local")
	zone := ""
	if object.has("timezone") && !object.isNull("timezone") {
		zone, _ = object.text("timezone")
	}
	fold := 0
	if object.has("fold") {
		fold, _ = foldValue(object)
	}
	value, err := temporal.NewValue(date, local, zone, fold, true)
	if err != nil {
		return temporal.Value{}, validationError(reason(timeKey, err.Error()))
	}
	// A floating wall time names no zone of its own, so it is the READER's zone
	// that decides whether it exists at all.
	if value.Floating() {
		if _, err := value.Instant(s.options.TemporalContext()); err != nil {
			return temporal.Value{}, validationError(reason(timeKey, err.Error()))
		}
	}
	return value, nil
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

// deleteTask is the undoable hard delete. It answers 204 with no body, so the
// only thing a client learns from a success is that the subtree is gone — which
// is why the refusals below it carry the counts instead.
//
// The If-Match is MANDATORY here and reaches the store as the precondition it
// guards the whole subtree with, so unlike the delegation routes there is no
// precondition this adapter would have to drop.
func (s *Server) deleteTask(request *http.Request, id, requestID string) (response, error) {
	if err := rejectBody(request, "DELETE requests"); err != nil {
		return response{}, err
	}
	expected, err := ifMatch(request)
	if err != nil {
		return response{}, err
	}
	params, err := queryParams(request, "cascade")
	if err != nil {
		return response{}, err
	}
	no := false
	cascade, err := booleanQuery(params, "cascade", &no)
	if err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	outcome := s.options.App.DeleteTask(application.DeleteCommand{
		ID: id, Cascade: *cascade, ExpectedRevision: expected,
	}, operation)
	if err := s.mutationFailure(outcome, mutationRefusal{ID: id}); err != nil {
		return response{}, err
	}
	return response{status: 204}, nil
}

// decideProposal accepts or declines one proposal.
//
// Approve takes no body; reject may carry withdrawal notes, which are the CLI's
// repeatable `--note` and are appended to the body in the SAME write. Both carry
// the domain's own sentence on a refusal — "task is TODO; only proposals can be
// approved" is the only useful thing to say, and the generic "one or more fields
// are invalid" would hide it.
func (s *Server) decideProposal(request *http.Request, id, action, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	var notes []string
	if action == "reject" {
		parsed, err := rejectNotes(request)
		if err != nil {
			return response{}, err
		}
		notes = parsed
	} else if err := rejectBody(request, "Task proposal action requests"); err != nil {
		return response{}, err
	}
	expected, err := ifMatch(request)
	if err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	outcome := s.options.App.DecideProposal(application.ProposalDecision{
		ID: id, Action: application.ProposalAction(action),
		ExpectedRevision: expected, Notes: notes,
	}, operation)
	if err := s.mutationFailure(outcome, mutationRefusal{ID: id, SemanticInvalid: true}); err != nil {
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

// rejectNotes is App#optional_reject_notes!: an absent body keeps the historical
// no-body contract, and a present one may supply only `notes`, as one string or
// an ordered list of them. An empty list is the same as none.
func rejectNotes(request *http.Request) ([]string, error) {
	body, err := optionalBody(request)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, nil
	}
	if err := rejectUnknownFields(body, rejectFields); err != nil {
		return nil, err
	}
	if !body.has("notes") {
		return nil, nil
	}
	if text, isText := body.text("notes"); isText {
		return []string{text}, nil
	}
	notes, isList := body.stringList("notes")
	if !isList {
		return nil, validationError(reason("notes", "must be text or an ordered list of text"))
	}
	if len(notes) == 0 {
		return nil, nil
	}
	return notes, nil
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

// notImplemented is the honest refusal: 501, the operation, and the reason.
func notImplemented(operation, why string) error {
	return errorWith(501, "not_implemented",
		"This build cannot "+operation+" — "+why+".")
}

// -- project writes -----------------------------------------------------------
//
// Projects and areas are rolled-up SECTIONS, so three things about this group
// differ from the task writes above, and all three come straight from Ruby.
//
// First, no If-Match. The domain exposes no per-resource revision for a section
// retitle, a completing cascade or an archive sweep, so there is nothing for a
// precondition to compare; requiring a header the server could only ignore
// would be theatre. The parity difference is documented in the OpenAPI contract.
//
// Second, rename, complete and archive PRE-VALIDATE the id through the project
// read model before writing. The store's section operations are mechanical —
// `RenameSection` retitles any section it can find, `CompleteProject` closes any
// section's descendants — so without the pre-read, a PATCH aimed at Inbox or at
// the "Projects" root would retitle it and then answer 404 about the write it
// had just performed.
//
// Third, a successful complete or rename may leave the section OUTSIDE the read
// model: a completed area holds no open work and drops out, and an area retitled
// "Inbox" is excluded by name. The write committed, so a post-write 404 would be
// a lie about durable state. Those two cases synthesize the response from the
// pre-read with the post-state applied.

// projectFields is the one-member body the create and rename routes accept.
var projectFields = []string{"title"}

// createProject creates a new empty project section under the top-level
// "Projects" root, which the domain bootstraps when it is absent.
//
// A blank or missing title is 422 at the adapter; a duplicate title (an existing
// project or area) is the domain's `invalid`, mapped to 422 below. The 201 body
// is the new project re-read from the committed store, which is also what makes
// its counts real rather than assumed.
func (s *Server) createProject(request *http.Request, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	title, err := projectTitle(body)
	if err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	outcome := s.options.App.CreateProject(title, operation)
	if err := s.projectMutationFailure(outcome, ""); err != nil {
		return response{}, err
	}
	id := outcome.Summary.CreatedID
	if id == "" {
		// Ruby's `result.summary.fetch(:created_id)` raises here and its rescue
		// turns that into a 503. A create that reported OK and named no id is a
		// store defect either way; answering 201 without a resource is worse.
		return response{}, errorOf(503, "unavailable")
	}
	read := s.options.App.ProjectResult(id, operation)
	if !read.OK() {
		return response{}, projectReadFailure(read.Status)
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { writeProject(w, read.Data) }, read.StoreRevision)
	return response{
		status: 201,
		headers: map[string]string{
			"location": "/api/v1/projects/" + id,
			"etag":     etag(read.StoreRevision),
		},
		body: w.Bytes(),
	}, nil
}

// renameProject replaces a project or area section's title.
func (s *Server) renameProject(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	body, err := jsonBody(request)
	if err != nil {
		return response{}, err
	}
	title, err := projectTitle(body)
	if err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	before := s.options.App.ProjectResult(id, operation)
	if !before.OK() {
		return response{}, projectReadFailure(before.Status)
	}
	outcome := s.options.App.RenameProject(id, title, operation)
	if err := s.projectMutationFailure(outcome, id); err != nil {
		return response{}, err
	}
	// The domain trims the stored title, so the synthesized fallback has to name
	// the trimmed one — otherwise the out-of-scope response would report a title
	// no store holds.
	renamed := strings.TrimSpace(title)
	return s.projectAfterMutation(id, operation, func() taskquery.ProjectView {
		return renamedProjectView(before.Data, renamed)
	})
}

// completeProject closes every open descendant task of the project.
func (s *Server) completeProject(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if err := rejectBody(request, "Project action requests"); err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	before := s.options.App.ProjectResult(id, operation)
	if !before.OK() {
		return response{}, projectReadFailure(before.Status)
	}
	outcome := s.options.App.CompleteProject(id, operation)
	if err := s.projectMutationFailure(outcome, id); err != nil {
		return response{}, err
	}
	return s.projectAfterMutation(id, operation, func() taskquery.ProjectView {
		return completedProjectView(before.Data)
	})
}

// archiveProject sweeps the project's whole subtree into the archive.
//
// The refusal is the CLI's: a sweep that would carry live open work needs an
// explicit force. Deferred and held tasks are open work too, so a parked project
// refuses as well — parity with complete's cascade, which closes them. The store
// sweep itself is mechanical, so this policy lives in the adapter, exactly as it
// does in Ruby.
func (s *Server) archiveProject(request *http.Request, id, requestID string) (response, error) {
	params, err := queryParams(request, "force")
	if err != nil {
		return response{}, err
	}
	no := false
	force, err := booleanQuery(params, "force", &no)
	if err != nil {
		return response{}, err
	}
	if err := rejectBody(request, "Project action requests"); err != nil {
		return response{}, err
	}
	if err := s.ensureStoreReady(); err != nil {
		return response{}, err
	}
	operation, err := s.operationContext(requestID)
	if err != nil {
		return response{}, err
	}
	view := s.options.App.ProjectResult(id, operation)
	if !view.OK() {
		return response{}, projectReadFailure(view.Status)
	}
	openCount, heldCount := view.Data.OpenCount, view.Data.HeldCount
	if openCount+heldCount > 0 && !*force {
		return response{}, errorWith(409, "conflict",
			"The project still has open tasks; retry with force=true to archive them.").
			withDetails(pairDetails(
				detailPair{Key: "open_count", Value: openCount},
				detailPair{Key: "held_count", Value: heldCount},
			))
	}
	outcome := s.options.App.ArchiveProject(id, operation)
	if err := s.projectMutationFailure(outcome, id); err != nil {
		return response{}, err
	}
	// The project resource is gone after this, so the answer is a small archive
	// summary tagged with the store's revision AFTER the sweep. Ruby reads the
	// status without gating on it, and so does this: the write already landed,
	// and refusing to describe it because a follow-up read failed would lose the
	// moved ids the client needs.
	status := s.options.App.ReadStatusResult(operation)
	archived := len(outcome.TouchedIDs)
	if outcome.Project != nil {
		archived = outcome.Project.Archived
	}
	moved := outcome.TouchedIDs
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginObject()
		w.KeyStr("id", id)
		w.KeyInt("archived", archived)
		w.Key("moved_ids")
		w.Strings(moved)
		w.EndObject()
	}, status.StoreRevision)
	return response{
		status:  200,
		headers: map[string]string{"etag": etag(status.StoreRevision)},
		body:    w.Bytes(),
	}, nil
}

// projectTitle is the shared body validation of create and rename: exactly one
// member, present, a string, and not blank.
func projectTitle(body *jsonObject) (string, error) {
	if err := rejectUnknownFields(body, projectFields); err != nil {
		return "", err
	}
	if !body.has("title") {
		return "", validationError(reason("title", "is required"))
	}
	title, isText := body.text("title")
	if !isText || strings.TrimSpace(title) == "" {
		return "", validationError(reason("title", "must be non-empty text"))
	}
	return title, nil
}

// projectAfterMutation shapes a successful complete or rename.
//
// The post-mutation re-read is preferred: a project stays in the read model, so
// its refreshed counts come from a coherent checked snapshot. When the section no
// longer surfaces there the write has ALREADY committed, so the synthesized
// pre-read stands in rather than a 404 that would misdescribe the store.
func (s *Server) projectAfterMutation(id string, operation *application.OperationContext,
	synthesize func() taskquery.ProjectView) (response, error) {
	read := s.options.App.ProjectResult(id, operation)
	if read.OK() {
		return projectResponse(read.Data, read.StoreRevision), nil
	}
	status := s.options.App.ReadStatusResult(operation)
	if !status.OK() {
		return response{}, projectReadFailure(status.Status)
	}
	return projectResponse(synthesize(), status.StoreRevision), nil
}

func projectResponse(view taskquery.ProjectView, revision string) response {
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { writeProject(w, view) }, revision)
	return response{
		status:  200,
		headers: map[string]string{"etag": etag(revision)},
		body:    w.Bytes(),
	}
}

// completedProjectView is the pre-read project as it stands after a completing
// cascade: every open task, deferred included, is closed, so the rollups are zero
// and the project is stuck.
func completedProjectView(view taskquery.ProjectView) taskquery.ProjectView {
	return taskquery.ProjectView{
		ID: view.ID, Title: view.Title,
		ParentID: view.ParentID, HasParentID: view.HasParentID,
		Kind: view.Kind, Line: view.Line,
		OpenCount: 0, NextCount: 0, Stuck: true, HeldCount: 0,
		Body: view.Body, HasBody: view.HasBody, TaskIDs: []string{},
	}
}

// renamedProjectView is the pre-read project with only its title replaced: a
// retitle moves no task, so every rollup — including the timed `next_time` and
// `next_at` pair — is the one the pre-read observed.
func renamedProjectView(view taskquery.ProjectView, title string) taskquery.ProjectView {
	renamed := view
	renamed.Title = title
	return renamed
}

// projectMutationFailure is App#project_mutation_failure!: the shared outcome
// vocabulary mapped onto HTTP for the section operations.
//
// It is deliberately not mutationFailure: a project refusal names no parent id,
// carries no placement, has no revision to be stale against, and says "project"
// rather than "task" in its 404.
func (s *Server) projectMutationFailure(outcome application.Outcome, id string) error {
	if outcome.OK() {
		return nil
	}
	// A capability this build does not have is a 501 whatever status the
	// application dressed it in, for the reason mutationFailure states.
	if capabilityRefusal(outcome) {
		return errorWith(501, "not_implemented", outcome.FirstError())
	}
	// Ruby's create passes no id, so its refusal details carry an explicit null.
	var subject any
	if id != "" {
		subject = id
	}
	switch outcome.Status {
	case store.MutationNotFound:
		return errorWith(404, "not_found", "No project with that id.").
			withDetails(pairDetails(detailPair{Key: "id", Value: subject}))
	case store.MutationInvalid:
		if len(outcome.FieldErrors) == 0 {
			return errorOf(422, "validation_failed")
		}
		return errorOf(422, "validation_failed").withDetails(fieldDetails(sortedFieldErrors(outcome.FieldErrors)))
	case store.MutationConflict:
		text := outcome.FirstError()
		if text == "" {
			text = message("conflict")
		}
		return errorWith(409, "conflict", text).
			withDetails(pairDetails(detailPair{Key: "id", Value: subject}))
	case store.MutationUnsupportedSchema:
		return errorOf(503, "unsupported_schema_version").
			withDetails(pairDetails(detailPair{Key: "supported_version", Value: schemaVersion}))
	case store.MutationStoreInvalid:
		return errorOf(503, "store_invalid")
	}
	return errorOf(503, "unavailable")
}

// sortedFieldErrors orders a refusal's per-field reasons by field name.
//
// Ruby's Hash preserves insertion order and a Go map has none, so an unsorted
// walk would emit the members of a multi-field refusal in a DIFFERENT order on
// every run — the same defect that once randomized `check`'s rows. Sorting is
// stable and agrees with Ruby wherever a refusal names one field, which is every
// project refusal today.
func sortedFieldErrors(fields map[string][]string) []fieldError {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	errors := make([]fieldError, 0, len(names))
	for _, name := range names {
		errors = append(errors, fieldError{Field: name, Reasons: fields[name]})
	}
	return errors
}

// projectReadFailure is App#read_failure! over the application's read
// vocabulary. The project writes pre-read through Application rather than
// through the api.Reader seam, so they need the same mapping over the other
// status type — including the 404 that says "task", because a pre-read refusal
// is a READ refusal and Ruby answers it with the shared not_found sentence.
func projectReadFailure(status application.ReadStatus) error {
	switch status {
	case application.ReadNotFound:
		return errorOf(404, "not_found")
	case application.ReadUnsupportedSchema:
		return errorOf(503, "unsupported_schema_version").
			withDetails(pairDetails(detailPair{Key: "supported_version", Value: schemaVersion}))
	case application.ReadStoreInvalid:
		return errorOf(503, "store_invalid")
	}
	return errorOf(503, "unavailable")
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
		// A delete refused for descendants is the one conflict that carries
		// counts, and they are the whole content of the refusal: they tell the
		// caller what cascade=true would actually remove. Every other conflict
		// keeps the canonical sentence — Ruby hands back its untyped summary Hash
		// there, and the typed Go summary has no member to render for them.
		if outcome.Summary.Descendants > 0 {
			return errorWith(409, "conflict",
				"The task has descendants; retry with cascade=true to delete them.").
				withDetails(pairDetails(
					detailPair{Key: "descendants", Value: outcome.Summary.Descendants},
					detailPair{Key: "open_descendants", Value: outcome.Summary.OpenDescendants},
				))
		}
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
		if strings.HasPrefix(message, "this build cannot") {
			return true
		}
	}
	return false
}
