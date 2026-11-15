package ui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var escapes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestTable(t *testing.T) {
	titles := []string{"Name", "URL"}
	for _, color := range []bool{false, true} {
		green := func(s string) string { return paint(color, "32", s) }
		rows := [][]string{{green("a"), "https://example.com/a.git"}, {green("long-name"), ""}}
		got := table(titles, rows, color)
		if color != escapes.MatchString(got) {
			t.Errorf("table(color %v): escapes don't match color:\n%s", color, got)
		}
		want := "" +
			"╭───────────┬───────────────────────────╮\n" +
			"│ Name      │ URL                       │\n" +
			"├───────────┼───────────────────────────┤\n" +
			"│ a         │ https://example.com/a.git │\n" +
			"│ long-name │                           │\n" +
			"╰───────────┴───────────────────────────╯\n"
		if plain := escapes.ReplaceAllString(got, ""); plain != want {
			t.Errorf("table(color %v) =\n%s\nwant\n%s", color, plain, want)
		}
	}
}

func TestWidth(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"↑1 ↓2", 5},
		{"\033[1;36mname\033[0m", 4},
		{"\033[32m\033[0m", 0},
		{"x\033[1", 4}, // unterminated: counts as text
	} {
		if got := Width(c.s); got != c.want {
			t.Errorf("Width(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestHeader(t *testing.T) {
	for _, c := range []struct{ name, path, want string }{
		{"a", "", "─── a ───"},
		{".", "", "─── . ───"},
		{"b", "nested/b", "─── b (nested/b) ───"},
		{"same", "same", "─── same ───"},
		{strings.Repeat("x", 90), "", "─── " + strings.Repeat("x", 90) + " ───"},
	} {
		for _, color := range []bool{false, true} {
			got := header(c.name, c.path, color)
			if color != escapes.MatchString(got) {
				t.Errorf("header(%q, %q, %v) = %q: escapes don't match color", c.name, c.path, color, got)
			}
			plain := escapes.ReplaceAllString(got, "")
			if !strings.HasPrefix(plain, c.want) {
				t.Errorf("header(%q, %q, %v) = %q, want it to start with %q", c.name, c.path, color, plain, c.want)
			}
			// the rule reaches column 80; a name too long for that still gets a bit of it
			if n := utf8.RuneCountInString(plain); n != max(HeaderWidth, utf8.RuneCountInString(c.want)) {
				t.Errorf("header(%q, %q, %v) is %d wide: %q", c.name, c.path, color, n, plain)
			}
		}
	}
}
