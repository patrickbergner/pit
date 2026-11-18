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
}

func TestSyncClonesAndIgnores(t *testing.T) {
	s := newSandbox(t)
	empty := s.remote("empty", true)
	root := s.root(true, ext{"a", s.remote("a", false), "a"}, ext{"b", s.remote("b", false), "nested/b"}, ext{"e", empty, "e"})
	s.write(root, ".gitignore", "*.log\n")

	// the root directory, then a table with a row per external; a path column, as b's path isn't its name
	out := s.pit(root, "list")
	contains(t, out, "(pit root) "+root+"\n╭", "│ Name │ Path     │ Branch │ State      │ URL ",
		"│ a    │ a        │ main   │ not cloned │ "+fileURL(s.path("remotes/a.git")), "│ b    │ nested/b │ main   │ not cloned │ ",
		"╯\n3 externals, 3 not cloned (pit sync)\n")
	lacks(t, out, "Groups")
	// the root directory, then the directories down to the externals and their siblings (not gone into; .git left out), sorted, with what
	// follows the externals aligned
	for _, d := range []string{"tools/x", "nested/other"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	contains(t, s.pit(root, "tree"), root+" (pit root)\n"+
		"├── a      main  not cloned\n"+
		"├── e      main  not cloned\n"+
		"├── nested\n"+
		"│   ├── b  main  not cloned\n"+
		"│   └── other\n"+
		"└── tools\n"+
		"3 externals, 3 not cloned (pit sync)\n")
	contains(t, s.pit(root, "status"), "not cloned (pit sync)")

	out = s.pit(root, "sync")
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
	// a groups column, padded like the others; no path column, as each path is the external's name
	out = s.pit(root, "list")
	contains(t, out, "│ Name │ Branch │ Groups   │ State      │ URL ", "│ a    │ main   │ libs, go │ cloned     │ file://",
		"│ b    │ main   │ libs     │ not cloned │ file://", "│ c    │ main   │          │ not cloned │ file://",
		"3 externals, 2 not cloned (pit sync); groups: libs, go\n")
	lacks(t, out, "Path")
	out = s.pit(root, "list", "-g", "libs")
	contains(t, out, "│ a ", "│ b ", "2 externals")
	lacks(t, out, "│ c ", "(pit root)")
	// tree: a groups column too, and only the selected externals
	out = s.pit(root, "tree", "-g", "libs")
	contains(t, out, root+" (pit root)\n"+
		"├── a  main  libs, go  cloned\n"+
		"└── b  main  libs      not cloned\n"+
		"2 externals, 1 not cloned (pit sync); groups: libs, go\n")
	lacks(t, out, "── c")

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

func TestRm(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	for _, dir := range []string{a, b, root} {
		s.write(dir, "old.txt", "old\n")
		s.commitAll(dir, "add old.txt")
	}

	// each path is removed in the repo owning it; git lists the files relative to the repo, so each repo gets a block
	out := s.pit(s.path("forge/nested"), "rm", "b/old.txt", "../a/old.txt", "../old.txt")
	contains(t, out, block("a", "rm 'old.txt'\n"), block("b (nested/b)", "rm 'old.txt'\n"), block("(pit root)", "rm 'old.txt'\n"))
	for _, dir := range []string{a, b, root} {
		if s.exists(dir, "old.txt") {
			t.Errorf("%s: old.txt is still there", dir)
		}
		if got := s.git(dir, "diff", "--cached", "--name-status"); got != "D\told.txt" {
			t.Errorf("%s: staged %q", dir, got)
		}
	}
	contains(t, s.pit(root, "commit", "-m", "Remove old.txt"), block("a", "[main "), block("(pit root)", "[main "))

	// -n only shows what would be removed
	contains(t, s.pit(root, "rm", "-n", "notes.md"), block("(pit root)", "rm 'notes.md'\n"))
	if !s.exists(root, "notes.md") || s.git(root, "status", "--porcelain") != "" {
		t.Error("rm -n changed the root repo")
	}
	// --cached untracks and keeps the file; -q lists nothing
	if out := s.pit(root, "rm", "-q", "--cached", "nested/b/README.md"); out != "" {
		t.Errorf("rm -q printed %q", out)
	}
	if !s.exists(b, "README.md") || s.git(b, "ls-files") != "" {
		t.Error("rm --cached didn't just untrack README.md in b")
	}
	contains(t, s.pitFails(root, "rm"), "rm: give the paths to remove")
}

func TestRmChecksAllReposFirst(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "dir/x.txt", "x\n")
	s.commitAll(a, "add dir")

	// a file with changes in one repo: git's reason is shown in its block, and nothing is removed anywhere
	s.write(a, "README.md", "changed\n")
	out := s.pitFails(root, "rm", "a/README.md", "nested/b/README.md")
	contains(t, out, block("a", "error: the following file has local modifications:"), "rm: nothing removed, as git rm would fail in a")
	lacks(t, out, "─── b")
	if !s.exists(b, "README.md") || s.git(b, "status", "--porcelain") != "" {
		t.Error("b was changed")
	}
	contains(t, s.pitFails(root, "rm", "nested/b/nope.txt"), block("b (nested/b)", "fatal: pathspec 'nope.txt' did not match any files"))

	// directories need --recursive, as -r is pit's and selects repos
	contains(t, s.pitFails(root, "rm", "a/dir"), "not removing 'dir' recursively without -r")
	contains(t, s.pitFails(root, "rm", "-r", "a/dir"),
		`rm: unknown repo "a/dir"; -r selects repos in pit, so remove a directory with --recursive`)
	contains(t, s.pitFails(root, "rm", "-rf", "a/dir"), `rm: unknown repo "f"; -r selects repos in pit`)
	contains(t, s.pit(root, "rm", "--recursive", "a/dir"), block("a", "rm 'dir/x.txt'\n"))

	// an external's directory, or one holding externals, would empty them
	contains(t, s.pitFails(root, "rm", "--recursive", "nested"), "rm: the paths cover all of b; pit rm removes files, not externals")
	contains(t, s.pitFails(a, "rm", "--recursive", "."), "rm: the paths cover all of a;")
	if got := s.git(b, "status", "--porcelain"); got != "" {
		t.Errorf("b was changed: %q", got)
	}
}

func TestMv(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "src/x.go", "x\n")
	s.write(a, "src/y.go", "y\n")
	s.write(a, "dest/keep.txt", "keep\n")
	s.commitAll(a, "add src and dest")

	// from the root, git mv runs in the repo owning the paths, with the paths as it sees them; it is silent
	if out := s.pit(root, "mv", "a/src/x.go", "a/src/z.go"); out != "" {
		t.Errorf("mv printed %q", out)
	}
	if got := s.git(a, "status", "--porcelain"); got != "R  src/x.go -> src/z.go" {
		t.Errorf("a after mv: %q", got)
	}
	// several sources into a directory; -v lists the moves in the repo's block
	out := s.pit(s.path("forge/a/src"), "mv", "-v", "y.go", "z.go", "../dest")
	contains(t, out, block("a", "Renaming src/y.go to dest/y.go\nRenaming src/z.go to dest/z.go\n"))
	if !s.exists(a, "dest/y.go") || !s.exists(a, "dest/z.go") || s.git(a, "ls-files", "src") != "" {
		t.Error("the files didn't move into dest")
	}
	// -n only shows
	out = s.pit(b, "mv", "-n", "README.md", "README.txt")
	contains(t, out, block("b (nested/b)", "Checking rename of 'README.md' to 'README.txt'\nRenaming README.md to README.txt\n"))
	if !s.exists(b, "README.md") {
		t.Error("mv -n moved README.md")
	}

	contains(t, s.pitFails(root, "mv", "notes.md"), "mv: give the sources and the destination")
	contains(t, s.pitFails(root, "mv", "-r", "a", "x", "y"), "mv: the paths decide the repo; -r and -g don't apply")
}

func TestMvRefusals(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "b", "a file named like the external b\n")
	s.commitAll(root, "add b")

	// between repos, with the way to do it by hand
	out := s.pitFails(root, "mv", "a/README.md", "nested/b/A.md")
	contains(t, out, "a/README.md is in a, its destination in b, and git can't move files between repos; "+
		"move it yourself, then run pit add a/README.md nested/b/A.md to stage both sides", "mv: nothing was moved")
	// into a directory, the new path decides: an external's directory, or an external below the directory
	contains(t, s.pitFails(root, "mv", "notes.md", "a"), "notes.md is in (pit root), its destination in a,", "pit add notes.md a/notes.md")
	contains(t, s.pitFails(root, "mv", "b", "nested"), "b is in (pit root), its destination in b,", "pit add b nested/b")

	// an external and a directory holding one, both reported at once
	out = s.pitFails(root, "mv", "nested/b", "nested", "moved")
	contains(t, out, "nested/b is the external b; to move it, change its path in pit-externals.json, move the directory and run pit sync",
		"nested holds the externals b, which git mv would move along without updating pit-externals.json")

	for _, dir := range []string{"forge", "forge/a", "forge/nested/b"} {
		if got := s.git(s.path(dir), "status", "--porcelain"); got != "" {
			t.Errorf("%s changed: %q", dir, got)
		}
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

func TestNestedExternals(t *testing.T) {
	s := newSandbox(t)
	// app has plugins/core and plugins/extra of its own, and myplugin goes to plugins/myplugin
	app := s.remote("app", false)
	seed := s.path("seed/app")
	s.write(seed, "plugins/core/core.txt", "core\n")
	s.write(seed, "plugins/extra/extra.txt", "extra\n")
	s.commitAll(seed, "plugins")
	s.git(seed, "push", "-q", "origin", "main")
	plugin := ext{"myplugin", s.remote("myplugin", false), "app/plugins/myplugin"}
	root := s.root(true, plugin, ext{"app", app, "app"}) // myplugin listed first

	// myplugin can't be cloned before app
	contains(t, s.pitFails(root, "sync", "-r", "myplugin"), "app/plugins/myplugin lies inside app, which isn't cloned yet")
	if s.exists(root, "app") {
		t.Error("sync -r myplugin created app/")
	}

	// app first, and keeping myplugin out of it before myplugin is cloned
	out := s.pit(root, "sync")
	contains(t, out, block("app", "Cloning into 'app'...\n"),
		"pit: updated .git/info/exclude, which keeps plugins/myplugin out of this repo\n\n"+
			block("myplugin (app/plugins/myplugin)", "Cloning into 'app/plugins/myplugin'...\n"))
	contains(t, s.read(root, "app/.git/info/exclude"), "\n# >>> pit externals - managed by pit, do not edit\n/plugins/myplugin/\n# <<< pit externals\n")
	contains(t, s.read(root, ".gitignore"), "\n/.pit/\n/app/\n# <<< pit externals") // myplugin is app's business
	appDir := s.path("forge/app")
	if st := s.git(appDir, "status", "--porcelain", "--ignored"); st != "!! plugins/myplugin/" {
		t.Errorf("app status = %q", st)
	}
	contains(t, s.pit(root, "sync"), "all 2 externals already cloned")
	contains(t, s.pit(root, "tree"), root+" (pit root)\n"+
		"└── app               main  cloned\n"+
		"    └── plugins\n"+
		"        ├── core\n"+
		"        ├── extra\n"+
		"        └── myplugin  main  cloned\n")

	// every file belongs to the innermost repo holding it
	s.write(root, "app/plugins/core/core.txt", "core2\n")
	s.write(root, "app/plugins/new.txt", "new\n")
	s.write(root, "app/plugins/myplugin/README.md", "myplugin2\n")
	out = s.pit(root, "status")
	contains(t, out, "   M app/plugins/myplugin/README.md\n", "   M app/plugins/core/core.txt\n", "  ?? app/plugins/new.txt\n")
	lacks(t, out, "?? app/plugins/myplugin")
	out = s.pit(root, "status", "app/plugins/myplugin")
	contains(t, out, "app/plugins/myplugin/README.md")
	lacks(t, out, "core.txt")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Update the plugins")
	if got := s.git(appDir, "show", "--name-only", "--format=", "HEAD"); got != "plugins/core/core.txt\nplugins/new.txt" {
		t.Errorf("app's commit has %q", got)
	}
	if got := s.git(s.path("forge/app/plugins/myplugin"), "show", "--name-only", "--format=", "HEAD"); got != "README.md" {
		t.Errorf("myplugin's commit has %q", got)
	}

	// dropped from the config: app's block goes, and myplugin's clone stays
	s.config(root, ext{"app", app, "app"})
	out = s.pit(root, "status")
	contains(t, out, "pit: warning: .git/info/exclude of app doesn't list the current externals inside it; run pit sync")
	contains(t, s.pitFails(root, "add", "-A"), "run pit sync first")
	out = s.pit(root, "sync")
	contains(t, out, block("app", "pit: app/plugins/myplugin is no longer an external and no longer ignored; its directory was left as it is\n"+
		"pit: removed pit's block from .git/info/exclude\n"))
	lacks(t, s.read(root, "app/.git/info/exclude"), "pit externals")
	if !s.exists(root, "app/plugins/myplugin/README.md") {
		t.Error("sync deleted a dropped external's clone")
	}

	// an external can't go where the repo holding it has files
	s.config(root, ext{"app", app, "app"}, ext{"other", s.remote("other", false), "app/plugins/core"})
	contains(t, s.pitFails(root, "sync"), block("other (app/plugins/core)", "pit: app tracks files in plugins/core; an external can only go where"))
	if s.read(root, "app/plugins/core/core.txt") != "core2\n" {
		t.Error("sync touched app/plugins/core")
	}
}

func TestLogFilters(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/x.txt", "x\n")
	s.write(root, "nested/b/x.txt", "x\n")
	s.pit(root, "add", "-A")
	s.pit(root, "commit", "-m", "Add x")

	// options that choose commits go to git log, and the grouping stays
	out := s.pit(root, "log", "--grep=Add x", "--oneline")
	contains(t, out, "Add x  [a ", ", b ")
	lacks(t, out, "init a", "track externals")
	contains(t, s.pit(root, "log", "-Sx", "--oneline", "a"), "Add x  [a ")
	if out := s.pit(root, "log", "--author=nobody"); out != "" {
		t.Errorf("log --author=nobody printed %q", out)
	}
	// options that change git's output, which pit reads, are refused, as are values in a separate argument
	contains(t, s.pitFails(root, "log", "--stat"), "pit log takes only git log options that choose commits", "--stat isn't one")
	contains(t, s.pitFails(root, "log", "--author", "x"), "give --author its value in the same argument (--author=<value>)")
	contains(t, s.pitFails(root, "log", "-S"), "give -S its value in the same argument (-S<value>)")
}

func TestShow(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	b := s.path("forge/nested/b")
	s.write(root, "a/README.md", "changed a\n")
	s.write(b, "README.md", "changed b\n")
	s.pit(root, "commit", "-am", "Change both")

	// the newest pit commit: its log entry, then one diff with the repos' paths
	out := s.pit(root, "show")
	contains(t, out, "commit a ", ", b ", "    Change both\n", "+++ b/a/README.md", "+changed a", "+++ b/nested/b/README.md")
	lacks(t, out, "notes.md", "init a")
	// a commit of one repo finds the others of its pit commit
	hash := s.git(b, "rev-parse", "--short", "HEAD")
	contains(t, s.pit(root, "show", hash), "+++ b/a/README.md", "+++ b/nested/b/README.md")
	// a list of files gets a block per repo
	contains(t, s.pit(root, "show", "--stat", hash), block("a", " README.md | 2 +-\n"), block("b (nested/b)", " README.md | 2 +-\n"))
	// -r narrows the repos
	out = s.pit(root, "show", "-r", "a", "HEAD")
	contains(t, out, "+++ b/a/README.md")
	lacks(t, out, "nested/b")

	contains(t, s.pitFails(root, "show", "HEAD"),
		"HEAD names a commit in a, b, (pit root); give more of its hash, or select the repo with -r")
	contains(t, s.pitFails(root, "show", "nonexistent"), "nonexistent is no commit in any of the repos")
}

func TestGrep(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/x.txt", "a needle\n")
	s.write(root, "nested/b/y.txt", "b needle\n")
	s.pit(root, "add", "-A")

	// each repo with matches gets a block, as git prints paths relative to the repo
	out := s.pit(root, "grep", "-n", "needle")
	contains(t, out, block("a", "x.txt:1:a needle\n"), block("b (nested/b)", "y.txt:1:b needle\n"))
	lacks(t, out, "(pit root)")
	// -e gives the patterns, paths narrow the repos
	out = s.pit(root, "grep", "-e", "needle", "nested")
	contains(t, out, block("b (nested/b)", "y.txt:b needle\n"))
	lacks(t, out, block("a", ""))
	// nothing found anywhere fails, as with git grep
	if out := s.pitFails(root, "grep", "nothing-like-this"); out != "" {
		t.Errorf("grep without matches printed %q", out)
	}
	contains(t, s.pitFails(root, "grep"), "grep: give the pattern to search for")
}

func TestCommitPaths(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "README.md", "changed a\n")
	s.write(a, "other.txt", "other\n")
	s.git(a, "add", "other.txt") // staged, but not among the paths
	s.write(b, "README.md", "changed b\n")
	s.write(root, "notes.md", "changed\n")

	// just the paths, staged or not, each in the repo owning it
	out := s.pit(root, "commit", "-m", "Change the READMEs", "a/README.md", "nested/b/README.md")
	contains(t, out, block("a", "[main "), block("b (nested/b)", "[main "))
	lacks(t, out, "(pit root)")
	if got := s.git(a, "show", "--name-only", "--format=", "HEAD"); got != "README.md" {
		t.Errorf("a committed %q", got)
	}
	if got := s.git(a, "status", "--porcelain"); got != "A  other.txt" {
		t.Errorf("a after the commit: %q", got)
	}
	if got := s.git(root, "status", "--porcelain"); got != "M notes.md" {
		t.Errorf("the root repo after the commit: %q", got)
	}

	// a new file has to be added first; then nothing is committed anywhere
	s.write(b, "new.txt", "new\n")
	out = s.pitFails(root, "commit", "-m", "x", "nested/b/new.txt", "notes.md")
	contains(t, out, "b: pathspec 'new.txt' did not match any file(s) known to git; pit add new files first",
		"commit: nothing was committed")
	if got := s.git(root, "log", "-1", "--format=%s"); got != "track externals" {
		t.Errorf("the root repo got the commit %q", got)
	}
	contains(t, s.pitFails(root, "commit", "-m", "x", "a/README.md"), "nothing to commit in these paths")
	contains(t, s.pitFails(root, "commit", "-a", "-m", "x", "notes.md"), "commit: paths don't go with -a or --amend")
}

func TestRevert(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "README.md", "changed a\n")
	s.write(b, "README.md", "changed b\n")
	s.pit(root, "commit", "-am", "Change both")
	aHash, bHash := s.git(a, "rev-parse", "HEAD"), s.git(b, "rev-parse", "HEAD")

	// a commit of one repo reverts the whole pit commit, as one new pit commit with git's wording
	out := s.pit(root, "revert", "--no-edit", aHash[:7])
	contains(t, out, block("a", "[main "), block("b (nested/b)", "[main "), `Revert "Change both"`)
	if s.read(a, "README.md") != "a\n" || s.read(b, "README.md") != "b\n" {
		t.Error("the changes weren't reverted")
	}
	want := "Revert \"Change both\"\n\nThis reverts commit " + aHash + " in a, commit " + bHash + " in b."
	for _, dir := range []string{a, b} {
		if got := s.git(dir, "log", "-1", "--format=%B"); got != want {
			t.Errorf("%s: message %q", dir, got)
		}
	}
	contains(t, s.pit(root, "log", "--oneline", "-1"), `Revert "Change both"  [a `, ", b ")

	// uncommitted changes in one of the repos: refused up front
	s.write(b, "README.md", "dirty\n")
	contains(t, s.pitFails(root, "revert", "-m", "x", aHash), "b: has uncommitted changes; commit or stash them first",
		"revert: nothing was reverted")
	contains(t, s.pitFails(root, "revert"), "revert: give a commit of the pit commit to revert")
}

func TestRevertConflict(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "README.md", "first\n")
	s.write(b, "README.md", "first\n")
	s.pit(root, "commit", "-am", "First")
	first := s.git(b, "rev-parse", "HEAD")
	s.write(a, "README.md", "second\n")
	s.commitAll(a, "Second")

	// a conflicts and stops the revert before b; pit commit --continue finishes it once resolved
	out := s.pitFails(root, "revert", "--no-edit", first)
	contains(t, out, "a: git revert of ", "failed; resolve the conflicts there", "not committed yet: a, b")
	s.write(a, "README.md", "a\n")
	s.git(a, "add", "README.md")
	contains(t, s.pit(root, "commit", "--continue"), block("a", "[main "), block("b (nested/b)", "[main "))
	for _, dir := range []string{a, b} {
		if got := s.git(dir, "log", "-1", "--format=%s"); got != `Revert "First"` {
			t.Errorf("%s: last commit %q", dir, got)
		}
	}
	if s.read(b, "README.md") != "b\n" {
		t.Error("b wasn't reverted")
	}
}

func TestStash(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(a, "README.md", "changed a\n")
	s.write(root, "notes.md", "changed\n")

	// a stash in each repo with changes, as one pit stash
	out := s.pit(root, "stash", "-m", "wip")
	contains(t, out, block("a", "Saved working directory and index state On main: wip\n"),
		block("(pit root)", "Saved working directory and index state On main: wip\n"))
	lacks(t, out, block("b (nested/b)", ""))
	if s.read(a, "README.md") != "a\n" || s.read(root, "notes.md") != "notes\n" {
		t.Error("the changes weren't stashed")
	}
	// b gets a stash of its own with git, which pit leaves alone
	s.write(b, "README.md", "changed b\n")
	s.git(b, "stash")
	// a second pit stash; the list shows the newest first
	s.write(a, "README.md", "second\n")
	s.pit(root, "stash", "-m", "second")
	contains(t, s.pit(root, "stash", "list"), "0  On main: second  [a]\n1  On main: wip  [a, (pit root)]\n")

	// pop takes the number of a pit stash and pops exactly its stashes
	s.pit(root, "stash", "pop", "1")
	if s.read(a, "README.md") != "changed a\n" || s.read(root, "notes.md") != "changed\n" || s.read(b, "README.md") != "b\n" {
		t.Error("pop 1 didn't bring back just the first pit stash")
	}
	if got := s.git(b, "stash", "list", "--format=%s"); !strings.HasPrefix(got, "WIP on main") {
		t.Errorf("b's own stash: %q", got)
	}
	if got := s.pit(root, "stash", "list"); got != "0  On main: second  [a]\n" {
		t.Errorf("stash list after the pop: %q", got)
	}
	// a stash dropped with git since is left out
	s.git(a, "stash", "drop")
	contains(t, s.pit(root, "stash", "drop"), "a: its stash ", " is gone (popped or dropped with git?); leaving it out")
	if got := s.pit(root, "stash", "list"); got != "" {
		t.Errorf("stash list after the drop: %q", got)
	}
	contains(t, s.pitFails(root, "stash", "pop"), "stash pop: there is no pit stash")
	contains(t, s.pit(root, "stash", "-r", "b"), "no local changes to save")
	contains(t, s.pitFails(root, "stash", "--all"), "stash: --all would stash ignored files too")
	// untracked files with -u
	s.write(b, "new.txt", "new\n")
	contains(t, s.pit(root, "stash", "-u", "-r", "b"), block("b (nested/b)", "Saved working directory"))
	if s.exists(b, "new.txt") {
		t.Error("stash -u left new.txt")
	}
}

func TestPager(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	paged := []string{"PIT_TEST_TERMINAL=1", `GIT_PAGER=sed -e "s/^/| /"`}

	// with a terminal, the output goes through git's pager, which gets LESS=FRX as from git
	out, err := s.pitEnv(root, paged, "log", "--oneline")
	if err != nil || !strings.HasPrefix(out, "| ") || strings.Contains(out, "\n2") {
		t.Errorf("log through the pager: %v\n%s", err, out)
	}
	out, _ = s.pitEnv(root, []string{"PIT_TEST_TERMINAL=1", `GIT_PAGER=echo "LESS=$LESS"; cat >/dev/null`}, "log")
	contains(t, out, "LESS=FRX")
	// --no-pager, commands that don't page, and no terminal
	for _, args := range [][]string{{"--no-pager", "log"}, {"log", "--no-pager"}, {"status"}} {
		out, _ := s.pitEnv(root, paged, args...)
		lacks(t, out, "| ")
	}
	lacks(t, s.pit(root, "log"), "| ")

	// quitting the pager before the end is no failure
	s.write(root, "a/big.txt", strings.Repeat("a line of a big file\n", 20000))
	s.pit(root, "add", "a/big.txt")
	if out, err := s.pitEnv(root, []string{"PIT_TEST_TERMINAL=1", "GIT_PAGER=head -n 1"}, "diff", "--staged"); err != nil {
		t.Errorf("pit diff with a pager quit early: %v\n%s", err, out)
	}
}

func TestCompletion(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pit(root, "completion", "bash"), "complete -o default -F _pit pit")
	contains(t, s.pit(root, "completion", "powershell"), "Register-ArgumentCompleter -Native -CommandName pit")
	contains(t, s.pitFails(root, "completion", "fish"), "completion: give the shell: bash or powershell")
	// the root's repos and groups come from its config
	if got := s.pit(s.path("forge/a"), "__complete", "_", "status", "-r"); got != ".\na\nb\n" {
		t.Errorf("repo names: %q", got)
	}
	if got := s.pit(root, "__complete", "_st"); got != "stash\nstatus\n" {
		t.Errorf("commands: %q", got)
	}
}

// mergedFeature gives repo dir a branch feature with f.txt, merged into main without fast-forward.
func (s *sandbox) mergedFeature(dir string) {
	s.t.Helper()
	s.git(dir, "switch", "-q", "-c", "feature")
	s.write(dir, "f.txt", "f\n")
	s.commitAll(dir, "Add f")
	s.git(dir, "switch", "-q", "main")
	s.write(dir, "README.md", "changed\n")
	s.commitAll(dir, "Change README")
	s.git(dir, "merge", "-q", "--no-ff", "-m", "Merge feature", "feature")
}

func TestGraph(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	s.mergedFeature(a)

	// the repo the current directory is in, as git draws it: one line per commit, with branch names
	out := s.pit(s.path("forge/a/sub-does-not-matter/.."), "graph")
	contains(t, out, block("a", "*   "), " Merge feature\n", "|\\", "(feature) Add f", "init a")
	lacks(t, out, "track externals", "init b")
	// at the root, it is the root repo; -r selects others
	out = s.pit(root, "graph")
	contains(t, out, block("(pit root)", "* "), "track externals")
	lacks(t, out, "Merge feature")
	contains(t, s.pit(root, "graph", "-r", "b"), block("b (nested/b)", "* "), "init b")
	// git's options and paths, relative to the current directory; a format of one's own replaces the one-line default
	out = s.pit(a, "graph", "--format=%s", "--", "f.txt")
	contains(t, out, "Add f")
	lacks(t, out, "init a", "Change README")
	contains(t, s.pitFails(root, "graph", "--", "a/f.txt"), "the paths lie outside (pit root)")
}

func TestChanges(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	s.write(a, "x.txt", "x\n")
	s.write(a, "README.md", "first\n")
	s.commitAll(a, "First")
	first := s.git(a, "rev-parse", "--short", "HEAD")
	s.git(a, "mv", "x.txt", "y.txt")
	s.write(a, "z.txt", "z\n")
	s.commitAll(a, "Second")
	second := s.git(a, "rev-parse", "--short", "HEAD")

	// a commit compared with the one before it, in the repo the current directory is in
	out := s.pit(a, "changes", "HEAD")
	contains(t, out, block("a", "from "+first+" First\nto   "+second+" Second\n"), "  R x.txt -> y.txt\n", "  A z.txt\n")
	lacks(t, out, "README")
	// a hash from another repo is found there; the paths are relative to the current directory
	out = s.pit(root, "changes", first)
	contains(t, out, block("a", "from "), "  M a/README.md\n", "  A a/x.txt\n")
	// with another revision
	out = s.pit(a, "changes", first, "HEAD")
	contains(t, out, "from "+first+" First\nto   "+second+" Second\n", "  R x.txt -> y.txt\n")
	// the first commit, compared with nothing
	contains(t, s.pit(a, "changes", "HEAD~2"), "from nothing, as it is the first commit\n", "  A README.md\n")
	// a list of git's own, and paths
	contains(t, s.pit(a, "changes", "--stat", "HEAD"), " x.txt => y.txt | 0\n", " 2 files changed, 1 insertion(+)")
	out = s.pit(a, "changes", "HEAD", "--", "z.txt")
	contains(t, out, "  A z.txt\n")
	lacks(t, out, "y.txt")

	// a merge, compared with its first parent: what the merge brought
	s.mergedFeature(a)
	out = s.pit(a, "changes", "HEAD")
	contains(t, out, " Change README\nto   ", " Merge feature\n", "  A f.txt\n")
	lacks(t, out, "  M README.md")

	contains(t, s.pitFails(root, "changes"), "changes: give a commit")
	contains(t, s.pitFails(root, "changes", "nonexistent"), "nonexistent is no commit in any of the repos")
	contains(t, s.pitFails(a, "changes", "HEAD", "nonexistent"), "changes: nonexistent is no commit in a")
}
