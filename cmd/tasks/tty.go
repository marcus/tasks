package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal answers Ruby's IO#tty? the way Ruby answers it: by asking the
// kernel about the descriptor, not by inspecting TERM. The harness redirects
// every stream to a file, so this is false for all three there — which is what
// makes the colour helpers the identity function under observation.
func isTerminal(file *os.File) bool {
	var termios [64]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(),
		uintptr(ttyRequest), uintptr(unsafe.Pointer(&termios[0])))
	return errno == 0
}
