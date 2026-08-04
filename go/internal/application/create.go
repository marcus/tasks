package application

import (
	"fmt"
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
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
	dates, messages := createTemporalValues(prepared, a.today(operation))
	if len(messages) > 0 {
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
		Title:        prepared.Title,
		Priority:     prepared.Priority,
		Tags:         copyOf(prepared.Tags),
		State:        prepared.State,
		Project:      prepared.Project,
		ParentID:     prepared.ParentID,
		Notes:        copyOf(notes),
		Deferred:     prepared.Deferred,
		Scheduled:    dates.scheduled,
		HasScheduled: dates.hasScheduled,
		Deadline:     dates.deadline,
		HasDeadline:  dates.hasDeadline,
		Recurrence:   prepared.Recurrence,
		Lead:         prepared.Lead,
	}, a.today(operation))}
}

// createDates is the two dates a create may carry, parsed once.
type createDates struct {
	scheduled    temporal.Value
	hasScheduled bool
	deadline     temporal.Value
	hasDeadline  bool
}

// createTemporalValues is `normalize_create_temporal`: the command carries the
// dates as ISO TEXT because a transport supplies text, and this is the seam that
// turns them into values.
//
// The store owns every RULE about the dates — the recurrence seed, the lead's
// five gates, the dated state default. What is owned here is only "is this a
// date at all", because refusing an unparseable one before the transaction is
// the difference between a named argument error and a generic invalid.
func createTemporalValues(command CreateCommand, today string) (createDates, []string) {
	dates := createDates{}
	messages := []string{}
	for _, field := range []struct {
		name  string
		text  string
		value *temporal.Value
		set   *bool
	}{
		{"scheduled", command.Scheduled, &dates.scheduled, &dates.hasScheduled},
		{"deadline", command.Deadline, &dates.deadline, &dates.hasDeadline},
	} {
		if field.text == "" {
			continue
		}
		date, ok := temporal.ParseDate(field.text)
		if !ok {
			messages = append(messages, fmt.Sprintf("%s must be an ISO date", field.name))
			continue
		}
		*field.value = temporal.Value{Date: date}
		*field.set = true
	}
	_ = today
	return dates, messages
}
