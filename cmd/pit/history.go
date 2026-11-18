package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// The history of single repos, as git shows it: pit graph and pit changes work on the repo the current directory is in, unless -r or -g
// select others. Their arguments are git's, in order: options and revisions, then "--" and paths relative to the current directory.

// hereRepos returns the repos selected with -r and -g, or else the repo the current directory is in.
func hereRepos(w *workspace.Workspace, p *parsed) ([]*workspace.Repo, error) {
	if p.narrowed() {
		return p.selected(w)
	}
	l, err := w.Locate(".")
	switch {
	case err != nil:
		return nil, err
	case l.Owner == nil:
		return nil, fmt.Errorf("%s is in none of the repos; select one with -r", w.Cwd)
	case !l.Owner.Cloned():
		return nil, fmt.Errorf("%s is not cloned yet; run pit sync", l.Owner.Name)
	}
	return []*workspace.Repo{l.Owner}, nil
}

// splitPaths splits git's arguments at "--" into options and revisions, and paths.
func splitPaths(args []string) (before, paths []string) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], args[i+1:]
	}
	return args, nil
}

// pathTargets returns the repos with the paths each owns; without paths, the repos as they are. With paths, repos owning none are left
// out.
func pathTargets(w *workspace.Workspace, repos []*workspace.Repo, paths []string) ([]workspace.Target, error) {
	if len(paths) == 0 {
		return targetsOf(repos), nil
	}
	routed, err := w.Route(paths)
	if err != nil {
		return nil, err
	}
	var targets []workspace.Target
	for _, t := range routed {
		if slices.Contains(repos, t.Repo) {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		names := make([]string, len(repos))
		for i, r := range repos {
			names[i] = r.Label()
		}
		return nil, fmt.Errorf("the paths lie outside %s", strings.Join(names, ", "))
	}
	return targets, nil
}

// cmdGraph shows the history of the repo the current directory is in as git log --graph draws it, with git's colors for the branch lines
// and names; one line per commit unless the options give a format of their own.
func cmdGraph(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("graph", args, spec{passthrough: true, gitArgs: true})
	if err != nil {
		return err
	}
	opts, paths := splitPaths(p.opts)
	repos, err := hereRepos(w, p)
	if err != nil {
		return err
	}
	targets, err := pathTargets(w, repos, paths)
	if err != nil {
		return err
	}
	gitArgs := []string{"--no-pager", "log", "--graph", "--decorate"}
	if !slices.ContainsFunc(opts, func(o string) bool {
		return o == "--oneline" || strings.HasPrefix(o, "--format") || strings.HasPrefix(o, "--pretty")
	}) {
		gitArgs = append(gitArgs, "--oneline")
	}
	gitArgs = append(gitArgs, opts...)
	return sequence("log", targets, true, func(t workspace.Target) []string {
		return pathspecArgs(slices.Clone(gitArgs), t)
	})
}

// cmdChanges lists the files a commit changed, compared with the commit before it (the first parent of a merge), or the files that differ
// between a commit and another revision. Names like HEAD~2 are the current repo's; a commit not found there is looked for in all repos.
func cmdChanges(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("changes", args, spec{passthrough: true, gitArgs: true})
	if err != nil {
		return err
	}
	before, paths := splitPaths(p.opts)
	var opts, revs []string
	for _, a := range before {
		if strings.HasPrefix(a, "-") {
			opts = append(opts, a)
		} else {
			revs = append(revs, a)
		}
	}
	if len(revs) == 0 || len(revs) > 2 {
		return errors.New("changes: give a commit, and the revision to compare it with if not the commit before it")
	}
	r, commit, err := changesCommit(w, p, revs[0])
	if err != nil {
		return err
	}
	from, to, first := "", commit, false
	if len(revs) == 2 {
		if from, to = commit, resolveIn(r, revs[1]); to == "" {
			return fmt.Errorf("changes: %s is no commit in %s", revs[1], r.Label())
		}
	} else if from = resolveIn(r, commit+"^1"); from == "" {
		// the repo's first commit: compared with the empty tree
		first = true
		if from, err = git.Input(r.Dir, "", "hash-object", "-t", "tree", "--stdin"); err != nil {
			return err
		}
	}
	targets, err := pathTargets(w, []*workspace.Repo{r}, paths)
	if err != nil {
		return err
	}
	t := targets[0]

	header(r)
	for i, rev := range []string{from, to} {
		label := []string{"from", "to  "}[i]
		if i == 0 && first {
			fmt.Printf("%s %s\n", label, ui.Dim("nothing, as it is the first commit"))
			continue
		}
		line, _ := git.Output(r.Dir, "log", "-1", "--format=%h%x1f%s", rev)
		short, subject, _ := strings.Cut(line, "\x1f")
		fmt.Printf("%s %s %s\n", label, ui.Yellow(short), subject)
	}
	defer ui.End()
	if listsFiles(opts) || hasOpt(opts, "-p", "--patch") {
		return git.Pass(r.Dir, pathspecArgs(append(append([]string{"--no-pager", "diff"}, opts...), from, to), t)...)
	}
	out, err := git.Output(r.Dir, pathspecArgs(append(append([]string{"diff", "--name-status", "-z", "-M"}, opts...), from, to), t)...)
	if err != nil {
		return err
	}
	recs := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if out == "" {
		fmt.Println(ui.Dim("  no changes to files"))
		return nil
	}
	for i := 0; i+1 < len(recs); i += 2 {
		status, path := recs[i], w.Display(r, recs[i+1])
		if (status[0] == 'R' || status[0] == 'C') && i+2 < len(recs) {
			i++
			path += " -> " + w.Display(r, recs[i+1])
		}
		fmt.Printf("  %s %s\n", changeLetter(status[:1]), path)
	}
	return nil
}

// changesCommit finds rev for pit changes: in the selected repo, or the one the current directory is in, or else in any repo.
func changesCommit(w *workspace.Workspace, p *parsed, rev string) (*workspace.Repo, string, error) {
	if strings.HasPrefix(rev, "-") {
		return nil, "", fmt.Errorf("changes: %s is no commit", rev)
	}
	if p.narrowed() {
		repos, err := p.selected(w)
		if err != nil {
			return nil, "", err
		}
		if len(repos) != 1 {
			return nil, "", errors.New("changes: select one repo")
		}
		if hash := resolveIn(repos[0], rev); hash != "" {
			return repos[0], hash, nil
		}
		return nil, "", fmt.Errorf("changes: %s is no commit in %s", rev, repos[0].Label())
	}
	if l, err := w.Locate("."); err == nil && l.Owner != nil && l.Owner.Cloned() {
		if hash := resolveIn(l.Owner, rev); hash != "" {
			return l.Owner, hash, nil
		}
	}
	c, err := findCommit(w.Cloned(), rev)
	if err != nil {
		return nil, "", fmt.Errorf("changes: %s", err)
	}
	return c.repo, c.hash, nil
}

// resolveIn returns the commit rev names in r, "" if none.
func resolveIn(r *workspace.Repo, rev string) string {
	hash, err := git.Output(r.Dir, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return hash
}

// changeLetter colors a status letter of git diff --name-status: added green, deleted red, the others yellow.
func changeLetter(l string) string {
	switch l {
	case "A":
		return ui.Green(l)
	case "D":
		return ui.Red(l)
	}
	return ui.Yellow(l)
}
