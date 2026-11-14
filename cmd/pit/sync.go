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

	failed := 0
	for _, r := range exts {
		if err := stopped(); err != nil {
			return err
		}
		if err := syncExternal(w, r); err != nil {
			fmt.Fprintf(os.Stderr, "pit: %s: %s\n", r.Name, err)
			failed++
		}
	}
	if w.RootRepo != nil {
		if err := syncIgnore(w); err != nil {
			fmt.Fprintf(os.Stderr, "pit: .gitignore: %s\n", err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("sync: %s failed", ui.Plural(failed, "step"))
	}
	return nil
}

func syncExternal(w *workspace.Workspace, r *workspace.Repo) error {
	if r.Cloned() {
		url, err := git.Output(r.Dir, "remote", "get-url", "origin")
		switch {
		case err != nil:
			ui.Warn("%s: has no remote 'origin'; expected %s", r.Name, r.Ext.URL)
		case normalizeURL(url) != normalizeURL(r.Ext.URL):
			ui.Warn("%s: origin is %s, but %s is configured; pit leaves it as it is", r.Name, url, r.Ext.URL)
		}
		return nil
	}
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

	ui.Note("cloning %s into %s", r.Name, r.Path)
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

// syncIgnore writes the managed block of the root's .gitignore and reports externals that were dropped from the config.
func syncIgnore(w *workspace.Workspace) error {
	content, err := w.ReadIgnore()
	if err != nil {
		return err
	}
	old, _ := workspace.ParseIgnoreBlock(content)
	entries := w.IgnoreEntries()
	for _, e := range old {
		if !slices.Contains(entries, e) {
			ui.Note("%s is no longer an external and no longer ignored; its directory was left as it is", strings.Trim(e, "/"))
		}
	}
	updated := workspace.UpdateIgnoreBlock(content, entries)
	if updated == content {
		return nil
	}
	if err := os.WriteFile(w.IgnoreFile(), []byte(updated), 0o644); err != nil {
		return err
	}
	ui.Note("updated .gitignore; commit it together with %s", "pit-externals.json")
	return nil
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

// cmdList shows the configured externals.
func cmdList(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("list", args, spec{noPaths: true})
	if err != nil {
		return err
	}
	repos := w.All()
	if p.narrowed() {
		if repos, err = w.Match(p.repos, p.groups); err != nil {
			return err
		}
	}
	if len(w.Externals) == 0 {
		fmt.Println("no externals configured in", w.Root)
	}
	nameW, pathW, branchW, groupsW := 0, 0, 0, 0
	for _, r := range repos {
		nameW = max(nameW, len(r.Name))
		if !r.IsRoot() {
			pathW, branchW = max(pathW, len(r.Path)), max(branchW, len(r.Ext.Branch))
			groupsW = max(groupsW, len(groupList(r)))
		}
	}
	for _, r := range repos {
		if r.IsRoot() {
			fmt.Printf("%s  (root repo)\n", ui.Pad(workspace.RootName, nameW))
			continue
		}
		line := ui.Pad(r.Name, nameW) + "  " + ui.Pad(r.Path, pathW) + "  " + ui.Pad(r.Ext.Branch, branchW) + "  "
		if groupsW > 0 {
			line += ui.Pad(groupList(r), groupsW) + "  "
		}
		line += r.Ext.URL
		if !r.Cloned() {
			line += "  (not cloned)"
		}
		fmt.Println(line)
	}
	return nil
}

// groupList shows an external's groups as "[a, b]".
func groupList(r *workspace.Repo) string {
	if len(r.Ext.Groups) == 0 {
		return ""
	}
	return "[" + strings.Join(r.Ext.Groups, ", ") + "]"
}
