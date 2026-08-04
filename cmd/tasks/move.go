package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/updatestamp"
)

const moveUsage = `tasks move <ref> ("Section" | --under <ref> | --top | ["Section" | --under <ref>] --before <ref>)`

// move relocates a task's subtree.
//
// Four destinations, and the argument rules differ between them because they
// mean different things. Without `--before` exactly ONE destination is
// required — a section name, `--under`, or `--top` — because an append with no
// destination has nowhere to go. WITH `--before` the anchor can infer its own
// parent, so zero or one explicit destination is accepted and `--top` is not:
// "put it before that task" and "put it at the top level" are contradictory
// instructions.
func (s *surfaceContext) move(args []string) int {
	under, rest, _ := extractValue(args, "--under")
	before, rest, hasBefore := extractValue(rest, "--before")
	flags, rest, err := takeFlags(rest, "--dry-run", "--json", "--include-done", "--top")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: " + moveUsage)
	}
	ref := rest[0]
	section := joinPositional(rest[1:])
	top := flags["--top"]

	destinations := 0
	for _, given := range []bool{section != "", under != "", top} {
		if given {
			destinations++
		}
	}
	if hasBefore {
		if top || destinations > 1 {
			return abort("usage: " + moveUsage)
		}
	} else if destinations != 1 {
		return abort("usage: " + moveUsage)
	}

	queries, status := s.readQueries(args, "move")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	if item.ID == "" {
		return abort("task has no stable id")
	}

	switch {
	case hasBefore:
		return s.movePlacement(args, queries, item, before, under, section, flags)
	case top:
		return s.moveTop(args, queries, item, flags)
	case under != "":
		return s.moveUnder(args, queries, item, under, flags)
	default:
		return s.moveToSection(args, item, section, flags)
	}
}

// movePlacement is the anchored move: land the subtree immediately in front of
// a named sibling.
//
// Every refusal below is checked BEFORE the write, and each names the argument
// that caused it. That matters more here than for the append forms, because the
// caller supplied two ids and a generic "could not move" leaves it unable to
// tell which one was wrong.
func (s *surfaceContext) movePlacement(args []string, queries *taskquery.Queries, item store.Item,
	before, under, section string, flags map[string]bool) int {

	anchor, code := resolveRef(queries, before, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	writer := s.writeStore()
	anchorRecord, found := placementRecord(writer, anchor.ID)
	if !found {
		return abort("anchor task no longer exists")
	}

	var parent record.Record
	missingParent := ""
	switch {
	case under != "":
		parentItem, code := resolveRef(queries, under, refScope{includeDone: flags["--include-done"]})
		if code != 0 {
			return code
		}
		parent, found = placementRecord(writer, parentItem.ID)
		missingParent = `destination task "` + under + `" no longer exists`
	case section != "":
		parent, found = writer.SectionNamed(section)
		missingParent = `could not move (no "` + section + `" section?)`
	default:
		parent, found = placementRecord(writer, anchorRecord.String("parent"))
		missingParent = "anchor has no live task or section parent"
	}
	if !found {
		return abort(missingParent)
	}
	parentID := parent.String("id")

	if !editable(writer, item.ID) {
		return abort("task no longer exists")
	}
	subtree := map[string]bool{}
	for _, moving := range subtreeItems(queries, item) {
		subtree[moving.ID] = true
	}
	switch {
	case anchor.ID == item.ID:
		return abort("can't place a task before itself")
	case subtree[parentID] || subtree[anchor.ID]:
		return abort("can't place a task relative to its own subtree")
	}
	if anchorRecord.String("parent") != parentID {
		return abort(`"` + anchor.Title + `" is not a direct child of ` + placementDestination(parent))
	}

	summary := `move "` + item.Title + `" ` + placementDestination(parent) +
		` before "` + anchor.Title + `"`
	label := "move before " + anchor.Title + ": " + item.Title
	placement := store.PlacementValue(parentID, anchor.ID)

	if flags["--dry-run"] {
		// The preview runs the REAL placement path against an isolated
		// byte-for-byte copy of the store, so depth, cycle, anchor and post-write
		// validation all apply to it. A preview that re-derived those rules would
		// be a second implementation of them, and the two would drift.
		preview, status := s.previewPlacement(item.ID, placement, label)
		if status != 0 {
			return status
		}
		if code := s.placementResultFailed(preview, args, anchor, parent); code != 0 {
			return code
		}
		out("would " + summary)
		out(taskquery.Headline(item))
		return 0
	}

	today, status := s.today()
	if status != 0 {
		return status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	revision, _ := writer.TaskRevision(item.ID)
	result := settlePatch(writer.ApplyChangeset(store.Changeset{
		ID: item.ID, Changes: []store.Change{{Field: store.FieldLocation, Value: placement}},
		ExpectedRevision: revision, HistoryLabel: label, Today: today, Context: context,
	}))
	if code := s.placementResultFailed(result, args, anchor, parent); code != 0 {
		return code
	}

	if flags["--json"] {
		return s.reportTouchedSnapshot(result.ReadSnapshot, []string{item.ID}, true,
			func(w *jsonout.Writer) {
				w.Key("placement")
				w.BeginObject()
				w.KeyStr("parent_id", parentID)
				w.KeyStr("parent_type", parent.String("type"))
				w.KeyStrOrNull("parent_title", parent.String("title"))
				w.KeyStr("before_id", anchor.ID)
				w.KeyStr("before_title", anchor.Title)
				w.EndObject()
			})
	}
	verb := "moved"
	if result.Status == store.MutationNoChange {
		verb = "already placed"
	}
	out(verb + ` "` + item.Title + `" ` + placementDestination(parent) + ` before "` + anchor.Title + `"`)
	return s.reportTouched(result, []string{item.ID}, false)
}

// moveTop unnests a task to its enclosing section.
//
// It is deliberately NOT forced: a task already parented to its section is a
// clean no-op that burns no undo slot, which is the difference between "move
// this" and "make sure it is here".
func (s *surfaceContext) moveTop(args []string, queries *taskquery.Queries, item store.Item,
	flags map[string]bool) int {

	if flags["--dry-run"] {
		out("would unnest to top level: " + taskquery.Headline(item))
		return 0
	}
	writer := s.writeStore()
	sectionID := nearestSectionID(queries, item.ID)
	if sectionID == "" {
		return abort("failed to unnest")
	}
	result := s.patchLocation(writer, item, sectionID, "unnest: "+item.Title, false)
	if status := mutationResultFailed(result, args, "move", "failed to unnest"); status != 0 {
		return status
	}
	if result.Status == store.MutationNoChange {
		out("already at top level")
		out(taskquery.Headline(item))
		return 0
	}
	return s.reportTouched(result, []string{item.ID}, flags["--json"])
}

// moveUnder nests a task below another task. This is the one destination the
// nesting cap applies to, because it is the only one that can deepen a subtree.
func (s *surfaceContext) moveUnder(args []string, queries *taskquery.Queries, item store.Item,
	under string, flags map[string]bool) int {

	parent, code := resolveRef(queries, under, refScope{})
	if code != 0 {
		return code
	}
	if flags["--dry-run"] {
		out(`would nest under "` + parent.Title + `": ` + taskquery.Headline(item))
		return 0
	}
	result := s.patchLocation(s.writeStore(), item, parent.ID,
		"nest under "+parent.Title+": "+item.Title, true)
	if result.Status == store.MutationTooDeep {
		return abort(depthRefusal(s.paths.MaxDepth))
	}
	if result.Status == store.MutationCycle {
		return abort("can't nest a task under its own subtree")
	}
	if status := mutationResultFailed(result, args, "move",
		`could not nest under "`+parent.Title+`"`); status != 0 {
		return status
	}
	return s.reportTouched(result, []string{item.ID}, flags["--json"])
}

// moveToSection files a task directly under a named heading. The name is
// resolved through the store's own widening tiers, so a nested project
// sub-section is reachable by name and not only a top-level heading.
func (s *surfaceContext) moveToSection(args []string, item store.Item, section string,
	flags map[string]bool) int {

	if flags["--dry-run"] {
		out("would move to " + rubyInspectQuote(section) + ": " + taskquery.Headline(item))
		return 0
	}
	writer := s.writeStore()
	target, found := writer.SectionNamed(section)
	if !found {
		return abort(`could not move (no "` + section + `" section?)`)
	}
	result := s.patchLocation(writer, item, target.String("id"),
		"move → "+section+": "+item.Title, true)
	if status := mutationResultFailed(result, args, "move",
		`could not move (no "`+section+`" section?)`); status != 0 {
		return status
	}
	return s.reportTouched(result, []string{item.ID}, flags["--json"])
}

// patchLocation is the shared single-field write behind the three append forms.
func (s *surfaceContext) patchLocation(writer *store.Store, item store.Item, parentID, label string,
	force bool) store.MutationResult {

	today, status := s.today()
	if status != 0 {
		return store.MutationResult{Status: store.MutationUnavailable}
	}
	return settlePatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: store.FieldLocation, Value: store.TextValue(parentID),
		Expected: patchBaseline(writer, item.ID, store.FieldLocation),
		Label:    label, Today: today, Force: force,
	}))
}

// previewPlacement runs the write through the SAME store code against an
// isolated copy of the files, so what the preview reports is what the real
// write would do — including a post-write validation failure.
func (s *surfaceContext) previewPlacement(id string, placement store.PatchValue,
	label string) (store.MutationResult, int) {

	dir, cleanup, err := copyStoreToTemp(s.paths.Org, s.paths.Archive)
	if err != nil {
		return store.MutationResult{}, abort(err.Error())
	}
	defer cleanup()

	today, status := s.today()
	if status != 0 {
		return store.MutationResult{}, status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return store.MutationResult{}, status
	}
	preview := s.previewStore(dir)
	revision, _ := preview.TaskRevision(id)
	return preview.ApplyChangeset(store.Changeset{
		ID: id, Changes: []store.Change{{Field: store.FieldLocation, Value: placement}},
		ExpectedRevision: revision, HistoryLabel: label, Today: today, Context: context,
	}), 0
}

// placementResultFailed maps the placement-specific refusals. Each names the
// thing the caller can act on: the cap, the subtree, or the anchor's real
// parent.
func (s *surfaceContext) placementResultFailed(result store.MutationResult, args []string,
	anchor store.Item, parent record.Record) int {

	switch result.Status {
	case store.MutationTooDeep:
		return abort(depthRefusal(s.paths.MaxDepth))
	case store.MutationCycle:
		return abort("can't place a task relative to its own subtree")
	case store.MutationConflict:
		return abort(`"` + anchor.Title + `" is not a direct child of ` + placementDestination(parent))
	}
	return mutationResultFailed(result, args, "move", "could not place task before anchor")
}

// depthRefusal is the nesting cap's one message, shared by `move --under` and
// the anchored move so a user reads the same fix either way.
func depthRefusal(maxDepth int) string {
	return fmt.Sprintf("would exceed max depth %d (max_depth config / TASKS_MAX_DEPTH)", maxDepth)
}

// copyStoreToTemp makes an isolated byte-for-byte copy of both files.
//
// A dry run must be able to run the REAL write path — that is the only way its
// verdict can be trusted — while leaving the configured files and journal
// untouched. Copying is what makes those two requirements compatible.
func copyStoreToTemp(org, archive string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "tasks-move-preview")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	source, err := os.ReadFile(org)
	if err != nil {
		source = []byte{}
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), source, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if archived, err := os.ReadFile(archive); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "archive.jsonl"), archived, 0o600); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

// previewStore is a writable store over the copied files, with its journal
// inside the same temporary directory. Nothing it does can reach the real
// history.
func (s *surfaceContext) previewStore(dir string) *store.Store {
	options := store.Options{
		JournalDir:    filepath.Join(dir, "journal"),
		Device:        updatestamp.Device(env),
		CoalesceScope: coalesceScope(),
		MaxDepth:      s.paths.MaxDepth,
		UndoLimit:     journal.Limit,
	}
	if clock := determinism.Clock(env); clock != nil {
		options.Now = clock
	}
	return store.NewWriter(filepath.Join(dir, "tasks.jsonl"), filepath.Join(dir, "archive.jsonl"),
		options)
}

// placementDestination is how a destination is named back to the user: a task
// is nested UNDER, a section is filed IN.
func placementDestination(parent record.Record) string {
	kind := "in section"
	if parent.String("type") == "task" {
		kind = "under task"
	}
	return kind + ` "` + parent.String("title") + `"`
}

// placementRecord is a live task or section by id. A record of any other type
// is not a placement destination and must miss rather than be accepted.
func placementRecord(reader *store.Store, id string) (record.Record, bool) {
	if id == "" {
		return record.Record{}, false
	}
	snapshot, err := reader.ReadSnapshot(false)
	if err != nil {
		return record.Record{}, false
	}
	for _, parsed := range snapshot.LiveRecords() {
		if parsed.String("id") != id {
			continue
		}
		if kind := parsed.String("type"); kind == "task" || kind == "section" {
			return parsed, true
		}
	}
	return record.Record{}, false
}

// nearestSectionID walks up to the task's nearest ancestor SECTION. A rootless
// task has no top-level destination, which is the refusal `move --top` reports.
func nearestSectionID(queries *taskquery.Queries, id string) string {
	snapshot := queries.Snapshot()
	byID := map[string]record.Record{}
	for _, parsed := range snapshot.LiveRecords() {
		if key := parsed.String("id"); key != "" {
			byID[key] = parsed
		}
	}
	current, ok := byID[id]
	if !ok {
		return ""
	}
	for {
		parent := current.String("parent")
		if parent == "" {
			return ""
		}
		next, ok := byID[parent]
		if !ok {
			return ""
		}
		if next.String("type") == "section" {
			return next.String("id")
		}
		current = next
	}
}

func init() {
	register("move", (*surfaceContext).move)
}
