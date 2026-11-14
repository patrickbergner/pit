// Package workspace is the pit root and its repos: the directory holding pit-externals.json, the externals cloned below it and, if the
// root is a git repo itself, the root repo.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/patrickbergner/pit/internal/config"
)

// RootName is how the root repo is named on the command line and in output.
const RootName = "."

// Repo is one repository of the workspace.
type Repo struct {
	Name string           // external name, or RootName
	Path string           // relative to the root with forward slashes; "" for the root repo
	Dir  string           // absolute directory
	Ext  *config.External // nil for the root repo
}

func (r *Repo) IsRoot() bool { return r.Ext == nil }

// Cloned reports whether the repo's directory is a git repository.
func (r *Repo) Cloned() bool { return isRepo(r.Dir) }

// GitDir returns the repo's git directory, following a .git file ("gitdir: ...") as worktrees use it.
func (r *Repo) GitDir() string {
	dotgit := filepath.Join(r.Dir, ".git")
	fi, err := os.Stat(dotgit)
	if err != nil || fi.IsDir() {
		return dotgit
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return dotgit
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return dotgit
	}
	dir = filepath.FromSlash(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.Dir, dir)
	}
	return dir
}

func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Workspace is an opened pit root.
type Workspace struct {
	Root      string // absolute
	Cwd       string // absolute; where pit was started
	Config    *config.Config
	Externals []*Repo // in config order
	RootRepo  *Repo   // nil if the root isn't a git repo
}

// ErrNoRoot is returned when no pit-externals.json is found.
var ErrNoRoot = errors.New("no " + config.FileName + " found here or in any parent directory")

// Open finds the nearest pit-externals.json at or above cwd and loads the workspace.
func Open(cwd string) (*Workspace, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, config.FileName)); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, ErrNoRoot
		}
		root = parent
	}
	cfg, err := config.Load(filepath.Join(root, config.FileName))
	if err != nil {
		return nil, err
	}
	w := &Workspace{Root: root, Cwd: cwd, Config: cfg}
	for i := range cfg.Externals {
		e := &cfg.Externals[i]
		w.Externals = append(w.Externals, &Repo{Name: e.Name, Path: e.Path, Dir: filepath.Join(root, filepath.FromSlash(e.Path)), Ext: e})
	}
	if isRepo(root) {
		w.RootRepo = &Repo{Name: RootName, Dir: root}
	}
	return w, nil
}

// All returns every repo: the externals in config order, then the root repo.
func (w *Workspace) All() []*Repo {
	all := append([]*Repo(nil), w.Externals...)
	if w.RootRepo != nil {
		all = append(all, w.RootRepo)
	}
	return all
}

// Cloned returns All without the externals that aren't cloned yet.
func (w *Workspace) Cloned() []*Repo {
	var repos []*Repo
	for _, r := range w.All() {
		if r.Cloned() {
			repos = append(repos, r)
		}
	}
	return repos
}

// Uncloned returns the externals that aren't cloned yet.
func (w *Workspace) Uncloned() []*Repo {
	var repos []*Repo
	for _, r := range w.Externals {
		if !r.Cloned() {
			repos = append(repos, r)
		}
	}
	return repos
}

// Named resolves repo names from the command line ("." is the root repo). Each must be cloned.
func (w *Workspace) Named(names []string) ([]*Repo, error) {
	want := map[*Repo]bool{}
	for _, n := range names {
		r, err := w.named(n)
		if err != nil {
			return nil, err
		}
		if !r.Cloned() {
			return nil, fmt.Errorf("%s is not cloned yet; run pit sync", n)
		}
		want[r] = true
	}
	return w.ordered(want), nil
}

// Match returns the repos given by name or belonging to one of the groups, in workspace order, whether cloned or not. The root repo belongs
// to no group; name it as "." to include it.
func (w *Workspace) Match(names, groups []string) ([]*Repo, error) {
	set := map[*Repo]bool{}
	for _, n := range names {
		r, err := w.named(n)
		if err != nil {
			return nil, err
		}
		set[r] = true
	}
	known := w.Config.Groups()
	for _, g := range groups {
		if !slices.Contains(known, g) {
			if len(known) == 0 {
				return nil, fmt.Errorf("unknown group %q: no external in %s has groups", g, config.FileName)
			}
			return nil, fmt.Errorf("unknown group %q (groups: %s)", g, strings.Join(known, ", "))
		}
		for _, r := range w.Externals {
			if slices.Contains(r.Ext.Groups, g) {
				set[r] = true
			}
		}
	}
	return w.ordered(set), nil
}

func (w *Workspace) named(n string) (*Repo, error) {
	if n == RootName {
		if w.RootRepo == nil {
			return nil, fmt.Errorf("%s is not a git repo, so there is no repo %q", w.Root, RootName)
		}
		return w.RootRepo, nil
	}
	for _, r := range w.Externals {
		if r.Name == n {
			return r, nil
		}
	}
	return nil, fmt.Errorf("unknown repo %q (see pit list)", n)
}

// ordered returns the repos in set, in workspace order.
func (w *Workspace) ordered(set map[*Repo]bool) []*Repo {
	var repos []*Repo
	for _, r := range w.All() {
		if set[r] {
			repos = append(repos, r)
		}
	}
	return repos
}

// Target is a repo with the pathspecs that apply to it. No pathspecs means the whole repo.
type Target struct {
	Repo      *Repo
	Pathspecs []string
}

// Route maps paths given on the command line (relative to the current directory) to the repos owning them. A path belongs to the external
// whose path is its longest prefix, otherwise to the root repo. A directory also covers every external below it, so "pit add ." at the root
// stages in all repos.
func (w *Workspace) Route(paths []string) ([]Target, error) {
	specs := map[*Repo][]string{}
	for _, p := range paths {
		rel, err := w.rootRel(p)
		if err != nil {
			return nil, err
		}
		covered := false
		if owner, spec := w.owner(rel); owner != nil {
			if !owner.Cloned() {
				return nil, fmt.Errorf("%s: %s is not cloned yet; run pit sync", p, owner.Name)
			}
			specs[owner] = append(specs[owner], spec)
			covered = true
		}
		for _, r := range w.Externals {
			if r.Cloned() && (rel == "." || hasPathPrefix(r.Path, rel)) {
				specs[r] = append(specs[r], ".")
				covered = true
			}
		}
		if !covered {
			return nil, fmt.Errorf("%s: not inside any repo of %s", p, w.Root)
		}
	}
	var targets []Target
	for _, r := range w.All() {
		if s, ok := specs[r]; ok {
			targets = append(targets, Target{r, s})
		}
	}
	return targets, nil
}

// rootRel turns a command-line path into a slash path relative to the root ("." for the root itself).
func (w *Workspace) rootRel(p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.Cwd, p)
	}
	rel, err := filepath.Rel(w.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: outside %s", p, w.Root)
	}
	return filepath.ToSlash(rel), nil
}

// owner returns the repo owning the root-relative path rel and the pathspec for it inside that repo.
func (w *Workspace) owner(rel string) (*Repo, string) {
	var best *Repo
	for _, r := range w.Externals {
		if (pathEqual(rel, r.Path) || hasPathPrefix(rel, r.Path)) && (best == nil || len(r.Path) > len(best.Path)) {
			best = r
		}
	}
	if best != nil {
		spec := strings.TrimPrefix(rel[len(best.Path):], "/")
		if spec == "" {
			spec = "."
		}
		return best, spec
	}
	if w.RootRepo != nil {
		return w.RootRepo, rel
	}
	return nil, ""
}

// Display turns a path relative to repo r into one relative to the current directory, as git prints paths.
func (w *Workspace) Display(r *Repo, file string) string {
	abs := filepath.Join(r.Dir, filepath.FromSlash(file))
	rel, err := filepath.Rel(w.Cwd, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	rel = filepath.ToSlash(rel)
	if strings.HasSuffix(file, "/") && !strings.HasSuffix(rel, "/") {
		rel += "/"
	}
	return rel
}

// RootRelative returns the repo's directory relative to the root for use as a path prefix: "" for the root repo, else "<path>/".
func (r *Repo) RootRelative() string {
	if r.IsRoot() {
		return ""
	}
	return r.Path + "/"
}

var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

func pathEqual(a, b string) bool {
	if foldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hasPathPrefix reports whether p lies strictly below dir.
func hasPathPrefix(p, dir string) bool {
	return len(p) > len(dir)+1 && p[len(dir)] == '/' && pathEqual(p[:len(dir)], dir)
}

// Parallel runs f for each repo, a few at a time, and returns the results in the repos' order.
func Parallel[T any](repos []*Repo, f func(*Repo) T) []T {
	results := make([]T, len(repos))
	sem := make(chan struct{}, min(8, max(2, runtime.NumCPU())))
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			results[i] = f(r)
		}()
	}
	wg.Wait()
	return results
}
