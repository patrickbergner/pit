package main

// End-to-end tests of the edges: what pit does when the environment, a repo or a file isn't as it expects.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitUnreadableRepo(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a, b := s.path("forge/a"), s.path("forge/nested/b")
	s.write(root, "a/x.txt", "x\n")
	s.write(root, "nested/b/y.txt", "y\n")
	s.pit(root, "add", "-A")
	before := s.git(a, "rev-parse", "HEAD")

	// a repo whose status can't be read may have something to commit: nothing is committed anywhere
	index := s.read(b, ".git/index")
	s.write(b, ".git/index", "garbage\n")
	contains(t, s.pitFails(root, "commit", "-m", "Both"), "pit: b: ", "nothing was committed")
	if got := s.git(a, "rev-parse", "HEAD"); got != before {
		t.Error("a was committed although b can't be read")
	}
	s.write(b, ".git/index", index)
	s.pit(root, "commit", "-m", "Both")
}

func TestCommitCommentChar(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.git(root, "config", "core.commentChar", ";")
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "-A")

	// the template's comments start with '#' and are stripped, whatever git is configured to take for comments
	s.write(s.dir, "ed.sh", "{ printf 'From the editor\\n'; cat \"$1\"; } > \"$1.new\" && mv \"$1.new\" \"$1\"\n")
	if out, err := s.pitEnv(root, []string{"GIT_EDITOR=sh '" + filepath.ToSlash(s.path("ed.sh")) + "'"}, "commit"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if got := s.git(s.path("forge/a"), "log", "-1", "--format=%B"); got != "From the editor" {
		t.Errorf("message %q", got)
	}
}

func TestPushUpstreamGone(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	heads := func() string {
		return s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "branch", "--format=%(refname:short)")
	}

	// a branch that was published, merged and deleted on the remote isn't published again
	s.git(a, "switch", "-q", "-c", "topic")
	s.git(a, "push", "-q", "-u", "origin", "topic")
	s.git(a, "push", "-q", "origin", "--delete", "topic")
	s.git(a, "fetch", "-q", "--prune")
	contains(t, s.pit(root, "push", "-y"), "a: skipped, the upstream origin/topic of topic is gone", "nothing to push")
	if heads() != "main" {
		t.Errorf("remote branches = %q", heads())
	}

	// with commits of its own it can't be pushed, and pit says so
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "On the deleted branch")
	contains(t, s.pitFails(root, "push", "-y"), "(can't push)", "the upstream origin/topic of topic is gone", "nothing was pushed")
	if heads() != "main" {
		t.Errorf("remote branches = %q", heads())
	}
}

func TestPushToUpstreamByName(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")

	// the branch goes where the plan says, to its upstream, also where a plain git push would refuse or pick another branch
	s.git(a, "switch", "-q", "-c", "local", "--track", "origin/main")
	s.write(root, "a/x.txt", "x\n")
	s.pit(root, "add", "a")
	s.pit(root, "commit", "-m", "For main")
	contains(t, s.pit(root, "push", "-y"), "local -> origin/main", "pushed 1 repo")
	if got := s.git(s.dir, "--git-dir="+s.path("remotes/a.git"), "log", "-1", "--format=%s", "main"); got != "For main" {
		t.Errorf("remote main is at %q", got)
	}
}

func TestInheritedGitEnvironment(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/x.txt", "x\n")

	// as from a hook of the root repo: each repo is still the one in its directory
	env := []string{"GIT_DIR=" + filepath.Join(root, ".git"), "GIT_WORK_TREE=" + root, "GIT_INDEX_FILE=" + filepath.Join(root, ".git", "index")}
	out, err := s.pitEnv(root, env, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	contains(t, out, block("a", "main\n  ?? a/x.txt\n"), "2 more repos clean and up to date")
}

func TestBrokenClone(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	b := s.path("forge/nested/b")
	s.write(root, "notes.md", "changed\n")
	s.git(root, "add", "-A")

	// a .git that isn't a repository: b fails, and is not taken for the root repo around it
	if err := os.Rename(filepath.Join(b, ".git"), filepath.Join(b, "git.bak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(b, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := s.pitFails(root, "status")
	contains(t, out, "pit: b: ", block("(pit root)", "main\n  M  notes.md\n"))
	lacks(t, out, block("b (nested/b)", ""))
	contains(t, s.pitFails(root, "commit", "-m", "Not in b's name"), "pit: b: ", "nothing was committed")
	if got := s.git(root, "log", "-1", "--format=%s"); got != "track externals" {
		t.Errorf("the root repo got the commit %q", got)
	}
}

func TestRestoreDirectoryOfExternals(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	b := s.path("forge/nested/b")

	// nested/ holds b and nothing of the root repo, which has nothing to restore there
	s.write(b, "README.md", "changed\n")
	s.pit(root, "add", "nested")
	if out := s.pit(root, "restore", "--staged", "nested"); out != "" {
		t.Errorf("restore --staged printed %q", out)
	}
	if got := s.git(b, "status", "--porcelain"); got != "M README.md" {
		t.Errorf("after restore --staged: %q", got)
	}
	s.pit(root, "restore", "nested")
	if got := s.read(b, "README.md"); got != "b\n" {
		t.Errorf("README.md = %q", got)
	}

	// a file of the root repo in there is restored along
	s.write(root, "nested/own.txt", "own\n")
	s.commitAll(root, "own file")
	s.write(root, "nested/own.txt", "changed\n")
	s.write(b, "README.md", "changed\n")
	s.pit(root, "restore", "nested")
	if s.read(root, "nested/own.txt") != "own\n" || s.read(b, "README.md") != "b\n" {
		t.Error("not everything in nested/ was restored")
	}
}

func TestDiffSummaries(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	s.write(root, "a/README.md", "changed\n")
	s.write(root, "nested/b/README.md", "changed\n")

	// a list of files gets a block per repo with differences, as its paths don't tell the repos apart
	out := s.pit(root, "diff", "--stat")
	contains(t, out, block("a", " README.md | 2 +-\n"), block("b (nested/b)", " README.md | 2 +-\n"))
	lacks(t, out, "(pit root)")
	contains(t, s.pit(root, "diff", "--name-only"), block("a", "README.md\n\n"))
	// a patch stays one diff
	out = s.pit(root, "diff")
	contains(t, out, "+++ b/nested/b/README.md")
	lacks(t, out, "───")
}

func TestPushTagChecksFirst(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	contains(t, s.pitFails(root, "pushtag", "-i", "-y", "-r", "a", "v1.0.0"), "-i (ask) and -y (don't ask) exclude each other")
	if got := s.git(s.path("forge/a"), "tag"); got != "" {
		t.Errorf("a was tagged %q although the options were wrong", got)
	}
}

func TestSyncRelativeURL(t *testing.T) {
	s := newSandbox(t)
	s.remote("a", false)
	root := s.root(false, ext{"a", "../remotes/a.git", "a"})
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// relative to the root, wherever pit runs
	s.pit(filepath.Join(root, "sub"), "sync")
	if !s.exists(root, "a/README.md") {
		t.Error("a was not cloned")
	}
}

func TestSyncInsideAnotherRepo(t *testing.T) {
	s := newSandbox(t)
	outer := s.path("outer")
	s.git(s.dir, "init", "-q", outer)
	root := filepath.Join(outer, "forge")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s.config(root, ext{"a", s.remote("a", false), "a"})
	contains(t, s.pit(root, "sync"), "lies inside the git repo "+outer+" without being a repo itself")
	// and that repo is not taken for the root repo
	lacks(t, s.pit(root, "status"), "(pit root)")
}

func TestHelpOption(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	for _, args := range [][]string{{"commit", "--help"}, {"diff", "-h"}, {"push", "--help"}} {
		if out := s.pit(root, args...); !strings.HasPrefix(out, "Usage: pit <command>") {
			t.Errorf("pit %s printed %q", strings.Join(args, " "), out)
		}
	}
	// each's arguments are git's
	out, _ := s.pitEnv(root, nil, "each", "-r", "a", "--", "log", "-h")
	contains(t, out, "usage: git log")
	lacks(t, out, "Usage: pit")
}
