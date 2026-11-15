package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// cmdSync clones missing externals, checks the cloned ones and updates the ignore blocks that keep the externals out of the repos holding
// them. It never changes or deletes an existing directory. Each repo with something to report gets a block; the externals cloned before,
// as configured, are summed up.
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
	// an external's parent first, as the external is cloned into the parent's work tree
	exts = slices.Clone(exts)
	slices.SortStableFunc(exts, func(a, b *workspace.Repo) int { return depth(a) - depth(b) })

	var failed []string
	shown, quiet := 0, 0
	// The ignore blocks of the externals not selected come first, as the selected ones may lie inside them.
	for _, b := range w.IgnoreBlocks() {
		if !b.Repo.IsRoot() && !slices.Contains(exts, b.Repo) && syncBlock(w, b, false, &failed) {
			ui.End()
			shown++
		}
	}
	for _, r := range exts {
		if err := stopped(); err != nil {
			return err
		}
		open := false
		if !r.Cloned() {
			header(r)
			open = true
			if err := clone(w, r); err != nil {
				ui.Error("%s", err)
				failed = append(failed, r.Label())
			}
		} else if problem := originProblem(r); problem != "" {
			header(r)
			open = true
			ui.Warn("%s", problem)
		}
		// right away, before the externals inside it are cloned
		if r.Cloned() {
			open = syncBlock(w, w.IgnoreBlock(r), open, &failed)
		}
		if open {
			ui.End()
			shown++
		} else {
			quiet++
		}
	}
	if w.RootRepo != nil && syncBlock(w, w.IgnoreBlock(w.RootRepo), false, &failed) {
		ui.End()
		shown++
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

// depth is the number of externals the external r lies inside.
func depth(r *workspace.Repo) int {
	d := 0
	for p := r.Parent; p != nil; p = p.Parent {
		d++
	}
	return d
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

// clone clones an external that isn't cloned yet, unless something else is in its way: the external it lies inside isn't cloned, the repo
// holding its directory tracks files in it, or the directory has other content.
func clone(w *workspace.Workspace, r *workspace.Repo) error {
	if r.Parent != nil && !r.Parent.Cloned() {
		return fmt.Errorf("%s lies inside %s, which isn't cloned yet", r.Path, r.Parent.Name)
	}
	if c := w.Container(r); c != nil {
		rel := r.PathIn(c)
		tracked, err := git.Output(c.Dir, "ls-files", "--", rel)
		if err != nil {
			return err
		}
		switch {
		case tracked != "" && c.IsRoot():
			return fmt.Errorf("the root repo tracks files in %s; untrack them first (git rm -r --cached %s, then commit)", rel, rel)
		case tracked != "":
			return fmt.Errorf("%s tracks files in %s; an external can only go where the repo holding it has no files", c.Name, rel)
		}
	}
	entries, err := os.ReadDir(r.Dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty, but is not a git repo; pit doesn't touch it", r.Path)
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

// syncBlock updates an ignore block and tells about it in its repo's output block, starting that unless it is open already. It returns
// whether the repo's block is open now. A failure is added to failed.
func syncBlock(w *workspace.Workspace, b workspace.IgnoreBlock, open bool, failed *[]string) bool {
	notes, err := syncIgnore(w, b)
	if len(notes) == 0 && err == nil {
		return open
	}
	if !open {
		header(b.Repo)
	}
	for _, n := range notes {
		ui.Note("%s", n)
	}
	if err != nil {
		ui.Error("%s: %s", b.Name(), err)
		*failed = append(*failed, b.Repo.Label())
	}
	return true
}

// syncIgnore writes an ignore block. The notes tell what changed, and which externals were dropped from the config.
func syncIgnore(w *workspace.Workspace, b workspace.IgnoreBlock) (notes []string, err error) {
	content, err := b.Read()
	if err != nil {
		return nil, err
	}
	old, _ := workspace.ParseIgnoreBlock(content)
	for _, e := range old {
		path := b.Repo.RootRelative() + strings.Trim(e, "/")
		if !slices.Contains(b.Entries, e) && !slices.ContainsFunc(w.Externals, func(r *workspace.Repo) bool { return strings.EqualFold(r.Path, path) }) {
			notes = append(notes, path+" is no longer an external and no longer ignored; its directory was left as it is")
		}
	}
	updated := workspace.UpdateIgnoreBlock(content, b.Entries)
	if updated == content {
		return notes, nil
	}
	if err := os.MkdirAll(filepath.Dir(b.File), 0o755); err != nil {
		return notes, err
	}
	if err := os.WriteFile(b.File, []byte(updated), 0o644); err != nil {
		return notes, err
	}
	switch {
	case b.Repo.IsRoot():
		return append(notes, "updated .gitignore; commit it together with pit-externals.json"), nil
	case len(b.Entries) == 0:
		return append(notes, "removed pit's block from "+b.Name()), nil
	}
	var inside []string
	for _, r := range w.Inside(b.Repo) {
		inside = append(inside, r.PathIn(b.Repo))
	}
	return append(notes, fmt.Sprintf("updated %s, which keeps %s out of this repo", b.Name(), strings.Join(inside, ", "))), nil
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
