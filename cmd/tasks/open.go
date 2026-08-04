package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/links"
)

var digits = regexp.MustCompile(`^\d+$`)

// openCommand opens a task's link in the browser. One link opens straight away;
// several need a pick — a 1-based index or --system — otherwise they are listed
// with a hint. Nothing launches unbidden: guessing which of five URLs you meant
// is the one mistake this command cannot take back. `--print` prints instead of
// launching.
//
// `--json` reports which link this resolved to (and whether it was launched),
// because "which URL did that command act on" is a result an agent has to be
// able to read back. Launching is still a side effect of the COMMAND, not of
// the output format: --json without --print opens the browser exactly as the
// human form does.
func (s *surfaceContext) openCommand(args []string) int {
	print, asJSON := false, false
	system := ""
	rest := []string{}
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; argument {
		case "--print", "-p":
			print = true
		case "--json":
			asJSON = true
		case "--system", "-s":
			value, consumed, status := takeSystemValue(args, index)
			if status != 0 {
				return status
			}
			system, index = value, consumed
		default:
			if strings.HasPrefix(argument, "-") {
				return abort("unknown flag: " + argument + " (open accepts: --print, --system, --json)")
			}
			rest = append(rest, argument)
		}
	}

	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks open <ref> [n] [--system <name>] [--print] [--json]")
	}
	ref := rest[0]
	pick, hasPick := 0, false
	if len(rest) > 1 {
		if !digits.MatchString(rest[1]) {
			return abort("expected a link number, got: " + rest[1])
		}
		hasPick = true
		pick, _ = strconv.Atoi(rest[1])
	}

	queries, status := s.readQueries(args, "open")
	if status != 0 {
		return status
	}
	item, refStatus := resolveRef(queries, ref, refScope{includeDone: true})
	if refStatus != 0 {
		return refStatus
	}
	found := queries.Links(item)
	if system != "" {
		filtered := []links.Link{}
		for _, link := range found {
			if link.System == system {
				filtered = append(filtered, link)
			}
		}
		found = filtered
	}
	if len(found) == 0 {
		message := "no links on: " + item.Title
		if system != "" {
			message += fmt.Sprintf(" (system %s)", system)
		}
		if asJSON {
			out(openErrorDocument("not_found", message, item.ID, item.Line, system, nil))
		}
		return abort(message)
	}

	var link links.Link
	switch {
	case hasPick:
		// 1-based; 0 would negative-index into the list's tail.
		if pick < 1 || pick > len(found) {
			message := fmt.Sprintf("no link #%d (task has %d)", pick, len(found))
			if asJSON {
				out(openErrorDocument("not_found", message, item.ID, item.Line, "", found))
			}
			return abort(message)
		}
		link = found[pick-1]
	case len(found) == 1:
		link = found[0]
	default:
		// Echo the --system filter in the hint: the numbers below index the
		// FILTERED list, so the re-run must filter the same way.
		filter := ""
		if system != "" {
			filter = " --system " + system
		}
		message := fmt.Sprintf("%d links — pick one: tasks open %s%s <n>",
			len(found), rubyInspectQuote(ref), filter)
		if asJSON {
			// The envelope already carries every candidate as DATA. Reprinting
			// the numbered list underneath it would only give a --json caller a
			// second, prose-shaped copy of what it just parsed.
			out(openErrorDocument("ambiguous", message, item.ID, item.Line, "", found))
			fmt.Fprintln(os.Stderr, message)
			return 1
		}
		fmt.Fprintln(os.Stderr, message)
		width := 0
		for _, candidate := range found {
			if length := len([]rune(candidate.System)); length > width {
				width = length
			}
		}
		for index, candidate := range found {
			system := candidate.System
			if pad := width - len([]rune(system)); pad > 0 {
				system += strings.Repeat(" ", pad)
			}
			fmt.Fprintf(os.Stderr, "  %d. %s %s\n", index+1, cyan(system), candidate.URL)
		}
		return 1
	}

	opened := false
	if !print {
		if !openURL(link.URL) {
			message := "no browser launcher found (set TASKS_OPENER)"
			if asJSON {
				w := jsonWriter()
				w.BeginObject()
				w.KeyStr("id", item.ID)
				w.KeyInt("line", item.Line)
				writeLinkMembers(w, link)
				w.KeyStr("error", "unavailable")
				w.KeyStr("action", "open")
				w.KeyStr("message", message)
				w.EndObject()
				out(w.String())
			}
			return abort(message)
		}
		opened = true
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStrOrNull("id", item.ID)
		w.KeyInt("line", item.Line)
		w.KeyStr("title", item.Title)
		writeLinkMembers(w, link)
		w.KeyBool("opened", opened)
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	if opened {
		out("opened " + link.URL)
		return 0
	}
	out(link.URL)
	return 0
}

// openErrorDocument is the CLI's machine-readable refusal for `open`. The extra
// members come FIRST so a payload key can never shadow the envelope's own
// discriminators, which is exactly how Ruby splats them.
func openErrorDocument(code, message, id string, line int, system string, candidates []links.Link) string {
	w := jsonWriter()
	w.BeginObject()
	w.KeyStrOrNull("id", id)
	w.KeyInt("line", line)
	if candidates != nil {
		w.Key("links")
		w.BeginArray()
		for _, link := range candidates {
			w.BeginObject()
			writeLinkMembers(w, link)
			w.EndObject()
		}
		w.EndArray()
	} else {
		w.KeyStrOrNull("system", system)
	}
	w.KeyStr("error", code)
	w.KeyStr("action", "open")
	w.KeyStr("message", message)
	w.EndObject()
	return w.String()
}

// openerCommand is the launcher argv (the URL is appended by openURL).
// TASKS_OPENER overrides the platform launcher — it is also how a test observes
// an open without a browser — and it is shell-split, so
// `TASKS_OPENER="open -a Safari"` works.
func openerCommand() []string {
	if override := env.Get("TASKS_OPENER"); override != "" {
		return shellSplit(override)
	}
	if runtime.GOOS == "darwin" {
		return []string{"open"}
	}
	return []string{"xdg-open"}
}

// openURL launches detached and reports whether the launcher could be spawned.
// Output is discarded — a TUI must not have a browser's stderr scribbled over
// it — and any spawn failure (missing launcher, not executable, …) is a false
// rather than a crash: a bad TASKS_OPENER must not take the caller down.
func openURL(target string) bool {
	argv := openerCommand()
	if len(argv) == 0 {
		return false
	}
	command := exec.Command(argv[0], append(append([]string{}, argv[1:]...), target)...)
	command.Stdout, command.Stderr, command.Stdin = nil, nil, nil
	if command.Start() != nil {
		return false
	}
	// Detach: the launcher outlives this process, and reaping it is not this
	// command's job.
	go func() { _ = command.Wait() }()
	return true
}

// shellSplit is Shellwords.split for the launcher override: words separated by
// whitespace, with single quotes, double quotes and backslash escapes honoured.
func shellSplit(value string) []string {
	words := []string{}
	current := strings.Builder{}
	pending := false
	quote := byte(0)
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case quote == '\'':
			if char == '\'' {
				quote = 0
				continue
			}
			current.WriteByte(char)
		case quote == '"':
			if char == '"' {
				quote = 0
				continue
			}
			if char == '\\' && index+1 < len(value) {
				index++
				current.WriteByte(value[index])
				continue
			}
			current.WriteByte(char)
		case char == '\'' || char == '"':
			quote = char
			pending = true
		case char == '\\' && index+1 < len(value):
			index++
			current.WriteByte(value[index])
			pending = true
		case char == ' ' || char == '\t' || char == '\n':
			if current.Len() > 0 || pending {
				words = append(words, current.String())
				current.Reset()
				pending = false
			}
		default:
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 || pending {
		words = append(words, current.String())
	}
	return words
}

func init() {
	register("open", (*surfaceContext).openCommand)
}
