package application

import (
	"strings"

	"tasks-go/internal/store"
)

// Project mutations mapped to the shared outcome vocabulary, so a CLI and a
// transport render one outcome set.
//
// These are the only application operations that run SEVERAL store calls, and
// that shape is the whole reason they live here rather than in the store. Two
// consequences follow, and both are implemented below:
//
//   - they gate on the checked read FIRST, so a store at another schema version
//     is refused before any of them runs, with the same diagnostic a
//     single-transaction mutation produces; and
//   - they report failure through a boolean or a count, so the store's recorded
//     rollback is the ONLY evidence a mutation wrote and reverted.

// CreateProject creates a new empty project: a section under the top-level
// "Projects" root.
//
// When no root exists yet — an empty or rootless store — it is created first,
// top-level and appended at the end, then the project beneath it, so an agent
// is never stranded. A blank title is refused, and so is a title that
// duplicates an existing project or area: those names are the project-ref
// candidate set, so a duplicate would make later refs ambiguous.
func (a *Application) CreateProject(title string, operation *OperationContext) Outcome {
	title = strings.TrimSpace(title)
	if title == "" {
		return invalid("title cannot be blank")
	}
	target := a.store()
	if refusal := unsupportedSchemaRefusal(target); refusal != nil {
		return *refusal
	}
	writer, ok := target.(ProjectWriter)
	if !ok {
		return unsupported("create a project")
	}
	existing, err := a.ListProjects(operation)
	if err != nil {
		return Outcome{MutationResult: store.MutationResult{
			Status: store.MutationUnavailable, Errors: []string{"task store unavailable"},
		}}
	}
	for _, view := range existing {
		if strings.EqualFold(strings.TrimSpace(view.Title), title) {
			return invalid("a project or area named " + inspect(title) + " already exists")
		}
	}
	return Outcome{MutationResult: writer.CreateProject(title)}
}

// RenameProject renames a project or area section. A blank title is invalid; a
// missing section is not_found.
func (a *Application) RenameProject(id, title string, _ *OperationContext) Outcome {
	title = strings.TrimSpace(title)
	if title == "" {
		return invalid("title cannot be blank")
	}
	target := a.store()
	if refusal := unsupportedSchemaRefusal(target); refusal != nil {
		return *refusal
	}
	writer, ok := target.(ProjectWriter)
	if !ok {
		return unsupported("rename a project")
	}
	touched, found := writer.RenameSection(id, title)
	if found {
		return Outcome{MutationResult: store.MutationResult{
			Status: store.MutationOK, TouchedIDs: []string{touched},
		}}
	}
	if rollback := rollbackOutcome(target); rollback != nil {
		return *rollback
	}
	return Outcome{MutationResult: store.MutationResult{Status: store.MutationNotFound}}
}

// CompleteProject closes a project's open tasks and reports how many.
//
// Zero closed is a CLEAN result for a project that was already fully closed —
// and the same zero the store returns after a rollback. Only the latter records
// a rollback, so asking for one is what keeps a reverted write from
// masquerading as a no-op success, with its stage intact so a failed write is
// not reported as a validation failure.
func (a *Application) CompleteProject(id string, operation *OperationContext) Outcome {
	target := a.store()
	if refusal := unsupportedSchemaRefusal(target); refusal != nil {
		return *refusal
	}
	writer, ok := target.(ProjectWriter)
	if !ok {
		return unsupported("complete a project")
	}
	closed, found := writer.CompleteProject(id, a.today(operation))
	if !found {
		return Outcome{MutationResult: store.MutationResult{Status: store.MutationNotFound}}
	}
	if closed == 0 {
		if rollback := rollbackOutcome(target); rollback != nil {
			return *rollback
		}
	}
	return Outcome{
		MutationResult: store.MutationResult{Status: store.MutationOK},
		Project:        &ProjectSummary{Closed: closed},
	}
}

// ArchiveProject sweeps a project's subtree into the archive and reports the
// moved stable ids. Undecided proposals block the sweep: a proposal archived
// without a decision is a decision nobody made.
func (a *Application) ArchiveProject(id string, operation *OperationContext) Outcome {
	target := a.store()
	if refusal := unsupportedSchemaRefusal(target); refusal != nil {
		return *refusal
	}
	writer, ok := target.(ProjectWriter)
	if !ok {
		return unsupported("archive a project")
	}
	moved, proposedDescendants, found := writer.ArchiveProject(id, a.today(operation))
	if proposedDescendants {
		return Outcome{MutationResult: store.MutationResult{
			Status: store.MutationConflict,
			Errors: []string{"decide proposed tasks before archiving the project"},
		}}
	}
	if !found {
		if rollback := rollbackOutcome(target); rollback != nil {
			return *rollback
		}
		return Outcome{MutationResult: store.MutationResult{Status: store.MutationNotFound}}
	}
	return Outcome{
		MutationResult: store.MutationResult{Status: store.MutationOK, TouchedIDs: moved},
		Project:        &ProjectSummary{Archived: len(moved)},
	}
}

// inspect is Ruby's String#inspect for the one use this package has: quoting a
// title back to the user inside a refusal.
func inspect(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
