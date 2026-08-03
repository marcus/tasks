package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"tasks-go/internal/store"
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

	command, flags, status := parseCaptureArgs(args, proposed)
	if status != 0 {
		return status
	}

	// The preflight is the store's own, taken under the store lock, so the
	// answer describes the bytes on disk at the moment of the attempt.
	if _, ok := s.store.CreatePreflightFailure(); !ok {
		return s.refuseMutation(args, action, "store_invalid",
			captureSummary(args, proposed),
			"task file is already invalid — run `tasks check` (nothing was written)")
	}

	today, status := s.today()
	if status != 0 {
		return status
	}
	result := s.writeStore().CreateTask(command, today)
	if !result.OK() {
		// A field refusal already says what to fix; the section guess is only
		// right when nothing else explains the failure.
		if result.Status == store.MutationInvalid && result.FirstError() != "" {
			return abort(result.FirstError())
		}
		return mutationResultFailed(result, args, action, captureSummary(args, proposed))
	}
	if proposed && !flags.json {
		out(fmt.Sprintf("proposed: %s [%s]", command.Title, firstID(result.TouchedIDs)))
		return 0
	}
	return s.reportTouched(result, result.TouchedIDs, flags.json)
}

type captureFlags struct {
	json bool
}

// parseCaptureArgs is cmd_capture's argument scan. The flags this build cannot
// yet honor are recognized and REFUSED rather than ignored, so a caller never
// gets a record that silently disagrees with what it asked for.
func parseCaptureArgs(args []string, proposed bool) (store.CreateCommand, captureFlags, int) {
	command := store.CreateCommand{}
	flags := captureFlags{}
	usage := captureUsage(proposed)
	contexts := []string{}
	positional := []string{}
	state := ""
	under := ""

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
			// Host context is not applied by this build at all, so opting out
			// of it is already the behaviour.
		case "--json":
			flags.json = true
		case "--due", "--deadline", "--scheduled", "--sched", "--due-timezone",
			"--scheduled-timezone", "--due-floating", "--scheduled-floating",
			"--due-fold", "--scheduled-fold", "--recur", "--repeat", "--lead":
			return command, flags, notPorted(strings.TrimPrefix(arg, "--"))
		case "--dry-run":
			return command, flags, notPorted("capture --dry-run")
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
	if under != "" {
		return command, flags, notPorted("capture --under")
	}
	if proposed && state != "" {
		return command, flags, abort("propose owns state PROPOSED")
	}

	priority := strings.ToUpper(command.Priority)
	if priority == "NONE" || priority == "CLEAR" || priority == "-" {
		priority = ""
	}
	if priority != "" && priority != "A" && priority != "B" && priority != "C" {
		return command, flags, abort("priority must be A, B, or C")
	}
	command.Priority = priority

	if proposed {
		command.State = "PROPOSED"
	} else if state != "" {
		command.State = strings.ToUpper(state)
	} else {
		command.State = "INBOX"
	}
	if !slices.Contains(allStates, command.State) {
		return command, flags, abort(fmt.Sprintf("unknown state: %s (want one of %s)",
			command.State, strings.Join(allStates, ", ")))
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
		out(errorDocument("unsupported_schema_version", action, message, false))
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
