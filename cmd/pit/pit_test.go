package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpVersionAndUnknownCommand(t *testing.T) {
	s := newSandbox(t)
	contains(t, s.pit(s.dir), "Usage: pit <command>")
	contains(t, s.pit(s.dir, "version"), "pit ")
	contains(t, s.pitFails(s.dir, "frobnicate"), "unknown command", "Usage: pit <command>")
}

func TestNoRootAndBadConfig(t *testing.T) {
	s := newSandbox(t)
	contains(t, s.pitFails(s.dir, "status"), "no pit-externals.json found")
	s.write(s.dir, "pit-externals.json", "{ nope")
	contains(t, s.pitFails(s.dir, "list"), "invalid JSON")
	s.write(s.dir, "pit-externals.json", `{"externals": {"x": {"url": "u", "repoPath": "x"}}}`)
	contains(t, s.pitFails(s.dir, "list"), "unknown field")
	s.write(s.dir, "pit-externals.json", `{"externals": {"x": {"url": "u", "path": "../x"}}}`)
	contains(t, s.pitFails(s.dir, "list"), `path "../x"`)
	s.write(s.dir, "pit-externals.json", `{"externals": {"x": {"url": "u", "path": "x"}, "y": {"url": "u", "path": "x/y"}}}`)
	contains(t, s.pitFails(s.dir, "list"), "lies inside external")
}

func TestSyncClonesAndIgnores(t *testing.T) {
	s := newSandbox(t)
	empty := s.remote("empty", true)
	root := s.root(true, ext{"a", s.remote("a", false), "a"}, ext{"b", s.remote("b", false), "nested/b"}, ext{"e", empty, "e"})
	s.write(root, ".gitignore", "*.log\n")

	contains(t, s.pit(root, "list"), "a  a ", "nested/b", "(not cloned)", ".  (root repo)")
	contains(t, s.pit(root, "status"), "not cloned (pit sync)")

	out := s.pit(root, "sync")
	contains(t, out, "cloning a into a", "cloning b into nested/b", "updated .gitignore")
	// plain clones: git works in them as usual
	if got := s.git(s.path("forge/nested/b"), "remote", "get-url", "origin"); got != fileURL(s.path("remotes/b.git")) {
		t.Errorf("origin of b = %s", got)
	}
	if got := s.git(s.path("forge/e"), "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("branch of empty clone = %s", got)
	}
	ignore := s.read(root, ".gitignore")
	contains(t, ignore, "*.log\n", "# >>> pit externals", "/.pit/\n/a/\n/nested/b/\n/e/\n", "# <<< pit externals")
	// the root repo sees only its own files
	st := s.git(root, "status", "--porcelain")
	if st != "?? .gitignore" {
		t.Errorf("root status = %q", st)
	}

	// idempotent
	lacks(t, s.pit(root, "sync"), "cloning", "updated")
	if s.read(root, ".gitignore") != ignore {
		t.Error("second sync changed .gitignore")
	}

	// with an outdated ignore block, pit won't stage in the root repo
	s.config(root, ext{"a", fileURL(s.path("remotes/a.git")), "a"}, ext{"x", fileURL(s.path("remotes/x.git")), "x"})
	contains(t, s.pitFails(root, "add", "-A"), "run pit sync first")
	s.pit(root, "add", "a") // the externals themselves are fine

	// a dropped external is reported, not deleted
	s.config(root, ext{"a", s.remote("a2", false), "a"})
	out = s.pit(root, "sync")
	contains(t, out, "nested/b is no longer an external", "origin is")
	if !s.exists(root, "nested/b/README.md") {
		t.Error("sync deleted a dropped external's clone")
	}
}

func TestSyncRefusesForeignContent(t *testing.T) {
	s := newSandbox(t)
	root := s.root(true, ext{"a", s.remote("a", false), "a"}, ext{"t", s.remote("t", false), "t"})
	s.write(root, "a/own.txt", "mine\n")
	s.write(root, "t/x.txt", "tracked\n")
	s.git(root, "add", "t/x.txt")
	s.git(root, "commit", "-q", "-m", "track t")
	os.Remove(s.path("forge/a/own.txt"))
	s.write(root, "a/keep.txt", "mine\n")
	os.Remove(s.path("forge/t/x.txt"))

	out := s.pitFails(root, "sync")
	contains(t, out, "a exists and is not empty", "the root repo tracks files in t")
	if s.read(root, "a/keep.txt") != "mine\n" {
		t.Error("sync touched a/")
	}
}

func TestRootWithoutRepo(t *testing.T) {
	s := newSandbox(t)
	root := s.root(false, ext{"a", s.remote("a", false), "a"})
	s.pit(root, "sync")
	if s.exists(root, ".gitignore") {
		t.Error("sync wrote a .gitignore into a root that isn't a repo")
	}
	s.write(root, "a/x.txt", "x\n")
	s.write(root, "loose.txt", "x\n")
	contains(t, s.pitFails(root, "add", "loose.txt"), "not inside any repo")
	s.pit(root, "add", ".")
	s.pit(root, "commit", "-m", "x")
	if got := s.git(s.path("forge/a"), "log", "-1", "--format=%s"); got != "x" {
		t.Errorf("a's last commit = %q", got)
	}
	contains(t, s.pitFails(root, "status", "-r", "."), "is not a git repo")
}

func TestStatusAndPathRouting(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pit(root, "status"), "all 3 repos clean and up to date")

	s.write(root, "a/new.txt", "new\n")
	s.write(root, "nested/b/README.md", "changed\n")
	s.write(root, "notes.md", "changed\n")
	out := s.pit(root, "status")
	contains(t, out, "a  main", "?? a/new.txt", " M nested/b/README.md", " M notes.md")

	// from a subdirectory, paths are relative to it; inside an external, pit still sees the whole root
	out = s.pit(s.path("forge/nested/b"), "status")
	contains(t, out, " M README.md", "?? ../../a/new.txt", " M ../../notes.md")

	// paths narrow the repos; a directory covers the externals below it
	out = s.pit(root, "status", "nested")
	contains(t, out, "b  main", "README.md")
	lacks(t, out, "new.txt", "notes.md")

	// add routes each path to its repo
	s.pit(s.path("forge/nested"), "add", "b/README.md", "../a")
	if got := s.git(s.path("forge/a"), "diff", "--cached", "--name-only"); got != "new.txt" {
		t.Errorf("a staged %q", got)
	}
	if got := s.git(s.path("forge/nested/b"), "diff", "--cached", "--name-only"); got != "README.md" {
		t.Errorf("b staged %q", got)
	}
	s.pit(root, "restore", "--staged", "a")
	if got := s.git(s.path("forge/a"), "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("a still staged %q", got)
	}

	// diff shows all repos as one tree
	out = s.pit(root, "diff")
	contains(t, out, "a/notes.md", "b/notes.md")
	out = s.pit(root, "diff", "--staged")
	contains(t, out, "--- a/nested/b/README.md", "+++ b/nested/b/README.md")
	lacks(t, out, "notes.md")

	// a different branch than configured is pointed out
	s.git(s.path("forge/a"), "switch", "-q", "-c", "topic")
	contains(t, s.pit(root, "status", "-r", "a"), "a  topic", "expected main")
}

func TestCommitAcrossRepos(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pitFails(root, "commit", "-m", "x"), "nothing to commit")

	s.write(root, "a/x.txt", "x\n")
	s.write(root, "notes.md", "more\n")
	contains(t, s.pitFails(root, "commit", "-m", "x"), "nothing staged", "changes in a, .")

	s.pit(root, "add", "-A")
	out := s.pit(root, "commit", "-m", "Add x", "-m", "Body")
	contains(t, out, "a  main  ", ".  main  ")
	lacks(t, out, "b  ")
	for _, dir := range []string{"forge/a", "forge"} {
		if got := s.git(s.path(dir), "log", "-1", "--format=%B"); got != "Add x\n\nBody" {
			t.Errorf("%s: message %q", dir, got)
		}
	}
	if s.git(s.path("forge/nested/b"), "rev-list", "--count", "HEAD") != "1" {
		t.Error("b got a commit")
	}

	// -a commits tracked changes, not untracked files
	s.write(root, "nested/b/README.md", "changed\n")
	s.write(root, "nested/b/untracked.txt", "u\n")
	s.pit(root, "commit", "-am", "Change b")
	if got := s.git(s.path("forge/nested/b"), "status", "--porcelain"); got != "?? untracked.txt" {
		t.Errorf("b status after -a: %q", got)
	}

	// the log shows a pit commit as one entry
	out = s.pit(root, "log")
	contains(t, out, "commit b ", "    Change b", "commit a ", ", . ", "    Add x", "    Body", "commit . ", "init forge")
	if strings.Count(out, "Add x") != 1 {
		t.Errorf("Add x not grouped:\n%s", out)
	}
	contains(t, s.pit(root, "log", "--oneline", "-2"), "Change b  [b ", "Add x  [a ")
}

func TestCommitPreflight(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/x.txt", "x\n")
	s.write(root, "nested/b/y.txt", "y\n")
	s.pit(root, "add", "-A")
	s.git(s.path("forge/nested/b"), "checkout", "-q", "--detach")

	out := s.pitFails(root, "commit", "-m", "x")
	contains(t, out, "b: HEAD is detached", "nothing was committed")
	if s.git(s.path("forge/a"), "rev-list", "--count", "HEAD") != "1" {
		t.Error("a was committed despite the failed pre-flight")
	}
}

func TestCommitContinueAfterHookFailure(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	hook := s.path("forge/nested/b/.git/hooks/pre-commit")
	s.write(root, "nested/b/.git/hooks/pre-commit", "#!/bin/sh\necho hook says no >&2\nexit 1\n")
	os.Chmod(hook, 0o755)

	s.write(root, "a/x.txt", "x\n")
	s.write(root, "nested/b/y.txt", "y\n")
	s.write(root, "notes.md", "z\n")
	s.pit(root, "add", "-A")
	out := s.pitFails(root, "commit", "-m", "Together")
	contains(t, out, "hook says no", "committed: a ", "not committed yet: b, .", "pit commit --continue")

	contains(t, s.pitFails(root, "commit", "-m", "Other"), "stopped halfway")

	os.Remove(hook)
	out = s.pit(root, "commit", "--continue")
	contains(t, out, "b  main", ".  main")
	lacks(t, out, "a  main")
	if got := s.git(s.path("forge/nested/b"), "log", "-1", "--format=%s"); got != "Together" {
		t.Errorf("b: %q", got)
	}
	contains(t, s.pitFails(root, "commit", "--continue"), "no pit commit in progress")
	if strings.Count(s.pit(root, "log", "--oneline"), "Together") != 1 {
		t.Error("continued commit not grouped in the log")
	}
}

func TestCommitAbort(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "nested/b/.git/hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	os.Chmod(s.path("forge/nested/b/.git/hooks/pre-commit"), 0o755)
	s.write(root, "nested/b/y.txt", "y\n")
	s.pit(root, "add", "-A")
	s.pitFails(root, "commit", "-m", "x")
	contains(t, s.pit(root, "commit", "--abort"), "forgot the pit commit")
	if s.exists(root, ".pit/commit-in-progress.json") {
		t.Error("in-progress state left behind")
	}
}

func TestCommitAmend(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pitFails(root, "commit", "--amend", "-m", "x"), "no pit commit to amend")

	s.write(root, "a/x.txt", "x\n")
	s.write(root, "notes.md", "n\n")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Frist")

	// reword, and take along a fix in another repo
	s.write(root, "nested/b/fix.txt", "fix\n")
	s.pit(root, "add", "nested/b")
	out := s.pit(root, "commit", "--amend", "-m", "First")
	contains(t, out, "a  main", "b  main", ".  main")
	for _, dir := range []string{"forge/a", "forge/nested/b", "forge"} {
		if got := s.git(s.path(dir), "log", "-1", "--format=%s"); got != "First" {
			t.Errorf("%s: %q", dir, got)
		}
	}
	if s.git(s.path("forge/a"), "rev-list", "--count", "HEAD") != "2" {
		t.Error("amend created a new commit in a")
	}

	// --no-edit keeps the message
	s.write(root, "a/x.txt", "x2\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "--amend", "--no-edit")
	if got := s.git(s.path("forge/a"), "show", "HEAD:x.txt"); got != "x2" {
		t.Errorf("a/x.txt in HEAD = %q", got)
	}

	// once a repo moved on or pushed, pit refuses
	s.git(s.path("forge/a"), "push", "-q")
	contains(t, s.pitFails(root, "commit", "--amend", "-m", "y"), "a: ", "already pushed", "nothing was changed")
	s.git(s.path("forge/nested/b"), "commit", "-q", "--allow-empty", "-m", "by hand")
	contains(t, s.pitFails(root, "commit", "--amend", "-m", "y"), "b: has moved on")
}

func TestCommitEditor(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "-A")

	// the editor sees the template with all repos; writing nothing aborts
	tmpl := s.path("template.txt")
	editor := "cp \"$1\" '" + filepath.ToSlash(tmpl) + "'"
	s.write(s.dir, "ed.sh", editor+"\n")
	out, err := s.pitEnv(root, []string{"GIT_EDITOR=sh '" + filepath.ToSlash(s.path("ed.sh")) + "'"}, "commit")
	if err == nil {
		t.Fatalf("commit with an empty message succeeded:\n%s", out)
	}
	contains(t, out, "empty commit message")
	contains(t, s.read(s.dir, "template.txt"), "# a (main):", "#\tA  x.txt")

	s.write(s.dir, "ed.sh", "printf 'From the editor\\n# a comment\\n' > \"$1\"\n")
	if out, err := s.pitEnv(root, []string{"GIT_EDITOR=sh '" + filepath.ToSlash(s.path("ed.sh")) + "'"}, "commit"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if got := s.git(s.path("forge/a"), "log", "-1", "--format=%B"); got != "From the editor" {
		t.Errorf("message %q", got)
	}
}

func TestPushPullFetch(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pit(root, "push"), "nothing to push")

	s.write(root, "a/x.txt", "x\n")
	s.write(root, "notes.md", "n\n")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Publish x")

	out := s.pit(root, "push", "-n")
	contains(t, out, "a  main -> origin/main", "Publish x", ".  main -> origin/main")
	if s.git(s.path("forge/a"), "status", "-sb") != "## main...origin/main [ahead 1]" {
		t.Error("dry run pushed")
	}
	contains(t, s.pit(root, "push", "-y"), "pushed 2 repos")
	if got := s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "log", "-1", "--format=%s", "main"); got != "Publish x" {
		t.Errorf("remote a: %q", got)
	}

	// someone else pushes to b; a local commit there makes b diverge, so without questions nothing is pushed anywhere
	other := s.path("other-b")
	s.git(s.dir, "clone", "-q", fileURL(s.path("remotes/b.git")), other)
	s.write(other, "theirs.txt", "t\n")
	s.commitAll(other, "Theirs")
	s.git(other, "push", "-q")
	s.write(root, "a/y.txt", "y\n")
	s.write(root, "nested/b/mine.txt", "m\n")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Mine")
	out = s.pitFails(root, "push", "-y")
	contains(t, out, "b  main -> origin/main  (can't push)", "b: main and origin/main have diverged", "nothing was pushed",
		"pit push -i asks repo by repo")
	if s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "log", "-1", "--format=%s", "main") != "Publish x" {
		t.Error("a was pushed although b diverged")
	}

	// with questions, b is skipped and the others are still offered
	out, err := s.pitInput(root, "y\n", nil, "push")
	if err == nil {
		t.Fatalf("push with a diverged repo succeeded:\n%s", out)
	}
	contains(t, out, "skipping b; asking about the other repos", "(1/1) Push a:", "pushed 1 repo", "not pushed because of the problems above: b")
	lacks(t, out, "Push b:")
	if s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "log", "-1", "--format=%s", "main") != "Mine" {
		t.Error("a wasn't pushed")
	}

	contains(t, s.pit(root, "status"), "b  main  ↑1 ↓1")
	out = s.pit(root, "pull", "--no-edit")
	contains(t, out, "=== b (nested/b)", "=== a")
	if !s.exists(root, "nested/b/theirs.txt") {
		t.Error("pull didn't bring theirs.txt")
	}
	contains(t, s.pit(root, "push", "-y"), "pushed 1 repo") // a went out above

	// a new branch gets an upstream
	s.git(s.path("forge/a"), "switch", "-q", "-c", "topic")
	s.write(root, "a/t.txt", "t\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Topic")
	contains(t, s.pit(root, "push", "--yes"), "a  topic -> origin/topic  (sets upstream)")
	if got := s.git(s.path("forge/a"), "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/topic" {
		t.Errorf("upstream = %s", got)
	}

	s.pit(root, "fetch")
}

func TestPushAsksRepoByRepo(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	remoteHead := func(name string) string {
		t.Helper()
		return s.git(s.dir, "--git-dir="+s.path("remotes/"+name+".git"), "log", "-1", "--format=%s", "main")
	}
	for _, r := range []string{"a", "nested/b"} {
		s.write(root, r+"/x.txt", "x\n")
	}
	s.write(root, "notes.md", "n\n")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Everywhere")

	// on by default; without answers nothing is pushed
	out, err := s.pitInput(root, "", nil, "push")
	if err == nil {
		t.Fatalf("push without answers succeeded:\n%s", out)
	}
	contains(t, out, "a  main -> origin/main", "Everywhere", "(1/3) Push a: main -> origin/main, 1 commit?", "no answer on stdin")
	if remoteHead("a") != "init a" {
		t.Error("a was pushed without an answer")
	}

	// an unknown answer shows the help and asks again; q skips the rest
	out, err = s.pitInput(root, "x\nn\ny\nq\n", nil, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	contains(t, out, "a - push this repo and all remaining ones", "(2/3) Push b:", "(3/3) Push .:", "pushed 1 repo")
	if remoteHead("a") != "init a" || remoteHead("b") != "Everywhere" || s.git(s.dir, "--git-dir="+s.path("remotes/forge.git"), "log", "-1",
		"--format=%s", "main") != "track externals" {
		t.Error("expected exactly b to be pushed")
	}

	// a pushes this one and all remaining
	out, err = s.pitInput(root, "a\n", nil, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	contains(t, out, "(1/2) Push a:", "pushed 2 repos")
	lacks(t, out, "(2/2)")

	// confirmPush: false turns it off; -i turns it on again
	s.write(root, "pit-externals.json", `{
  "settings": {"confirmPush": false},
  "externals": {
    "a": {"url": "`+fileURL(s.path("remotes/a.git"))+`", "path": "a"},
    "b": {"url": "`+fileURL(s.path("remotes/b.git"))+`", "path": "nested/b"}
  }
}`)
	s.write(root, "a/y.txt", "y\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Quietly")
	contains(t, s.pitFails(root, "push", "-i"), "(1/1) Push a:", "no answer on stdin")
	contains(t, s.pitFails(root, "push", "-i", "-y"), "exclude each other")
	lacks(t, s.pit(root, "push"), "Push a:")
	if remoteHead("a") != "Quietly" {
		t.Error("push with confirmPush false didn't push")
	}
}

func TestEach(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	out := s.pit(root, "each", "--", "rev-parse", "--abbrev-ref", "HEAD")
	contains(t, out, "=== a\nmain", "=== b (nested/b)\nmain", "=== .\nmain")
	out = s.pit(root, "each", "-r", "a", "log", "--format=%s")
	contains(t, out, "init a")
	lacks(t, out, "init b")
	contains(t, s.pitFails(root, "each", "frobnicate"), "failed in a, b, .")
}

func TestGroups(t *testing.T) {
	s := newSandbox(t)
	root := s.root(true)
	s.write(root, "pit-externals.json", `{"externals": {
		"a": {"url": "`+s.remote("a", false)+`", "path": "a", "groups": ["libs", "go"]},
		"b": {"url": "`+s.remote("b", false)+`", "path": "b", "groups": ["libs"]},
		"c": {"url": "`+s.remote("c", false)+`", "path": "c"}
	}}`)

	// sync and list take groups too
	out := s.pit(root, "sync", "-g", "go")
	contains(t, out, "cloning a")
	lacks(t, out, "cloning b", "cloning c")
	out = s.pit(root, "list")
	contains(t, out, "[libs, go]", "[libs]    ", "(not cloned)")
	out = s.pit(root, "list", "-g", "libs")
	contains(t, out, "a  ", "b  ")
	lacks(t, out, "c  ", "(root repo)")

	// status lists the group's uncloned members; other commands leave them out
	out = s.pit(root, "status", "-g", "libs")
	contains(t, out, "b  not cloned", "1 repo clean")
	lacks(t, out, "c  not cloned")
	s.pit(root, "sync")
	s.commitAll(root, "track externals")

	for _, r := range []string{"a", "b", "c"} {
		s.write(root, r+"/x.txt", r+"\n")
	}
	s.write(root, "notes.md", "changed\n")
	s.pit(root, "add", "-g", "libs", "-A")
	if got := s.git(s.path("forge/c"), "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("c staged %q", got)
	}
	// -r and -g combine: the group plus the root repo
	s.pit(root, "add", "-r", ".", "notes.md")
	out = s.pit(root, "commit", "-g", "libs", "-r", ".", "-m", "Libs")
	contains(t, out, "a  main", "b  main", ".  main")
	lacks(t, out, "c  main")

	contains(t, s.pit(root, "each", "-g", "go", "--", "log", "-1", "--format=%s"), "=== a\nLibs")
	contains(t, s.pitFails(root, "status", "-g", "nope"), `unknown group "nope" (groups: libs, go)`)
	contains(t, s.pitFails(root, "commit", "--amend", "-g", "libs", "-m", "x"), "doesn't take -r or -g")
}

func TestLock(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, ".pit/lock", "1\n")
	contains(t, s.pitFails(root, "add", "-A"), "another pit command is running")
	s.pit(root, "status") // read-only commands don't lock
}
