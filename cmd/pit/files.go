package main

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// header starts the block of r's output; ui.End closes it.
func header(r *workspace.Repo) { ui.Header(r.Label(), r.Path) }

// sequence runs git in each target one after the other, attached to the terminal. A failing repo doesn't stop the others; the failures are
// summed up at the end. With blocks, each repo's output comes in a block of its own; without, as for commands that are usually silent, pit
// names the repos that failed.
func sequence(cmd string, targets []workspace.Target, blocks bool, argsFor func(workspace.Target) []string) error {
	var failed []string
	for _, t := range targets {
		if err := stopped(); err != nil {
			return err
		}
		if blocks {
			header(t.Repo)
		}
		err := git.Pass(t.Repo.Dir, argsFor(t)...)
		if blocks {
			ui.End()
		}
		if err != nil {
			if !blocks {
				ui.Error("%s: git %s failed", t.Repo.Label(), cmd)
			}
			failed = append(failed, t.Repo.Label())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s failed in %s", cmd, strings.Join(failed, ", "))
	}
	return nil
}

// hasOpt reports whether one of the options is among opts.
func hasOpt(opts []string, options ...string) bool {
	return slices.ContainsFunc(opts, func(o string) bool { return slices.Contains(options, o) })
}

// interactive reports whether git add or restore asks hunk by hunk. Then each repo gets a block, so that it is clear which repo git asks
// about; otherwise these commands are silent.
func interactive(opts []string) bool { return hasOpt(opts, "-p", "--patch", "-i", "--interactive") }

func cmdAdd(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("add", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	if len(p.paths) == 0 && !hasOpt(p.opts, "-A", "--all", "-u", "--update") && !interactive(p.opts) {
		return errors.New("add: nothing specified, nothing added (give paths, or -A for everything)")
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	// Without its ignore block, a repo would stage the clones of the externals inside it as embedded repos.
	for _, b := range w.IgnoreBlocks() {
		if !b.UpToDate() && slices.ContainsFunc(targets, func(t workspace.Target) bool { return t.Repo == b.Repo }) {
			if b.Repo.IsRoot() {
				return errors.New("add: .gitignore doesn't list the current externals, so the root repo would pick up their clones; run pit sync first")
			}
			return fmt.Errorf("add: %s of %s doesn't list the current externals inside it, so the repo would pick up their clones; run pit sync first",
				b.Name(), b.Repo.Name)
		}
	}
	return sequence("add", targets, interactive(p.opts), func(t workspace.Target) []string {
		return pathspecArgs(append([]string{"add"}, p.opts...), t)
	})
}

func cmdRestore(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("restore", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	if len(p.paths) == 0 {
		return errors.New("restore: give the paths to restore (\".\" for everything below the current directory)")
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	return sequence("restore", targets, interactive(p.opts), func(t workspace.Target) []string {
		return pathspecArgs(append([]string{"restore"}, p.opts...), t)
	})
}

// cmdDiff prints the diffs of all repos as one diff of the whole tree: each repo's paths are prefixed with its path below the root.
func cmdDiff(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("diff", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	differs := false
	var failed []string
	for _, t := range targets {
		if err := stopped(); err != nil {
			return err
		}
		prefix := t.Repo.RootRelative()
		gitArgs := []string{"--no-pager", "diff", "--src-prefix=a/" + prefix, "--dst-prefix=b/" + prefix}
		err := git.Pass(t.Repo.Dir, pathspecArgs(append(gitArgs, p.opts...), t)...)
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1: // --exit-code / --quiet: there are differences
			differs = true
		default:
			failed = append(failed, t.Repo.Label())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("diff failed in %s", strings.Join(failed, ", "))
	}
	if differs {
		return errSilent
	}
	return nil
}
