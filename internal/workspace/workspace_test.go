package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixture creates a root with externals a (cloned), lib/b (cloned), lib/c (not cloned) and, if rootRepo, a root repo.
func fixture(t *testing.T, rootRepo bool) string {
	t.Helper()
	root := t.TempDir()
	cfg := `{"externals": {"a": {"url": "u", "path": "a"}, "b": {"url": "u", "path": "lib/b"}, "c": {"url": "u", "path": "lib/c"}}}`
	must(t, os.WriteFile(filepath.Join(root, "pit-externals.json"), []byte(cfg), 0o644))
	for _, d := range []string{"a/.git", "lib/b/.git", "lib/b/src", "docs"} {
		must(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	if rootRepo {
		must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	}
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func names(repos []*Repo) []string {
	var n []string
	for _, r := range repos {
		n = append(n, r.Name)
	}
	return n
}

func TestOpenFindsRootFromBelow(t *testing.T) {
	root := fixture(t, true)
	w, err := Open(filepath.Join(root, "lib", "b", "src"))
	must(t, err)
	if w.Root != root {
		t.Errorf("Root = %s, want %s", w.Root, root)
	}
	if got := names(w.All()); !slices.Equal(got, []string{"a", "b", "c", "."}) {
		t.Errorf("All = %v", got)
	}
	if got := names(w.Cloned()); !slices.Equal(got, []string{"a", "b", "."}) {
		t.Errorf("Cloned = %v", got)
	}
	if got := names(w.Uncloned()); !slices.Equal(got, []string{"c"}) {
		t.Errorf("Uncloned = %v", got)
	}

	if _, err := Open(t.TempDir()); err != ErrNoRoot {
		t.Errorf("Open without config = %v", err)
	}
}

func TestNamed(t *testing.T) {
	w, err := Open(fixture(t, false))
	must(t, err)
	got, err := w.Named([]string{"b", "a"})
	must(t, err)
	if !slices.Equal(names(got), []string{"a", "b"}) {
		t.Errorf("Named = %v", names(got))
	}
	for name, want := range map[string]string{".": "not a git repo", "c": "not cloned", "x": "unknown repo"} {
		if _, err := w.Named([]string{name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Named(%s) = %v, want %q", name, err, want)
		}
	}
}

func TestMatch(t *testing.T) {
	root := t.TempDir()
	cfg := `{"externals": {"a": {"url": "u", "path": "a", "groups": ["x"]}, "b": {"url": "u", "path": "b", "groups": ["x", "y"]},
		"c": {"url": "u", "path": "c"}}}`
	must(t, os.WriteFile(filepath.Join(root, "pit-externals.json"), []byte(cfg), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	w, err := Open(root)
	must(t, err)
	for _, tc := range []struct {
		names, groups []string
		want          string
	}{
		{nil, []string{"x"}, "a b"},
		{nil, []string{"y"}, "b"},
		{nil, []string{"y", "x"}, "a b"},
		{[]string{"."}, []string{"y"}, "b ."},
		{[]string{"c"}, []string{"y"}, "b c"},
	} {
		got, err := w.Match(tc.names, tc.groups)
		must(t, err)
		if strings.Join(names(got), " ") != tc.want {
			t.Errorf("Match(%v, %v) = %v, want %s", tc.names, tc.groups, names(got), tc.want)
		}
	}
	if _, err := w.Match(nil, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "unknown group \"nope\" (groups: x, y)") {
		t.Errorf("unknown group: %v", err)
	}
}

func TestRoute(t *testing.T) {
	root := fixture(t, true)
	route := func(cwd string, paths ...string) string {
		t.Helper()
		w, err := Open(filepath.Join(root, filepath.FromSlash(cwd)))
		must(t, err)
		targets, err := w.Route(paths)
		if err != nil {
			return "error: " + err.Error()
		}
		var parts []string
		for _, tg := range targets {
			parts = append(parts, tg.Repo.Name+"="+strings.Join(tg.Pathspecs, ","))
		}
		return strings.Join(parts, " ")
	}
	for _, tc := range []struct{ cwd, paths, want string }{
		{"", "a/x.go", "a=x.go"},
		{"", "a", "a=."},
		{"", "docs/readme.md", ".=docs/readme.md"},
		{"", ".", "a=. b=. .=."},
		{"", "lib", "b=. .=lib"},
		{"lib/b/src", "main.go ../../../a", "a=. b=src/main.go"},
		{"lib/b/src", "../..", "b=. .=lib"},
		{"", "lib/c/x", "error: lib/c/x: c is not cloned yet; run pit sync"},
		{"", "../outside", "error: ../outside: outside"},
	} {
		got := route(tc.cwd, strings.Fields(tc.paths)...)
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("cwd %q: Route(%s) = %q, want %q", tc.cwd, tc.paths, got, tc.want)
		}
	}

	// without a root repo, a path outside every external is an error, but a directory holding externals is fine
	root = fixture(t, false)
	w, err := Open(root)
	must(t, err)
	if _, err := w.Route([]string{"docs"}); err == nil || !strings.Contains(err.Error(), "not inside any repo") {
		t.Errorf("Route(docs) = %v", err)
	}
	targets, err := w.Route([]string{"lib"})
	must(t, err)
	if len(targets) != 1 || targets[0].Repo.Name != "b" {
		t.Errorf("Route(lib) = %+v", targets)
	}
}

func TestDisplay(t *testing.T) {
	root := fixture(t, true)
	w, err := Open(filepath.Join(root, "lib"))
	must(t, err)
	b := w.Externals[1]
	if got := w.Display(b, "src/x.go"); got != "b/src/x.go" {
		t.Errorf("Display = %s", got)
	}
	if got := w.Display(w.RootRepo, "docs/"); got != "../docs/" {
		t.Errorf("Display = %s", got)
	}
}

func TestIgnoreBlock(t *testing.T) {
	root := fixture(t, true)
	w, err := Open(root)
	must(t, err)
	entries := w.IgnoreEntries()
	if !slices.Equal(entries, []string{"/.pit/", "/a/", "/lib/b/", "/lib/c/"}) {
		t.Errorf("entries = %v", entries)
	}
	if got := ignorePattern("we*ird [x]"); got != `/we\*ird \[x]/` {
		t.Errorf("ignorePattern = %s", got)
	}

	// appended to an existing file, then replaced in place
	content := UpdateIgnoreBlock("*.log", entries)
	want := "*.log\n\n" + IgnoreBegin + "\n/.pit/\n/a/\n/lib/b/\n/lib/c/\n" + IgnoreEnd + "\n"
	if content != want {
		t.Errorf("appended:\n%q\nwant\n%q", content, want)
	}
	content += "tmp/\n"
	updated := UpdateIgnoreBlock(content, []string{"/.pit/", "/x/"})
	if want := "*.log\n\n" + IgnoreBegin + "\n/.pit/\n/x/\n" + IgnoreEnd + "\ntmp/\n"; updated != want {
		t.Errorf("replaced:\n%q\nwant\n%q", updated, want)
	}
	got, found := ParseIgnoreBlock(updated)
	if !found || !slices.Equal(got, []string{"/.pit/", "/x/"}) {
		t.Errorf("ParseIgnoreBlock = %v, %v", got, found)
	}

	// CRLF files stay CRLF
	crlf := UpdateIgnoreBlock("*.log\r\n", []string{"/a/"})
	if want := "*.log\r\n\r\n" + IgnoreBegin + "\r\n/a/\r\n" + IgnoreEnd + "\r\n"; crlf != want {
		t.Errorf("CRLF:\n%q", crlf)
	}
	if UpdateIgnoreBlock("", []string{"/a/"}) != IgnoreBegin+"\n/a/\n"+IgnoreEnd+"\n" {
		t.Error("new file")
	}

	if w.IgnoreUpToDate() {
		t.Error("IgnoreUpToDate without a .gitignore")
	}
	must(t, os.WriteFile(w.IgnoreFile(), []byte(UpdateIgnoreBlock("", entries)), 0o644))
	if !w.IgnoreUpToDate() {
		t.Error("IgnoreUpToDate after writing the block")
	}
}

func TestParseStatus(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid 1234",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -1",
		"1 .M N... 100644 100644 100644 aaaa bbbb file with spaces.txt",
		"1 A. N... 000000 100644 100644 0000 cccc new.txt",
		"2 R. N... 100644 100644 100644 dddd dddd R100 renamed.txt", "old.txt",
		"u UU N... 100644 100644 100644 100644 e f g conflict.txt",
		"? untracked.txt",
		"",
	}, "\x00")
	s := parseStatus(out)
	if s.Branch != "main" || s.Upstream != "origin/main" || s.Ahead != 2 || s.Behind != 1 || s.UpstreamGone || s.Detached {
		t.Errorf("branch info = %+v", s)
	}
	want := []Entry{{" M", "file with spaces.txt", ""}, {"A ", "new.txt", ""}, {"R ", "renamed.txt", "old.txt"}, {"UU", "conflict.txt", ""},
		{"??", "untracked.txt", ""}}
	if !slices.Equal(s.Entries, want) {
		t.Errorf("entries = %q", s.Entries)
	}
	if !s.Staged() || !s.TrackedChanges() || !s.Conflicts() || s.Clean() {
		t.Error("predicates")
	}

	s = parseStatus("# branch.oid 1234\x00# branch.head (detached)\x00? x\x00")
	if !s.Detached || s.Staged() || s.TrackedChanges() {
		t.Errorf("detached, untracked only: %+v", s)
	}
	s = parseStatus("# branch.oid 1234\x00# branch.head topic\x00# branch.upstream origin/topic\x00")
	if !s.UpstreamGone {
		t.Error("upstream without ahead/behind should be gone")
	}
	s = parseStatus("# branch.oid (initial)\x00# branch.head main\x00")
	if !s.Unborn || s.UpstreamGone {
		t.Errorf("unborn: %+v", s)
	}
}
