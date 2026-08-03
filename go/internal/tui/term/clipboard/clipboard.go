// Package clipboard writes to the system clipboard via whatever tool the
// platform has, with no third-party dependency.
//
// Go port of Ruby's lib/tui/clipboard.rb.
package clipboard

import (
	"os/exec"
	"strings"
	"sync"
)

// Commands are tried in order; the first one on PATH wins.
var Commands = [][]string{
	{"pbcopy"},                           // macOS
	{"wl-copy"},                          // Wayland
	{"xclip", "-selection", "clipboard"}, // X11
	{"xsel", "--clipboard", "--input"},
}

var (
	once     sync.Once
	detected []string
)

// Command returns the clipboard command available on this machine, or nil.
// The lookup is performed once per process, as in Ruby.
func Command() []string {
	once.Do(func() {
		for _, candidate := range Commands {
			if _, err := exec.LookPath(candidate[0]); err == nil {
				detected = candidate
				return
			}
		}
	})
	return detected
}

// Copy writes text to the clipboard and reports success. cmd is injectable for
// tests; a nil cmd uses the detected command, and no command at all is a
// non-fatal false.
func Copy(text string, cmd []string) bool {
	if cmd == nil {
		cmd = Command()
	}
	if len(cmd) == 0 {
		return false
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader(text)
	return c.Run() == nil
}
