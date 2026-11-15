package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/config"
	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// fetchAll fetches the repos in parallel. Afterwards, each repo that printed something gets a block with it; quiet shows only failures.
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
			failed = append(failed, repos[i].Label())
		} else if quiet || out == "" {
			continue
		}
		header(repos[i])
		if out != "" {
			fmt.Println(out)
		} else {
			ui.Error("%s", res.err)
		}
		ui.End()
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
			ui.Error("%s: %s", s.repo.Label(), s.err)
		case explicit:
			targets = append(targets, workspace.Target{Repo: s.repo})
		case s.Detached:
			ui.Note("%s: skipped, HEAD is detached", s.repo.Label())
		case s.Upstream == "" || s.UpstreamGone:
			if s.repo.HasRemotes() {
				ui.Note("%s: skipped, %s has no upstream", s.repo.Label(), s.Branch)
			}
		default:
			targets = append(targets, workspace.Target{Repo: s.repo})
		}
	}
	return sequence("pull", targets, true, func(workspace.Target) []string {
		return append([]string{"pull"}, p.opts...)
	})
}

// pushStep is what pit push does in one repo: push the branch, its new tags, or both.
type pushStep struct {
	repo     *workspace.Repo
	branch   string
	dest     string   // e.g. origin/main; "" if only tags are pushed
	setUp    bool     // no upstream yet: push -u origin <branch>
	commits  []string // "<hash> <subject>"
	remote   string   // where the tags go
	mergeRef string   // the upstream branch on the remote (refs/heads/main), to push the branch to along with tags
	tags     []string // new tags on the branch
	blocked  string   // why the repo can't be pushed, e.g. it diverged from its upstream
}

// pushesBranch reports whether the step pushes the branch; otherwise it pushes only tags.
func (st pushStep) pushesBranch() bool { return st.dest != "" }

// what tells what the step pushes: "main -> origin/main, 2 commits, tag v1.4.1", or "tag v1.4.1 -> origin".
func (st pushStep) what() string {
	var parts []string
	if st.pushesBranch() {
		parts = append(parts, st.branch+" -> "+st.dest, ui.Plural(len(st.commits), "commit"))
	}
	if len(st.tags) > 0 {
		t := tagList(st.tags, func(s string) string { return s })
		if !st.pushesBranch() {
			t += " -> " + st.remote
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, ", ")
}

// args returns the arguments of the step's git push. The branch goes to its upstream branch by name, as the plan said, whatever
// push.default and the like would make of a plain git push. With tags, branch and tags go in one atomic push, so that a tag never gets
// to the remote without the commits it is on.
func (st pushStep) args(opts []string) []string {
	args := []string{"push"}
	if st.pushesBranch() && len(st.tags) > 0 {
		args = append(args, "--atomic")
	}
	switch {
	case st.setUp:
		args = append(args, "-u", "origin", st.branch)
	case st.pushesBranch() && st.remote != "" && st.mergeRef != "":
		args = append(args, st.remote, "refs/heads/"+st.branch+":"+st.mergeRef)
	case st.pushesBranch():
		// the upstream is a branch of the repo itself: as git push does it
	default:
		args = append(args, st.remote)
	}
	for _, t := range st.tags {
		args = append(args, "refs/tags/"+t)
	}
	return append(args, opts...)
}

// tagList returns "tag v1" or "tags v1, v2", the names passed through paint.
func tagList(tags []string, paint func(string) string) string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = paint(t)
	}
	if len(tags) == 1 {
		return "tag " + names[0]
	}
	return "tags " + strings.Join(names, ", ")
}

func cmdPush(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("push", args, spec{bools: []string{"n|dry-run", "i|interactive", "y|yes"}, passthrough: true, noPaths: true})
	if err != nil {
		return err
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	if err := pushOptions(p); err != nil {
		return err
	}
	if err := fetchAll(repos, nil, true); err != nil {
		return err
	}
	_, err = pushRepos(w, p, repos)
	return err
}

// pushOptions checks the options of pit push.
func pushOptions(p *parsed) error {
	if p.on("interactive") && p.on("yes") {
		return errors.New("push: -i (ask) and -y (don't ask) exclude each other")
	}
	for _, o := range []string{"--tags", "--follow-tags"} {
		if hasOpt(p.opts, o) {
			return fmt.Errorf("push: pit pushes the new tags on each branch it pushes by itself; leave out %s", o)
		}
	}
	return nil
}

// pushRepos is pit push in the repos, once the options are checked (pushOptions) and the repos fetched: the plan, the questions, the
// pushes. It returns the steps pushed.
func pushRepos(w *workspace.Workspace, p *parsed, repos []*workspace.Repo) ([]pushStep, error) {
	// asks reports whether pit asks before pushing r: -y and -i decide for all repos, otherwise the settings for r's groups
	asks := func(r *workspace.Repo) bool {
		switch {
		case p.on("yes"):
			return false
		case p.on("interactive"):
			return true
		case r.IsRoot():
			return w.Config.Settings.AskBeforePush([]string{config.RootKey})
		}
		return w.Config.Settings.AskBeforePush(r.Ext.Groups)
	}
	force := slices.ContainsFunc(p.opts, func(o string) bool {
		return o == "-f" || o == "--force" || strings.HasPrefix(o, "--force-with-lease") || strings.HasPrefix(o, "--force-if-includes")
	})
	statuses := readStatuses(targetsOf(repos))
	tags := findNewTags(statuses)

	var steps []pushStep
	var problems []string
	var unpushable []string // repos left out because of a problem
	for _, s := range statuses {
		r := s.repo
		nt := tags[r]
		if nt.err != nil {
			ui.Warn("%s: can't tell which tags %s lacks, so none are pushed: %s", r.Label(), nt.remote, nt.err)
		}
		if len(nt.elsewhere) > 0 {
			ui.Note("%s: not pushing %s, not on %s", r.Label(), tagList(nt.elsewhere, func(s string) string { return s }), s.Branch)
		}
		switch {
		case s.err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s", r.Label(), s.err))
			unpushable = append(unpushable, r.Label())
			continue
		case s.Unborn:
			continue
		case s.Upstream != "" && !s.UpstreamGone:
			if s.Ahead == 0 && len(nt.onBranch) == 0 {
				continue
			}
			st := pushStep{repo: r, branch: s.Branch, remote: tagRemote(s), tags: nt.onBranch}
			if s.Ahead > 0 {
				st.dest, st.commits = s.Upstream, logLines(r, "@{upstream}..HEAD")
				st.mergeRef, _ = git.Output(r.Dir, "config", "--get", "branch."+s.Branch+".merge")
				if s.Behind > 0 && !force {
					st.blocked = fmt.Sprintf("%s and %s have diverged (↑%d ↓%d); pit pull first", s.Branch, s.Upstream, s.Ahead, s.Behind)
				}
			}
			steps = append(steps, st)
			continue
		}
		// no upstream (or it is gone)
		if !s.repo.HasRemotes() {
			continue
		}
		if s.Detached {
			ui.Note("%s: skipped, HEAD is detached", r.Label())
			continue
		}
		if s.UpstreamGone {
			// The branch was published and then deleted on the remote, typically once it was merged. Pushing it would bring it back.
			commits := logLines(r, "HEAD", "--not", "--remotes")
			if len(commits) == 0 {
				ui.Note("%s: skipped, the upstream %s of %s is gone and there are no new commits", r.Label(), s.Upstream, s.Branch)
				continue
			}
			steps = append(steps, pushStep{repo: r, branch: s.Branch, dest: s.Upstream, commits: commits, blocked: fmt.Sprintf(
				"the upstream %s of %s is gone; publish the branch again with git push -u, or move its commits elsewhere", s.Upstream, s.Branch)})
			continue
		}
		if !git.OK(r.Dir, "remote", "get-url", "origin") {
			ui.Note("%s: skipped, %s has no upstream and there is no remote 'origin'", r.Label(), s.Branch)
			continue
		}
		st := pushStep{repo: r, branch: s.Branch, remote: "origin", tags: nt.onBranch}
		commits := logLines(r, "HEAD", "--not", "--remotes=origin")
		if len(commits) > 0 || !git.OK(r.Dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+s.Branch) {
			st.dest, st.setUp, st.commits = "origin/"+s.Branch, true, commits
		} else if len(st.tags) == 0 {
			continue
		}
		steps = append(steps, st)
	}

	if len(steps) == 0 && len(problems) == 0 {
		fmt.Println("nothing to push")
		return nil, nil
	}
	// the plan: a block per repo with where it goes, the commits and the tags
	var pushable []pushStep
	for _, st := range steps {
		if st.blocked != "" {
			problems = append(problems, st.repo.Label()+": "+st.blocked)
			unpushable = append(unpushable, st.repo.Label())
		} else {
			pushable = append(pushable, st)
		}
		header(st.repo)
		if st.pushesBranch() {
			line := ui.Green(st.branch) + " -> " + st.dest
			if st.setUp {
				line += "  (sets upstream)"
			}
			if st.blocked != "" {
				line += "  " + ui.Red("(can't push)")
			}
			fmt.Println(line)
			for _, c := range st.commits {
				hash, subject, _ := strings.Cut(c, " ")
				fmt.Println("  " + ui.Yellow(hash) + " " + subject)
			}
		}
		if len(st.tags) > 0 {
			fmt.Println(tagList(st.tags, ui.Yellow) + " -> " + st.remote)
		}
		ui.End()
	}
	for _, pr := range problems {
		ui.Error("%s", pr)
	}
	var asked, unasked []pushStep
	for _, st := range pushable {
		if asks(st.repo) {
			asked = append(asked, st)
		} else {
			unasked = append(unasked, st)
		}
	}
	switch {
	case p.on("dry-run"):
		if len(problems) > 0 {
			return nil, errSilent
		}
		return nil, nil
	case len(problems) > 0 && len(asked) == 0:
		// Without questions nobody chose to leave these repos behind, so related commits elsewhere aren't pushed either.
		return nil, errors.New("push: nothing was pushed (pit push -i asks repo by repo and pushes the others)")
	case len(pushable) == 0:
		return nil, errors.New("push: nothing was pushed")
	}
	if len(unpushable) > 0 {
		ui.Note("skipping %s; asking about the other repos", strings.Join(unpushable, ", "))
	}
	steps = pushable
	if len(asked) > 0 {
		if len(unasked) > 0 {
			names := make([]string, len(unasked))
			for i, st := range unasked {
				names[i] = st.repo.Label()
			}
			ui.Note("pushing %s without asking, as configured", strings.Join(names, ", "))
		}
		chosen, err := askPush(asked)
		if err != nil {
			return nil, err
		}
		yes := map[*workspace.Repo]bool{}
		for _, st := range chosen {
			yes[st.repo] = true
		}
		steps = slices.DeleteFunc(steps, func(st pushStep) bool { return asks(st.repo) && !yes[st.repo] })
		if len(steps) == 0 {
			ui.Note("nothing pushed")
			return nil, notPushed(unpushable)
		}
		fmt.Fprintln(os.Stderr) // the questions end, the pushes begin
	}

	// progress tells where the pushes stand when they end early: steps[:done] are pushed, steps[next:] are still to do
	progress := func(done, next int) {
		var pushed, rest []string
		for _, d := range steps[:done] {
			pushed = append(pushed, d.repo.Label())
		}
		for _, d := range steps[next:] {
			rest = append(rest, d.repo.Label())
		}
		if len(pushed) > 0 {
			ui.Note("pushed: %s", strings.Join(pushed, ", "))
		}
		if len(rest) > 0 {
			ui.Note("not pushed yet: %s", strings.Join(rest, ", "))
		}
		if len(unpushable) > 0 {
			ui.Note("can't push: %s", strings.Join(unpushable, ", "))
		}
	}
	for i, st := range steps {
		if err := stopped(); err != nil {
			progress(i, i)
			return steps[:i], err
		}
		header(st.repo)
		err := git.Pass(st.repo.Dir, st.args(p.opts)...)
		ui.End()
		if err != nil {
			progress(i, i+1)
			return steps[:i], fmt.Errorf("push failed in %s", st.repo.Label())
		}
	}
	ui.Note("pushed %s", ui.Plural(len(steps), "repo"))
	return steps, notPushed(unpushable)
}

// newTags are the tags of a repo that its push remote lacks: those on the current branch, which pit push pushes along with it, and the
// others, which it leaves alone.
type newTags struct {
	remote    string
	onBranch  []string
	elsewhere []string
	err       error
}

// tagRemote returns the remote pit push sends a repo's tags to, along with its branch: the upstream's remote, or origin if the branch has
// no upstream; "" if there is no such remote (or the upstream is in the repo itself).
func tagRemote(s repoStatus) string {
	remote := "origin"
	if s.Upstream != "" && !s.UpstreamGone {
		if rm, err := git.Output(s.repo.Dir, "config", "--get", "branch."+s.Branch+".remote"); err == nil && rm != "" {
			remote = rm
		}
	}
	if !git.OK(s.repo.Dir, "remote", "get-url", remote) {
		return ""
	}
	return remote
}

// findNewTags asks the remotes, in parallel, which tags they lack. Repos without tags don't need to ask.
func findNewTags(statuses []repoStatus) map[*workspace.Repo]newTags {
	var rs []*workspace.Repo
	byRepo := map[*workspace.Repo]repoStatus{}
	for _, s := range statuses {
		rs, byRepo[s.repo] = append(rs, s.repo), s
	}
	found := map[*workspace.Repo]newTags{}
	for i, nt := range workspace.Parallel(rs, func(r *workspace.Repo) newTags { return lookupNewTags(byRepo[r]) }) {
		found[rs[i]] = nt
	}
	return found
}

func lookupNewTags(s repoStatus) newTags {
	r := s.repo
	if s.err != nil || s.Detached || s.Unborn || !r.HasRemotes() {
		return newTags{}
	}
	local, err := git.Output(r.Dir, "tag", "--list", "--sort=version:refname")
	if err != nil || local == "" {
		return newTags{err: err}
	}
	nt := newTags{remote: tagRemote(s)}
	if nt.remote == "" {
		return newTags{}
	}
	out, err := git.Remote(r.Dir, "ls-remote", "--tags", "--refs", nt.remote)
	if err != nil {
		nt.err = err
		return nt
	}
	onRemote := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(l, "\t"); ok {
			onRemote[strings.TrimPrefix(ref, "refs/tags/")] = true
		}
	}
	merged, _ := git.Output(r.Dir, "tag", "--merged", "HEAD")
	onBranch := strings.Split(merged, "\n")
	for _, t := range strings.Split(local, "\n") {
		switch {
		case onRemote[t]:
		case slices.Contains(onBranch, t):
			nt.onBranch = append(nt.onBranch, t)
		default:
			nt.elsewhere = append(nt.elsewhere, t)
		}
	}
	return nt
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
s - skip this repo and all remaining ones
? - show this help`

// askPush asks repo by repo whether to push and returns the steps to do. All questions come before the first push, so a no costs nothing
// and the pushes then run without interruption.
func askPush(steps []pushStep) ([]pushStep, error) {
	var chosen []pushStep
	for i, st := range steps {
		prompt := fmt.Sprintf("(%d/%d) Push %s: %s? [y,n,a,s,?]", i+1, len(steps), st.repo.Label(), st.what())
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
			case "s", "skip":
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
