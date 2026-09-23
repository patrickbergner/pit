// Package lfs is pit's Git LFS support. pit publishes files exactly as committed (LFS pointer or regular blob) and works the same with or
// without LFS; this package finds LFS pointers, checks that stored files match the `filter` attribute their tree gives them, and moves LFS
// objects between repos. Nothing here runs git-lfs unless LFS pointers or rules are involved.
package lfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/patrickbergner/pit/internal/git"
)

var specs = []string{
	"version https://git-lfs.github.com/spec/v1",
	"version https://hawser.github.com/spec/v1", // pre-1.0 pointers
}

// maxPointer: LFS pointers are always smaller than this.
const maxPointer = 1024

// IsPointer reports whether a blob is an LFS pointer.
func IsPointer(b []byte) bool {
	if len(b) == 0 || len(b) >= maxPointer {
		return false
	}
	line, _, _ := bytes.Cut(b, []byte{'\n'})
	for _, s := range specs {
		if string(line) == s {
			return true
		}
	}
	return false
}

// Available reports whether git-lfs is installed.
var Available = sync.OnceValue(func() bool { return git.OK("lfs", "version") })

// Require fails when git-lfs is needed for what but not installed.
func Require(what string) error {
	if !Available() {
		return fmt.Errorf("%s contains Git LFS files but git-lfs is not installed (https://git-lfs.com)", what)
	}
	return nil
}

// catBlobs reads the given blobs with one git cat-file --batch.
func catBlobs(shas []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(shas))
	if len(shas) == 0 {
		return blobs, nil
	}
	out, err := git.Raw(strings.NewReader(strings.Join(shas, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for {
		header, err := r.ReadString('\n')
		if err == io.EOF {
			return blobs, nil
		} else if err != nil {
			return nil, err
		}
		f := strings.Fields(header) // "<sha> <type> <size>" or "<sha> missing"
		if len(f) != 3 {
			continue
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file: unexpected header %q", header)
		}
		content := make([]byte, size+1) // content and a newline
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, err
		}
		blobs[f[0]] = content[:size]
	}
}

// PointerCount returns how many LFS pointers are among the blobs that `git rev-list --objects <args>` lists.
func PointerCount(args ...string) (int, error) {
	objects, err := git.Output(append([]string{"rev-list", "--objects", "--no-object-names"}, args...)...)
	if err != nil || objects == "" {
		return 0, err
	}
	sizes, err := git.Input(objects+"\n", "cat-file", "--batch-check=%(objecttype) %(objectsize) %(objectname)")
	if err != nil {
		return 0, err
	}
	var small []string
	for _, line := range strings.Split(sizes, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "blob" {
			if n, _ := strconv.Atoi(f[1]); n > 0 && n < maxPointer {
				small = append(small, f[2])
			}
		}
	}
	blobs, err := catBlobs(small)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range blobs {
		if IsPointer(b) {
			n++
		}
	}
	return n, nil
}

// Mismatches lists "<prefix><path>: <problem>" for each file under dir (default: all) whose storage (LFS pointer or regular blob)
// contradicts the `filter` attribute that the .gitattributes files inside rev itself give it. For a split commit, that is what a clone of
// the external repo sees.
func Mismatches(rev, dir, prefix string) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	tree, err := git.Raw(nil, "ls-tree", "-r", "-z", "-l", rev, "--", dir)
	if err != nil {
		return nil, err
	}
	var files, small []string
	shaOf := map[string]string{}
	for _, rec := range strings.Split(string(tree), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta) // mode type sha size
		if !ok || len(f) != 4 || f[1] != "blob" || f[0] == "120000" {
			continue
		}
		size, _ := strconv.Atoi(f[3])
		if size == 0 { // empty files are never stored as pointers
			continue
		}
		files = append(files, path)
		if size < maxPointer {
			small = append(small, f[2])
			shaOf[path] = f[2]
		}
	}
	blobs, err := catBlobs(small)
	if err != nil {
		return nil, err
	}
	pointer := map[string]bool{}
	for path, sha := range shaOf {
		if IsPointer(blobs[sha]) {
			pointer[path] = true
		}
	}

	// Nothing LFS-related here: no pointers and no LFS rules.
	if len(pointer) == 0 && !git.OK("grep", "-q", "-e", "filter=lfs", rev, "--", ":(glob)**/.gitattributes") {
		return nil, nil
	}
	if !git.OK("check-attr", "--source="+rev, "filter", "--", ".gitattributes") {
		return nil, errors.New("git >= 2.40 is required to check Git LFS attributes")
	}
	out, err := git.Raw(strings.NewReader(strings.Join(files, "\x00")+"\x00"), "check-attr", "--source="+rev, "-z", "--stdin", "filter")
	if err != nil {
		return nil, err
	}
	var issues []string
	f := strings.Split(string(out), "\x00") // path, attribute, value, path, ...
	for i := 0; i+2 < len(f); i += 3 {
		path, lfs := f[i], f[i+2] == "lfs"
		switch {
		case lfs && !pointer[path]:
			issues = append(issues, prefix+path+": tracked by LFS in .gitattributes, but committed as a regular file")
		case !lfs && pointer[path]:
			issues = append(issues, prefix+path+": committed as an LFS pointer, but not tracked by LFS in .gitattributes")
		}
	}
	return issues, nil
}

// Hint tells how to fix Mismatches for the external at repoPath.
func Hint(repoPath string) string {
	return fmt.Sprintf("make %[1]s/.gitattributes self-contained: track the patterns there (e.g. '*.png filter=lfs diff=lfs "+
		"merge=lfs -text') or opt out of the monorepo's LFS rules with the first line '* !filter !diff !merge'; then 'git add "+
		"--renormalize %[1]s' and commit", repoPath)
}

// Push uploads the LFS objects that the history of commit needs to remote. Explicit rather than left to Git LFS's pre-push hook, which may
// not be installed.
func Push(remote, commit string) error { return git.Pass(nil, "lfs", "push", remote, commit) }

// FetchAll downloads the LFS objects of every commit reachable from ref, from remote.
func FetchAll(remote, ref string) error { return git.Pass(nil, "lfs", "fetch", "--all", remote, ref) }
