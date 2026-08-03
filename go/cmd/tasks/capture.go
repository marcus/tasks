package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"tasks-go/internal/jsonout"
	"tasks-go/internal/store"
)

// capture appends a new task — or, in this build, gets as far as the checks
// that would refuse to and stops.
//
// Those checks are worth having on their own. Every one of them runs BEFORE a
// byte is written, and each answers a question the write path cannot: is this
// store a version this build understands, and is it valid enough to extend? A
// store that fails either is refused with the file exactly as it was, which is
// the same answer Ruby gives and the same answer a caller has to be able to
// rely on.
//
// What is missing is only the last step. A capture that passes every gate ends
// in an explicit refusal rather than a partial record.
func (s *surfaceContext) capture(args []string, proposed bool) int {
	if refusal := s.refuseUnsupportedSchema(args, actionName(proposed)); refusal != 0 {
		return refusal
	}
	// The preflight is the store's own, taken under the store lock, so the
	// answer describes the bytes on disk at the moment of the attempt.
	reason, ok := s.store.CreatePreflightFailure()
	if !ok {
		return s.refuseMutation(args, actionName(proposed), "store_invalid",
			captureSummary(args, proposed),
			"task file is already invalid — run `tasks check` (nothing was written)")
	}
	_ = reason
	return abort(fmt.Sprintf("%s: not implemented in the Go port — the store passed every "+
		"preflight, so the next step would be a write, and this build has no write path",
		actionName(proposed)))
}

func actionName(proposed bool) string {
	if proposed {
		return "propose"
	}
	return "capture"
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
	w := jsonout.New()
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

var _ = store.New
