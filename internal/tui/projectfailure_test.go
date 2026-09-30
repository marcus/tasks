package tui

import (
	"testing"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/store"
)

// A refused section action says what happened: only a missing section is
// "gone", and an unreadable or locked store is never reported as a deleted
// project.
func TestProjectFailureNamesTheActualRefusal(t *testing.T) {
	outcome := func(status store.MutationStatus, errors ...string) application.Outcome {
		return application.Outcome{MutationResult: store.MutationResult{Status: status, Errors: errors}}
	}
	for _, test := range []struct {
		name    string
		outcome application.Outcome
		want    string
	}{
		{"missing", outcome(store.MutationNotFound), "project no longer exists"},
		{"refused", outcome(store.MutationConflict, "the Inbox cannot be renamed"), "the Inbox cannot be renamed"},
		{"locked", outcome(store.MutationUnavailable, "task store is locked by another writer"),
			"task store is locked by another writer"},
		{"unavailable", outcome(store.MutationUnavailable), "task store unavailable — try again"},
		{"invalid", outcome(store.MutationStoreInvalid), "task file is invalid — run `tasks check`"},
	} {
		if got := projectFailure(test.outcome); got != test.want {
			t.Errorf("%s: projectFailure = %q, want %q", test.name, got, test.want)
		}
	}
}
