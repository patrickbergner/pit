package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/monorepo"
	"github.com/patrickbergner/pit/internal/ui"
)

func cmdPull(r *monorepo.Repo, args []string) error {
	names, err := parseNames("pull", args)
	if err != nil {
		return err
	}
	all, targets := r.Targets(names)
	if err := monorepo.RequireClean(); err != nil {
		return err
	}

	// 1. Monorepo first, so we build on what others already merged.
	//    Always a merge, never a rebase: rebasing flattens the subtree merges.
	if all {
		v, err := r.Upstream()
		if err != nil {
			return fmt.Errorf("monorepo: %w", err)
		}
		if err := v.Fetch(); err != nil {
			return err
		}
		switch {
		case !v.HaveTrack():
			ui.Info("monorepo: %s doesn't exist yet, nothing to pull", v.Label)
		case git.IsAncestor(v.Track, "HEAD"):
			ui.Info("monorepo: already up to date with %s", v.Label)
		default:
			ui.Info("monorepo: merging %s into %s", v.Label, v.Branch)
			if err := git.Pass(nil, "merge", "--no-edit", v.Track); err != nil {
				return errors.New("monorepo: merge conflict; resolve, commit, then re-run pit pull")
			}
		}
	}

	// 2. Then each external repo into its repoPath.
	for _, name := range targets {
		e, err := r.External(name)
		if err != nil {
			return err
		}
		if !e.InMonorepo("HEAD") {
			if all {
				ui.Warn("%s: %s not in monorepo yet, skipping (see: pit add / pit push)", name, e.Path)
				continue
			}
			return fmt.Errorf("%s: %s missing; run 'pit add %s' first", name, e.Path, name)
		}
		if err := e.Fetch(); err != nil {
			return err
		}
		if !e.HaveTrack() {
			ui.Info("%s: external repo is empty, nothing to pull", name)
			continue
		}
		// Up to date when the external tip is already part of our split history. (Commits we published exist here only in unsplit form, so
		// comparing against HEAD directly would create pointless merges.)
		sha, err := e.Split("HEAD")
		if err != nil {
			return err
		}
		if git.IsAncestor(e.Track, sha) {
			ui.Info("%s: already up to date", name)
			continue
		}
		if err := e.FetchLFS(e.Track, "--not", "HEAD"); err != nil {
			return err
		}
		ui.Info("%s: merging %s/%s into %s", name, e.Remote, e.Branch, e.Path)
		msg := fmt.Sprintf("pit: pull %s from %s", name, e.Label())
		if err := git.Pass(nil, "subtree", "merge", "--prefix="+e.Path, "-m", msg, e.Track); err != nil {
			return fmt.Errorf("%s: merge conflict; resolve, commit, then re-run pit pull", name)
		}
		if err := e.CheckLFS(); err != nil {
			return err
		}
	}
	return nil
}

func cmdAdd(r *monorepo.Repo, args []string) error {
	all := false
	var names []string
	for _, a := range args {
		switch {
		case a == "--all":
			all = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("add: unknown option %s", a)
		default:
			names = append(names, a)
		}
	}
	if all {
		names = r.Config.Names()
	}
	if len(names) == 0 {
		return errors.New("usage: pit add (<external>... | --all)")
	}
	if err := monorepo.RequireClean(); err != nil {
		return err
	}

	for _, name := range names {
		e, err := r.External(name)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(e.Path); err == nil {
			if all {
				ui.Info("%s: %s already present, skipping", name, e.Path)
				continue
			}
			return fmt.Errorf("%s: %s already exists", name, e.Path)
		}
		if err := e.Fetch(); err != nil {
			return err
		}
		if !e.HaveTrack() {
			msg := fmt.Sprintf("%s: external repo has no '%s' branch yet; create %s in the monorepo and run 'pit push %s'",
				name, e.Branch, e.Path, name)
			if all {
				ui.Warn("%s", msg)
				continue
			}
			return errors.New(msg)
		}
		if err := e.FetchLFS(e.Track); err != nil {
			return err
		}
		ui.Info("%s: importing %s into %s", name, e.Label(), e.Path)
		msg := fmt.Sprintf("pit: add %s from %s", name, e.Label())
		if err := git.Pass(nil, "subtree", "add", "--prefix="+e.Path, "-m", msg, e.Track); err != nil {
			return err
		}
		if err := e.CheckLFS(); err != nil {
			return err
		}
	}
	return nil
}
