package ui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var escapes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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
