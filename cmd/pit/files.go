package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/config"
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
	// A directory with externals below, in which the repo has nothing of its own, is restored in the externals only.
	var owning []workspace.Target
	for _, t := range targets {
		if t = ownPathspecs(t); len(t.Pathspecs) > 0 {
			owning = append(owning, t)
		}
	}
	return sequence("restore", owning, interactive(p.opts), func(t workspace.Target) []string {
		return pathspecArgs(append([]string{"restore"}, p.opts...), t)
	})
}

// cmdRm removes files from the work trees and the indexes, each path in the repo that owns it. In one repo, git rm removes nothing when one
// of its paths fails git's checks; pit extends that to all repos by running git rm --dry-run in each first.
func cmdRm(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("rm", args, spec{bools: []string{"recursive"}, passthrough: true})
	if err != nil {
		return err
	}
	// git rm's -r has no long form, so pit's -r <repo> is easily meant as git's: -r build, or -rf, which reads as -r f.
	for _, n := range p.repos {
		if n != workspace.RootName && !slices.ContainsFunc(w.Externals, func(r *workspace.Repo) bool { return r.Name == n }) {
			return fmt.Errorf("rm: unknown repo %q; -r selects repos in pit, so remove a directory with --recursive (git rm -r)", n)
		}
	}
	if len(p.paths) == 0 {
		return errors.New("rm: give the paths to remove")
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	// An external's directory, or a directory above it, routes to all of the external: removing that would empty the repo.
	var whole []string
	for _, t := range targets {
		if !t.Repo.IsRoot() && slices.Contains(t.Pathspecs, ".") {
			whole = append(whole, t.Repo.Name)
		}
	}
	if len(whole) > 0 {
		return fmt.Errorf("rm: the paths cover all of %s; pit rm removes files, not externals "+
			"(to drop one, remove it from %s and run pit sync)", strings.Join(whole, ", "), config.FileName)
	}
	rmArgs := func(t workspace.Target, extra ...string) []string {
		args := append([]string{"rm"}, p.opts...)
		if p.on("recursive") {
			args = append(args, "-r")
		}
		return pathspecArgs(append(args, extra...), t)
	}

	type result struct {
		out []byte
		err error
	}
	checks := eachTarget(targets, func(t workspace.Target) result {
		out, err := git.Combined(t.Repo.Dir, rmArgs(t, "--dry-run")...)
		return result{out, err}
	})
	var failing []string
	for i, c := range checks {
		if c.err == nil {
			continue
		}
		failing = append(failing, targets[i].Repo.Label())
		header(targets[i].Repo)
		if out := strings.TrimRight(string(c.out), "\n"); out != "" {
			fmt.Println(out)
		} else {
			ui.Error("%s", c.err)
		}
		ui.End()
	}
	if len(failing) > 0 {
		return fmt.Errorf("rm: nothing removed, as git rm would fail in %s", strings.Join(failing, ", "))
	}
	// git rm lists what it removes with paths relative to its repo, so each repo gets a block.
	return sequence("rm", targets, !hasOpt(p.opts, "-q", "--quiet"), func(t workspace.Target) []string { return rmArgs(t) })
}

// cmdMv moves or renames files with git mv in the repo that owns them. A move has one destination, so it runs in one repo: git can't move
// files from one repo to another, and git mv checks all its sources before it moves any.
func cmdMv(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("mv", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	if p.narrowed() {
		return errors.New("mv: the paths decide the repo; -r and -g don't apply")
	}
	if len(p.paths) < 2 {
		return errors.New("mv: give the sources and the destination")
	}
	sources, dest := p.paths[:len(p.paths)-1], p.paths[len(p.paths)-1]
	to, err := w.Locate(dest)
	if err != nil {
		return err
	}
	if to.Owner == nil {
		return fmt.Errorf("mv: %s: not inside any repo of %s", dest, w.Root)
	}
	// Into an existing directory, as git moves it, each source keeps its name; its new path may then lie in an external below dest.
	abs := dest
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.Cwd, dest)
	}
	info, err := os.Stat(abs)
	into := err == nil && info.IsDir()

	var problems []string
	specs := make([]string, 0, len(p.paths))
	for _, src := range sources {
		from, err := w.Locate(src)
		if err != nil {
			return err
		}
		newPath, at := dest, to
		if into {
			newPath = filepath.Join(dest, filepath.Base(src))
			if at, err = w.Locate(newPath); err != nil {
				return err
			}
		}
		switch {
		case from.Owner == nil:
			problems = append(problems, fmt.Sprintf("%s: not inside any repo of %s", src, w.Root))
		case !from.Owner.Cloned():
			problems = append(problems, fmt.Sprintf("%s: %s is not cloned yet; run pit sync", src, from.Owner.Name))
		case !from.Owner.IsRoot() && from.Spec == ".":
			problems = append(problems, fmt.Sprintf("%s is the external %s; to move it, change its path in %s, "+
				"move the directory and run pit sync", src, from.Owner.Name, config.FileName))
		case len(from.Below) > 0:
			names := make([]string, len(from.Below))
			for i, r := range from.Below {
				names[i] = r.Name
			}
			problems = append(problems, fmt.Sprintf("%s holds the externals %s, which git mv would move along without updating %s",
				src, strings.Join(names, ", "), config.FileName))
		case to.Owner != from.Owner || at.Owner != from.Owner:
			other := at.Owner
			if to.Owner != from.Owner {
				other = to.Owner
			}
			problems = append(problems, fmt.Sprintf("%s is in %s, its destination in %s, and git can't move files between repos; "+
				"move it yourself, then run pit add %s %s to stage both sides",
				src, from.Owner.Label(), other.Label(), src, filepath.ToSlash(newPath)))
		default:
			specs = append(specs, from.Spec)
		}
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return errors.New("mv: nothing was moved")
	}
	// git mv is silent unless it lists what it moves (or would).
	target := workspace.Target{Repo: to.Owner, Pathspecs: append(specs, to.Spec)}
	listed := hasOpt(p.opts, "-v", "--verbose", "-n", "--dry-run")
	return sequence("mv", []workspace.Target{target}, listed, func(t workspace.Target) []string {
		return pathspecArgs(append([]string{"mv"}, p.opts...), t)
	})
}

// ownPathspecs leaves out t's pathspecs that are directories with externals below in which the repo has nothing of its own. git commands
// that insist on a pathspec matching something, like restore, commit or stash, fail for them.
func ownPathspecs(t workspace.Target) workspace.Target {
	t.Pathspecs = slices.DeleteFunc(slices.Clone(t.Pathspecs), func(spec string) bool {
		return slices.Contains(t.Shared, spec) && !hasFiles(t.Repo, spec)
	})
	return t
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

// listsFiles reports whether the diff options ask for a list of files instead of a patch. The prefixes put a repo's location into the paths
// of a patch only, so such a list gets a block per repo, which makes clear whose files they are.
func listsFiles(opts []string) bool {
	return slices.ContainsFunc(opts, func(o string) bool {
		name, _, _ := strings.Cut(o, "=")
		return slices.Contains(diffSummaries, name)
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
	blocks := listsFiles(p.opts)
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

// cmdGrep runs git grep in all repos in parallel. As git prints the paths relative to the repo, each repo with matches gets a block.
// Like git grep, it fails if nothing matched anywhere.
func cmdGrep(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("grep", args, spec{values: []string{"e"}, passthrough: true})
	if err != nil {
		return err
	}
	patterns := p.vals["e"]
	if len(patterns) == 0 {
		if len(p.paths) == 0 {
			return errors.New("grep: give the pattern to search for")
		}
		patterns, p.paths = p.paths[:1], p.paths[1:]
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	gitArgs := []string{"grep"}
	if ui.ColorsOut() {
		gitArgs = append(gitArgs, "--color=always") // git sees a pipe, not pit's terminal
	}
	gitArgs = append(gitArgs, p.opts...)
	for _, pattern := range patterns {
		gitArgs = append(gitArgs, "-e", pattern)
	}

	type result struct {
		out []byte
		err error
	}
	results := eachTarget(targets, func(t workspace.Target) result {
		out, err := git.Combined(t.Repo.Dir, pathspecArgs(slices.Clone(gitArgs), t)...)
		return result{out, err}
	})
	matched := false
	var failed []string
	for i, res := range results {
		var exit *exec.ExitError
		switch {
		case res.err == nil:
			matched = true
		case errors.As(res.err, &exit) && exit.ExitCode() == 1: // no match
			continue
		default:
			failed = append(failed, targets[i].Repo.Label())
		}
		out := strings.TrimRight(string(res.out), "\n")
		if out == "" && res.err == nil {
			continue // -q
		}
		header(targets[i].Repo)
		if out != "" {
			fmt.Println(out)
		} else {
			ui.Error("%s", res.err)
		}
		ui.End()
	}
	switch {
	case len(failed) > 0:
		return fmt.Errorf("grep failed in %s", strings.Join(failed, ", "))
	case !matched:
		return errSilent
	}
	return nil
}
