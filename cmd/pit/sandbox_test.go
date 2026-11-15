package main

// Test harness: every test gets a sandbox directory with its own git configuration and local bare repositories reached through file://
// URLs. The test binary doubles as pit, so the tests exercise the real command-line entry point.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMain(m *testing.M) {
	if os.Getenv("PIT_TEST_AS_PIT") == "1" {
		os.Exit(run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type sandbox struct {
	t   *testing.T
	dir string
	env []string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Parallel()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	gitconfig := "[init]\n\tdefaultBranch = main\n" +
		"[user]\n\tname = Pit Test\n\temail = pit@example.com\n" +
		"[core]\n\tautocrlf = false\n" +
		"[pull]\n\trebase = false\n" +
		"[protocol \"file\"]\n\tallow = always\n"
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte(gitconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k = strings.ToUpper(k); strings.HasPrefix(k, "GIT_") || k == "HOME" || k == "EDITOR" || k == "VISUAL" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_EDITOR=false", "PIT_TEST_AS_PIT=1")
	return &sandbox{t: t, dir: dir, env: env}
}

func (s *sandbox) path(rel string) string { return filepath.Join(s.dir, filepath.FromSlash(rel)) }

func (s *sandbox) exec(dir, stdin string, env []string, name string, args ...string) (string, error) {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, append(append([]string(nil), s.env...), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// git runs git in dir and returns its trimmed output; failing fails the test.
func (s *sandbox) git(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.exec(dir, "", nil, "git", args...)
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

func (s *sandbox) pitEnv(dir string, env []string, args ...string) (string, error) {
	s.t.Helper()
	return s.pitInput(dir, "", env, args...)
}

// pitInput runs pit with stdin, e.g. answers to its questions.
func (s *sandbox) pitInput(dir, stdin string, env []string, args ...string) (string, error) {
	s.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		s.t.Fatal(err)
	}
	return s.exec(dir, stdin, env, exe, args...)
}

func (s *sandbox) pit(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.pitEnv(dir, nil, args...)
	if err != nil {
		s.t.Fatalf("pit %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (s *sandbox) pitFails(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.pitEnv(dir, nil, args...)
	if err == nil {
		s.t.Fatalf("pit %s succeeded, want failure\n%s", strings.Join(args, " "), out)
	}
	return out
}

func fileURL(path string) string {
	u := filepath.ToSlash(path)
	if !strings.HasPrefix(u, "/") {
		u = "/" + u // file:///C:/...
	}
	return "file://" + u
}

// remote creates the bare repo remotes/<name>.git with one commit holding README.md (none if empty) and returns its file:// URL.
func (s *sandbox) remote(name string, empty bool) string {
	s.t.Helper()
	bare := s.path("remotes/" + name + ".git")
	s.git(s.dir, "init", "-q", "--bare", bare)
	if !empty {
		work := s.path("seed/" + name)
		s.git(s.dir, "clone", "-q", fileURL(bare), work)
		s.write(work, "README.md", name+"\n")
		s.commitAll(work, "init "+name)
		s.git(work, "push", "-q", "origin", "main")
	}
	return fileURL(bare)
}

func (s *sandbox) write(dir, rel, content string) {
	s.t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) read(dir, rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func (s *sandbox) exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func (s *sandbox) commitAll(dir, msg string) {
	s.t.Helper()
	s.git(dir, "add", "-A")
	s.git(dir, "commit", "-q", "-m", msg)
}

type ext struct{ name, url, path string }

// config writes pit-externals.json into dir.
func (s *sandbox) config(dir string, exts ...ext) {
	s.t.Helper()
	var b strings.Builder
	b.WriteString("{\n  \"externals\": {")
	for i, e := range exts {
		if i > 0 {
			b.WriteString(",")
		}
		entry, _ := json.Marshal(map[string]string{"url": e.url, "path": e.path})
		b.WriteString("\n    \"" + e.name + "\": " + string(entry))
	}
	b.WriteString("\n  }\n}\n")
	s.write(dir, "pit-externals.json", b.String())
}

// root creates the pit root "forge" with the externals; as a git repo with an origin of its own if repo is set. It doesn't sync.
func (s *sandbox) root(repo bool, exts ...ext) string {
	s.t.Helper()
	root := s.path("forge")
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.t.Fatal(err)
	}
	s.config(root, exts...)
	if repo {
		s.git(root, "init", "-q")
		s.write(root, "notes.md", "notes\n")
		s.commitAll(root, "init forge")
		s.git(root, "remote", "add", "origin", s.remote("forge", true))
		s.git(root, "push", "-q", "-u", "origin", "main")
	}
	return root
}

// standard is a synced root repo with externals "a" at a/ and "b" at nested/b.
func (s *sandbox) standard() string {
	s.t.Helper()
	root := s.root(true, ext{"a", s.remote("a", false), "a"}, ext{"b", s.remote("b", false), "nested/b"})
	s.pit(root, "sync")
	s.commitAll(root, "track externals")
	s.git(root, "push", "-q")
	return root
}

// block is how a repo's block starts in output without colors: the header with its title ("b (nested/b)"), a rule to column 80, then
// the beginning of the output.
func block(title, output string) string {
	return "─── " + title + " " + strings.Repeat("─", 80-5-utf8.RuneCountInString(title)) + "\n" + output
}

func contains(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func lacks(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(out, w) {
			t.Errorf("output contains %q:\n%s", w, out)
		}
	}
}
