//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func init() {
	enableVirtualTerminalProcessing()
}

// enableVirtualTerminalProcessing enables ANSI escape code support on Windows 10+.
// Without this, color codes and cursor positioning render as literal characters.
func enableVirtualTerminalProcessing() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	// Enable for stdout
	stdout := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	getConsoleMode.Call(uintptr(stdout), uintptr(unsafe.Pointer(&mode)))
	const enableVirtualTerminalProcessingFlag = 0x0004
	setConsoleMode.Call(uintptr(stdout), uintptr(mode|enableVirtualTerminalProcessingFlag))

	// Enable for stderr
	stderr := syscall.Handle(os.Stderr.Fd())
	getConsoleMode.Call(uintptr(stderr), uintptr(unsafe.Pointer(&mode)))
	setConsoleMode.Call(uintptr(stderr), uintptr(mode|enableVirtualTerminalProcessingFlag))
}
