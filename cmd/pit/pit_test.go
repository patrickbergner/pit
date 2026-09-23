package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickbergner/pit/internal/hook"
)

func TestHelpVersionAndUnknownCommand(t *testing.T) {
	s := newSandbox(t)
	contains(t, s.pit(s.dir), "Usage: pit <command>")
	contains(t, s.pit(s.dir, "version"), "pit ")
	contains(t, s.pitFails(s.dir, "frobnicate"), "Usage: pit <command>")
}

func TestOutsideRepoAndBadConfig(t *testing.T) {
	s := newSandbox(t)
	contains(t, s.pitFails(s.dir, "list"), "not inside a git working tree")

	repo := s.path("repo")
	s.git(s.dir, "init", "-q", "repo")
	contains(t, s.pitFails(repo, "list"), "config not found")
	s.write(repo, "externals.json", "{ nope")
	contains(t, s.pitFails(repo, "list"), "invalid JSON")
	s.write(repo, "externals.json", `{"externals": {"x": {"url": "u"}}}`)
	contains(t, s.pitFails(repo, "status", "x"), "needs 'url' and 'repoPath'")
	contains(t, s.pitFails(repo, "status", "nope"), "unknown external 'nope'")
	contains(t, s.pitFails(repo, "status", "../x"), "invalid external name")
}

func TestListAndStatusBeforeFirstPublish(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "libs/lib"}})

	contains(t, s.pit(mono, "list"), "lib", "libs/lib", "main", extRepo)
	out := s.pit(mono, "status")
	contains(t, out, "(monorepo)", "origin/main", "in sync", "external repo empty; create libs/lib, then pit push")

	s.write(mono, "libs/lib/a.txt", "a\n")
	s.commitAll(mono, "lib: a")
	contains(t, s.pit(mono, "status"), "1 to push, 0 to pull", "external repo empty; 1 commit(s) to push")
}

func TestPushPublishesOnlyTheSubtree(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "secret.txt", "monorepo stuff\n")
	s.commitAll(mono, "add secret")
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")

	out := s.pit(mono, "push", "-y")
	contains(t, out, "Push plan", "EXTERNAL lib", "publish 1 commit(s)", "lib: add a", "done: monorepo lib")
	lacks(t, out, "add secret\n         ") // monorepo commit messages are listed only under MONOREPO

	// The external repo has the subtree at its root and nothing else.
	c := s.clone(extRepo, "extclone")
	if got := s.read(c, "a.txt"); got != "a\n" {
		t.Errorf("a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(c, "secret.txt")); err == nil {
		t.Error("secret.txt was published")
	}
	secret := s.git(mono, "rev-parse", "HEAD:secret.txt")
	objects := s.git(s.path("external.git"), "rev-list", "--objects", "--all")
	if strings.Contains(objects, secret) {
		t.Error("the secret blob reached the external repo")
	}
	lacks(t, s.git(s.path("external.git"), "log", "--format=%s", "--all"), "add secret", "init")

	// The monorepo got everything, including the rejoin merge.
	head := s.git(mono, "rev-parse", "HEAD")
	if got := s.git(s.path("origin.git"), "rev-parse", "main"); got != head {
		t.Errorf("origin main = %s, want %s", got, head)
	}
	contains(t, s.git(mono, "log", "-1", "--format=%B"), "Split 'lib/' into commit", "git-subtree-split: ")

	contains(t, s.pit(mono, "push", "-y"), "everything is up to date")
	contains(t, s.pit(mono, "status"), "lib ", "in sync")
}

func TestPushDryRunAndConfirmation(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	head := s.git(mono, "rev-parse", "HEAD")

	contains(t, s.pit(mono, "push", "-n"), "Push plan", "dry run: nothing was pushed")

	out, err := s.pitIn(mono, "", "push") // no one to answer
	if err == nil {
		t.Fatal("push without an answer succeeded")
	}
	contains(t, out, "re-run with --yes")

	out, err = s.pitIn(mono, "n\n", "push")
	if err == nil {
		t.Fatal("push answered 'n' succeeded")
	}
	contains(t, out, "aborted; nothing was pushed")

	if s.git(s.path("external.git"), "for-each-ref") != "" {
		t.Error("external repo got refs")
	}
	if got := s.git(mono, "rev-parse", "HEAD"); got != head {
		t.Error("HEAD moved (rejoin committed) without executing the plan")
	}

	out, err = s.pitIn(mono, "y\n", "push")
	if err != nil {
		t.Fatalf("push answered 'y': %v\n%s", err, out)
	}
	contains(t, out, "done: monorepo lib")
}

func TestPullBringsExternalCommits(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	s.pit(mono, "push", "-y")

	c := s.clone(extRepo, "contrib")
	s.write(c, "b.txt", "b\n")
	s.commitAll(c, "external: add b")
	s.git(c, "push", "-q", "origin", "main")

	contains(t, s.pit(mono, "status"), "0 to push, 1 to pull")
	contains(t, s.pit(mono, "pull"), "monorepo: already up to date", "lib: merging pit-lib/main into lib")
	if got := s.read(mono, "lib/b.txt"); got != "b\n" {
		t.Errorf("lib/b.txt = %q", got)
	}
	contains(t, s.pit(mono, "pull"), "lib: already up to date")

	// Nothing to publish (the external commit is already there); the merge goes monorepo.
	out := s.pit(mono, "push", "-y")
	contains(t, out, "lib → "+extRepo+" (main): up to date", "done: monorepo")
	contains(t, s.pit(mono, "status"), "in sync")

	// Our changes on top of theirs publish as a fast-forward.
	s.write(mono, "lib/a.txt", "a2\n")
	s.commitAll(mono, "lib: change a")
	contains(t, s.pit(mono, "push", "-y"), "publish 1 commit(s)", "done: monorepo lib")
	s.git(c, "pull", "-q")
	if got := s.read(c, "a.txt"); got != "a2\n" {
		t.Errorf("external a.txt = %q", got)
	}
}

func TestPushRefusesWhenExternalIsAhead(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	s.pit(mono, "push", "-y")

	c := s.clone(extRepo, "contrib")
	s.write(c, "b.txt", "b\n")
	s.commitAll(c, "external: add b")
	s.git(c, "push", "-q", "origin", "main")
	extHead := s.git(s.path("external.git"), "rev-parse", "main")
	originHead := s.git(s.path("origin.git"), "rev-parse", "main")

	s.write(mono, "lib/c.txt", "c\n")
	s.commitAll(mono, "lib: add c")
	contains(t, s.pitFails(mono, "push", "-y"), "external repo has commits missing from the monorepo", "nothing was pushed")
	if s.git(s.path("external.git"), "rev-parse", "main") != extHead || s.git(s.path("origin.git"), "rev-parse", "main") != originHead {
		t.Error("something was pushed")
	}

	s.pit(mono, "pull")
	contains(t, s.pit(mono, "push", "-y"), "done: monorepo lib")
}

func TestPushRefusesWhenMonorepoDiverged(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})

	other := s.clone(fileURL(s.path("origin.git")), "other")
	s.write(other, "other.txt", "x\n")
	s.commitAll(other, "other: change")
	s.git(other, "push", "-q", "origin", "main")

	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	out := s.pitFails(mono, "push", "-y")
	contains(t, out, "CANNOT PUSH", "has commits you don't have; run pit pull first")

	contains(t, s.pit(mono, "pull"), "monorepo: merging origin/main into main")
	contains(t, s.pit(mono, "push", "-y"), "done: monorepo lib")
}

func TestPullRequiresCleanTree(t *testing.T) {
	s := newSandbox(t)
	mono := s.monorepo(map[string]ext{"lib": {s.bare("external"), "lib"}})
	s.write(mono, "externals.json", s.read(mono, "externals.json")+" ")
	contains(t, s.pitFails(mono, "pull"), "uncommitted changes")
}

func TestAddImportsExternalRepo(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	empty := s.bare("empty")
	c := s.clone(extRepo, "tool")
	s.write(c, "x.txt", "x\n")
	s.commitAll(c, "tool: x")
	s.git(c, "push", "-q", "origin", "main")
	mono := s.monorepo(map[string]ext{"tool": {extRepo, "tools/tool"}, "blank": {empty, "blank"}})

	contains(t, s.pitFails(mono, "add"), "usage: pit add")
	contains(t, s.pitFails(mono, "add", "blank"), "external repo has no 'main' branch yet")
	contains(t, s.pit(mono, "add", "tool"), "tool: importing "+extRepo)
	if got := s.read(mono, "tools/tool/x.txt"); got != "x\n" {
		t.Errorf("x.txt = %q", got)
	}
	contains(t, s.git(mono, "log", "--format=%s"), "pit: add tool from "+extRepo, "tool: x")
	contains(t, s.pitFails(mono, "add", "tool"), "tools/tool already exists")
	contains(t, s.pit(mono, "add", "--all"), "tools/tool already present, skipping", "blank: external repo has no 'main' branch yet")

	contains(t, s.pit(mono, "status"), "tool", "in sync")
	contains(t, s.pit(mono, "push", "-y"), "tool → "+extRepo+" (main): up to date", "done: monorepo")
}

func TestTag(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	s.pit(mono, "push", "-y")
	extGit := s.path("external.git")

	contains(t, s.pitFails(mono, "tag", "lib"), "usage: pit tag")
	contains(t, s.pitFails(mono, "tag", "-y", "lib", "bad..name"), "invalid tag name")
	out := s.pit(mono, "tag", "-y", "-m", "first release", "lib", "v1.0.0")
	contains(t, out, "Tag v1.0.0 on "+extRepo, "lib: add a", "pushed v1.0.0 (local: lib/v1.0.0)")

	if typ := s.git(extGit, "cat-file", "-t", "v1.0.0"); typ != "tag" {
		t.Errorf("external v1.0.0 is a %s, want an annotated tag", typ)
	}
	if s.git(extGit, "rev-parse", "v1.0.0^{commit}") != s.git(extGit, "rev-parse", "main") {
		t.Error("external tag doesn't point at the published commit")
	}
	contains(t, s.git(extGit, "cat-file", "tag", "v1.0.0"), "first release")
	s.git(mono, "rev-parse", "-q", "--verify", "refs/tags/lib/v1.0.0")
	s.gitFails(mono, "rev-parse", "-q", "--verify", "refs/tags/v1.0.0")
	contains(t, s.pitFails(mono, "tag", "-y", "lib", "v1.0.0"), "local tag lib/v1.0.0 already exists")

	// Unpublished commits need --allow-unpublished; the tag then carries them.
	s.write(mono, "lib/b.txt", "b\n")
	s.commitAll(mono, "lib: add b")
	contains(t, s.pitFails(mono, "tag", "-y", "lib", "v2.0.0"), "is not on the external 'main' branch yet")
	s.pit(mono, "tag", "-y", "--allow-unpublished", "lib", "v2.0.0")
	s.git(extGit, "cat-file", "-e", "v2.0.0:b.txt")
	s.gitFails(extGit, "cat-file", "-e", "main:b.txt")

	// -r tags an older monorepo revision.
	s.pit(mono, "tag", "-y", "-r", "HEAD~1", "lib", "v1.0.1")
	if s.git(extGit, "rev-parse", "v1.0.1^{commit}") != s.git(extGit, "rev-parse", "main") {
		t.Error("-r HEAD~1 didn't tag the published commit")
	}
}

func TestHookBlocksDirectPushesAndChainsExistingHook(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")

	marker := filepath.ToSlash(s.path("hook-ran"))
	custom := "#!/bin/sh\necho \"$1\" >> '" + marker + "'\ncat >> '" + marker + "'\n"
	s.write(mono, ".git/hooks/pre-push", custom)
	if err := os.Chmod(filepath.Join(mono, ".git/hooks/pre-push"), 0o755); err != nil {
		t.Fatal(err)
	}

	contains(t, s.pit(mono, "install-hook"), "kept the existing pre-push hook")
	contains(t, s.read(mono, ".git/hooks/pre-push"), hook.Marker)
	if s.read(mono, ".git/hooks/pre-push.pit-chained") != custom {
		t.Error("existing hook not kept as pre-push.pit-chained")
	}

	// Direct pushes to the external repo are blocked, however the URL is spelled, and the chained hook doesn't run for them.
	for _, dest := range []string{extRepo, s.path("external.git"), s.path("external")} {
		contains(t, s.gitFails(mono, "push", dest, "HEAD:refs/heads/leak"), "is the external repo of 'lib'")
	}
	if _, err := os.Stat(s.path("hook-ran")); err == nil {
		t.Error("chained hook ran for a blocked push")
	}

	// pit's own pushes pass the guard, and the chained hook sees them with their stdin.
	s.pit(mono, "push", "-y")
	contains(t, s.gitFails(mono, "push", "pit-lib", "HEAD:refs/heads/leak"), "is the external repo of 'lib'")
	ran := s.read(s.dir, "hook-ran")
	contains(t, ran, "origin\n", "pit-lib\n", "refs/heads/main")
	if s.git(s.path("external.git"), "for-each-ref", "refs/heads/leak") != "" {
		t.Error("leak branch reached the external repo")
	}

	// Pushes elsewhere are not affected.
	s.write(mono, "x.txt", "x\n")
	s.commitAll(mono, "x")
	s.git(mono, "push", "-q", "origin", "main")

	// Re-running is safe and keeps the chain.
	lacks(t, s.pit(mono, "install-hook"), "kept the existing")
	if s.read(mono, ".git/hooks/pre-push.pit-chained") != custom {
		t.Error("re-running install-hook lost the chained hook")
	}
}

func TestPushWithoutMonorepoUpstream(t *testing.T) {
	s := newSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.git(mono, "switch", "-q", "-c", "feature")
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: add a")
	contains(t, s.pitFails(mono, "push", "-y"), "branch 'feature' has no upstream")

	// Named externals leave the monorepo alone.
	contains(t, s.pit(mono, "push", "-y", "lib"), "rejoin merges were committed locally", "done: lib")
}
