package main

// push has two phases: (1) fetch and compute everything, print the full plan, touch nothing; (2) after one confirmation: upload LFS
// objects, record a rejoin per published external (local bookkeeping merge), push the monorepo, then each external repo. Any problem
// found in phase 1 aborts before anything is pushed or committed.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/lfs"
	"github.com/patrickbergner/pit/internal/monorepo"
	"github.com/patrickbergner/pit/internal/ui"
)

type publication struct {
	e     *monorepo.External
	split string // commit to publish; "" when the external repo is up to date
	rng   string // what gets published: <track>..<split>, or <split> for an empty repo
	lfs   int    // LFS pointers in rng
}

func cmdPush(r *monorepo.Repo, args []string) error {
	dry, yes := false, false
	var names []string
	for _, a := range args {
		switch {
		case a == "-n" || a == "--dry-run":
			dry = true
		case a == "-y" || a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("push: unknown option %s", a)
		default:
			names = append(names, a)
		}
	}
	all, targets := r.Targets(names)
	if !git.OK("diff", "--quiet", "HEAD", "--") {
		ui.Warn("uncommitted changes are not pushed (only HEAD is)")
	}

	var errs []string
	var up *monorepo.Upstream
	upRange, upState := "", ""
	upCount, upLFS := 0, 0

	// ---- phase 1: monorepo
	if all {
		v, err := r.Upstream()
		if err != nil {
			errs = append(errs, "monorepo: "+err.Error())
		} else {
			up = v
			if err := v.Fetch(); err != nil {
				return err
			}
			switch {
			case !v.HaveTrack():
				upRange = "HEAD" // new branch on the remote
			case git.IsAncestor("HEAD", v.Track):
				upState = "nothing new"
				head, _ := git.RevParse("HEAD")
				if track, _ := git.RevParse(v.Track); head != track {
					upState = "behind (run pit pull)"
				}
			case !git.IsAncestor(v.Track, "HEAD"):
				upState = "CANNOT PUSH – diverged, see below"
				errs = append(errs, fmt.Sprintf("monorepo: %s has commits you don't have; run pit pull first", v.Label))
			default:
				upRange = v.Track + "..HEAD"
			}
			if upRange != "" {
				if upCount, err = git.RevCount(upRange); err != nil {
					return err
				}
				if upLFS, err = lfs.PointerCount(upRange); err != nil {
					return err
				}
				if upLFS > 0 && !lfs.Available() {
					errs = append(errs, "monorepo: commits contain Git LFS files but git-lfs is not installed")
				}
			}
		}
	}

	// ---- phase 1: external
	var order []*publication // in the monorepo: up to date or to publish
	var skipped []*monorepo.External
	for _, name := range targets {
		e, err := r.External(name)
		if err != nil {
			return err
		}
		if !e.InMonorepo("HEAD") {
			if all {
				skipped = append(skipped, e)
			} else {
				errs = append(errs, fmt.Sprintf("%s: %s does not exist in the monorepo", name, e.Path))
			}
			continue
		}
		ui.Info("%s: fetching and splitting %s", name, e.Path)
		if err := e.Fetch(); err != nil {
			return err
		}
		sha, err := e.Split("HEAD")
		if err != nil {
			return err
		}
		pub := &publication{e: e, rng: sha} // first publish to an empty repo: everything
		if e.HaveTrack() {
			if track, _ := git.RevParse(e.Track); track == sha {
				order = append(order, pub)
				continue
			}
			if !git.IsAncestor(e.Track, sha) {
				errs = append(errs, fmt.Sprintf("%s: external repo has commits missing from the monorepo; run pit pull first", name))
				continue
			}
			pub.rng = e.Track + ".." + sha
		}
		pub.split = sha
		if pub.lfs, err = lfs.PointerCount(pub.rng); err != nil {
			return err
		}
		if pub.lfs > 0 && !lfs.Available() {
			errs = append(errs, name+": commits contain Git LFS files but git-lfs is not installed")
		}
		// Files must be stored the way the external repo's own .gitattributes expect them.
		issues, err := lfs.Mismatches(sha, ".", e.Path+"/")
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			for _, issue := range issues {
				errs = append(errs, name+": "+issue)
			}
			errs = append(errs, name+": "+lfs.Hint(e.Path))
		} else if err := e.CheckLFS(); err != nil {
			return err
		}
		order = append(order, pub)
	}

	// ---- the plan
	nPublish := 0
	for _, pub := range order {
		if pub.split != "" {
			nPublish++
		}
	}
	todo := nPublish > 0
	fmt.Printf("\n%s\n\n", ui.Bold("Push plan – nothing has been pushed yet"))
	if up != nil {
		switch {
		case strings.HasPrefix(upState, "CANNOT"):
			fmt.Printf("MONOREPO %s: %s\n", up.Label, upState)
		case upRange != "" || nPublish > 0:
			todo = true
			fmt.Printf("MONOREPO %s: push %d commit(s)", up.Label, upCount)
			if nPublish > 0 {
				fmt.Printf(" + %d bookkeeping merge(s) recording what gets published", nPublish)
			}
			if upLFS > 0 {
				fmt.Printf(", with %d Git LFS file(s)", upLFS)
			}
			fmt.Println()
			if upRange != "" {
				if err := git.Pass(nil, "--no-pager", "log", "--format=         %h %s", "-n", "15", upRange); err != nil {
					return err
				}
				if upCount > 15 {
					fmt.Printf("         … and %d more\n", upCount-15)
				}
			}
		default:
			if upState == "" {
				upState = "up to date"
			}
			fmt.Printf("MONOREPO %s: %s\n", up.Label, upState)
		}
		fmt.Println()
	}
	for _, pub := range order {
		if pub.split == "" {
			fmt.Printf("EXTERNAL %s → %s: up to date\n\n", pub.e.Name, pub.e.Label())
			continue
		}
		n, err := git.RevCount(pub.rng)
		if err != nil {
			return err
		}
		fmt.Printf("EXTERNAL %s → %s: publish %d commit(s)", pub.e.Name, pub.e.Label(), n)
		if pub.lfs > 0 {
			fmt.Printf(" with %d Git LFS file(s)", pub.lfs)
		}
		fmt.Print("; these messages get published:\n\n")
		if err := git.Pass(nil, "--no-pager", "log", "--reverse",
			"--format=         %C(auto,yellow)%h%C(auto,reset) %s%n%w(0,17,17)%b", pub.rng); err != nil {
			return err
		}
		fmt.Println()
	}
	for _, e := range skipped {
		fmt.Printf("EXTERNAL %s: skipped, %s not in monorepo yet\n\n", e.Name, e.Path)
	}

	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "Cannot push:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		return errors.New("nothing was pushed")
	}
	if !todo {
		ui.Info("everything is up to date")
		return nil
	}
	if dry {
		ui.Info("dry run: nothing was pushed")
		return nil
	}
	if ok, err := ui.Confirm("Execute this plan?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("aborted; nothing was pushed")
	}

	// ---- phase 2: execute
	// LFS objects first: uploading them moves no refs, so a failure leaves everything as it was.
	if up != nil && upLFS > 0 {
		ui.Info("monorepo: uploading Git LFS objects to %s", up.Remote)
		if err := lfs.Push(up.Remote, "HEAD"); err != nil {
			return errors.New("monorepo: git lfs push failed; nothing was pushed")
		}
	}
	for _, pub := range order {
		if pub.lfs > 0 {
			ui.Info("%s: uploading Git LFS objects to %s", pub.e.Name, pub.e.URL)
			if err := lfs.Push(pub.e.Remote, pub.split); err != nil {
				return fmt.Errorf("%s: git lfs push failed; nothing was pushed", pub.e.Name)
			}
		}
	}

	// Rejoins next (local only, tree unchanged), so the monorepo push carries them.
	for _, pub := range order {
		if pub.split != "" {
			if err := pub.e.Rejoin(pub.split); err != nil {
				return err
			}
		}
	}
	if !all && nPublish > 0 {
		ui.Info("rejoin merges were committed locally; the next 'pit push' sends them to the monorepo")
	}

	// Monorepo first: it is the source of truth.
	var done []string
	if up != nil && (upRange != "" || nPublish > 0) {
		ui.Info("monorepo: pushing to %s", up.Label)
		if err := git.Pass(nil, "push", "--quiet", up.Remote, "HEAD:"+up.Merge); err != nil {
			return errors.New("monorepo push failed; nothing else was pushed")
		}
		done = append(done, "monorepo")
	}
	for _, pub := range order {
		if pub.split == "" {
			continue
		}
		ui.Info("%s: publishing to %s", pub.e.Name, pub.e.Label())
		if err := pub.e.Push(pub.split + ":refs/heads/" + pub.e.Branch); err != nil {
			pushed := "nothing"
			if len(done) > 0 {
				pushed = strings.Join(done, " ")
			}
			return fmt.Errorf("%s: push failed; already pushed: %s", pub.e.Name, pushed)
		}
		done = append(done, pub.e.Name)
	}
	ui.Info("done: %s", strings.Join(done, " "))
	return nil
}
