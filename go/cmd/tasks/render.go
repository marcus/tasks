package main

import (
	"fmt"
	"os"
	"strings"
)

// stdoutIsTTY is bin/tasks' `$stdout.tty?`, evaluated once. Colour's real
// switch is the terminal, not an environment variable: every colour helper
// below is the identity function when stdout is redirected, which is why a
// captured observation never contains an escape sequence.
var stdoutIsTTY = isTerminal(os.Stdout)

func colorize(text string, code int) string {
	if !stdoutIsTTY {
		return text
	}
	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", code, text)
}

func bold(text string) string   { return colorize(text, 1) }
func dim(text string) string    { return colorize(text, 90) }
func red(text string) string    { return colorize(text, 31) }
func yellow(text string) string { return colorize(text, 33) }

// dueColor grades a deadline by how close it is: red = overdue or today,
// yellow = the next two days, cyan = this week, dim = further out.
func dueColor(days int) int {
	switch {
	case days <= 0:
		return 31
	case days <= 2:
		return 33
	case days <= 7:
		return 36
	default:
		return 90
	}
}

// abort is Ruby's Kernel#abort: the message on stderr, exit status 1.
func abort(message string) int {
	fmt.Fprintln(os.Stderr, message)
	return 1
}

// pluralize is the `#{n} #{noun}#{n == 1 ? "" : "s"}` idiom the check report
// uses twice.
func pluralize(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// out writes one line to stdout, matching Kernel#puts: a string that already
// ends in a newline does not get a second one.
func out(line string) {
	if strings.HasSuffix(line, "\n") {
		fmt.Print(line)
		return
	}
	fmt.Println(line)
}
