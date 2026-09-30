package api

import (
	"net/http"

	"github.com/marcus/tasks/internal/application"
)

// activateTask is `tasks activate` over HTTP: make the task available now.
//
// It cannot be composed from PATCH. `{deferred: false, scheduled: null}` drops a
// past available-from date that activate keeps, and on a lead or recurring task
// it releases nothing at all, because the release there is a one-occurrence
// marker (`lead_skip`) no PATCH field writes. So this is its own verb, and it
// runs the one application operation the CLI runs — same field, same store
// transaction, one undo step, the same refusals.
//
// No body: the verb has no parameters. If-Match is mandatory and is compared as
// on PATCH, against the task's own content. Any live task may be activated by
// its stable id; the CLI's refusal of a closed task by default is a property of
// its fuzzy ref resolution (`--include-done` lifts it), not of the operation.
func (s *Server) activateTask(request *http.Request, id, requestID string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	if err := rejectBody(request, "Activate requests"); err != nil {
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
	outcome := s.options.App.ActivateTask(application.ActivateCommand{
		ID: id, ExpectedRevision: expected,
	}, operation)
	if err := s.mutationFailure(outcome, mutationRefusal{ID: id, SemanticInvalid: true}); err != nil {
		return response{}, err
	}
	return s.taskWriteResponse(outcome, id)
}
