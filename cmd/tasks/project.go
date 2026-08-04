package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

const projectUsage = "usage: tasks project <create|show|complete|archive|rename> <title|ref> [...]"

// projectSubcommands is CliCommands.subcommands_of("project"): every accepted
// sub-verb spelling mapped to its canonical command.
//
// The sub-verbs go through a table for the same reason the top-level commands
// do — `project` cannot grow a verb nobody stated — and the aliases read
// "project new", never a bare "new" that would look like a top-level command.
var projectSubcommands = map[string]string{
	"create": "project create", "new": "project create",
	"show":     "project show",
	"complete": "project complete", "done": "project complete",
	"archive": "project archive",
	"rename":  "project rename",
}

// project dispatches the five project lifecycle sub-verbs.
//
// It is ONE canonical command whose subcommand is its own argument, which is
// why the registry holds `project` rather than five entries: the bare word is
// what the alias table resolves, and everything after it belongs to the handler.
func (s *surfaceContext) project(args []string) int {
	if len(args) == 0 {
		return abort(projectUsage)
	}
	verb, rest := args[0], args[1:]
	canonical, known := projectSubcommands[verb]
	if !known {
		return abort("unknown project command: " + rubyInspectQuote(verb) + "\n" + projectUsage)
	}
	switch canonical {
	case "project create":
		return s.projectCreate(rest)
	case "project show":
		return s.projectShow(rest)
	case "project rename":
		return s.projectRename(rest)
	case "project complete":
		return s.projectComplete(rest)
	default:
		return s.projectArchive(rest)
	}
}

// projectCreate creates a new empty project section under the top-level
// "Projects" root, bootstrapping that root when it is absent.
func (s *surfaceContext) projectCreate(args []string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json")
	if err != nil {
		return abort(err.Error())
	}
	title := joinPositional(rest)
	if title == "" {
		return abort("usage: tasks project create <title>")
	}
	if status := s.refuseUnsupportedSchema(args, "project create"); status != 0 {
		return status
	}
	if flags["--dry-run"] {
		out(`would create project "` + title + `"`)
		return 0
	}

	result := s.writeStore().CreateProject(title)
	if result.Status == store.MutationInvalid {
		fmt.Fprintln(os.Stderr, defaultText(result.FirstError(), "invalid project title"))
		return result.ExitCode()
	}
	if status := mutationResultFailed(result, args, "project create",
		"failed to create project"); status != 0 {
		return status
	}
	return s.emitProject(args, result.Summary.CreatedID, flags["--json"],
		`created "`+title+`"`)
}

// projectShow renders one project's rollup.
func (s *surfaceContext) projectShow(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks project show <ref> [--json]")
	}
	view, status := s.resolveProject(args, "project show", rest[0])
	if status != 0 {
		return status
	}
	if flags["--json"] {
		w := jsonWriter()
		writeProjectJSON(w, view)
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	out(bold(view.Title) + "  " + dim("["+view.Kind+"]"))
	out("  id:        " + view.ID)
	out("  " + projectSummary(view))
	if view.HasBody && view.Body != "" {
		out("  " + view.Body)
	}
	return 0
}

// projectRename retitles a project or area section.
func (s *surfaceContext) projectRename(args []string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks project rename <ref> <new title>")
	}
	ref := rest[0]
	title := joinPositional(rest[1:])
	if title == "" {
		return abort("usage: tasks project rename <ref> <new title>")
	}
	view, status := s.resolveProject(args, "project rename", ref)
	if status != 0 {
		return status
	}
	if flags["--dry-run"] {
		out(`would rename "` + view.Title + `" → "` + title + `"`)
		return 0
	}

	writer := s.writeStore()
	touched, found := writer.RenameSection(view.ID, title)
	result := store.MutationResult{Status: store.MutationOK, TouchedIDs: []string{touched}}
	if !found {
		result = rollbackResult(writer, store.MutationNotFound)
	}
	if status := mutationResultFailed(result, args, "project rename",
		"failed to rename project"); status != 0 {
		return status
	}
	return s.emitProject(args, touched, flags["--json"],
		`renamed "`+view.Title+`" → "`+title+`"`)
}

// projectComplete closes a project's open descendant tasks.
//
// The touched ids are captured BEFORE the cascade, because afterwards the tasks
// are closed and "which ones did this close" is no longer derivable from the
// file — every open task under the project is now a DONE one.
func (s *surfaceContext) projectComplete(args []string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks project complete <ref>")
	}
	view, status := s.resolveProject(args, "project complete", rest[0])
	if status != 0 {
		return status
	}
	queries, status := s.readQueries(args, "project complete")
	if status != 0 {
		return status
	}
	touched := openDescendantIDs(queries, view.ID)

	if flags["--dry-run"] {
		out(fmt.Sprintf(`would complete "%s": close %s`, view.Title,
			pluralize(len(touched), "open task")))
		return 0
	}

	today, status := s.today()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	closed, found := writer.CompleteProject(view.ID, today)
	result := store.MutationResult{Status: store.MutationOK}
	if !found {
		result = rollbackResult(writer, store.MutationNotFound)
	} else if closed == 0 {
		// Zero closed is a CLEAN result for a project that was already fully
		// closed — and the same zero the store returns after a rollback. Asking
		// for the rollback is what keeps a reverted write from masquerading as a
		// successful no-op.
		if reverted := rollbackResult(writer, store.MutationOK); reverted.RolledBack {
			result = reverted
		}
	}
	if status := mutationResultFailed(result, args, "project complete",
		"failed to complete project"); status != 0 {
		return status
	}
	if !flags["--json"] {
		out(fmt.Sprintf(`completed "%s" (closed %d)`, view.Title, closed))
	}
	fresh, status := s.readQueries(args, "project complete")
	if status != 0 {
		return status
	}
	return s.reportTouchedSnapshot(fresh.Snapshot(), touched, flags["--json"], nil)
}

// projectArchive sweeps a project's subtree into the archive.
//
// Deferred and held tasks are still open work, so they block the sweep too —
// parity with `complete`'s cascade, which closes them. --force is the override.
func (s *surfaceContext) projectArchive(args []string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json", "--force")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks project archive <ref> [--force]")
	}
	view, status := s.resolveProject(args, "project archive", rest[0])
	if status != 0 {
		return status
	}

	blocking := view.OpenCount + view.HeldCount
	if blocking > 0 && !flags["--force"] {
		held := ""
		if view.HeldCount > 0 {
			held = fmt.Sprintf(" (%d deferred)", view.HeldCount)
		}
		fmt.Fprintf(os.Stderr, "refusing to archive %q: %s%s remain. "+
			"Complete them or re-run with --force.\n",
			view.Title, pluralize(blocking, "open task"), held)
		return 1
	}
	if flags["--dry-run"] {
		out(`would archive "` + view.Title + `" and its subtree to archive.jsonl`)
		return 0
	}

	today, status := s.today()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	moved, proposed, found := writer.ArchiveProject(view.ID, today)
	if proposed {
		return abort("decide proposed tasks before archiving the project")
	}
	result := store.MutationResult{Status: store.MutationOK, TouchedIDs: moved}
	if !found {
		result = rollbackResult(writer, store.MutationNotFound)
	}
	if status := mutationResultFailed(result, args, "project archive",
		"failed to archive project"); status != 0 {
		return status
	}

	if flags["--json"] {
		w := jsonWriter()
		w.BeginObject()
		w.KeyInt("archived", len(moved))
		w.Key("moved_ids")
		w.Strings(moved)
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	out(fmt.Sprintf(`archived "%s" — %s moved to archive.jsonl`,
		view.Title, pluralize(len(moved), "record")))
	return 0
}

// rollbackResult turns the store's recorded rollback into a typed result.
//
// The project lifecycle calls report failure through a bare boolean or count,
// so the recorded rollback is the ONLY evidence that a mutation wrote and
// reverted. Without it a rolled-back write is indistinguishable from a project
// that simply was not found.
func rollbackResult(writer *store.Store, fallback store.MutationStatus) store.MutationResult {
	reason, stage := writer.LastRollback()
	if reason == "" {
		return store.MutationResult{Status: fallback}
	}
	status := store.MutationStoreInvalid
	if stage == store.RollbackWrite {
		status = store.MutationUnavailable
	}
	return store.MutationResult{
		Status: status, Errors: []string{reason},
		RolledBack: true, RollbackStage: stage,
	}
}

// emitProject re-reads and renders the now-current project.
//
// The project mutations return only ids and a summary, so the fresh view comes
// from a follow-up read rather than from the write — which also means it
// describes the file as it stands, not as the write intended it.
func (s *surfaceContext) emitProject(args []string, id string, asJSON bool, human string) int {
	queries, status := s.readQueries(args, "project")
	if status != 0 {
		return status
	}
	var view taskquery.ProjectView
	found := false
	if id != "" {
		for _, candidate := range queries.Projects() {
			if candidate.ID == id {
				view, found = candidate, true
				break
			}
		}
	}
	if asJSON {
		w := jsonWriter()
		if !found {
			w.BeginObject()
			w.EndObject()
		} else {
			writeProjectJSON(w, view)
		}
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	out(human)
	if found {
		out("  " + projectRow(view, 0))
	}
	return 0
}

var projectLineRef = regexp.MustCompile(`(?i)\AL(\d+)\z`)

// resolveProject resolves a project <ref> to exactly one view, or exits 2.
//
// The precedence is task-ref precedence one level up: an exact 8-hex section id
// wins, then an L<line> section line, then a case-insensitive title substring
// across projects and areas. The `projects` listing is the candidate set, so an
// empty area is not addressable — which matches what `projects` shows.
func (s *surfaceContext) resolveProject(args []string, action, ref string) (taskquery.ProjectView, int) {
	if strings.TrimSpace(ref) == "" {
		return taskquery.ProjectView{}, abort("missing <ref>")
	}
	queries, status := s.readQueries(args, action)
	if status != 0 {
		return taskquery.ProjectView{}, status
	}
	if checked, err := s.store.CheckedReadSnapshot(); err != nil || !checked.OK() {
		return taskquery.ProjectView{}, abort("task file is invalid — run `tasks check`")
	}
	views := queries.Projects()

	if match := projectLineRef.FindStringSubmatch(strings.TrimSpace(ref)); match != nil {
		line, _ := strconv.Atoi(match[1])
		for _, view := range views {
			if view.Line == line {
				return view, 0
			}
		}
		return taskquery.ProjectView{}, projectRefFailed(args, action, "not_found",
			"no project section on line "+match[1], nil)
	}
	for _, view := range views {
		if view.ID != "" && strings.EqualFold(view.ID, strings.TrimSpace(ref)) {
			return view, 0
		}
	}

	want := strings.ToLower(strings.TrimSpace(ref))
	matches := []taskquery.ProjectView{}
	for _, view := range views {
		if strings.Contains(strings.ToLower(view.Title), want) {
			matches = append(matches, view)
		}
	}
	switch len(matches) {
	case 0:
		return taskquery.ProjectView{}, projectRefFailed(args, action, "not_found", "no match: "+ref, nil)
	case 1:
		return matches[0], 0
	}
	lines := []string{fmt.Sprintf("ambiguous: %s — matches %d projects:", ref, len(matches))}
	for _, view := range matches {
		lines = append(lines, fmt.Sprintf("  L%d: %s", view.Line, view.Title))
	}
	return taskquery.ProjectView{}, projectRefFailed(args, action, "ambiguous",
		strings.Join(lines, "\n"), matches)
}

// refFailed is the exit-2 refusal for a ref that resolved to nothing or to too
// much. Under --json the candidates are DATA, so a caller narrows its next
// attempt without parsing the display text it would otherwise have to read.
func projectRefFailed(args []string, action, code, message string, candidates []taskquery.ProjectView) int {
	if jsonRequested(args) {
		w := jsonWriter()
		w.BeginObject()
		w.Key("candidates")
		w.BeginArray()
		for _, view := range candidates {
			w.BeginObject()
			w.KeyStr("id", view.ID)
			w.KeyInt("line", view.Line)
			w.KeyStr("kind", view.Kind)
			w.KeyStr("title", view.Title)
			w.EndObject()
		}
		w.EndArray()
		w.KeyStr("error", code)
		w.KeyStr("action", action)
		w.KeyStr("message", message)
		w.EndObject()
		out(w.String())
	}
	fmt.Fprintln(os.Stderr, message)
	return 2
}

// openDescendantIDs is every OPEN task under a section, at any depth — the tasks
// a completion cascade closes. It walks the live parent pointers; order is
// irrelevant because the report re-sorts by line before printing.
func openDescendantIDs(queries *taskquery.Queries, sectionID string) []string {
	byParent := map[string][]string{}
	state := map[string]string{}
	kind := map[string]string{}
	for _, parsed := range queries.Snapshot().LiveRecords() {
		id := parsed.String("id")
		if id == "" {
			continue
		}
		byParent[parsed.String("parent")] = append(byParent[parsed.String("parent")], id)
		state[id] = parsed.String("state")
		kind[id] = parsed.String("type")
	}
	ids := []string{}
	queue := append([]string{}, byParent[sectionID]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = append(queue[1:], byParent[current]...)
		if kind[current] == "task" && contains(taskquery.OpenStates(), state[current]) {
			ids = append(ids, current)
		}
	}
	return ids
}

func init() {
	register("project", (*surfaceContext).project)
}
