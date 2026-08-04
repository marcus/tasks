package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"tasks-go/internal/application"
	"tasks-go/internal/lead"
	"tasks-go/internal/recur"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// capture appends a new task to the store.
//
// Every gate runs BEFORE a byte is written, and each answers a question the
// write path cannot: is this store a version this build understands, and is it
// valid enough to extend? A store that fails either is refused with the file
// exactly as it was — the same answer Ruby gives and the same answer a caller
// has to be able to rely on.
func (s *surfaceContext) capture(args []string, proposed bool) int {
	action := actionName(proposed)
	if refusal := s.refuseUnsupportedSchema(args, action); refusal != 0 {
		return refusal
	}

	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	command, flags, status := parseCaptureArgs(args, proposed, context, s.dateOrder())
	if status != 0 {
		return status
	}

	// --under nests below an existing task. The convenience ref is resolved
	// HERE and only its durable id crosses into the store, so the write can
	// never be retargeted by a line number that moved.
	var parent store.Item
	if flags.under != "" {
		queries, status := s.readQueries(args, action)
		if status != 0 {
			return status
		}
		item, code := resolveRef(queries, flags.under, refScope{includeProposed: proposed})
		if code != 0 {
			return code
		}
		parent = item
		command.ParentID = item.ID
	}
	app, err := application.New(application.Options{
		Factory:         func() application.Store { return s.writeStore() },
		TemporalContext: func() temporal.Context { return context },
		HostContext:     s.paths.HostContext,
	})
	if err != nil {
		return abort(err.Error())
	}
	command = app.PrepareCreateTask(command)

	// The preview runs BEFORE the preflight, and writes nothing — not even the
	// lock. It renders the same command the create would submit, so what it
	// shows is what would land rather than a second description of it.
	if flags.dryRun {
		if flags.under != "" {
			out(fmt.Sprintf("would %s under %q: %s", action, parent.Title, captureHeadline(command)))
			return 0
		}
		out(fmt.Sprintf("would %s under %s: %s", action, captureDestination(command),
			captureHeadline(command)))
		return 0
	}

	// The preflight is the store's own, taken under the store lock, so the
	// answer describes the bytes on disk at the moment of the attempt.
	if _, ok := s.store.CreatePreflightFailure(); !ok {
		return s.refuseMutation(args, action, "store_invalid",
			captureSummary(args, proposed),
			"task file is already invalid — run `tasks check` (nothing was written)")
	}

	result := app.CreateTask(command, nil)
	if !result.OK() {
		if result.Status == store.MutationTooDeep {
			return abort(fmt.Sprintf("would exceed max depth %d (max_depth config / TASKS_MAX_DEPTH)",
				s.paths.MaxDepth))
		}
		// A parent can disappear after the CLI resolved --under. That is a stale
		// mutation (exit 1), not a fresh ref-resolution miss (exit 2).
		if result.Status == store.MutationNotFound && command.ParentID != "" {
			result.Status = store.MutationStale
		}
		// A field refusal already says what to fix; the section guess is only
		// right when nothing else explains the failure.
		if result.Status == store.MutationInvalid && result.FirstError() != "" {
			return abort(result.FirstError())
		}
		return mutationResultFailed(result.MutationResult, args, action, captureSummary(args, proposed))
	}
	if proposed && !flags.json {
		out(fmt.Sprintf("proposed: %s [%s]", command.Title, firstID(result.TouchedIDs)))
		return 0
	}
	return s.reportTouched(result.MutationResult, result.TouchedIDs, flags.json)
}

type captureFlags struct {
	json   bool
	dryRun bool
	under  string
}

// parseCaptureArgs is cmd_capture's argument scan.
func parseCaptureArgs(args []string, proposed bool, context temporal.Context,
	order temporal.Order) (application.CreateCommand, captureFlags, int) {

	command := application.CreateCommand{}
	flags := captureFlags{}
	usage := captureUsage(proposed)
	contexts := []string{}
	positional := []string{}
	state := ""
	under := ""
	due, scheduled, recurInput, leadInput := "", "", "", ""
	dueGiven, scheduledGiven := false, false
	dueOptions, scheduledOptions := temporalOptions{}, temporalOptions{}
	dueFold, scheduledFold := "", ""

	// need fetches a flag's value or aborts — a forgotten value must never fail
	// silently, because the next positional word would become the value.
	index := 0
	need := func(flag string) (string, bool) {
		index++
		if index >= len(args) {
			return "", false
		}
		return args[index], true
	}
	for ; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--priority", "--pri":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			command.Priority = value
		case "--tag":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			command.Tags = append(command.Tags, value)
		case "--context", "--ctx":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			contexts = append(contexts, value)
		case "--state":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			state = value
		case "--project":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			command.Project = value
		case "--note":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			command.Notes = append(command.Notes, value)
		case "--under":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			under = value
		case "--no-host-context":
			command.SkipHostContext = true
		case "--json":
			flags.json = true
		case "--due", "--deadline":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			due, dueGiven = value, true
		case "--scheduled", "--sched":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			scheduled, scheduledGiven = value, true
		case "--due-timezone":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			dueOptions.timezone, dueOptions.modified = value, true
		case "--scheduled-timezone":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			scheduledOptions.timezone, scheduledOptions.modified = value, true
		case "--due-floating":
			dueOptions.floating, dueOptions.modified = true, true
		case "--scheduled-floating":
			scheduledOptions.floating, scheduledOptions.modified = true, true
		case "--due-fold":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			dueFold, dueOptions.modified = value, true
		case "--scheduled-fold":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			scheduledFold, scheduledOptions.modified = value, true
		case "--recur", "--repeat":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			recurInput = value
		case "--lead":
			value, ok := need(arg)
			if !ok {
				return command, flags, abort("missing value for " + arg)
			}
			leadInput = value
		case "--dry-run":
			flags.dryRun = true
		default:
			if strings.HasPrefix(arg, "--") {
				return command, flags, abort("unknown flag: " + arg)
			}
			positional = append(positional, arg)
		}
	}

	command.Title = joinPositional(positional)
	if command.Title == "" {
		return command, flags, abort(usage)
	}
	// --under (nest under a task) and --project (file under a section) are two
	// different destinations — pick one.
	if under != "" && command.Project != "" {
		return command, flags, abort("can't combine --under and --project\n" + usage)
	}
	flags.under = under
	if proposed && state != "" {
		return command, flags, abort("propose owns state PROPOSED")
	}
	if proposed && recurInput != "" {
		return command, flags, abort("proposals cannot recur")
	}
	if !dueGiven && dueOptions.modified {
		return command, flags, abort("due temporal modifiers require --due")
	}
	if !scheduledGiven && scheduledOptions.modified {
		return command, flags, abort("scheduled temporal modifiers require --scheduled")
	}
	if dueOptions.timezone != "" && dueOptions.floating {
		return command, flags, abort("--due-timezone and --due-floating are mutually exclusive")
	}
	if scheduledOptions.timezone != "" && scheduledOptions.floating {
		return command, flags, abort("--scheduled-timezone and --scheduled-floating are mutually exclusive")
	}
	for _, fold := range []struct{ flag, value string }{
		{"--due-fold", dueFold}, {"--scheduled-fold", scheduledFold},
	} {
		if fold.value != "" && fold.value != "earlier" && fold.value != "later" {
			return command, flags, abort(fold.flag + " must be earlier or later")
		}
	}
	if dueFold == "later" {
		dueOptions.fold = 1
	}
	if scheduledFold == "later" {
		scheduledOptions.fold = 1
	}

	if recurInput != "" {
		parsed := recur.Parse(recurInput, ".+")
		if parsed.Error != "" {
			return command, flags, abort(parsed.Error + "\n" + recurHint)
		}
		if parsed.Canonical == "off" {
			return command, flags,
				abort("a capture cannot start with recurrence cleared — drop --recur")
		}
		command.Recurrence = parsed.Canonical
		// A recurrence needs a date to repeat; default to scheduling it today —
		// unless a lead is also set, where "today" would put the window in the
		// past and hide the schedule's own first occurrence. The store seeds
		// that occurrence instead, so the date is left unset here.
		if !dueGiven && leadInput == "" && !scheduledGiven {
			scheduled, scheduledGiven = "today", true
		}
	}
	if leadInput != "" {
		parsed := lead.Parse(leadInput)
		if parsed.Error != "" {
			return command, flags, abort(parsed.Error + "\n" + leadHint)
		}
		if parsed.IsOff() {
			return command, flags,
				abort("a capture cannot start with its lead time cleared — drop --lead")
		}
		command.Lead = parsed.Canonical
	}

	if dueGiven {
		value, status := parseCaptureTemporal(due, "--due", context, dueOptions, order)
		if status != 0 {
			return command, flags, status
		}
		command.DeadlineValue = &value
	}
	if scheduledGiven {
		value, status := parseCaptureTemporal(scheduled, "--scheduled", context, scheduledOptions, order)
		if status != 0 {
			return command, flags, status
		}
		command.ScheduledValue = &value
	}

	priority := strings.ToUpper(command.Priority)
	if priority == "NONE" || priority == "CLEAR" || priority == "-" {
		priority = ""
	}
	if priority != "" && priority != "A" && priority != "B" && priority != "C" {
		return command, flags, abort("priority must be A, B, or C")
	}
	command.Priority = priority

	// A recurring capture always ends up dated — either from a flag here or from
	// the first occurrence the store seeds for a lead — so it lands processed as
	// TODO either way, rather than as an INBOX item that is already scheduled.
	dated := command.DeadlineValue != nil || command.ScheduledValue != nil || command.Recurrence != ""
	switch {
	case proposed:
		command.State = "PROPOSED"
	case state != "":
		command.State = strings.ToUpper(state)
	case dated:
		command.State = "TODO"
	default:
		command.State = "INBOX"
	}
	if !slices.Contains(allStates, command.State) {
		return command, flags, abort(fmt.Sprintf("unknown state: %s (want one of %s)",
			command.State, strings.Join(allStates, ", ")))
	}
	if command.Recurrence != "" &&
		(command.State == "DONE" || command.State == "CANCELLED" || command.State == "PROPOSED") {
		return command, flags, abort("can't set recurrence on a " + command.State + " task")
	}
	// Two lead refusals live here rather than in the store, and only because the
	// CLI can name the exact FLAGS that caused them. The store refuses the same
	// two shapes in its own words; naming `--due` is worth more to someone who
	// just typed it.
	if command.Lead != "" && command.DeadlineValue == nil && command.ScheduledValue == nil && command.Recurrence == "" {
		return command, flags, abort("a lead time needs a date to hide before — add --due or --scheduled")
	}
	if command.Lead != "" && command.DeadlineValue != nil && command.ScheduledValue != nil {
		return command, flags, abort(leadGateConflictHint(command.Lead))
	}

	// Contexts are tags that start with "@"; list them before plain tags.
	prefixed := make([]string, 0, len(contexts))
	for _, value := range contexts {
		if strings.HasPrefix(value, "@") {
			prefixed = append(prefixed, value)
			continue
		}
		prefixed = append(prefixed, "@"+value)
	}
	command.Tags = append(prefixed, command.Tags...)
	return command, flags, 0
}

// leadGateConflictHint is Rule 3's CLI-side wording, shared with `tasks lead`.
// It fires before a write the store would only reject, so a capture never
// half-lands.
func leadGateConflictHint(span string) string {
	human, _ := lead.Humanize(span)
	return "a lead of " + human + " measures from the deadline, so an " +
		"available-from date would be a second, ignored gate — pick one"
}

// parseCaptureTemporal is `parse_temporal(..., field:)`: the same expression
// parser every dated verb uses, with the FLAG named in an engine error.
//
// Naming it matters here and nowhere else: `capture` can carry two dates at
// once, so "a time is required with --timezone" without a prefix leaves the
// caller unable to tell which of the two it is being told about.
func parseCaptureTemporal(expression, field string, context temporal.Context,
	options temporalOptions, order temporal.Order) (temporal.Value, int) {

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
	return temporal.Value{}, abort(field + ": " + err.Error())
}

// captureHeadline is the preview's rendering of a task that does not exist yet:
// the same headline shape `list` and every mutation report print, built from the
// command rather than from a record.
func captureHeadline(command application.CreateCommand) string {
	headline := command.State + " "
	if command.Priority != "" {
		headline += "[#" + command.Priority + "] "
	}
	headline += command.Title
	if len(command.Tags) > 0 {
		headline += " :" + strings.Join(command.Tags, ":") + ":"
	}
	if command.Recurrence != "" {
		headline += " (recur " + command.Recurrence + ")"
	}
	if command.Lead != "" {
		headline += " (lead " + command.Lead + ")"
	}
	return headline
}

// captureDestination is where the preview says the task would land. It is the
// requested section NAME rather than a resolved one, because resolution happens
// under the write lock and a preview does not take it.
func captureDestination(command application.CreateCommand) string {
	if command.Project != "" {
		return command.Project
	}
	return "Inbox"
}

// allStates is Check::STATES in Ruby's own order, which the usage sentence
// quotes back verbatim.
var allStates = []string{"PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"}

func captureUsage(proposed bool) string {
	if proposed {
		return proposeUsage
	}
	return captureUsageText
}

func actionName(proposed bool) string {
	if proposed {
		return "propose"
	}
	return "capture"
}

func firstID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// captureSummary is the first line of a capture refusal: what was attempted,
// with the section guess that is right far more often than not.
func captureSummary(args []string, proposed bool) string {
	section := "Inbox"
	for index, arg := range args {
		if arg == "--project" && index+1 < len(args) {
			section = args[index+1]
		}
	}
	return fmt.Sprintf("could not %s (no %s section found?)", actionName(proposed), rubyInspectQuote(section))
}

// refuseUnsupportedSchema is the gate applied once for the whole CLI rather
// than per command. A store at a schema version this build does not implement
// is refused before it is touched — on read exactly as on write. There is no
// conversion in either direction: this is a refusal, not an invitation.
func (s *surfaceContext) refuseUnsupportedSchema(args []string, action string) int {
	detail := s.store.UnsupportedSchemaError()
	if detail == "" {
		return 0
	}
	message := unsupportedSchemaMessage(detail)
	if slices.Contains(args, "--json") {
		// No `rolled_back` member here, and that is not an oversight: this gate
		// fires BEFORE anything is attempted, on reads as on writes, so there is
		// no write to have been rolled back. The field belongs to the mutation
		// envelope, where false genuinely means "refused before writing".
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("error", "unsupported_schema_version")
		w.KeyStr("action", action)
		w.KeyStr("message", message)
		w.EndObject()
		out(w.String())
	}
	fmt.Fprintln(os.Stderr, message)
	return 1
}

// refuseMutation is the CLI's machine-readable failure envelope alongside the
// human sentence. Under --json the refusal is a document on stdout, not only
// prose on stderr, because a caller that got nothing on stdout cannot tell a
// refusal apart from an empty result.
//
// `rolled_back` is the load-bearing field: a mutation that wrote and then
// reverted and one that was refused before it wrote leave byte-identical files
// behind and exit the same way. The boolean is the only thing that tells them
// apart, so it is stated rather than implied by the wording.
func (s *surfaceContext) refuseMutation(args []string, action, code, summary, detail string) int {
	message := summary + "\n" + detail
	if slices.Contains(args, "--json") {
		out(errorDocument(code, action, message, false))
	}
	fmt.Fprintln(os.Stderr, message)
	return 1
}

func errorDocument(code, action, message string, rolledBack bool) string {
	w := jsonWriter()
	w.BeginObject()
	w.KeyBool("rolled_back", rolledBack)
	w.KeyStr("error", code)
	w.KeyStr("action", action)
	w.KeyStr("message", message)
	w.EndObject()
	return w.String()
}

// rubyInspectQuote is String#inspect for the section names a refusal quotes.
func rubyInspectQuote(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"':
			quoted.WriteString(`\"`)
		case '\\':
			quoted.WriteString(`\\`)
		case '\n':
			quoted.WriteString(`\n`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			quoted.WriteRune(char)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

// The two usage sentences, byte for byte as bin/tasks spells them: they are
// stderr contract, and a caller reads them back to correct its own invocation.
const captureUsageText = `usage: tasks capture "text" [--due d] [--scheduled d] [--priority A|B|C] [--tag t] [--context @x] [--no-host-context] [--state STATE] [--project "Heading" | --under <ref>] [--note "text"]`

const proposeUsage = `usage: tasks propose "text" [--due d] [--scheduled d] [--lead span] [--priority A|B|C] [--tag t] [--context @x] [--no-host-context] [--project "Heading" | --under <ref>] [--note "rationale"]`

func init() {
	register("capture", func(s *surfaceContext, args []string) int {
		return s.capture(args, false)
	})
	register("propose", func(s *surfaceContext, args []string) int {
		return s.capture(args, true)
	})
}
