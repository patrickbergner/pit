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
	bold       = "1"
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

// Red, Green, Yellow, Cyan, BoldCyan (repo names, as in block headers), Bold and Dim color s for stdout.
func Bold(s string) string     { return paint(colorOut, bold, s) }
func Dim(s string) string      { return paint(colorOut, dim, s) }
func Cyan(s string) string     { return paint(colorOut, cyan, s) }
func BoldCyan(s string) string { return paint(colorOut, boldCyan, s) }
func Red(s string) string      { return paint(colorOut, red, s) }
func Green(s string) string    { return paint(colorOut, green, s) }
func Yellow(s string) string   { return paint(colorOut, yellow, s) }

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

// Table prints rows under a row of column titles, in a box with rules between the columns, as in
//
//	╭──────┬────────╮
//	│ Name │ Branch │
//	├──────┼────────┤
//	│ a    │ main   │
//	╰──────┴────────╯
//
// Cells may come colored already (their escapes don't count for the column widths); the box is dim, the titles bold. A row has a cell per
// title.
func Table(titles []string, rows [][]string) {
	fmt.Print(table(titles, rows, colorOut))
}

func table(titles []string, rows [][]string, color bool) string {
	widths := make([]int, len(titles))
	for i, t := range titles {
		widths[i] = Width(t)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], Width(c))
		}
	}
	var b strings.Builder
	rule := func(left, mid, right string) {
		cols := make([]string, len(widths))
		for i, w := range widths {
			cols[i] = strings.Repeat("─", w+2)
		}
		b.WriteString(paint(color, dim, left+strings.Join(cols, mid)+right) + "\n")
	}
	bar := paint(color, dim, "│")
	line := func(cells []string) {
		b.WriteString(bar)
		for i, c := range cells {
			b.WriteString(" " + c + strings.Repeat(" ", widths[i]-Width(c)) + " " + bar)
		}
		b.WriteString("\n")
	}
	head := make([]string, len(titles))
	for i, t := range titles {
		head[i] = paint(color, bold, t)
	}
	rule("╭", "┬", "╮")
	line(head)
	rule("├", "┼", "┤")
	for _, r := range rows {
		line(r)
	}
	rule("╰", "┴", "╯")
	return b.String()
}

// Width is the number of runes s shows, not counting its color escapes.
func Width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "\033[") {
			if end := strings.IndexByte(s[i:], 'm'); end >= 0 {
				i += end + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// Pad right-pads s to w runes, not counting its color escapes.
func Pad(s string, w int) string {
	if n := Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
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
