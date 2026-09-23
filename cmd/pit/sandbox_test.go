package main

// Test harness: every test gets a sandbox directory with its own git configuration and local bare repositories reached through file://
// URLs. The test binary doubles as pit, so commands run by the tests, and by the git hooks they trigger, exercise the real command-line
// entry point.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("PIT_TEST_AS_PIT") == "1" {
		os.Exit(run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

var testHaveLFS = exec.Command("git", "lfs", "version").Run() == nil

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
		"[protocol \"file\"]\n\tallow = always\n"
	if testHaveLFS {
		gitconfig += "[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n" +
			"\tprocess = git-lfs filter-process\n\trequired = true\n"
	}
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte(gitconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k = strings.ToUpper(k); strings.HasPrefix(k, "GIT_") || k == "HOME" || k == "PIT_PUSH" {
			continue
		}
		env = append(env, kv)
	}
	// GIT_CLONE_PROTECTION_ACTIVE: git 2.39.4 to 2.45.1 abort a clone when a hook appears during checkout, which git-lfs's smudge filter
	// does by installing its hooks.
	env = append(env, "HOME="+home, "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_CLONE_PROTECTION_ACTIVE=false", "PIT_TEST_AS_PIT=1")
	return &sandbox{t: t, dir: dir, env: env}
}

func (s *sandbox) path(rel string) string { return filepath.Join(s.dir, filepath.FromSlash(rel)) }

func (s *sandbox) exec(dir, stdin, name string, args ...string) (string, error) {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, s.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// git runs git in dir and returns its trimmed output; failing fails the test.
func (s *sandbox) git(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.exec(dir, "", "git", args...)
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

func (s *sandbox) gitFails(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.exec(dir, "", "git", args...)
	if err == nil {
		s.t.Fatalf("git %s succeeded, want failure\n%s", strings.Join(args, " "), out)
	}
	return out
}

func (s *sandbox) pitIn(dir, stdin string, args ...string) (string, error) {
	s.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		s.t.Fatal(err)
	}
	return s.exec(dir, stdin, exe, args...)
}

func (s *sandbox) pit(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.pitIn(dir, "", args...)
	if err != nil {
		s.t.Fatalf("pit %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (s *sandbox) pitFails(dir string, args ...string) string {
	s.t.Helper()
	out, err := s.pitIn(dir, "", args...)
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

// bare creates an empty bare repo <name>.git and returns its file:// URL.
func (s *sandbox) bare(name string) string {
	s.t.Helper()
	s.git(s.dir, "init", "-q", "--bare", name+".git")
	return fileURL(s.path(name + ".git"))
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

func (s *sandbox) commitAll(dir, msg string) {
	s.t.Helper()
	s.git(dir, "add", "-A")
	s.git(dir, "commit", "-q", "-m", msg)
}

func (s *sandbox) clone(url, name string) string {
	s.t.Helper()
	s.git(s.dir, "clone", "-q", url, name)
	return s.path(name)
}

type ext struct{ url, path string }

// monorepo creates "mono" with externals.json, pushed to a new bare "origin".
func (s *sandbox) monorepo(externals map[string]ext) string {
	s.t.Helper()
	cfg := map[string]any{}
	for name, e := range externals {
		cfg[name] = map[string]string{"url": e.url, "repoPath": e.path}
	}
	data, err := json.MarshalIndent(map[string]any{"externals": cfg}, "", "  ")
	if err != nil {
		s.t.Fatal(err)
	}
	mono := s.path("mono")
	s.git(s.dir, "init", "-q", "mono")
	s.write(mono, "externals.json", string(data)+"\n")
	s.commitAll(mono, "init")
	s.git(mono, "remote", "add", "origin", s.bare("origin"))
	s.git(mono, "push", "-q", "-u", "origin", "main")
	return mono
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
