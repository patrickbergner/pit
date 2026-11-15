package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Repo  string `json:"repo"`
	Amend string `json:"amend,omitempty"` // commit to amend; HEAD must still be it
	Hash  string `json:"hash,omitempty"`  // the new commit, once made
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
		bools:   []string{"a|all", "n|no-verify", "continue", "abort", "amend", "no-edit"},
		values:  []string{"m|message", "F|file"},
		noPaths: true,
	})
	if err != nil {
		return err
	}
	msgGiven := len(p.vals["message"]) > 0 || p.last("file") != ""
	switch {
	case p.on("continue") || p.on("abort"):
		if p.on("continue") && p.on("abort") || msgGiven || p.on("amend") || p.on("all") || p.narrowed() {
			return errors.New("commit: --continue and --abort take no other options")
		}
	case p.on("amend") && p.narrowed():
		return errors.New("commit: --amend covers the repos of the last pit commit; it doesn't take -r or -g")
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

	// Fresh commits in the repos with something to commit
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	statuses := map[string]repoStatus{}
	for _, s := range readStatuses(targetsOf(repos)) {
		statuses[s.repo.Name] = s
	}
	amending := map[string]bool{}
	for _, st := range plan.Steps {
		amending[st.Repo] = true
	}
	// Pre-flight: everything that would foreseeably fail, for all repos at once
	var problems []string
	var unstaged []string
	for _, r := range repos {
		s := statuses[r.Name]
		switch {
		case amending[r.Name]: // checked with the plan's steps below
		case s.err != nil:
			// whether it has something to commit is unknown, so it can't be left out silently
			problems = append(problems, fmt.Sprintf("%s: %s", r.Label(), s.err))
		case hasWork(s, plan.All):
			plan.Steps = append(plan.Steps, commitStep{Repo: r.Name})
		case !s.Clean() && !s.Conflicts():
			unstaged = append(unstaged, r.Label())
		}
	}
	plan.Steps = inWorkspaceOrder(w, plan.Steps)
	if len(plan.Steps) == 0 && len(problems) == 0 {
		if len(unstaged) > 0 {
			return fmt.Errorf("nothing staged to commit in any repo (changes in %s; use pit add or pit commit -a)", strings.Join(unstaged, ", "))
		}
		return errors.New("nothing to commit, all repos clean")
	}

	for _, st := range plan.Steps {
		r, _ := byName(w, st.Repo)
		s, ok := statuses[st.Repo]
		if !ok {
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
		b.WriteString(":\n")
		if s.Status == nil {
			continue
		}
		for _, e := range s.Entries {
			if e.Untracked() || (!plan.All && e.XY[0] == ' ') {
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
	if st.Amend != "" {
		if head, _ := git.Output(r.Dir, "rev-parse", "HEAD"); head != st.Amend {
			return fmt.Errorf("%s has moved on (HEAD is no longer %.7s); not amending", r.Label(), st.Amend)
		}
		args = append(args, "--amend")
	} else {
		s, err := workspace.ReadStatus(r, nil)
		if err != nil {
			return err
		}
		if !hasWork(repoStatus{r, s, nil}, plan.All) {
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
