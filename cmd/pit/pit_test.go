package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// a block per repo: branch and URL; the root repo's block holds the root directory
	contains(t, s.pit(root, "list"), block("a", "main  "+fileURL(s.path("remotes/a.git"))+"  (not cloned)\n\n"), block("b (nested/b)", "main  "), "  (not cloned)\n\n",
		block("(pit root)", root+"\n\n"), "3 externals, 3 not cloned (pit sync)\n")
	contains(t, s.pit(root, "status"), "not cloned (pit sync)")

	out := s.pit(root, "sync")
	contains(t, out, block("a", "Cloning into 'a'...\n"), block("b (nested/b)", "Cloning into 'nested/b'...\n"),
		block("(pit root)", "pit: updated .gitignore"))
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
	out = s.pit(root, "sync")
	contains(t, out, "all 3 externals already cloned")
	lacks(t, out, "Cloning", "updated", "───")
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
	contains(t, out, block("a", "pit: warning: origin is "), block("(pit root)", "pit: nested/b is no longer an external"))
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
	contains(t, out, block("a", "pit: a exists and is not empty"), block("t", "pit: the root repo tracks files in t"), "sync failed in a, t")
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
	contains(t, out, block("a", "main\n  ?? a/new.txt\n\n"), block("b (nested/b)", "main\n   M nested/b/README.md\n\n"),
		block("(pit root)", "main\n   M notes.md\n\n"))

	// from a subdirectory, paths are relative to it; inside an external, pit still sees the whole root
	out = s.pit(s.path("forge/nested/b"), "status")
	contains(t, out, " M README.md", "?? ../../a/new.txt", " M ../../notes.md")

	// paths narrow the repos; a directory covers the externals below it
	out = s.pit(root, "status", "nested")
	contains(t, out, block("b (nested/b)", "main\n"), "README.md")
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
	contains(t, s.pit(root, "status", "-r", "a"), block("a", "topic  (expected main, "))
}

func TestCommitAcrossRepos(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pitFails(root, "commit", "-m", "x"), "nothing to commit")

	s.write(root, "a/x.txt", "x\n")
	s.write(root, "notes.md", "more\n")
	contains(t, s.pitFails(root, "commit", "-m", "x"), "nothing staged", "changes in a, (pit root)")

	s.pit(root, "add", "-A")
	// a block per repo with git's report of the commit
	out := s.pit(root, "commit", "-m", "Add x", "-m", "Body")
	contains(t, out, block("a", "[main "), "] Add x\n 1 file changed", block("(pit root)", "[main "))
	lacks(t, out, "─── b")
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
	// commit times have a resolution of one second; date this commit later so the log's order across repos is defined
	later := "GIT_COMMITTER_DATE=" + time.Now().Add(time.Minute).Format(time.RFC3339)
	if out, err := s.pitEnv(root, []string{later}, "commit", "-am", "Change b"); err != nil {
		t.Fatalf("pit commit -am: %v\n%s", err, out)
	}
	if got := s.git(s.path("forge/nested/b"), "status", "--porcelain"); got != "?? untracked.txt" {
		t.Errorf("b status after -a: %q", got)
	}

	// the log shows a pit commit as one entry
	out = s.pit(root, "log")
	contains(t, out, "commit b ", "    Change b", "commit a ", ", (pit root) ", "    Add x", "    Body", "commit (pit root) ", "init forge")
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
	contains(t, out, block("b (nested/b)", "hook says no\n\n"), "committed: a ", "not committed yet: b, (pit root)", "pit commit --continue")

	contains(t, s.pitFails(root, "commit", "-m", "Other"), "stopped halfway")

	os.Remove(hook)
	out = s.pit(root, "commit", "--continue")
	contains(t, out, block("b (nested/b)", "[main "), block("(pit root)", "[main "))
	lacks(t, out, "─── a")
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
	contains(t, out, block("a", "[main "), block("b (nested/b)", "[main "), block("(pit root)", "[main "))
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
	contains(t, out, block("a", "main -> origin/main\n  "), " Publish x\n\n", block("(pit root)", "main -> origin/main\n"))
	if s.git(s.path("forge/a"), "status", "-sb") != "## main...origin/main [ahead 1]" {
		t.Error("dry run pushed")
	}
	// the plan, then a block per push with git's output
	out = s.pit(root, "push", "-y")
	contains(t, out, block("a", "To file://"), block("(pit root)", "To file://"), "pushed 2 repos")
	if n := strings.Count(out, block("a", "")); n != 2 {
		t.Errorf("a has %d blocks, want 2 (plan and push):\n%s", n, out)
	}
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
	contains(t, out, block("b (nested/b)", "main -> origin/main  (can't push)"), "b: main and origin/main have diverged", "nothing was pushed",
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

	contains(t, s.pit(root, "status"), block("b (nested/b)", "main  ↑1 ↓1\n"))
	out = s.pit(root, "pull", "--no-edit")
	contains(t, out, block("a", "Already up to date.\n\n"), block("b (nested/b)", ""))
	if !s.exists(root, "nested/b/theirs.txt") {
		t.Error("pull didn't bring theirs.txt")
	}
	contains(t, s.pit(root, "push", "-y"), "pushed 1 repo") // a went out above

	// a new branch gets an upstream
	s.git(s.path("forge/a"), "switch", "-q", "-c", "topic")
	s.write(root, "a/t.txt", "t\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Topic")
	contains(t, s.pit(root, "push", "--yes"), block("a", "topic -> origin/topic  (sets upstream)\n"))
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
	contains(t, out, block("a", "main -> origin/main\n"), "Everywhere", "(1/3) Push a: main -> origin/main, 1 commit?", "no answer on stdin")
	if remoteHead("a") != "init a" {
		t.Error("a was pushed without an answer")
	}

	// an unknown answer shows the help and asks again; q skips the rest
	out, err = s.pitInput(root, "x\nn\ny\ns\n", nil, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	contains(t, out, "a - push this repo and all remaining ones", "(2/3) Push b:", "(3/3) Push (pit root):", "pushed 1 repo")
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
	// a block per repo: a header, the output, an empty line
	out := s.pit(root, "each", "--", "rev-parse", "--abbrev-ref", "HEAD")
	want := block("a", "main\n\n") + block("b (nested/b)", "main\n\n") + block("(pit root)", "main\n\n")
	if out != want {
		t.Errorf("each output:\n%s\nwant:\n%s", out, want)
	}
	out = s.pit(root, "each", "-r", "a", "log", "--format=%s")
	contains(t, out, "init a")
	lacks(t, out, "init b")
	contains(t, s.pitFails(root, "each", "frobnicate"), "failed in a, b, (pit root)")
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
	contains(t, out, block("a", "Cloning into 'a'"))
	lacks(t, out, "─── b", "─── c")
	// groups are padded alike, so that the URLs line up
	out = s.pit(root, "list")
	contains(t, out, block("a", "main  [libs, go]  file://"), block("b", "main  [libs]      file://"), block("c", "main              file://"),
		"(not cloned)", "3 externals, 2 not cloned (pit sync); groups: libs, go\n")
	out = s.pit(root, "list", "-g", "libs")
	contains(t, out, block("a", ""), block("b", ""), "2 externals")
	lacks(t, out, "─── c", "(pit root)")

	// status lists the group's uncloned members; other commands leave them out
	out = s.pit(root, "status", "-g", "libs")
	contains(t, out, block("b", "not cloned (pit sync)\n\n"), "1 repo clean")
	lacks(t, out, "─── c")
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
	contains(t, out, block("a", "[main "), block("b", "[main "), block("(pit root)", "[main "))
	lacks(t, out, "─── c")

	contains(t, s.pit(root, "each", "-g", "go", "--", "log", "-1", "--format=%s"), block("a", "Libs\n"))
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

func TestColor(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/README.md", "changed\n")
	const esc = "\x1b["

	// not a terminal: no colors, unless asked for, before the command or after it
	lacks(t, s.pit(root, "status"), esc)
	for _, args := range [][]string{{"--color=always", "status"}, {"status", "--color"}} {
		contains(t, s.pit(root, args...), "\x1b[1;36ma\x1b[0m", "\x1b[32mmain\x1b[0m", "\x1b[31mM\x1b[0m")
	}
	// the last one counts; after "--" they are git's
	lacks(t, s.pit(root, "--color", "status", "--no-color"), esc)
	contains(t, s.pit(root, "--color=always", "each", "-r", "a", "--", "log", "-1", "--no-color", "--format=%s"), "\x1b[1;36ma\x1b[0m", "\ninit a\n")
	// questions and errors on stderr too
	s.git(s.path("forge/a"), "commit", "-qam", "Change a")
	out, _ := s.pitInput(root, "", nil, "--color=always", "push", "-i")
	contains(t, out, "\x1b[1;34m(1/1) Push a:", "\x1b[1;31mpit:\x1b[0m push: no answer")
	contains(t, s.pitFails(root, "--color=sometimes", "status"), `invalid value "sometimes" for --color`)

	// git's output follows: --no-color and NO_COLOR win over the repo's color.ui, --color over NO_COLOR
	s.write(root, "a/README.md", "changed again\n")
	s.git(s.path("forge/a"), "config", "color.ui", "always")
	contains(t, s.pit(root, "diff"), esc)
	lacks(t, s.pit(root, "diff", "--no-color"), esc)
	noColor := []string{"NO_COLOR=1"}
	for _, args := range [][]string{{"diff"}, {"status"}} {
		out, err := s.pitEnv(root, noColor, args...)
		if err != nil || strings.Contains(out, esc) {
			t.Errorf("NO_COLOR pit %s: %v\n%q", args, err, out)
		}
	}
	if out, _ := s.pitEnv(root, noColor, "--color", "diff"); !strings.Contains(out, esc) {
		t.Errorf("NO_COLOR pit --color diff has no colors:\n%q", out)
	}
}

func TestAddPatchNamesRepos(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/README.md", "changed a\n")
	s.write(root, "nested/b/README.md", "changed b\n")
	// git asks hunk by hunk, so each repo gets a block; plain add stays silent
	out, err := s.pitInput(root, "y\nn\n", nil, "add", "-p")
	if err != nil {
		t.Fatalf("pit add -p: %v\n%s", err, out)
	}
	contains(t, out, block("a", "diff --git a/README.md"), block("b (nested/b)", "diff --git a/README.md"))
	if got := s.git(s.path("forge/a"), "diff", "--cached", "--name-only"); got != "README.md" {
		t.Errorf("a staged %q", got)
	}
	if got := s.git(s.path("forge/nested/b"), "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("b staged %q", got)
	}
	if out := s.pit(root, "add", "-A"); out != "" {
		t.Errorf("pit add -A printed %q", out)
	}
}

func TestPushConfirmPushGroups(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	remoteHead := func(name string) string {
		t.Helper()
		return s.git(s.dir, "--git-dir="+s.path("remotes/"+name+".git"), "log", "-1", "--format=%s", "main")
	}
	s.write(root, "pit-externals.json", `{
  "settings": {"confirmPushGroups": {"private": false}},
  "externals": {
    "a": {"url": "`+fileURL(s.path("remotes/a.git"))+`", "path": "a", "groups": ["private"]},
    "b": {"url": "`+fileURL(s.path("remotes/b.git"))+`", "path": "nested/b", "groups": ["public"]}
  }
}`)
	for _, r := range []string{"a", "nested/b"} {
		s.write(root, r+"/x.txt", "x\n")
	}
	s.pit(root, "add", "-A") // the root repo gets the changed config
	s.pit(root, "commit", "-m", "Mixed")

	// pit asks about b and the root repo only; without an answer, a isn't pushed either
	out, err := s.pitInput(root, "", nil, "push")
	if err == nil {
		t.Fatalf("push without answers succeeded:\n%s", out)
	}
	contains(t, out, "pushing a without asking", "(1/2) Push b:", "no answer on stdin")
	lacks(t, out, "Push a:")
	if remoteHead("a") != "init a" {
		t.Error("a was pushed although the questions got no answer")
	}

	// a goes along without a question, b and the root repo as answered
	out, err = s.pitInput(root, "n\ny\n", nil, "push")
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	contains(t, out, "(1/2) Push b:", "(2/2) Push (pit root):", block("a", "To file://"), "pushed 2 repos")
	if remoteHead("a") != "Mixed" || remoteHead("b") != "init b" || remoteHead("forge") != "Mixed" {
		t.Error("expected a and the root repo to be pushed, not b")
	}
	s.git(s.path("forge/nested/b"), "push", "-q")

	// when only repos without questions have something to push, pit asks nothing
	s.write(root, "a/y.txt", "y\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Quiet")
	out = s.pit(root, "push")
	contains(t, out, "pushed 1 repo")
	lacks(t, out, "Push a:", "without asking")
	if remoteHead("a") != "Quiet" {
		t.Error("a wasn't pushed")
	}

	// -i asks about every repo
	s.write(root, "a/z.txt", "z\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Asked")
	contains(t, s.pitFails(root, "push", "-i"), "(1/1) Push a:", "no answer on stdin")

	// "." configures the root repo: now a and the root repo go without a question
	config := strings.Replace(s.read(root, "pit-externals.json"), `{"private": false}`, `{"private": false, ".": false}`, 1)
	s.write(root, "pit-externals.json", config)
	s.git(root, "commit", "-qam", "Root")
	out = s.pit(root, "push")
	contains(t, out, block("a", "To file://"), block("(pit root)", "To file://"), "pushed 2 repos")
	lacks(t, out, "Push ")
	if remoteHead("a") != "Asked" || remoteHead("forge") != "Root" {
		t.Error("expected a and the root repo to be pushed")
	}
}

func TestPushTags(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	remoteTags := func() string {
		t.Helper()
		return strings.ReplaceAll(s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "tag"), "\n", " ")
	}
	remoteHead := func() string {
		t.Helper()
		return s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "log", "-1", "--format=%s", "main")
	}

	// a new tag alone is something to push
	s.git(a, "tag", "v1.0.0")
	out := s.pit(root, "push", "-y")
	contains(t, out, block("a", "tag v1.0.0 -> origin\n\n"), "[new tag]", "pushed 1 repo")
	if remoteTags() != "v1.0.0" {
		t.Errorf("remote tags = %q", remoteTags())
	}
	contains(t, s.pit(root, "push"), "nothing to push")

	// a commit and its tag go together, and the question names the tag
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Release 1.1.0")
	s.git(a, "tag", "v1.1.0")
	out, err := s.pitInput(root, "", nil, "push")
	if err == nil {
		t.Fatalf("push without an answer succeeded:\n%s", out)
	}
	contains(t, out, block("a", "main -> origin/main\n  "), " Release 1.1.0\ntag v1.1.0 -> origin\n\n",
		"(1/1) Push a: main -> origin/main, 1 commit, tag v1.1.0?")
	s.pit(root, "push", "-y")
	if remoteHead() != "Release 1.1.0" || remoteTags() != "v1.0.0 v1.1.0" {
		t.Errorf("remote: main at %q, tags %q", remoteHead(), remoteTags())
	}

	// a tag that isn't on the branch stays local; --tags, which would push it, is refused
	s.git(a, "switch", "-q", "-c", "side")
	s.write(root, "a/side.txt", "s\n")
	s.commitAll(a, "Side")
	s.git(a, "tag", "exp")
	s.git(a, "switch", "-q", "main")
	contains(t, s.pit(root, "push"), "a: not pushing tag exp, not on main", "nothing to push")
	contains(t, s.pitFails(root, "push", "--tags"), "leave out --tags")
	if remoteTags() != "v1.0.0 v1.1.0" {
		t.Errorf("remote tags = %q", remoteTags())
	}

	// a branch that can't be pushed keeps its tag back too
	other := s.path("other-a")
	s.git(s.dir, "clone", "-q", fileURL(s.path("remotes/a.git")), other)
	s.write(other, "theirs.txt", "t\n")
	s.commitAll(other, "Theirs")
	s.git(other, "push", "-q")
	s.write(root, "a/y.txt", "y\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "Release 1.2.0")
	s.git(a, "tag", "v1.2.0")
	contains(t, s.pitFails(root, "push", "-y"), "(can't push)", "nothing was pushed")
	if remoteTags() != "v1.0.0 v1.1.0" {
		t.Errorf("remote tags = %q", remoteTags())
	}
}

func TestTagAndPushTag(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pit(root, "tag"), "all 3 repos without tags")

	// tags are per repo, so pit tags only the repos named
	contains(t, s.pitFails(root, "tag", "v1.0.0"), "give the repos to tag with -r or -g")
	contains(t, s.pitFails(root, "tag", "-r", "a", "bad..name"), `"bad..name" isn't a valid tag name`)
	out := s.pit(root, "tag", "-r", "a", "v1.0.0")
	contains(t, out, block("a", "v1.0.0 on "), " init a\n", "pit push publishes it")
	// all repos are checked before any is tagged
	contains(t, s.pitFails(root, "tag", "-r", "a", "-r", "b", "v1.0.0"), "a: already has a tag v1.0.0", "nothing was tagged")
	if s.git(s.path("forge/nested/b"), "tag") != "" {
		t.Error("b was tagged")
	}

	// the overview: latest tag and the commits since
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "More")
	contains(t, s.pit(root, "tag"), block("a", "v1.0.0, 1 commit since\n\n"), "2 more repos without tags")

	// pushtag tags and pushes, asking as configured; v1.0.0, not pushed yet, goes along
	out, err := s.pitInput(root, "y\n", nil, "pushtag", "-r", "a", "v1.1.0")
	if err != nil {
		t.Fatalf("pushtag: %v\n%s", err, out)
	}
	contains(t, out, "tagged v1.1.0 in a", "(1/1) Push a: main -> origin/main, 1 commit, tags v1.0.0, v1.1.0?", "pushed 1 repo")
	if got := s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "tag"); got != "v1.0.0\nv1.1.0" {
		t.Errorf("remote tags = %q", got)
	}
	// answered with no, the tag stays local; the same name can't be tagged where a remote has it
	out, err = s.pitInput(root, "n\n", nil, "pushtag", "-r", "b", "v0.1.0")
	if err != nil {
		t.Fatalf("pushtag: %v\n%s", err, out)
	}
	contains(t, out, "nothing pushed", "v0.1.0 stays local in b; pit push publishes it")
	s.pit(root, "push", "-y")
	s.git(s.path("forge/nested/b"), "tag", "-d", "v0.1.0")
	contains(t, s.pitFails(root, "pushtag", "-y", "-r", "b", "v0.1.0"), "b: origin already has a tag v0.1.0", "nothing was tagged")
	if s.git(s.path("forge/nested/b"), "tag") != "" {
		t.Error("b was tagged although origin has the tag")
	}
}
