package merge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tasks-go/internal/atomic"
)

const (
	// defaultMarkerSize is Git's own conflict-marker width, and the floor: a
	// narrower fence would be ambiguous inside content that contains markers.
	defaultMarkerSize = 7
	minimumMarkerSize = 7
	// maxMarkerSize bounds what a hand invocation can ask for. Git's %L is a
	// small integer; refusing to allocate gigabytes for a typo is the only
	// difference from Ruby's unbounded Integer#to_i, and it is unreachable
	// through Git.
	maxMarkerSize = 1 << 20
)

// Ruby's \s in a Regexp is [ \t\r\n\f\v]. Go's \s omits the vertical tab, so it
// is spelled out — a label carrying one would otherwise keep it and widen the
// marker line.
var whitespaceRun = regexp.MustCompile(`[ \t\r\n\f\v]+`)

var digitsOnly = regexp.MustCompile(`\A\d+\z`)

// RunDriver is `tasks merge-driver <base> <ours> <theirs> <pathname>
// [marker-size] [ours-label] [theirs-label]` — the Git merge driver.
//
// Git's contract is what shapes the failure path. Git hands the driver three
// TEMP files and copies whatever %A holds back over the working file when the
// driver returns, on failure exactly as on success. Git seeds %A with the ours
// blob and has already checked that same blob into the working file before the
// driver runs. So leaving %A untouched does NOT mean "wrote nothing": it means
// the working file is left as ours' full content, valid JSONL, no markers
// anywhere — a file `tasks check` passes and the reflex `git add tasks.jsonl`
// stages, silently discarding all of theirs. That is the failure this refusal
// path exists to prevent.
//
// So a refusal writes what Git's own text driver would: ours verbatim, theirs
// verbatim, both fenced. The path stays UU with a nonzero exit, `tasks check`
// fails on the marker lines, and neither side has been summarized or dropped.
//
// The rule for anyone changing this: never write a MERGED result to %A before
// it is known valid. Merge builds and validates entirely in memory and only a
// result that passed carries text, so the merged write below is reached only by
// a merge that is already known good.
func RunDriver(args []string, stdout, stderr io.Writer, verbose bool) int {
	if len(args) < 4 || len(args) > 7 {
		fmt.Fprintln(stderr, "usage: tasks merge-driver <base> <ours> <theirs> <pathname> "+
			"[marker-size] [ours-label] [theirs-label]")
		return 2
	}
	basePath, oursPath, theirsPath, pathname := args[0], args[1], args[2], args[3]
	markerSize, oursLabel, theirsLabel := argAt(args, 4), argAt(args, 5), argAt(args, 6)

	baseText, err := readUTF8(basePath)
	if err != nil {
		return driverIOFailure(pathname, err, stderr)
	}
	oursText, err := readUTF8(oursPath)
	if err != nil {
		return driverIOFailure(pathname, err, stderr)
	}
	theirsText, err := readUTF8(theirsPath)
	if err != nil {
		return driverIOFailure(pathname, err, stderr)
	}

	result := Merge(baseText, oursText, theirsText)
	if !result.OK() {
		writeConflict(oursPath, theirsPath, result.Error, markerSize, oursLabel, theirsLabel, stderr)
		appendLog(pathname, result.LogLines(pathname), stderr)
		fmt.Fprintf(stderr, "tasks JSONL merge failed: %s\n", result.Error)
		return 1
	}

	if err := atomic.Write(oursPath, result.Text); err != nil {
		return driverIOFailure(pathname, err, stderr)
	}
	appendLog(pathname, result.LogLines(pathname), stderr)
	if verbose {
		fmt.Fprintf(stdout, "merged %s\n", pathname)
	}
	return 0
}

func argAt(args []string, index int) string {
	if index < len(args) {
		return args[index]
	}
	return ""
}

func driverIOFailure(pathname string, err error, stderr io.Writer) int {
	if pathname != "" {
		appendLog(pathname, []string{
			fmt.Sprintf("merge %s: failed", pathname),
			"  error: " + err.Error(),
		}, stderr)
	}
	fmt.Fprintf(stderr, "tasks JSONL merge failed: %s\n", err.Error())
	return 1
}

// writeConflict leaves the conflicted file Git copies back over the working
// path on a refusal:
//
//	<<<<<<< HEAD (tasks JSONL merge failed: <why>)
//	…ours, byte for byte…
//	=======
//	…theirs, byte for byte…
//	>>>>>>> other-branch
//
// The reason rides on the opening marker line, where Git itself puts a
// free-form label, so it travels with the file rather than only with a terminal
// that has since scrolled away. Everything strictly between the fences is one
// side's original bytes, so either side is recoverable with no un-mangling.
//
// The bytes are copied RAW from the two merge-stage temp files, including a side
// that is not valid JSONL or not valid UTF-8 at all. It writes %A, the driver's
// own temp file — never %P, the working path — so no clean/smudge filter is
// re-applied on the way through.
func writeConflict(oursPath, theirsPath, mergeError, markerSize, oursLabel, theirsLabel string, stderr io.Writer) {
	size := resolvedMarkerSize(markerSize)
	ours := labelOr(oursLabel, "ours")
	theirs := labelOr(theirsLabel, "theirs")
	reason := collapseWhitespace(mergeError)

	oursBytes, err := os.ReadFile(oursPath)
	if err != nil {
		conflictWarning(stderr, err)
		return
	}
	theirsBytes, err := os.ReadFile(theirsPath)
	if err != nil {
		conflictWarning(stderr, err)
		return
	}

	var out []byte
	out = append(out, fmt.Sprintf("%s %s (tasks JSONL merge failed: %s)\n",
		strings.Repeat("<", size), ours, reason)...)
	out = append(out, terminated(oursBytes)...)
	out = append(out, (strings.Repeat("=", size) + "\n")...)
	out = append(out, terminated(theirsBytes)...)
	out = append(out, fmt.Sprintf("%s %s\n", strings.Repeat(">", size), theirs)...)

	// Written in place rather than atomically: %A is the driver's own temp file,
	// and one side may not be valid UTF-8 at all.
	if err := os.WriteFile(oursPath, out, 0o644); err != nil {
		// A refusal that cannot write its markers must still be a refusal:
		// report it and let the caller exit nonzero with the original
		// diagnostic intact.
		conflictWarning(stderr, err)
	}
}

func conflictWarning(stderr io.Writer, err error) {
	fmt.Fprintf(stderr, "tasks JSONL merge warning: could not write conflict markers: %s\n", err.Error())
}

func resolvedMarkerSize(supplied string) int {
	text := strings.TrimSpace(supplied)
	if !digitsOnly.MatchString(text) {
		return defaultMarkerSize
	}
	size, err := strconv.Atoi(text)
	if err != nil || size > maxMarkerSize {
		return maxMarkerSize
	}
	if size < minimumMarkerSize {
		return minimumMarkerSize
	}
	return size
}

func labelOr(label, fallback string) string {
	value := strings.TrimSpace(collapseWhitespace(label))
	if value == "" {
		return fallback
	}
	return value
}

func collapseWhitespace(value string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(value, " "))
}

// terminated adds the newline a side missing its final one would otherwise be
// missing: without it the last line runs into the next marker and stops being
// recoverable. One added byte is the smallest fix, and matches what Git's own
// driver does with an incomplete final line.
func terminated(raw []byte) []byte {
	if len(raw) == 0 || raw[len(raw)-1] == '\n' {
		return raw
	}
	return append(append([]byte{}, raw...), '\n')
}

func readUTF8(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// appendLog appends the decision trail beside the store. It is best-effort: a
// log that cannot be written warns and never fails a merge that succeeded.
func appendLog(pathname string, lines []string, stderr io.Writer) {
	realPath, err := filepath.Abs(pathname)
	if err != nil {
		realPath = pathname
	}
	logPath := filepath.Join(filepath.Dir(realPath), ".tasks-merge.log")
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "tasks JSONL merge warning: could not write audit log: %s\n", err.Error())
		return
	}
	defer file.Close()
	var buffer strings.Builder
	for _, line := range lines {
		buffer.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			buffer.WriteString("\n")
		}
	}
	if _, err := file.WriteString(buffer.String()); err != nil {
		fmt.Fprintf(stderr, "tasks JSONL merge warning: could not write audit log: %s\n", err.Error())
	}
}
