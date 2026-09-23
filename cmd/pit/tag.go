package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/lfs"
	"github.com/patrickbergner/pit/internal/monorepo"
	"github.com/patrickbergner/pit/internal/ui"
)

func cmdTag(r *monorepo.Repo, args []string) error {
	msg, rev := "", "HEAD"
	unpublished, yes := false, false
	var pos []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-m", "--message", "-r", "--rev":
			if i+1 >= len(args) {
				return fmt.Errorf("tag: %s needs a value", a)
			}
			i++
			if a == "-m" || a == "--message" {
				msg = args[i]
			} else {
				rev = args[i]
			}
		case "--allow-unpublished":
			unpublished = true
		case "-y", "--yes":
			yes = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("tag: unknown option %s", a)
			}
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		return errors.New("usage: pit tag [-m msg] [-r rev] [-y] [--allow-unpublished] <external> <tag>")
	}

	e, err := r.External(pos[0])
	if err != nil {
		return err
	}
	tag := pos[1]
	localRef := "refs/tags/" + e.Name + "/" + tag // namespaced in the monorepo, plain on the external repo
	if !git.OK("check-ref-format", "refs/tags/"+tag) {
		return fmt.Errorf("invalid tag name '%s'", tag)
	}
	if git.OK("rev-parse", "-q", "--verify", localRef) {
		return fmt.Errorf("local tag %s/%s already exists", e.Name, tag)
	}
	if err := e.Fetch(); err != nil {
		return err
	}
	if git.OK("ls-remote", "--exit-code", "--tags", e.Remote, "refs/tags/"+tag) {
		return fmt.Errorf("tag %s already exists on %s", tag, e.URL)
	}

	sha, err := e.Split(rev)
	if err != nil {
		return err
	}
	if !unpublished && (!e.HaveTrack() || !git.IsAncestor(sha, e.Track)) {
		return fmt.Errorf("%s: %.12s is not on the external '%s' branch yet; run 'pit push' first (or --allow-unpublished)", e.Name, sha, e.Branch)
	}

	subject, err := git.Output("log", "-1", "--format=%h %s", sha)
	if err != nil {
		return err
	}
	fmt.Printf("Tag %s on %s -> %s\n", tag, e.URL, subject)
	if ok, err := ui.Confirm("Create and push this tag?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("aborted")
	}

	// Unpublished commits (--allow-unpublished) go out with the tag, and so must their LFS objects.
	rng := []string{sha}
	if e.HaveTrack() {
		rng = append(rng, "--not", e.Track)
	}
	if n, err := lfs.PointerCount(rng...); err != nil {
		return err
	} else if n > 0 {
		if err := lfs.Require(e.Name); err != nil {
			return err
		}
		if err := lfs.Push(e.Remote, sha); err != nil {
			return fmt.Errorf("%s: git lfs push failed; tag not created", e.Name)
		}
	}

	// Annotated tag object named with its plain name ("v1.2.0") but stored locally as "<external>/v1.2.0", so 'git describe' on the external
	// side doesn't complain.
	if msg == "" {
		msg = e.Name + " " + tag
	}
	ident, err := git.Output("var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return err
	}
	obj, err := git.Input(fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s\n\n%s\n", sha, tag, ident, msg), "mktag")
	if err != nil {
		return err
	}
	if _, err := git.Output("update-ref", localRef, obj, ""); err != nil {
		return err
	}
	if err := e.Push(localRef + ":refs/tags/" + tag); err != nil {
		return err
	}
	ui.Info("%s: pushed %s (local: %s/%s)", e.Name, tag, e.Name, tag)
	return nil
}
