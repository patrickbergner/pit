package ui

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode               = kernel32.NewProc("SetConsoleMode")
	procGetFileInformationByHandleEx = kernel32.NewProc("GetFileInformationByHandleEx")
)

const enableVirtualTerminalProcessing = 0x0004

// consoles whose mode ansiTerminal changed, with the mode to restore
var changed = map[syscall.Handle]uint32{}

// ansiTerminal reports whether f is a terminal that understands ANSI escapes: a console, in which it switches them on, or the pty of a
// Cygwin or MSYS2 terminal such as Git Bash's mintty, which is a named pipe.
func ansiTerminal(f *os.File) bool {
	h := syscall.Handle(f.Fd())
	var mode uint32
	if syscall.GetConsoleMode(h, &mode) != nil {
		return cygwinPty(h)
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	if ok, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing)); ok == 0 {
		return false // a console from before Windows 10
	}
	if _, seen := changed[h]; !seen {
		changed[h] = mode
	}
	return true
}

func resetTerminal() {
	for h, mode := range changed {
		procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	}
}

// cygwinPty reports whether h is the pipe of a Cygwin or MSYS2 pty, named like \msys-1888ae32e00d56aa-pty0-to-master.
func cygwinPty(h syscall.Handle) bool {
	if t, err := syscall.GetFileType(h); err != nil || t != syscall.FILE_TYPE_PIPE {
		return false
	}
	const fileNameInfo = 2 // FILE_INFO_BY_HANDLE_CLASS
	var info struct {      // FILE_NAME_INFO
		Length uint32 // in bytes
		Name   [syscall.MAX_PATH]uint16
	}
	if ok, _, _ := procGetFileInformationByHandleEx.Call(uintptr(h), fileNameInfo, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		return false
	}
	name := syscall.UTF16ToString(info.Name[:min(int(info.Length/2), len(info.Name))])
	parts := strings.Split(name, "-")
	return len(parts) >= 5 && (parts[0] == `\msys` || parts[0] == `\cygwin`) && parts[1] != "" && strings.HasPrefix(parts[2], "pty") &&
		(parts[3] == "from" || parts[3] == "to") && parts[4] == "master"
}
