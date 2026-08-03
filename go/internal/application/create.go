package application

import (
	"fmt"
	"strings"

	"tasks-go/internal/store"
)

// PrepareCreateTask applies the creation defaults that belong to the
// application RUNTIME rather than to persistence — today, only the host
// context. The store receives a complete command and stays unaware of
// hostnames and configuration.
//
// It is exported for the same reason Ruby exports it: the CLI's dry-run path
// must use the SAME preparation a real create uses, so its preview cannot
// disagree with what the store would persist.
func (a *Application) PrepareCreateTask(command CreateCommand) CreateCommand {
	prepared := command.clone()
	if prepared.SkipHostContext || a.hostContext == "" {
		return prepared
	}
	contexts, ordinary := prepared.partitionTags()
	// The host context comes FIRST and duplicates collapse, so a capture that
	// already names the machine's context does not name it twice.
	effective := []string{a.hostContext}
	for _, context := range contexts {
		if context != a.hostContext {
			effective = append(effective, context)
		}
	}
	prepared.Tags = append(effective, ordinary...)
	return prepared
}

// CreateTask creates one live task in one checked store transaction.
func (a *Application) CreateTask(command CreateCommand, operation *OperationContext) Outcome {
	prepared := a.PrepareCreateTask(command)
	if messages := unpersistableCreateFields(prepared); len(messages) > 0 {
		return invalid(messages...)
	}
	if prepared.Body != "" && len(prepared.Notes) > 0 {
		// The store refuses this too, but refusing here as well keeps the
		// message identical whichever half of the port a caller reaches first,
		// and costs nothing: neither spelling has reached the file yet.
		return invalid("body and notes cannot both be supplied")
	}
	notes := prepared.Notes
	if prepared.Body != "" {
		notes = strings.Split(prepared.Body, "\n")
	}
	return Outcome{MutationResult: a.store().CreateTask(store.CreateCommand{
		Title:    prepared.Title,
		Priority: prepared.Priority,
		Tags:     copyOf(prepared.Tags),
		State:    prepared.State,
		Project:  prepared.Project,
		ParentID: prepared.ParentID,
		Notes:    copyOf(notes),
		Deferred: prepared.Deferred,
	}, a.today(operation))}
}

// unpersistableCreateFields names every field this build's store cannot write.
//
// Dropping them silently is the failure mode worth engineering against: a
// caller that asked for a deadline and got a task without one has lost data it
// believes it stored, and neither the result nor the file says so. Refusing is
// recoverable; silence is not.
//
// This function disappears when store.CreateCommand grows the fields. Adding
// them there and deleting the corresponding line here is the whole change.
func unpersistableCreateFields(command CreateCommand) []string {
	unsupported := []string{}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"scheduled", command.Scheduled},
		{"deadline", command.Deadline},
		{"recur", command.Recurrence},
		{"lead", command.Lead},
	} {
		if field.value != "" {
			unsupported = append(unsupported, fmt.Sprintf(
				"this build cannot persist %s on a new task", field.name))
		}
	}
	return unsupported
}
