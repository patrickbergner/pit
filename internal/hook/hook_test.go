package hook

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	root := t.TempDir()
	remote := []string{
		"git@github.com:You/foo.git",
		"https://github.com/you/foo",
		"https://github.com/you/foo.git/",
		"ssh://git@github.com/you/foo/",
		"https://user@github.com/You/Foo",
		"git://github.com/you/foo.git",
	}
	for _, u := range remote {
		if got := NormalizeURL(u, root); got != "github.com/you/foo" {
			t.Errorf("NormalizeURL(%q) = %q", u, got)
		}
	}
	if got := NormalizeURL("ssh://git@host:2222/x.git", root); got != "host:2222/x" {
		t.Errorf("port: got %q", got)
	}
	if got := NormalizeURL("https://host/a@b/x", root); got != "host/a@b/x" {
		t.Errorf("@ in path: got %q", got)
	}

	repo := filepath.Join(root, "external.git")
	fileURL := "file://" + filepath.ToSlash(repo)
	if !strings.HasPrefix(filepath.ToSlash(repo), "/") {
		fileURL = "file:///" + filepath.ToSlash(repo) // file:///C:/...
	}
	want := NormalizeURL(repo, root)
	local := []string{
		repo,
		repo + "/",
		filepath.Join(root, "external"),
		fileURL,
		"./external.git",
		"sub/../external",
	}
	if runtime.GOOS == "windows" {
		local = append(local, filepath.ToSlash(repo))
	}
	for _, u := range local {
		if got := NormalizeURL(u, root); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", u, got, want)
		}
	}
	if NormalizeURL(filepath.Join(root, "other.git"), root) == want {
		t.Error("different local repos normalize to the same path")
	}
}

func TestShQuote(t *testing.T) {
	if got := shQuote("C:/it's/pit.exe"); got != `'C:/it'\''s/pit.exe'` {
		t.Errorf("shQuote = %s", got)
	}
}
