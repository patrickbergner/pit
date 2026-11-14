// Package ui holds pit's terminal conventions: warnings, notes and questions on stderr, per-repo headers and tables on stdout.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Warn reports a problem that doesn't stop the command.
func Warn(format string, a ...any) { fmt.Fprintf(os.Stderr, "pit: warning: "+format+"\n", a...) }

// Note reports something worth knowing on stderr.
func Note(format string, a ...any) { fmt.Fprintf(os.Stderr, "pit: "+format+"\n", a...) }

var stdin = bufio.NewReader(os.Stdin)

// ErrNoAnswer is returned by Ask when stdin is closed before an answer.
var ErrNoAnswer = errors.New("no answer on stdin")

// ErrInterrupted is returned by Ask when cancel is closed while waiting.
var ErrInterrupted = errors.New("interrupted")

// Ask prints prompt on stderr and reads one line from stdin, trimmed and lower-cased. Closing cancel (on Ctrl-C) stops the wait.
func Ask(prompt string, cancel <-chan struct{}) (string, error) {
	fmt.Fprint(os.Stderr, prompt+" ")
	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		s, err := stdin.ReadString('\n')
		ch <- line{s, err}
	}()
	select {
	case l := <-ch:
		if l.err != nil && l.s == "" {
			fmt.Fprintln(os.Stderr)
			return "", ErrNoAnswer
		}
		return strings.ToLower(strings.TrimSpace(l.s)), nil
	case <-cancel:
		fmt.Fprintln(os.Stderr)
		return "", ErrInterrupted
	}
}

// Header introduces the output of one repo.
func Header(name, path string) {
	title := name
	if path != "" && path != name {
		title += " (" + path + ")"
	}
	fmt.Println(Bold("=== " + title))
}

var ansi = func() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 && runtime.GOOS != "windows" && os.Getenv("NO_COLOR") == ""
}()

// Bold emphasizes s when stdout is a terminal that understands ANSI escapes.
func Bold(s string) string {
	if ansi {
		return "\033[1m" + s + "\033[0m"
	}
	return s
}

// Pad right-pads s to width runes.
func Pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// Plural returns "1 repo" / "2 repos".
func Plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
