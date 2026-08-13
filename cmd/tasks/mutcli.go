package main

import (
	"errors"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// The shared argument-and-report vocabulary every mutation verb draws on.
//
// It lives in one file because the alternative is thirteen command files each
// with its own nearly-identical flag scan, and "nearly" is where a port breaks:
// tasks aborts on a valued flag whose value is missing, and a command that
// forgot that turns the NEXT positional into the flag's value and writes it.

// takeFlagValue is tasks' extract_value: pull `--flag <value>` out of the
// argument stream and return the rest.
//
// A missing value is an ABORT, not an absence. `--timezone` at the end of argv,
// or followed by another `--flag`, is a typo, and treating it as "the flag was
// not passed" would silently perform a different write than the one asked for.
func takeFlagValue(args []string, flag string) (string, []string, bool, int) {
	for index, arg := range args {
		if arg != flag {
			continue
		}
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
			return "", args, false, abort("missing value for " + flag)
		}
		rest := append(append([]string{}, args[:index]...), args[index+2:]...)
		return args[index+1], rest, true, 0
	}
	return "", args, false, 0
}

// takeRepeatableValue is extract_repeatable_flag: every occurrence of a valued
// flag, in argv order, with the remainder. `cancel --note` is the one caller.
func takeRepeatableValue(args []string, flag string) ([]string, []string, int) {
	values := []string{}
	rest := []string{}
	for index := 0; index < len(args); index++ {
		if args[index] != flag {
			rest = append(rest, args[index])
			continue
		}
		index++
		if index >= len(args) {
			return nil, nil, abort("missing value for " + flag)
		}
		values = append(values, args[index])
	}
	return values, rest, 0
}

// temporalOptions is what --timezone / --floating / --fold say about how a wall
// time is to be read. `modified` is separate from the three values because
// `defer` refuses the modifiers without a date, and "the user passed --floating"
// is not recoverable from `floating == false`.
type temporalOptions struct {
	timezone string
	floating bool
	fold     int
	modified bool
}

// takeTemporalOptions is extract_temporal_options. It runs BEFORE takeFlags so
// the values it consumes cannot be mistaken for positionals — which is the
// whole reason tasks orders it that way too.
func takeTemporalOptions(args []string) (temporalOptions, []string, int) {
	options := temporalOptions{}
	timezone, rest, _, status := takeFlagValue(args, "--timezone")
	if status != 0 {
		return options, nil, status
	}
	foldName, rest, foldGiven, status := takeFlagValue(rest, "--fold")
	if status != 0 {
		return options, nil, status
	}
	floating := false
	filtered := []string{}
	for _, arg := range rest {
		if arg == "--floating" {
			floating = true
			continue
		}
		filtered = append(filtered, arg)
	}
	if timezone != "" && floating {
		return options, nil, abort("--timezone and --floating are mutually exclusive")
	}
	if foldGiven && foldName != "earlier" && foldName != "later" {
		return options, nil, abort("--fold must be earlier or later")
	}
	options.timezone = timezone
	options.floating = floating
	if foldName == "later" {
		options.fold = 1
	}
	options.modified = timezone != "" || floating || foldGiven
	return options, filtered, 0
}

// parseTemporalArg is tasks' parse_temporal: the friendly expression, or an
// abort naming what could not be read.
//
// The two failure shapes stay distinct because they say different things. "That
// is not a date" quotes the expression back so the user can see what was
// actually parsed; every other error — an impossible local time, an unusable
// zone — carries the engine's own sentence, which names the fix.
func parseTemporalArg(expression string, context temporal.Context, options temporalOptions,
	order temporal.Order) (temporal.Value, int) {

	value, err := temporal.ParseExpression(expression, temporal.ParseOptions{
		Today: context.LocalDate(), Order: order,
		Timezone: options.timezone, Floating: options.floating, Fold: options.fold,
		Context: &context,
	})
	if err == nil {
		return value, 0
	}
	if errors.Is(err, temporal.ErrNotADate) {
		return temporal.Value{}, abort("unrecognized date: " + expression)
	}
	return temporal.Value{}, abort(err.Error())
}

// temporalValueLabel is temporal_label: the value spelled the way every preview
// and every history entry spells it.
func temporalValueLabel(value temporal.Value) string { return temporalLabel(&value) }

// dateOrder is the configured reading of an ambiguous numeric date.
func (s *surfaceContext) dateOrder() temporal.Order {
	return temporal.OrderNamed(s.paths.DateOrder)
}

// -- reporting -----------------------------------------------------------------

// patchBaseline is tasks' `snapshot && patch_expected(snapshot, field)`.
//
// An absent baseline is deliberately NOT a refusal here. `edit_snapshot` returns
// nil for two different situations — the task is gone, and the FILE does not
// validate — and the difference matters to the user: one says "your edit went
// stale", the other says "run `tasks check`". Only the store can tell them
// apart, so the patch is submitted with an empty precondition and the store's
// own status is what gets reported. Refusing here instead would collapse both
// into "stale" and send the user chasing a concurrent edit that never happened.
func patchBaseline(writer *store.Store, id string, field store.PatchField) string {
	expected, _ := writer.ExpectedFor(id, field)
	return expected
}

// settlePatch maps the store's not-found onto the CLI's stale.
//
// A ref that did not resolve already exited 2 before any of this; a task that
// vanished BETWEEN resolution and the write is a different event, and Ruby's
// `patch_task_by_id` performs exactly this translation for the same reason.
func settlePatch(result store.MutationResult) store.MutationResult {
	if result.Status == store.MutationNotFound {
		result.Status = store.MutationStale
	}
	return result
}

// editable is `edit_snapshot(id) != nil`: whether there is a readable editor
// snapshot to build a CHANGESET from at all.
//
// The changeset path refuses where the patch path submits, and that asymmetry is
// Ruby's: a changeset is guarded by a whole-task revision taken from that
// snapshot, so without one there is no precondition to send and nothing to
// submit. The refusal is stale and carries no `tasks check` hint, because
// nothing was attempted.
func editable(writer *store.Store, id string) bool {
	_, found := writer.ExpectedFor(id, store.FieldTitle)
	return found
}

// patchValueAndReport is patchAndReport for a field whose value is not a plain
// string — a date with a wall time, a tag delta, a boolean.
func (s *surfaceContext) patchValueAndReport(args []string, item store.Item, field store.PatchField,
	value store.PatchValue, label, action, summary string, asJSON bool,
	beforeReport ...func(store.MutationResult)) int {

	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	return s.finishPatch(settlePatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: field, Value: value, Expected: patchBaseline(writer, item.ID, field),
		Label: label, Today: today, Context: context,
	})), args, item, action, summary, asJSON, beforeReport...)
}

// patchValueReporting is patchValueAndReport for a command whose success report
// is NOT the touched-task headline. `lead` prints an availability sentence
// instead, because "what does this window do to whether I can work on it" is the
// question the command was asked.
func (s *surfaceContext) patchValueReporting(args []string, item store.Item, field store.PatchField,
	value store.PatchValue, label, action, summary string, asJSON bool,
	report func(store.MutationResult) int) int {

	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	result := settlePatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: field, Value: value, Expected: patchBaseline(writer, item.ID, field),
		Label: label, Today: today, Context: context,
	}))
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return abort(result.FirstError())
	}
	if !result.OK() {
		return mutationResultFailed(result, args, action, summary)
	}
	if result.ReadSnapshot == nil {
		return abort(summary)
	}
	return report(result)
}

// finishPatch is the reporting half, shared with the commands that had to read
// the baseline themselves — `note` builds its new body out of it, and `undate`
// refuses on it before proposing a write at all.
func (s *surfaceContext) finishPatch(result store.MutationResult, args []string, item store.Item,
	action, summary string, asJSON bool, beforeReport ...func(store.MutationResult)) int {

	// A refusal that names a rule — a lead's single timed gate, say — is the
	// only thing that tells the user what to do instead, so it is surfaced
	// verbatim ahead of the command's own generic summary.
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return abort(result.FirstError())
	}
	if !result.OK() {
		return mutationResultFailed(result, args, action, summary)
	}
	for _, hook := range beforeReport {
		hook(result)
	}
	touched := result.TouchedIDs
	if len(touched) == 0 {
		touched = []string{item.ID}
	}
	return s.reportTouched(result, touched, asJSON)
}

// changesetAndReport is the shared tail of the multi-field verbs — `defer`,
// `activate`, `cancel --note`, `recur --on`.
//
// They are changesets rather than two patches for a reason that shows up in the
// file: `defer <ref> <date>` owns BOTH availability fields, so two independent
// patches would expose an intermediate state to a concurrent reader and cost
// the user two undo steps to get back.
func (s *surfaceContext) changesetAndReport(args []string, item store.Item, changes []store.Change,
	label, action, summary string, asJSON bool, report func(store.MutationResult) int) int {

	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	revision, found := writer.TaskRevision(item.ID)
	if !found || !editable(writer, item.ID) {
		return mutationResultFailed(store.MutationResult{Status: store.MutationStale},
			args, action, summary)
	}
	result := writer.ApplyChangeset(store.Changeset{
		ID: item.ID, Changes: changes, ExpectedRevision: revision,
		HistoryLabel: label, Today: today, Context: context,
	})
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return abort(result.FirstError())
	}
	if !result.OK() {
		return mutationResultFailed(result, args, action, summary)
	}
	if result.ReadSnapshot == nil {
		return abort(summary)
	}
	if report != nil {
		return report(result)
	}
	touched := result.TouchedIDs
	if len(touched) == 0 {
		touched = []string{item.ID}
	}
	return s.reportTouched(result, touched, asJSON)
}

// -- availability -----------------------------------------------------------

// printAvailabilityChange is the one line `defer`, `someday`, `activate` and
// `lead` print instead of a headline: the change, then what it means for
// whether the task can be worked on.
func (s *surfaceContext) printAvailabilityChange(snapshot *store.Snapshot, id, ownText string,
	dryRun bool, override taskquery.Override) int {

	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	queries := taskquery.New(snapshot, context)
	item, found := queries.FindLive(id)
	if !found {
		return abort("task changed before availability could be read")
	}
	availability := queries.AvailabilityAfter(item, override)
	prefix := ""
	if dryRun {
		prefix = "would "
	}
	out(prefix + ownText + " — " + availabilitySummary(queries, availability))
	return 0
}

// readSnapshotFor is the plain read a dry-run previews against. A preview never
// takes the write lock, so it reads the file as it stands.
func (s *surfaceContext) readSnapshotFor() (*store.Snapshot, int) {
	snapshot, err := s.store.ReadSnapshot(false)
	if err != nil {
		return nil, abort(store.UnavailableMessage(err))
	}
	return snapshot, 0
}
