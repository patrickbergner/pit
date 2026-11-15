package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// cmdSync clones missing externals, checks the cloned ones and updates the ignore block. It never changes or deletes an existing directory.
// Each repo with something to report gets a block; the externals cloned before, as configured, are summed up.
func cmdSync(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("sync", args, spec{noPaths: true})
	if err != nil {
		return err
	}
	exts := w.Externals
	if p.narrowed() {
		matched, err := w.Match(p.repos, p.groups)
		if err != nil {
			return err
		}
		exts = nil
		for _, r := range matched {
			if !r.IsRoot() {
				exts = append(exts, r)
			}
		}
	}

	var failed []string
	shown, quiet := 0, 0
	for _, r := range exts {
		if err := stopped(); err != nil {
			return err
		}
		if r.Cloned() {
			if problem := originProblem(r); problem != "" {
				header(r)
				ui.Warn("%s", problem)
				ui.End()
				shown++
			} else {
				quiet++
			}
			continue
		}
		header(r)
		if err := clone(w, r); err != nil {
			ui.Error("%s", err)
			failed = append(failed, r.Label())
		}
		ui.End()
		shown++
	}
	if w.RootRepo != nil {
		notes, err := syncIgnore(w)
		if len(notes) > 0 || err != nil {
			header(w.RootRepo)
			for _, n := range notes {
				ui.Note("%s", n)
			}
			if err != nil {
				ui.Error(".gitignore: %s", err)
				failed = append(failed, w.RootRepo.Label())
			}
			ui.End()
			shown++
		}
	}
	switch {
	case quiet > 0 && shown == 0:
		fmt.Printf("%s already cloned\n", all(quiet, "external"))
	case quiet > 0:
		fmt.Printf("%s already cloned\n", ui.Plural(quiet, "more external"))
	}
	if len(failed) > 0 {
		return fmt.Errorf("sync failed in %s", strings.Join(failed, ", "))
	}
	return nil
}

// originProblem tells what is wrong with the origin of a cloned external, if anything.
func originProblem(r *workspace.Repo) string {
	url, err := git.Output(r.Dir, "remote", "get-url", "origin")
	switch {
	case err != nil:
		return "no remote 'origin'; expected " + r.Ext.URL
	case normalizeURL(url) != normalizeURL(r.Ext.URL):
		return fmt.Sprintf("origin is %s, but %s is configured; pit leaves it as it is", url, r.Ext.URL)
	}
	return ""
}

// clone clones an external that isn't cloned yet, unless something else is in its way.
func clone(w *workspace.Workspace, r *workspace.Repo) error {
	entries, err := os.ReadDir(r.Dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty, but is not a git repo; pit doesn't touch it", r.Path)
	}
	if w.RootRepo != nil {
		tracked, err := git.Output(w.Root, "ls-files", "--", r.Path)
		if err != nil {
			return err
		}
		if tracked != "" {
			return fmt.Errorf("the root repo tracks files in %s; untrack them first (git rm -r --cached %s, then commit)", r.Path, r.Path)
		}
	}

	refs, err := git.Output("", "ls-remote", "--heads", r.Ext.URL)
	if err != nil {
		return err
	}
	if refs == "" { // empty repo: clone it and start the configured branch
		if err := git.Pass(w.Root, "clone", r.Ext.URL, r.Path); err != nil {
			return err
		}
		return gitDo(r.Dir, "symbolic-ref", "HEAD", "refs/heads/"+r.Ext.Branch)
	}
	if !slices.ContainsFunc(strings.Split(refs, "\n"), func(l string) bool { return strings.HasSuffix(l, "\trefs/heads/"+r.Ext.Branch) }) {
		return fmt.Errorf("%s has no branch %q", r.Ext.URL, r.Ext.Branch)
	}
	return git.Pass(w.Root, "clone", "--branch", r.Ext.Branch, r.Ext.URL, r.Path)
}

// gitDo runs a git command whose output doesn't matter.
func gitDo(dir string, args ...string) error {
	_, err := git.Output(dir, args...)
	return err
}

// syncIgnore writes the managed block of the root's .gitignore. The notes tell what changed, and which externals were dropped from the
// config.
func syncIgnore(w *workspace.Workspace) (notes []string, err error) {
	content, err := w.ReadIgnore()
	if err != nil {
		return nil, err
	}
	old, _ := workspace.ParseIgnoreBlock(content)
	entries := w.IgnoreEntries()
	for _, e := range old {
		if !slices.Contains(entries, e) {
			notes = append(notes, strings.Trim(e, "/")+" is no longer an external and no longer ignored; its directory was left as it is")
		}
	}
	updated := workspace.UpdateIgnoreBlock(content, entries)
	if updated == content {
		return notes, nil
	}
	if err := os.WriteFile(w.IgnoreFile(), []byte(updated), 0o644); err != nil {
		return notes, err
	}
	return append(notes, "updated .gitignore; commit it together with pit-externals.json"), nil
}

// normalizeURL makes the spellings of one repo URL comparable: scheme, user, scp-like syntax, trailing slash and .git, case.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if host, path, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") && len(host) > 1 {
		u = host + "/" + path // git@host:path
	}
	if at := strings.Index(u, "@"); at >= 0 && at < strings.Index(u+"/", "/") {
		u = u[at+1:]
	}
	u = strings.ReplaceAll(u, `\`, "/")
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	return strings.ToLower(strings.TrimRight(u, "/"))
}

// selection reads the arguments of list and tree, which take -r and -g but no paths, and returns the selected repos: all by default.
func selection(w *workspace.Workspace, cmd string, args []string) ([]*workspace.Repo, error) {
	p, err := parseArgs(cmd, args, spec{noPaths: true})
	if err != nil {
		return nil, err
	}
	if p.narrowed() {
		return w.Match(p.repos, p.groups)
	}
	return w.All(), nil
}

// cloneState shows whether an external is cloned.
func cloneState(r *workspace.Repo) string {
	if r.Cloned() {
		return ui.Green("cloned")
	}
	return ui.Yellow("not cloned")
}

// externalsSummary sums up the externals list and tree showed: how many, how many of them aren't cloned, and their groups.
func externalsSummary(exts []*workspace.Repo) string {
	uncloned := 0
	var groups []string
	for _, r := range exts {
		if !r.Cloned() {
			uncloned++
		}
		for _, g := range r.Ext.Groups {
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}
	}
	summary := ui.Plural(len(exts), "external")
	if uncloned > 0 {
		summary += fmt.Sprintf(", %d not cloned (pit sync)", uncloned)
	}
	if len(groups) > 0 {
		summary += "; groups: " + strings.Join(groups, ", ")
	}
	return summary
}

// cmdList shows the root directory, the selected externals in a table with path, branch, groups, whether they are cloned and URL, and a
// summary.
func cmdList(w *workspace.Workspace, args []string) error {
	repos, err := selection(w, "list", args)
	if err != nil {
		return err
	}
	if len(w.Externals) == 0 {
		fmt.Println("no externals configured in", w.Root)
		return nil
	}
	// the path and groups columns only when there is something to show in them
	showPath, showGroups := false, false
	for _, r := range repos {
		if !r.IsRoot() {
			showPath = showPath || r.Path != r.Name
			showGroups = showGroups || len(r.Ext.Groups) > 0
		}
	}
	titles := []string{"Name"}
	if showPath {
		titles = append(titles, "Path")
	}
	titles = append(titles, "Branch")
	if showGroups {
		titles = append(titles, "Groups")
	}
	titles = append(titles, "State", "URL")

	var exts []*workspace.Repo
	var rows [][]string
	for _, r := range repos {
		if r.IsRoot() {
			fmt.Println(ui.BoldCyan(r.Label()) + " " + w.Root)
			continue
		}
		row := []string{ui.BoldCyan(r.Label())}
		if showPath {
			row = append(row, r.Path)
		}
		row = append(row, ui.Green(r.Ext.Branch))
		if showGroups {
			row = append(row, ui.Cyan(strings.Join(r.Ext.Groups, ", ")))
		}
		rows = append(rows, append(row, cloneState(r), r.Ext.URL))
		exts = append(exts, r)
	}
	if len(exts) == 0 {
		return nil
	}
	ui.Table(titles, rows)
	fmt.Println(externalsSummary(exts))
	return nil
}
