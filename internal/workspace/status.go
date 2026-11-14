package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/patrickbergner/pit/internal/git"
)

// Status is a repo's state as `git status --porcelain=v2 --branch` reports it, plus the operation in progress.
type Status struct {
	Branch       string // "" when detached
	Detached     bool
	Unborn       bool // no commit yet
	Upstream     string
	UpstreamGone bool // configured, but the remote branch no longer exists
	Ahead        int
	Behind       int
	Entries      []Entry
	Operation    string // "merging", "rebasing", ... or ""
}

// Entry is a changed path in `git status --short` terms.
type Entry struct {
	XY   string // two status letters, ' ' for unmodified; "??" for untracked
	Path string // relative to the repo
	Orig string // source of a rename or copy
}

func (e Entry) Untracked() bool { return e.XY == "??" }

// Conflicted reports an unmerged path.
func (e Entry) Conflicted() bool {
	switch e.XY {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	}
	return false
}

// Staged reports whether anything is staged for commit.
func (s *Status) Staged() bool {
	for _, e := range s.Entries {
		if !e.Untracked() && !e.Conflicted() && e.XY[0] != ' ' {
			return true
		}
	}
	return false
}

// TrackedChanges reports whether anything is staged or modified in tracked files, i.e. what `git commit -a` would commit.
func (s *Status) TrackedChanges() bool {
	for _, e := range s.Entries {
		if !e.Untracked() {
			return true
		}
	}
	return false
}

func (s *Status) Conflicts() bool {
	for _, e := range s.Entries {
		if e.Conflicted() {
			return true
		}
	}
	return false
}

// Clean reports no changes, untracked files included.
func (s *Status) Clean() bool { return len(s.Entries) == 0 }

// ReadStatus runs git status in the repo, limited to pathspecs if any.
func ReadStatus(r *Repo, pathspecs []string) (*Status, error) {
	args := []string{"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal"}
	if len(pathspecs) > 0 {
		args = append(append(args, "--"), pathspecs...)
	}
	out, err := git.Raw(r.Dir, nil, args...)
	if err != nil {
		return nil, err
	}
	s := parseStatus(string(out))
	s.Operation = operation(r.GitDir())
	return s, nil
}

func parseStatus(out string) *Status {
	s := &Status{}
	hasAB := false
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			key, val, _ := strings.Cut(strings.TrimPrefix(rec, "# "), " ")
			switch key {
			case "branch.oid":
				s.Unborn = val == "(initial)"
			case "branch.head":
				if val == "(detached)" {
					s.Detached = true
				} else {
					s.Branch = val
				}
			case "branch.upstream":
				s.Upstream = val
			case "branch.ab":
				hasAB = true
				a, b, _ := strings.Cut(val, " ")
				s.Ahead, _ = strconv.Atoi(strings.TrimPrefix(a, "+"))
				s.Behind, _ = strconv.Atoi(strings.TrimPrefix(b, "-"))
			}
		case '1':
			if f := strings.SplitN(rec, " ", 9); len(f) == 9 {
				s.Entries = append(s.Entries, Entry{XY: xy(f[1]), Path: f[8]})
			}
		case '2':
			if f := strings.SplitN(rec, " ", 10); len(f) == 10 {
				e := Entry{XY: xy(f[1]), Path: f[9]}
				if i+1 < len(recs) {
					i++
					e.Orig = recs[i]
				}
				s.Entries = append(s.Entries, e)
			}
		case 'u':
			if f := strings.SplitN(rec, " ", 11); len(f) == 11 {
				s.Entries = append(s.Entries, Entry{XY: f[1], Path: f[10]})
			}
		case '?':
			s.Entries = append(s.Entries, Entry{XY: "??", Path: strings.TrimPrefix(rec, "? ")})
		}
	}
	s.UpstreamGone = s.Upstream != "" && !hasAB && !s.Unborn
	return s
}

func xy(v string) string { return strings.ReplaceAll(v, ".", " ") }

// operation names what is in progress in a git directory.
func operation(gitDir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge"), exists("rebase-apply"):
		return "rebasing"
	case exists("MERGE_HEAD"):
		return "merging"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-picking"
	case exists("REVERT_HEAD"):
		return "reverting"
	case exists("BISECT_LOG"):
		return "bisecting"
	}
	return ""
}

// LastFetch returns when the repo was last fetched, from git's FETCH_HEAD, or else when it was cloned; zero if unknown.
func (r *Repo) LastFetch() time.Time {
	for _, name := range []string{"FETCH_HEAD", "packed-refs"} {
		if fi, err := os.Stat(filepath.Join(r.GitDir(), name)); err == nil {
			return fi.ModTime()
		}
	}
	return time.Time{}
}

// HasRemotes reports whether the repo has any remote configured.
func (r *Repo) HasRemotes() bool {
	out, err := git.Output(r.Dir, "remote")
	return err == nil && strings.TrimSpace(out) != ""
}
