package monorepo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
)

// Upstream is the upstream of the current branch in the monorepo.
type Upstream struct {
	Branch string // main
	Remote string // origin
	Merge  string // refs/heads/main
	Track  string // refs/remotes/origin/main
	Label  string // origin/main
}

// Upstream describes the current branch's upstream; the error says why there is none.
func (r *Repo) Upstream() (*Upstream, error) {
	branch, err := git.Output("symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return nil, errors.New("HEAD is detached")
	}
	remote, err := git.Output("config", "branch."+branch+".remote")
	if err != nil {
		return nil, fmt.Errorf("branch '%s' has no upstream (run: git push -u origin %s)", branch, branch)
	}
	if strings.HasPrefix(remote, r.Config.Settings.RemotePrefix) {
		return nil, fmt.Errorf("branch '%s' tracks external remote '%s'", branch, remote)
	}
	merge, err := git.Output("config", "branch."+branch+".merge")
	if err != nil {
		return nil, err
	}
	track, err := git.Output("for-each-ref", "--format=%(upstream)", "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	return &Upstream{
		Branch: branch,
		Remote: remote,
		Merge:  merge,
		Track:  track,
		Label:  remote + "/" + strings.TrimPrefix(merge, "refs/heads/"),
	}, nil
}

func (v *Upstream) Fetch() error {
	_, err := git.Output("fetch", "--quiet", v.Remote)
	return err
}

// HaveTrack reports whether the upstream branch exists (as of the last fetch).
func (v *Upstream) HaveTrack() bool { return git.OK("rev-parse", "-q", "--verify", v.Track) }
