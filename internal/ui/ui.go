// Package ui holds pit's terminal conventions: notes, warnings, errors and questions on stderr; per-repo blocks and tables on stdout; and
// their light coloring, which a stream gets when it is a terminal, unless --color or NO_COLOR say otherwise.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// Color modes, as given with --color.
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

// whether stdout and stderr get colors
var colorOut, colorErr bool

// SetColor decides whether stdout and stderr get colors and returns the mode in effect. mode comes from --color or --no-color, "" if
// neither was given: then a non-empty NO_COLOR means never, and otherwise (auto) a stream gets colors if it is a terminal and TERM isn't
// dumb.
func SetColor(mode string) string {
	if mode == "" {
		mode = ColorAuto
		if os.Getenv("NO_COLOR") != "" {
			mode = ColorNever
		}
	}
	switch mode {
	case ColorAlways:
		ansiTerminal(os.Stdout) // switches ANSI escapes on in a Windows console
		ansiTerminal(os.Stderr)
		colorOut, colorErr = true, true
	case ColorNever:
		colorOut, colorErr = false, false
	default:
		dumb := os.Getenv("TERM") == "dumb"
		colorOut = !dumb && ansiTerminal(os.Stdout)
		colorErr = !dumb && ansiTerminal(os.Stderr)
	}
	return mode
}

// Reset undoes what SetColor changed in the terminal, before pit exits.
func Reset() { resetTerminal() }

// SGR parameters of pit's colors
const (
	dim        = "2"
	red        = "31"
	green      = "32"
	yellow     = "33"
	cyan       = "36"
	boldRed    = "1;31"
	boldYellow = "1;33"
	boldBlue   = "1;34"
	boldCyan   = "1;36"
)

func paint(on bool, sgr, s string) string {
	if !on || s == "" {
		return s
	}
	return "\033[" + sgr + "m" + s + "\033[0m"
}

// Dim, Red, Green and Yellow color s for stdout. Pad before coloring: padding counts the escapes otherwise.
func Dim(s string) string    { return paint(colorOut, dim, s) }
func Red(s string) string    { return paint(colorOut, red, s) }
func Green(s string) string  { return paint(colorOut, green, s) }
func Yellow(s string) string { return paint(colorOut, yellow, s) }

// Note reports something worth knowing on stderr.
func Note(format string, a ...any) {
	fmt.Fprintf(os.Stderr, paint(colorErr, dim, "pit:")+" "+format+"\n", a...)
}

// Warn reports a problem that doesn't stop the command.
func Warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, paint(colorErr, boldYellow, "pit: warning:")+" "+format+"\n", a...)
}

// Error reports a failure, of the whole command or in one repo.
func Error(format string, a ...any) {
	fmt.Fprintf(os.Stderr, paint(colorErr, boldRed, "pit:")+" "+format+"\n", a...)
}

var stdin = bufio.NewReader(os.Stdin)

// ErrNoAnswer is returned by Ask when stdin is closed before an answer.
var ErrNoAnswer = errors.New("no answer on stdin")

// ErrInterrupted is returned by Ask when cancel is closed while waiting.
var ErrInterrupted = errors.New("interrupted")

// Ask prints prompt on stderr and reads one line from stdin, trimmed and lower-cased. Closing cancel (on Ctrl-C) stops the wait.
func Ask(prompt string, cancel <-chan struct{}) (string, error) {
	fmt.Fprint(os.Stderr, paint(colorErr, boldBlue, prompt)+" ")
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

// HeaderWidth is the column the rule of a block header reaches.
const HeaderWidth = 80

// Header starts a block, the output of one repo: a rule reaching column HeaderWidth that carries the repo's name, and its path if that
// differs, as in "─── b (nested/b) ────…". End closes the block.
func Header(name, path string) {
	fmt.Println(header(name, path, colorOut))
}

func header(name, path string, color bool) string {
	const rule = "─"
	title := paint(color, boldCyan, name)
	used := 3 + 1 + utf8.RuneCountInString(name) + 1 // lead, space, name, space
	if path != "" && path != name {
		title += paint(color, cyan, " ("+path+")")
		used += 3 + utf8.RuneCountInString(path)
	}
	return paint(color, cyan, strings.Repeat(rule, 3)+" ") + title + paint(color, cyan, " "+strings.Repeat(rule, max(3, HeaderWidth-used)))
}

// End closes a block with an empty line.
func End() { fmt.Println() }

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
