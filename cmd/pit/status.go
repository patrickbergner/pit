package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/monorepo"
)

// columns returns a Printf format for rows of external name, repoPath and the rest, wide enough for every configured external.
func columns(r *monorepo.Repo) string {
	nameW, pathW := len("(monorepo)"), len("REPOPATH")
	for name, e := range r.Config.Externals {
		nameW, pathW = max(nameW, len(name)), max(pathW, len(e.RepoPath))
	}
	return fmt.Sprintf("%%-%ds  %%-%ds  %%s\n", nameW, pathW)
}

func cmdList(r *monorepo.Repo, args []string) error {
	if len(args) > 0 {
		return errors.New("usage: pit list")
	}
	row := columns(r)
	fmt.Printf(row, "EXTERNAL", "REPOPATH", fmt.Sprintf("%-10s %s", "BRANCH", "URL"))
	for _, name := range r.Config.Names() {
		e, err := r.External(name)
		if err != nil {
			return err
		}
		fmt.Printf(row, e.Name, e.Path, fmt.Sprintf("%-10s %s", e.Branch, e.URL))
	}
	return nil
}

func syncState(ahead, behind int) string {
	if ahead+behind == 0 {
		return "in sync"
	}
	return fmt.Sprintf("%d to push, %d to pull", ahead, behind)
}

// aheadBehind counts the commits only in a and only in b.
func aheadBehind(a, b string) (string, error) {
	ahead, err := git.RevCount(b + ".." + a)
	if err != nil {
		return "", err
	}
	behind, err := git.RevCount(a + ".." + b)
	if err != nil {
		return "", err
	}
	return syncState(ahead, behind), nil
}

func cmdStatus(r *monorepo.Repo, args []string) error {
	fetch := true
	var names []string
	for _, a := range args {
		switch {
		case a == "--no-fetch":
			fetch = false
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("status: unknown option %s", a)
		default:
			names = append(names, a)
		}
	}
	all, targets := r.Targets(names)
	row := columns(r)

	if all {
		label, state := "", ""
		if v, err := r.Upstream(); err != nil {
			state = err.Error()
		} else {
			label = v.Label
			if fetch {
				if err := v.Fetch(); err != nil {
					return err
				}
			}
			if v.HaveTrack() {
				if state, err = aheadBehind("HEAD", v.Track); err != nil {
					return err
				}
			} else {
				state = v.Label + " doesn't exist yet"
			}
		}
		fmt.Printf(row, "(monorepo)", label, state)
	}

	for _, name := range targets {
		e, err := r.External(name)
		if err != nil {
			return err
		}
		if fetch {
			err = e.Fetch()
		} else {
			err = e.EnsureRemote()
		}
		if err != nil {
			return err
		}
		var state string
		switch {
		case !e.InMonorepo("HEAD") && e.HaveTrack():
			state = fmt.Sprintf("not in monorepo yet (pit add %s)", e.Name)
		case !e.InMonorepo("HEAD"):
			state = fmt.Sprintf("external repo empty; create %s, then pit push", e.Path)
		default:
			sha, err := e.Split("HEAD")
			if err != nil {
				return err
			}
			if !e.HaveTrack() {
				n, err := git.RevCount(sha)
				if err != nil {
					return err
				}
				state = fmt.Sprintf("external repo empty; %d commit(s) to push", n)
			} else if state, err = aheadBehind(sha, e.Track); err != nil {
				return err
			}
		}
		fmt.Printf(row, e.Name, e.Path, state)
	}
	return nil
}
