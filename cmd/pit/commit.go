package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// Files in pit's state directory.
const (
	commitMsgFile  = "COMMIT_EDITMSG"
	inProgressFile = "commit-in-progress.json"
	lastCommitFile = "last-commit.json"
)

// commitStep is one repo of a pit commit.
type commitStep struct {
	Repo   string   `json:"repo"`
	Paths  []string `json:"paths,omitempty"`  // commit just these, as git commit <paths> does
	Amend  string   `json:"amend,omitempty"`  // commit to amend; HEAD must still be it
	Revert string   `json:"revert,omitempty"` // commit to revert first, with git revert --no-commit
	Hash   string   `json:"hash,omitempty"`   // the new commit, once made
}

// commitPlan is a pit commit underway, kept in inProgressFile until every step is done.
type commitPlan struct {
	All      bool         `json:"all"`
	NoVerify bool         `json:"noVerify"`
	Steps    []commitStep `json:"steps"`
}

// lastCommit records the commits of the last completed pit commit, for --amend.
type lastCommit struct {
	Commits []commitStep `json:"commits"`
}

func cmdCommit(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("commit", args, spec{
		bools:  []string{"a|all", "n|no-verify", "continue", "abort", "amend", "no-edit"},
		values: []string{"m|message", "F|file"},
	})
	if err != nil {
		return err
	}
	msgGiven := len(p.vals["message"]) > 0 || p.last("file") != ""
	switch {
	case p.on("continue") || p.on("abort"):
		if p.on("continue") && p.on("abort") || msgGiven || p.on("amend") || p.on("all") || p.narrowed() || len(p.paths) > 0 {
			return errors.New("commit: --continue and --abort take no other options")
		}
	case p.on("amend") && p.narrowed():
		return errors.New("commit: --amend covers the repos of the last pit commit; it doesn't take -r or -g")
	case len(p.paths) > 0 && (p.on("all") || p.on("amend")):
		return errors.New("commit: paths don't go with -a or --amend")
	case p.on("no-edit") && msgGiven:
		return errors.New("commit: --no-edit and a message exclude each other")
	case p.on("no-edit") && !p.on("amend"):
		return errors.New("commit: --no-edit only goes with --amend")
	}

	var plan commitPlan
	inProgress, err := w.ReadState(inProgressFile, &plan)
	if err != nil {
		return err
	}
	switch {
	case p.on("abort"):
		if !inProgress {
			return errors.New("commit: no pit commit in progress")
		}
		reportPlan(&plan)
		ui.Note("forgot the pit commit in progress; the commits made are kept")
		return w.RemoveState(inProgressFile)
	case p.on("continue"):
		if !inProgress {
			return errors.New("commit: no pit commit in progress")
		}
		return runPlan(w, &plan)
	case inProgress:
		reportPlan(&plan)
		return errors.New("commit: a pit commit stopped halfway; finish it with pit commit --continue, or pit commit --abort")
	}

	plan = commitPlan{All: p.on("all"), NoVerify: p.on("no-verify")}
	var oldMessage string
	if p.on("amend") {
		if oldMessage, err = planAmend(w, &plan); err != nil {
			return err
		}
	}

	// Fresh commits in the repos with something to commit; with paths, in the repos owning them, of just those paths
	targets, err := commitTargets(w, p)
	if err != nil {
		return err
	}
	statuses := map[string]repoStatus{}
	for _, s := range readStatuses(targets) {
		statuses[s.repo.Name] = s
	}
	amending := map[string]bool{}
	for _, st := range plan.Steps {
		amending[st.Repo] = true
	}
	// Pre-flight: everything that would foreseeably fail, for all repos at once
	var problems []string
	var unstaged []string
	for _, t := range targets {
		r, s := t.Repo, statuses[t.Repo.Name]
		switch {
		case amending[r.Name]: // checked with the plan's steps below
		case s.err != nil:
			// whether it has something to commit is unknown, so it can't be left out silently
			problems = append(problems, fmt.Sprintf("%s: %s", r.Label(), s.err))
		case hasWork(s, plan.All || len(t.Pathspecs) > 0):
			plan.Steps = append(plan.Steps, commitStep{Repo: r.Name, Paths: t.Pathspecs})
		case !s.Clean() && !s.Conflicts():
			unstaged = append(unstaged, r.Label())
		}
	}
	if len(p.paths) > 0 {
		problems = append(problems, unknownPaths(targets)...)
	}
	plan.Steps = inWorkspaceOrder(w, plan.Steps)
	if len(plan.Steps) == 0 && len(problems) == 0 {
		switch {
		case len(p.paths) > 0:
			return errors.New("nothing to commit in these paths")
		case len(unstaged) > 0:
			return fmt.Errorf("nothing staged to commit in any repo (changes in %s; use pit add or pit commit -a)", strings.Join(unstaged, ", "))
		}
		return errors.New("nothing to commit, all repos clean")
	}

	for _, st := range plan.Steps {
		r, _ := byName(w, st.Repo)
		s, ok := statuses[st.Repo]
		if !ok || len(st.Paths) > 0 { // the whole repo's state counts, not just that of the paths
			var err error
			s.Status, err = workspace.ReadStatus(r, nil)
			s.repo, s.err = r, err
		}
		problems = append(problems, preflight(s)...)
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return errors.New("commit: nothing was committed")
	}

	msg, err := commitMessage(w, p, &plan, statuses, oldMessage)
	if err != nil {
		return err
	}
	if err := w.WriteStateFile(commitMsgFile, []byte(msg)); err != nil {
		return err
	}
	if err := w.WriteState(inProgressFile, &plan); err != nil {
		return err
	}
	return runPlan(w, &plan)
}

// commitTargets returns the repos pit commit looks at: the selected ones, or with paths, the ones owning them, each with its paths.
func commitTargets(w *workspace.Workspace, p *parsed) ([]workspace.Target, error) {
	if len(p.paths) == 0 {
		repos, err := p.selected(w)
		return targetsOf(repos), err
	}
	routed, err := p.targets(w)
	if err != nil {
		return nil, err
	}
	var targets []workspace.Target
	for _, t := range routed {
		if t = ownPathspecs(t); len(t.Pathspecs) > 0 {
			targets = append(targets, t)
		}
	}
	return targets, nil
}

// unknownPaths lists the paths that git commit would refuse: those matching no file git knows, in the index or in HEAD, such as new files
// not added yet. A repo's top directory, for a directory above it, is left out.
func unknownPaths(targets []workspace.Target) []string {
	var problems []string
	for _, t := range targets {
		specs := slices.DeleteFunc(slices.Clone(t.Pathspecs), func(s string) bool { return s == "." })
		if len(specs) == 0 {
			continue
		}
		args := []string{"ls-files", "--error-unmatch"}
		if git.OK(t.Repo.Dir, "rev-parse", "-q", "--verify", "HEAD") {
			args = append(args, "--with-tree=HEAD")
		}
		_, err := git.Output(t.Repo.Dir, append(append(args, "--"), specs...)...)
		var gerr *git.Error
		if !errors.As(err, &gerr) {
			continue
		}
		reported := false
		for _, line := range strings.Split(gerr.Stderr, "\n") {
			if msg, ok := strings.CutPrefix(line, "error: "); ok {
				problems = append(problems, fmt.Sprintf("%s: %s; pit add new files first", t.Repo.Label(), msg))
				reported = true
			}
		}
		if !reported {
			problems = append(problems, fmt.Sprintf("%s: %s", t.Repo.Label(), err))
		}
	}
	return problems
}

func targetsOf(repos []*workspace.Repo) []workspace.Target {
	t := make([]workspace.Target, len(repos))
	for i, r := range repos {
		t[i] = workspace.Target{Repo: r}
	}
	return t
}

func hasWork(s repoStatus, all bool) bool {
	if all {
		return s.TrackedChanges()
	}
	return s.Staged()
}

func byName(w *workspace.Workspace, name string) (*workspace.Repo, error) {
	for _, r := range w.All() {
		if r.Name == name {
			if !r.Cloned() {
				return nil, fmt.Errorf("%s is not cloned", name)
			}
			return r, nil
		}
	}
	return nil, fmt.Errorf("repo %q is no longer in %s", name, "pit-externals.json")
}

func inWorkspaceOrder(w *workspace.Workspace, steps []commitStep) []commitStep {
	var out []commitStep
	for _, r := range w.All() {
		for _, st := range steps {
			if st.Repo == r.Name {
				out = append(out, st)
			}
		}
	}
	return out
}

// preflight lists why committing in a repo would fail.
func preflight(s repoStatus) []string {
	name := s.repo.Label()
	if s.err != nil {
		return []string{fmt.Sprintf("%s: %s", name, s.err)}
	}
	var p []string
	if s.Detached {
		p = append(p, name+": HEAD is detached; switch to a branch first")
	}
	if s.Conflicts() {
		p = append(p, name+": has unresolved conflicts")
	}
	if s.Operation != "" {
		p = append(p, fmt.Sprintf("%s: is in the middle of %s; finish it with git first", name, s.Operation))
	}
	for _, v := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if !git.OK(s.repo.Dir, "var", v) {
			p = append(p, name+": no git identity configured (git config user.name / user.email)")
			break
		}
	}
	return p
}

// planAmend adds the commits of the last pit commit to the plan and returns their message.
func planAmend(w *workspace.Workspace, plan *commitPlan) (string, error) {
	var last lastCommit
	found, err := w.ReadState(lastCommitFile, &last)
	if err != nil {
		return "", err
	}
	if !found || len(last.Commits) == 0 {
		return "", errors.New("commit --amend: no pit commit to amend (pit amends only the last commit it made; amend others with git)")
	}
	var problems []string
	for _, c := range last.Commits {
		r, err := byName(w, c.Repo)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		head, err := git.Output(r.Dir, "rev-parse", "HEAD")
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s", r.Label(), err))
		case head != c.Hash:
			problems = append(problems, fmt.Sprintf("%s: has moved on since the last pit commit (HEAD is no longer %.7s)", r.Label(), c.Hash))
		default:
			if remotes, _ := git.Output(r.Dir, "branch", "-r", "--contains", c.Hash); strings.TrimSpace(remotes) != "" {
				problems = append(problems, fmt.Sprintf("%s: %.7s is already pushed", r.Label(), c.Hash))
			}
		}
		plan.Steps = append(plan.Steps, commitStep{Repo: c.Repo, Amend: c.Hash})
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return "", errors.New("commit --amend: can't amend the last pit commit; nothing was changed")
	}
	r, _ := byName(w, last.Commits[0].Repo)
	return git.Output(r.Dir, "log", "-1", "--format=%B", last.Commits[0].Hash)
}

// commitMessage gets the message from -m, -F, the amended commit (--no-edit) or the editor.
func commitMessage(w *workspace.Workspace, p *parsed, plan *commitPlan, statuses map[string]repoStatus, oldMessage string) (string, error) {
	var msg string
	edit := false
	switch {
	case p.last("file") != "":
		path := p.last("file")
		if !filepath.IsAbs(path) {
			path = filepath.Join(w.Cwd, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		msg = string(data)
	case len(p.vals["message"]) > 0:
		msg = strings.Join(p.vals["message"], "\n\n")
	case p.on("no-edit"):
		msg = oldMessage
	default:
		edit = true
		path := w.StatePath(commitMsgFile)
		if err := w.WriteStateFile(commitMsgFile, []byte(template(oldMessage, plan, statuses))); err != nil {
			return "", err
		}
		// git config --edit opens git's configured editor (GIT_EDITOR, core.editor, ...) on any file.
		if err := git.Pass(w.Root, "config", "--file", path, "--edit"); err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		msg = string(data)
	}
	args := []string{"stripspace"}
	if edit {
		// the template's comments start with '#', whatever core.commentChar says
		args = []string{"-c", "core.commentChar=#", "stripspace", "--strip-comments"}
	}
	clean, err := git.Input(w.Root, msg, args...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(clean) == "" {
		return "", errors.New("aborting commit due to empty commit message")
	}
	return clean + "\n", nil
}

func template(oldMessage string, plan *commitPlan, statuses map[string]repoStatus) string {
	var b strings.Builder
	b.WriteString(oldMessage + "\n")
	b.WriteString("\n# Please enter the commit message for your changes. Lines starting\n" +
		"# with '#' will be ignored, and an empty message aborts the commit.\n#\n" +
		"# pit commits with this message in each of these repos:\n")
	for _, st := range plan.Steps {
		s := statuses[st.Repo]
		b.WriteString("#\n# " + workspace.Label(st.Repo))
		if s.Status != nil {
			b.WriteString(" (" + s.branchLabel() + ")")
		}
		if st.Amend != "" {
			b.WriteString(", amending " + st.Amend[:7])
		}
		if st.Revert != "" {
			b.WriteString(", reverting " + st.Revert[:7])
		}
		b.WriteString(":\n")
		if s.Status == nil {
			continue
		}
		for _, e := range s.Entries {
			// with paths, their changes are committed whether staged or not
			if e.Untracked() || (!plan.All && len(st.Paths) == 0 && e.XY[0] == ' ') {
				continue
			}
			path := e.Path
			if e.Orig != "" {
				path = e.Orig + " -> " + path
			}
			fmt.Fprintf(&b, "#\t%s %s\n", e.XY, path)
		}
	}
	return b.String()
}

// runPlan commits in the plan's remaining repos, one after the other, each in a block with what git reports, saving progress after each.
// It stops at the first failure.
func runPlan(w *workspace.Workspace, plan *commitPlan) error {
	msgFile := w.StatePath(commitMsgFile)
	if _, err := os.Stat(msgFile); err != nil {
		return fmt.Errorf("the message of the pit commit in progress is gone (%v); pit commit --abort, then commit again", err)
	}
	for i := range plan.Steps {
		st := &plan.Steps[i]
		if st.Hash != "" {
			continue
		}
		if err := stopped(); err != nil {
			return stopPlan(w, plan, i, err)
		}
		r, err := byName(w, st.Repo)
		if err != nil {
			return stopPlan(w, plan, i, err)
		}
		header(r)
		err = commitIn(r, st, plan, msgFile)
		ui.End()
		if err != nil {
			return stopPlan(w, plan, i, err)
		}
		if err := w.WriteState(inProgressFile, plan); err != nil {
			return err
		}
	}

	var last lastCommit
	for _, st := range plan.Steps {
		if st.Hash != "-" {
			last.Commits = append(last.Commits, commitStep{Repo: st.Repo, Hash: st.Hash})
		}
	}
	if len(last.Commits) == 0 {
		// every repo was skipped: the last pit commit stays the one to amend
		if err := w.RemoveState(inProgressFile); err != nil {
			return err
		}
		return errors.New("commit: nothing was committed")
	}
	if err := w.WriteState(lastCommitFile, &last); err != nil {
		return err
	}
	return w.RemoveState(inProgressFile)
}

// commitIn makes the step's commit in r and records its hash, "-" if there is nothing to commit anymore.
func commitIn(r *workspace.Repo, st *commitStep, plan *commitPlan, msgFile string) error {
	args := []string{"commit", "-F", msgFile}
	switch {
	case st.Amend != "":
		if head, _ := git.Output(r.Dir, "rev-parse", "HEAD"); head != st.Amend {
			return fmt.Errorf("%s has moved on (HEAD is no longer %.7s); not amending", r.Label(), st.Amend)
		}
		args = append(args, "--amend")
	case st.Revert != "":
		s, err := workspace.ReadStatus(r, nil)
		if err != nil {
			return err
		}
		// After a conflict, the revert is underway already; git commit finishes it.
		if s.Operation != "reverting" {
			if err := git.Pass(r.Dir, "revert", "--no-commit", st.Revert); err != nil {
				return fmt.Errorf("%s: git revert of %.7s failed; resolve the conflicts there and git add the files, "+
					"then pit commit --continue (or git revert --abort there, then pit commit --abort)", r.Label(), st.Revert)
			}
		}
	default:
		s, err := workspace.ReadStatus(r, st.Paths)
		if err != nil {
			return err
		}
		if !hasWork(repoStatus{r, s, nil}, plan.All || len(st.Paths) > 0) {
			ui.Note("nothing to commit anymore, skipped")
			st.Hash = "-"
			return nil
		}
	}
	if plan.All {
		args = append(args, "-a")
	}
	if plan.NoVerify {
		args = append(args, "--no-verify")
	}
	if len(st.Paths) > 0 {
		args = append(append(args, "--"), st.Paths...)
	}
	if err := git.Pass(r.Dir, args...); err != nil {
		return fmt.Errorf("%s: git commit failed", r.Label())
	}
	hash, err := git.Output(r.Dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	st.Hash = hash
	return nil
}

// stopPlan saves the plan after the step at index i failed and explains how to go on.
func stopPlan(w *workspace.Workspace, plan *commitPlan, i int, cause error) error {
	if err := w.WriteState(inProgressFile, plan); err != nil {
		return err
	}
	ui.Error("%s", cause)
	reportPlan(plan)
	return errors.New("fix the problem, then pit commit --continue (or pit commit --abort to leave it at that)")
}

func reportPlan(plan *commitPlan) {
	var done, pending []string
	for _, st := range plan.Steps {
		switch st.Hash {
		case "":
			pending = append(pending, workspace.Label(st.Repo))
		case "-":
		default:
			done = append(done, fmt.Sprintf("%s %.7s", workspace.Label(st.Repo), st.Hash))
		}
	}
	if len(done) > 0 {
		ui.Note("committed: %s", strings.Join(done, ", "))
	}
	if len(pending) > 0 {
		ui.Note("not committed yet: %s", strings.Join(pending, ", "))
	}
}

// cmdRevert reverts a pit commit, given by one of its commits: git revert --no-commit in each of its repos, then a commit with one message
// for all of them, made as pit commit makes its commits, so that --continue, --abort and --amend work for it too.
func cmdRevert(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("revert", args, spec{bools: []string{"n|no-verify", "no-edit"}, values: []string{"m|message", "F|file"}})
	if err != nil {
		return err
	}
	if len(p.paths) != 1 {
		return errors.New("revert: give a commit of the pit commit to revert (pit log shows them)")
	}
	if p.on("no-edit") && (len(p.vals["message"]) > 0 || p.last("file") != "") {
		return errors.New("revert: --no-edit and a message exclude each other")
	}
	var plan commitPlan
	if inProgress, err := w.ReadState(inProgressFile, &plan); err != nil {
		return err
	} else if inProgress {
		reportPlan(&plan)
		return errors.New("revert: a pit commit stopped halfway; finish it with pit commit --continue, or pit commit --abort")
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	c, err := findCommit(repos, p.paths[0])
	if err != nil {
		return err
	}
	e, err := entryOf(c, repos)
	if err != nil {
		return err
	}

	plan = commitPlan{NoVerify: p.on("no-verify")}
	statuses := map[string]repoStatus{}
	for _, s := range readStatuses(targetsOf(entryRepos(e))) {
		statuses[s.repo.Name] = s
	}
	var problems []string
	for _, d := range e.commits {
		s := statuses[d.repo.Name]
		problems = append(problems, preflight(s)...)
		if s.err == nil && s.TrackedChanges() {
			problems = append(problems, d.repo.Label()+": has uncommitted changes; commit or stash them first")
		}
		parents, err := git.Output(d.repo.Dir, "rev-list", "--parents", "-n", "1", d.hash)
		if err == nil && len(strings.Fields(parents)) > 2 {
			problems = append(problems, fmt.Sprintf("%s: %s is a merge; revert it with git revert -m <parent> there",
				d.repo.Label(), d.short))
		}
		plan.Steps = append(plan.Steps, commitStep{Repo: d.repo.Name, Revert: d.hash})
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return errors.New("revert: nothing was reverted")
	}
	plan.Steps = inWorkspaceOrder(w, plan.Steps)

	msg, err := commitMessage(w, p, &plan, statuses, revertMessage(e))
	if err != nil {
		return err
	}
	if err := w.WriteStateFile(commitMsgFile, []byte(msg)); err != nil {
		return err
	}
	if err := w.WriteState(inProgressFile, &plan); err != nil {
		return err
	}
	return runPlan(w, &plan)
}

func entryRepos(e logEntry) []*workspace.Repo {
	rs := make([]*workspace.Repo, len(e.commits))
	for i, c := range e.commits {
		rs[i] = c.repo
	}
	return rs
}

// revertMessage is the message for reverting e, worded as git revert words it, naming each repo's commit.
func revertMessage(e logEntry) string {
	reverted := make([]string, len(e.commits))
	for i, c := range e.commits {
		reverted[i] = c.hash
		if len(e.commits) > 1 {
			reverted[i] += " in " + c.repo.Label()
		}
	}
	return fmt.Sprintf("Revert \"%s\"\n\nThis reverts commit %s.", e.commits[0].subjectLine, strings.Join(reverted, ", commit "))
}
