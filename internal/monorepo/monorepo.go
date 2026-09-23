// Package monorepo is the monorepo pit runs in: its configured externals (subtree directories that are also standalone repos, see
// external.go) and the upstream of its current branch (see upstream.go).
package monorepo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/patrickbergner/pit/internal/config"
	"github.com/patrickbergner/pit/internal/git"
)

type Repo struct {
	Root   string
	Config *config.Config
}

// Open locates the monorepo around the working directory, loads its config and changes to its top directory (git subtree must run from
// there).
func Open() (*Repo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("git not found on PATH")
	}
	root, err := git.Output("rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return nil, errors.New("not inside a git working tree")
	}
	cfg, err := config.Load(filepath.Join(root, config.FileName))
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(root); err != nil {
		return nil, err
	}
	return &Repo{Root: root, Config: cfg}, nil
}

// Targets: no names → all=true and every external; else just those.
func (r *Repo) Targets(names []string) (all bool, list []string) {
	if len(names) == 0 {
		return true, r.Config.Names()
	}
	return false, names
}

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// External returns the configured external name.
func (r *Repo) External(name string) (*External, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid external name '%s'", name)
	}
	c, ok := r.Config.Externals[name]
	if !ok {
		return nil, fmt.Errorf("unknown external '%s' (see: pit list)", name)
	}
	if c.URL == "" || c.RepoPath == "" {
		return nil, fmt.Errorf("external '%s' needs 'url' and 'repoPath'", name)
	}
	branch := c.Branch
	if branch == "" {
		branch = r.Config.Settings.DefaultBranch
	}
	remote := r.Config.Settings.RemotePrefix + name
	return &External{
		Name:   name,
		URL:    c.URL,
		Path:   strings.TrimSuffix(c.RepoPath, "/"),
		Branch: branch,
		Remote: remote,
		Track:  "refs/remotes/" + remote + "/" + branch,
	}, nil
}

// RequireClean fails on uncommitted changes.
func RequireClean() error {
	if !git.OK("diff", "--quiet") || !git.OK("diff", "--cached", "--quiet") {
		return errors.New("uncommitted changes in the working tree; commit or stash first")
	}
	return nil
}
