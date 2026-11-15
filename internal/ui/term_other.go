//go:build !windows

package ui

import "os"

// ansiTerminal reports whether f is a terminal; terminals here understand ANSI escapes.
func ansiTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func resetTerminal() {}
