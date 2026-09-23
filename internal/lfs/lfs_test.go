package lfs

import (
	"strings"
	"testing"
)

func TestIsPointer(t *testing.T) {
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:" +
		"4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n"
	cases := map[string]bool{
		pointer: true,
		"version https://hawser.github.com/spec/v1\noid sha256:abc\nsize 1\n": true,
		"":                                   false,
		"hello\n":                            false,
		"x\n" + pointer:                      false, // spec line must come first
		pointer + string(make([]byte, 1100)): false,
	}
	for in, want := range cases {
		if got := IsPointer([]byte(in)); got != want {
			t.Errorf("IsPointer(%.30q) = %v, want %v", in, got, want)
		}
	}
}

func TestHintNamesThePath(t *testing.T) {
	h := Hint("libs/lib")
	for _, want := range []string{"libs/lib/.gitattributes", "git add --renormalize libs/lib", "* !filter !diff !merge"} {
		if !strings.Contains(h, want) {
			t.Errorf("Hint lacks %q: %s", want, h)
		}
	}
}
