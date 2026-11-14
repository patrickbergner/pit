package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// fetchAll fetches the repos in parallel. Output is printed per repo afterwards; quiet prints only failures.
func fetchAll(repos []*workspace.Repo, opts []string, quiet bool) error {
	type result struct {
		out []byte
		err error
	}
	results := workspace.Parallel(repos, func(r *workspace.Repo) result {
		if !r.HasRemotes() {
			return result{}
		}
		out, err := git.Combined(r.Dir, append([]string{"fetch"}, opts...)...)
		return result{out, err}
	})
	var failed []string
	for i, res := range results {
		out := strings.TrimRight(string(res.out), "\n")
		if res.err != nil {
			failed = append(failed, repos[i].Name)
		} else if quiet || out == "" {
			continue
		}
		ui.Header(repos[i].Name, repos[i].Path)
		if out != "" {
			fmt.Println(out)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("fetch failed in %s", strings.Join(failed, ", "))
	}
	return nil
}

func cmdFetch(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("fetch", args, spec{passthrough: true, gitArgs: true})
	if err != nil {
		return err
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	return fetchAll(repos, p.opts, false)
}

func cmdPull(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("pull", args, spec{passthrough: true, gitArgs: true})
	if err != nil {
		return err
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	explicit := slices.ContainsFunc(p.opts, func(o string) bool { return !strings.HasPrefix(o, "-") })
	var targets []workspace.Target
	for _, s := range readStatuses(targetsOf(repos)) {
		switch {
		case s.err != nil:
			fmt.Fprintf(os.Stderr, "pit: %s: %s\n", s.repo.Name, s.err)
		case explicit:
			targets = append(targets, workspace.Target{Repo: s.repo})
		case s.Detached:
			ui.Note("%s: skipped, HEAD is detached", s.repo.Name)
		case s.Upstream == "" || s.UpstreamGone:
			if s.repo.HasRemotes() {
				ui.Note("%s: skipped, %s has no upstream", s.repo.Name, s.Branch)
			}
		default:
			targets = append(targets, workspace.Target{Repo: s.repo})
		}
	}
	return sequence("pull", targets, true, func(workspace.Target) []string {
		return append([]string{"pull"}, p.opts...)
	})
}

// pushStep is what pit push does in one repo.
type pushStep struct {
	repo    *workspace.Repo
	branch  string
	dest    string   // e.g. origin/main
	setUp   bool     // no upstream yet: push -u origin <branch>
	commits []string // "<hash> <subject>"
	blocked string   // why the repo can't be pushed, e.g. it diverged from its upstream
}

func cmdPush(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("push", args, spec{bools: []string{"n|dry-run", "i|interactive", "y|yes"}, passthrough: true, noPaths: true})
	if err != nil {
		return err
	}
	if p.on("interactive") && p.on("yes") {
		return errors.New("push: -i (ask) and -y (don't ask) exclude each other")
	}
	ask := (w.Config.Settings.AskBeforePush() || p.on("interactive")) && !p.on("yes")
	force := slices.ContainsFunc(p.opts, func(o string) bool {
		return o == "-f" || o == "--force" || strings.HasPrefix(o, "--force-with-lease") || strings.HasPrefix(o, "--force-if-includes")
	})
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	if err := fetchAll(repos, nil, true); err != nil {
		return err
	}

	var steps []pushStep
	var problems []string
	var unpushable []string // repos left out because of a problem
	for _, s := range readStatuses(targetsOf(repos)) {
		r := s.repo
		switch {
		case s.err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s", r.Name, s.err))
			unpushable = append(unpushable, r.Name)
			continue
		case s.Unborn:
			continue
		case s.Upstream != "" && !s.UpstreamGone:
			if s.Ahead == 0 {
				continue
			}
			st := pushStep{repo: r, branch: s.Branch, dest: s.Upstream, commits: logLines(r, "@{upstream}..HEAD")}
			if s.Behind > 0 && !force {
				st.blocked = fmt.Sprintf("%s and %s have diverged (↑%d ↓%d); pit pull first", s.Branch, s.Upstream, s.Ahead, s.Behind)
			}
			steps = append(steps, st)
			continue
		}
		// no upstream (or it is gone)
		if !s.repo.HasRemotes() {
			continue
		}
		if s.Detached {
			ui.Note("%s: skipped, HEAD is detached", r.Name)
			continue
		}
		if !git.OK(r.Dir, "remote", "get-url", "origin") {
			ui.Note("%s: skipped, %s has no upstream and there is no remote 'origin'", r.Name, s.Branch)
			continue
		}
		remoteRef := "refs/remotes/origin/" + s.Branch
		commits := logLines(r, "HEAD", "--not", "--remotes=origin")
		if git.OK(r.Dir, "rev-parse", "-q", "--verify", remoteRef) && len(commits) == 0 {
			continue
		}
		steps = append(steps, pushStep{repo: r, branch: s.Branch, dest: "origin/" + s.Branch, setUp: true, commits: commits})
	}

	if len(steps) == 0 && len(problems) == 0 {
		fmt.Println("nothing to push")
		return nil
	}
	var pushable []pushStep
	for _, st := range steps {
		head := fmt.Sprintf("%s  %s -> %s", st.repo.Name, st.branch, st.dest)
		if st.setUp {
			head += "  (sets upstream)"
		}
		if st.blocked != "" {
			head += "  (can't push)"
			problems = append(problems, st.repo.Name+": "+st.blocked)
			unpushable = append(unpushable, st.repo.Name)
		} else {
			pushable = append(pushable, st)
		}
		fmt.Println(ui.Bold(head))
		for _, c := range st.commits {
			fmt.Println("  " + c)
		}
	}
	for _, pr := range problems {
		fmt.Fprintf(os.Stderr, "pit: %s\n", pr)
	}
	switch {
	case p.on("dry-run"):
		if len(problems) > 0 {
			return errSilent
		}
		return nil
	case len(problems) > 0 && !ask:
		// Without questions nobody chose to leave these repos behind, so related commits elsewhere aren't pushed either.
		return errors.New("push: nothing was pushed (pit push -i asks repo by repo and pushes the others)")
	case len(pushable) == 0:
		return errors.New("push: nothing was pushed")
	}
	if len(unpushable) > 0 {
		ui.Note("skipping %s; asking about the other repos", strings.Join(unpushable, ", "))
	}
	steps = pushable
	if ask {
		if steps, err = askPush(steps); err != nil {
			return err
		}
		if len(steps) == 0 {
			ui.Note("nothing pushed")
			return notPushed(unpushable)
		}
	}

	for i, st := range steps {
		if err := stopped(); err != nil {
			return err
		}
		args := []string{"push"}
		if st.setUp {
			args = append(args, "-u", "origin", st.branch)
		}
		if err := git.Pass(st.repo.Dir, append(args, p.opts...)...); err != nil {
			var pushed, rest []string
			for _, d := range steps[:i] {
				pushed = append(pushed, d.repo.Name)
			}
			for _, d := range steps[i+1:] {
				rest = append(rest, d.repo.Name)
			}
			if len(pushed) > 0 {
				fmt.Fprintf(os.Stderr, "pit: pushed: %s\n", strings.Join(pushed, ", "))
			}
			if len(rest) > 0 {
				fmt.Fprintf(os.Stderr, "pit: not pushed yet: %s\n", strings.Join(rest, ", "))
			}
			if len(unpushable) > 0 {
				fmt.Fprintf(os.Stderr, "pit: can't push: %s\n", strings.Join(unpushable, ", "))
			}
			return fmt.Errorf("push failed in %s", st.repo.Name)
		}
	}
	ui.Note("pushed %s", ui.Plural(len(steps), "repo"))
	return notPushed(unpushable)
}

// notPushed makes pit exit with an error when repos were left out because of a problem, so the push doesn't look complete.
func notPushed(repos []string) error {
	if len(repos) == 0 {
		return nil
	}
	return fmt.Errorf("push: not pushed because of the problems above: %s", strings.Join(repos, ", "))
}

const pushHelp = `y - push this repo
n - don't push this repo
a - push this repo and all remaining ones
q - don't push this repo or any remaining one
? - show this help`

// askPush asks repo by repo whether to push and returns the steps to do. All questions come before the first push, so a no costs nothing
// and the pushes then run without interruption.
func askPush(steps []pushStep) ([]pushStep, error) {
	var chosen []pushStep
	for i, st := range steps {
		prompt := fmt.Sprintf("(%d/%d) Push %s: %s -> %s, %s? [y,n,a,q,?]", i+1, len(steps), st.repo.Name, st.branch, st.dest,
			ui.Plural(len(st.commits), "commit"))
		for {
			answer, err := ui.Ask(prompt, interruptCh)
			if errors.Is(err, ui.ErrNoAnswer) {
				return nil, errors.New("push: no answer on stdin, nothing was pushed (pit push -y pushes without asking)")
			} else if err != nil {
				return nil, fmt.Errorf("push: %w, nothing was pushed", err)
			}
			switch answer {
			case "y", "yes":
				chosen = append(chosen, st)
			case "n", "no":
			case "a", "all":
				return append(chosen, steps[i:]...), nil
			case "q", "quit":
				return chosen, nil
			default:
				fmt.Fprintln(os.Stderr, pushHelp)
				continue
			}
			break
		}
	}
	return chosen, nil
}

// logLines lists commits as "<hash> <subject>".
func logLines(r *workspace.Repo, revs ...string) []string {
	out, err := git.Output(r.Dir, append([]string{"log", "--format=%h %s"}, revs...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func cmdEach(w *workspace.Workspace, args []string) error {
	// pit's own options (-r, -g) come first; everything from the first other argument (or after "--") is git's
	var own []string
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			args = args[1:]
			break
		}
		if a == "-r" || a == "--repo" || a == "-g" || a == "--group" {
			if len(args) < 2 {
				return errors.New("each: option " + a + " needs a value")
			}
			own, args = append(own, args[:2]...), args[2:]
			continue
		}
		if strings.HasPrefix(a, "-r") || strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "-g") || strings.HasPrefix(a, "--group=") {
			own, args = append(own, a), args[1:]
			continue
		}
		break
	}
	if len(args) == 0 {
		return errors.New("each: give the git command to run, e.g. pit each -- switch -c feature/x")
	}
	p, err := parseArgs("each", own, spec{noPaths: true})
	if err != nil {
		return err
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	return sequence("git "+args[0], targetsOf(repos), true, func(workspace.Target) []string { return args })
}
