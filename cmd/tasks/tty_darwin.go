package main

import "syscall"

// ttyRequest is the terminal-attribute ioctl this platform answers `tty?` with.
const ttyRequest = syscall.TIOCGETA
