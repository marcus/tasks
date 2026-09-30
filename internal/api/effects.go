package api

import (
	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/jsonout"
)

// Write effects.
//
// Some writes change more than the task they return: completing a recurring
// task rolls it forward and leaves it open, and completing a parent closes its
// open descendants in the same undo step. The response would otherwise show
// only the subject, and a client would have to guess the roll from a date or
// refetch a subtree to learn which rows closed. `meta.effects` says so. It is
// the application's report of what the store did inside the transaction,
// translated here, never re-derived by comparing reads.

// writeSuccessWithEffects is writeSuccess plus an optional `meta.effects`,
// present only when the write did something the resource does not show.
func writeSuccessWithEffects(w *jsonout.Writer, data func(*jsonout.Writer), revision string,
	effects application.Effects) {

	w.BeginObject()
	w.Key("data")
	data(w)
	w.Key("meta")
	w.BeginObject()
	w.KeyStrOrNull("store_revision", revision)
	if effects.Any() {
		w.Key("effects")
		writeEffects(w, effects)
	}
	w.EndObject()
	w.EndObject()
}

func writeEffects(w *jsonout.Writer, effects application.Effects) {
	w.BeginObject()
	if effects.Rolled != nil {
		w.Key("rolled")
		w.BeginObject()
		w.KeyStr("from", effects.Rolled.From)
		w.KeyStr("to", effects.Rolled.To)
		w.EndObject()
	}
	if len(effects.TouchedIDs) > 0 {
		w.Key("touched_ids")
		w.Strings(effects.TouchedIDs)
	}
	w.EndObject()
}

// taskWriteResponse is the 200 every single-task write answers with: the
// canonical task from the write's own snapshot, its ETag, and the effects.
func (s *Server) taskWriteResponse(outcome application.Outcome, id string) (response, error) {
	item, resources, revision, err := s.resourceAfter(outcome, id)
	if err != nil {
		return response{}, err
	}
	w := jsonout.New()
	writeSuccessWithEffects(w, func(w *jsonout.Writer) { resources.writeTask(w, item) }, revision,
		outcome.EffectsFor(id))
	return response{
		status:  200,
		headers: map[string]string{"etag": etag(resources.revisionFor(item))},
		body:    w.Bytes(),
	}, nil
}
