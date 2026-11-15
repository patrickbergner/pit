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
	// git restore fails for a pathspec that matches nothing, as a directory with externals below does in a repo that has nothing of its
	// own in it. Such a directory is restored in the externals only.
	var owning []workspace.Target
	for _, t := range targets {
		t.Pathspecs = slices.DeleteFunc(slices.Clone(t.Pathspecs), func(spec string) bool {
			return slices.Contains(t.Shared, spec) && !hasFiles(t.Repo, spec)
		})
		if len(t.Pathspecs) > 0 {
			owning = append(owning, t)
		}
	}
	return sequence("restore", owning, interactive(p.opts), func(t workspace.Target) []string {
		return pathspecArgs(append([]string{"restore"}, p.opts...), t)
	})
}

// hasFiles reports whether the repo has files of its own in pathspec: tracked ones, or deleted ones still to restore. If that can't be
// told, it says yes, and git reports what is wrong.
func hasFiles(r *workspace.Repo, pathspec string) bool {
	if tracked, err := git.Output(r.Dir, "ls-files", "--", pathspec); err != nil || tracked != "" {
		return true
	}
	s, err := workspace.ReadStatus(r, []string{pathspec})
	return err != nil || s.TrackedChanges()
}

// diffSummaries are the git diff options for output that lists files with paths relative to the repo, where a patch has headers.
var diffSummaries = []string{"--stat", "--numstat", "--shortstat", "--dirstat", "--cumulative", "--summary", "--compact-summary",
	"--name-only", "--name-status", "--raw"}

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
	// The prefixes put a repo's location into the paths of a patch only. The formats that list files instead get a block per repo, so
	// that it is clear whose files they are.
	blocks := slices.ContainsFunc(p.opts, func(o string) bool {
		name, _, _ := strings.Cut(o, "=")
		return slices.Contains(diffSummaries, name)
	})
	differs := false
	var failed []string
	for _, t := range targets {
		if err := stopped(); err != nil {
			return err
		}
		prefix := t.Repo.RootRelative()
		gitArgs := []string{"--no-pager", "diff", "--src-prefix=a/" + prefix, "--dst-prefix=b/" + prefix}
		if blocks {
			if git.OK(t.Repo.Dir, pathspecArgs(append(append(gitArgs, p.opts...), "--quiet"), t)...) {
				continue // no differences, no block
			}
			header(t.Repo)
		}
		err := git.Pass(t.Repo.Dir, pathspecArgs(append(gitArgs, p.opts...), t)...)
		if blocks {
			ui.End()
		}
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
