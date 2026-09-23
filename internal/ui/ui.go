// Package ui holds pit's terminal conventions: progress and warnings on stderr, the single confirmation prompt, and emphasis for the push
// plan on stdout.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Info reports progress ("==> ...") on stderr.
func Info(format string, a ...any) { fmt.Fprintf(os.Stderr, "==> "+format+"\n", a...) }

// Warn reports a problem that doesn't stop the command.
func Warn(format string, a ...any) { fmt.Fprintf(os.Stderr, "pit: warning: "+format+"\n", a...) }

// Confirm asks on stdin unless yes is set. Without an answer to read (no terminal) it fails instead of assuming one.
func Confirm(prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && answer == "" {
		fmt.Fprintln(os.Stderr)
		return false, errors.New("no terminal for confirmation; re-run with --yes")
	}
	answer = strings.TrimSpace(answer)
	return strings.HasPrefix(answer, "y") || strings.HasPrefix(answer, "Y"), nil
}

// Bold emphasizes s when stdout is a terminal that understands ANSI escapes.
func Bold(s string) string {
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 && runtime.GOOS != "windows" {
		return "\033[1m" + s + "\033[0m"
	}
	return s
}
