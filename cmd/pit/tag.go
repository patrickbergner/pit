package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// cmdTag shows each repo's latest tag, or with a name, tags HEAD of the selected repos.
func cmdTag(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("tag", args, spec{})
	if err != nil {
		return err
	}
	if len(p.paths) == 0 {
		return tagOverview(w, p)
	}
	name, repos, err := tagTargets(w, p, "tag")
	if err != nil {
		return err
	}
	unlock, err := w.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := createTags("tag", repos, name); err != nil {
		return err
	}
	for _, r := range repos {
		header(r)
		commit, _ := git.Output(r.Dir, "log", "-1", "--format=%h %s", "refs/tags/"+name)
		fmt.Println(ui.Yellow(name) + " on " + commit)
		ui.End()
	}
	ui.Note("pit push publishes it")
	return nil
}

// cmdPushTag tags HEAD of the selected repos, then pushes them as pit push does, the new tag along with the branch.
func cmdPushTag(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("pushtag", args, spec{bools: []string{"i|interactive", "y|yes"}})
	if err != nil {
		return err
	}
	name, repos, err := tagTargets(w, p, "pushtag")
	if err != nil {
		return err
	}
	// everything that can stop the push comes before the tags are made
	if err := pushOptions(p); err != nil {
		return err
	}
	// a tag of that name on a remote would keep the new one from being pushed
	type remoteTag struct {
		remote string
		has    bool
		err    error
	}
	statuses := map[*workspace.Repo]repoStatus{}
	for _, s := range readStatuses(targetsOf(repos)) {
		statuses[s.repo] = s
	}
	var problems []string
	lookup := func(r *workspace.Repo, asking bool) remoteTag {
		s := statuses[r]
		if s.err != nil {
			return remoteTag{err: s.err}
		}
		remote := tagRemote(s)
		if remote == "" {
			return remoteTag{}
		}
		args := []string{"ls-remote", "--tags", "--refs", remote, "refs/tags/" + name}
		run := git.Output
		if asking {
			run = git.Prompting
		}
		out, err := run(r.Dir, args...)
		return remoteTag{remote, out != "", err}
	}
	needsCredentials := func(rt remoteTag) bool { return rt.err != nil && git.NeedsCredentials(rt.err.Error()) }
	for i, rt := range inTurns(repos, "to look at the remote's tags", lookup, needsCredentials) {
		switch {
		case rt.err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s", repos[i].Label(), rt.err))
			if git.NeedsCredentials(rt.err.Error()) {
				noteCredentials(repos[i])
			}
		case rt.has:
			problems = append(problems, fmt.Sprintf("%s: %s already has a tag %s", repos[i].Label(), rt.remote, name))
		}
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return errors.New("pushtag: nothing was tagged")
	}
	if err := createTags("pushtag", repos, name); err != nil {
		return err
	}
	labels := make([]string, len(repos))
	for i, r := range repos {
		labels[i] = r.Label()
	}
	ui.Note("tagged %s in %s", name, strings.Join(labels, ", "))

	pushed, err := pushRepos(w, p, repos)
	var local []string
	for _, r := range repos {
		if !slices.ContainsFunc(pushed, func(st pushStep) bool { return st.repo == r && slices.Contains(st.tags, name) }) {
			local = append(local, r.Label())
		}
	}
	if len(local) > 0 {
		ui.Note("%s stays local in %s; pit push publishes it", name, strings.Join(local, ", "))
	}
	return err
}

// tagTargets returns the tag name and the repos to tag. Each repo has versions of its own, so they must be selected.
func tagTargets(w *workspace.Workspace, p *parsed, cmd string) (string, []*workspace.Repo, error) {
	if len(p.paths) != 1 {
		return "", nil, fmt.Errorf("%s: give one tag name, e.g. pit %s -r <repo> v1.0.0", cmd, cmd)
	}
	if !p.narrowed() {
		return "", nil, fmt.Errorf("%s: give the repos to tag with -r or -g; each has versions of its own", cmd)
	}
	repos, err := p.selected(w)
	if err != nil {
		return "", nil, err
	}
	if len(repos) == 0 {
		return "", nil, fmt.Errorf("%s: none of the selected repos is cloned", cmd)
	}
	return p.paths[0], repos, nil
}

// createTags tags HEAD of each repo with name, after checking that all of them can be tagged.
func createTags(cmd string, repos []*workspace.Repo, name string) error {
	if !git.OK("", "check-ref-format", "refs/tags/"+name) {
		return fmt.Errorf("%s: %q isn't a valid tag name", cmd, name)
	}
	var problems []string
	for _, r := range repos {
		switch {
		case !git.OK(r.Dir, "rev-parse", "-q", "--verify", "HEAD"):
			problems = append(problems, r.Label()+": has no commits yet")
		case git.OK(r.Dir, "rev-parse", "-q", "--verify", "refs/tags/"+name):
			problems = append(problems, fmt.Sprintf("%s: already has a tag %s", r.Label(), name))
		}
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return fmt.Errorf("%s: nothing was tagged", cmd)
	}
	var done []string
	for _, r := range repos {
		if _, err := git.Output(r.Dir, "tag", name); err != nil {
			if len(done) > 0 {
				ui.Note("tagged: %s", strings.Join(done, ", "))
			}
			return fmt.Errorf("%s: %s: %w", cmd, r.Label(), err)
		}
		done = append(done, r.Label())
	}
	return nil
}

// tagOverview shows a block per repo with its latest tag on HEAD and the commits since; repos without tags are summed up.
func tagOverview(w *workspace.Workspace, p *parsed) error {
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	type latest struct {
		tag   string
		since int
	}
	results := workspace.Parallel(repos, func(r *workspace.Repo) latest {
		tag, err := git.Output(r.Dir, "describe", "--tags", "--abbrev=0", "HEAD")
		if err != nil || tag == "" {
			return latest{} // no commits, or no tag on them
		}
		n, _ := git.Output(r.Dir, "rev-list", "--count", "refs/tags/"+tag+"..HEAD")
		since, _ := strconv.Atoi(n)
		return latest{tag, since}
	})
	untagged := 0
	for i, r := range repos {
		res := results[i]
		if res.tag == "" {
			untagged++
			continue
		}
		header(r)
		line := ui.Yellow(res.tag)
		if res.since > 0 {
			line += ", " + ui.Plural(res.since, "commit") + " since"
		}
		fmt.Println(line)
		ui.End()
	}
	switch {
	case len(repos) == 0:
		fmt.Println("no repos")
	case untagged == len(repos):
		fmt.Printf("%s without tags\n", all(untagged, "repo"))
	case untagged > 0:
		fmt.Printf("%s without tags\n", ui.Plural(untagged, "more repo"))
	}
	return nil
}
