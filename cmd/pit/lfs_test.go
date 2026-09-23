package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickbergner/pit/internal/hook"
)

const lfsRule = "filter=lfs diff=lfs merge=lfs -text"

func newLFSSandbox(t *testing.T) *sandbox {
	t.Helper()
	if !testHaveLFS {
		t.Skip("git-lfs not installed")
	}
	return newSandbox(t)
}

func randomBytes(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lfsObjects counts the LFS objects stored in a bare repo (file:// LFS remote).
func lfsObjects(t *testing.T, bare string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(bare, "lfs", "objects"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return n
}

func TestLFSRoundTrip(t *testing.T) {
	s := newLFSSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, ".gitattributes", "*.png "+lfsRule+"\n")
	logo := randomBytes(t, 5000)
	s.write(mono, "lib/logo.png", logo)
	s.write(mono, "lib/a.txt", "a\n")
	s.commitAll(mono, "lib: logo")

	// The external repo wouldn't know logo.png is in LFS: refuse to publish.
	out := s.pitFails(mono, "push", "-y")
	contains(t, out, "lib/logo.png: committed as an LFS pointer, but not tracked by LFS in .gitattributes",
		"make lib/.gitattributes self-contained", "nothing was pushed")
	if s.git(s.path("external.git"), "for-each-ref") != "" {
		t.Fatal("external repo got refs")
	}

	s.write(mono, "lib/.gitattributes", "*.png "+lfsRule+"\n")
	s.commitAll(mono, "lib: track png in LFS")
	out = s.pit(mono, "push", "-y")
	contains(t, out, "publish 2 commit(s) with 1 Git LFS file(s)", "uploading Git LFS objects", "done: monorepo lib")
	if n := lfsObjects(t, s.path("external.git")); n != 1 {
		t.Errorf("external repo has %d LFS objects, want 1", n)
	}
	if n := lfsObjects(t, s.path("origin.git")); n != 1 {
		t.Errorf("monorepo has %d LFS objects, want 1", n)
	}

	// A clone of the external repo gets the real file.
	c := s.clone(extRepo, "contrib")
	if s.read(c, "logo.png") != logo {
		t.Fatal("external clone has no real logo.png")
	}

	// LFS files added in the external repo come back with their objects.
	icon := randomBytes(t, 3000)
	s.write(c, "icon.png", icon)
	s.commitAll(c, "external: icon")
	s.git(c, "lfs", "push", "origin", "main")
	s.git(c, "push", "-q", "origin", "main")

	out = s.pit(mono, "pull")
	contains(t, out, "lib: fetching Git LFS objects from "+extRepo, "lib: merging")
	lacks(t, out, "warning")
	if s.read(mono, "lib/icon.png") != icon {
		t.Error("pulled icon.png is not the real file")
	}
	if st := s.git(mono, "status", "--porcelain"); st != "" {
		t.Errorf("worktree not clean after pull:\n%s", st)
	}
	out = s.pit(mono, "push", "-y")
	contains(t, out, "with 1 Git LFS file(s)", "done: monorepo")
	if n := lfsObjects(t, s.path("origin.git")); n != 2 {
		t.Errorf("monorepo has %d LFS objects, want 2", n)
	}
}

func TestLFSExternalWithoutLFS(t *testing.T) {
	s := newLFSSandbox(t)
	extRepo := s.bare("external")
	c := s.clone(extRepo, "contrib")
	icon := randomBytes(t, 3000)
	s.write(c, "icon.ico", icon) // no .gitattributes: a regular blob
	s.commitAll(c, "external: icon")
	s.git(c, "push", "-q", "origin", "main")
	rawBlob := s.git(c, "rev-parse", "HEAD:icon.ico")

	mono := s.monorepo(map[string]ext{"ico": {extRepo, "ico"}})
	s.write(mono, ".gitattributes", "*.ico "+lfsRule+"\n")
	s.commitAll(mono, "track ico in LFS")

	out := s.pit(mono, "add", "ico")
	contains(t, out, "ico/icon.ico: tracked by LFS in .gitattributes, but committed as a regular file", "'* !filter !diff !merge'")

	// Opt the external out of the monorepo's LFS rules.
	s.write(mono, "ico/.gitattributes", "* !filter !diff !merge\n")
	s.git(mono, "add", "ico/.gitattributes")
	s.git(mono, "add", "--renormalize", "ico")
	s.git(mono, "commit", "-q", "-m", "ico: opt out of LFS")
	if st := s.git(mono, "status", "--porcelain"); st != "" {
		t.Errorf("worktree not clean:\n%s", st)
	}

	out = s.pit(mono, "push", "-y")
	lacks(t, out, "warning", "Git LFS file")
	contains(t, out, "done: monorepo ico")
	if got := s.git(s.path("external.git"), "rev-parse", "main:icon.ico"); got != rawBlob {
		t.Error("icon.ico changed in the external repo")
	}
	if n := lfsObjects(t, s.path("external.git")); n != 0 {
		t.Errorf("external repo has %d LFS objects, want 0", n)
	}
}

func TestLFSInstallHookChainsLFSHook(t *testing.T) {
	s := newLFSSandbox(t)
	extRepo := s.bare("external")
	mono := s.monorepo(map[string]ext{"lib": {extRepo, "lib"}})
	s.write(mono, ".gitattributes", "*.png "+lfsRule+"\n")
	s.write(mono, "lib/.gitattributes", "*.png "+lfsRule+"\n")
	s.write(mono, "lib/logo.png", randomBytes(t, 2000))
	s.commitAll(mono, "lib: logo")
	// git-lfs may already have put its hooks there; start without a pre-push hook.
	if err := os.Remove(filepath.Join(mono, ".git", "hooks", "pre-push")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	contains(t, s.pit(mono, "install-hook"), "installed Git LFS's pre-push hook")
	contains(t, s.read(mono, ".git/hooks/pre-push.pit-chained"), "git lfs pre-push")
	contains(t, s.read(mono, ".git/hooks/pre-push"), hook.Marker)

	// Both hooks run on pit's pushes; the guard still blocks direct ones.
	contains(t, s.pit(mono, "push", "-y"), "done: monorepo lib")
	contains(t, s.gitFails(mono, "push", extRepo, "HEAD:refs/heads/leak"), "is the external repo of 'lib'")
	if n := lfsObjects(t, s.path("external.git")); n != 1 {
		t.Errorf("external repo has %d LFS objects, want 1", n)
	}
}
