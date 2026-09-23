package monorepo

import (
	"fmt"
	"regexp"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/lfs"
	"github.com/patrickbergner/pit/internal/ui"
)

// External is a monorepo directory that is also published as an external repo.
type External struct {
	Name, URL, Path, Branch string
	Remote                  string // <remotePrefix><name>
	Track                   string // refs/remotes/<remote>/<branch>
}

// Label is how the external side is shown: "<url> (<branch>)".
func (e *External) Label() string { return fmt.Sprintf("%s (%s)", e.URL, e.Branch) }

// EnsureRemote sets up one remote per external, fetching only its branch and no tags, so external tags never land in the monorepo's tag
// namespace.
func (e *External) EnsureRemote() error {
	if cur, err := git.Output("remote", "get-url", e.Remote); err != nil {
		if _, err := git.Output("remote", "add", e.Remote, e.URL); err != nil {
			return err
		}
	} else if cur != e.URL {
		if _, err := git.Output("remote", "set-url", e.Remote, e.URL); err != nil {
			return err
		}
	}
	if _, err := git.Output("config", "remote."+e.Remote+".tagOpt", "--no-tags"); err != nil {
		return err
	}
	_, err := git.Output("config", "--replace-all", "remote."+e.Remote+".fetch", "+refs/heads/"+e.Branch+":"+e.Track)
	return err
}

// Fetch updates Track; an empty external repo (no branch yet) removes it.
func (e *External) Fetch() error {
	if err := e.EnsureRemote(); err != nil {
		return err
	}
	code, err := git.Code("ls-remote", "--exit-code", e.Remote, "refs/heads/"+e.Branch)
	switch {
	case err != nil:
		return err
	case code == 0:
		_, err = git.Output("fetch", "--quiet", e.Remote)
		return err
	case code == 2: // branch doesn't exist yet (empty repo)
		_, _ = git.Output("update-ref", "-d", e.Track)
		return nil
	default:
		return fmt.Errorf("%s: cannot reach %s", e.Name, e.URL)
	}
}

// HaveTrack reports whether the external branch exists (as of the last fetch).
func (e *External) HaveTrack() bool { return git.OK("rev-parse", "-q", "--verify", e.Track) }

// InMonorepo reports whether repoPath exists at monorepo rev.
func (e *External) InMonorepo(rev string) bool { return git.OK("cat-file", "-e", rev+":"+e.Path) }

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Split returns the external-history commit corresponding to monorepo rev. Deterministic, so push and tag always agree on commit IDs.
func (e *External) Split(rev string) (string, error) {
	if !e.InMonorepo(rev) {
		return "", fmt.Errorf("%s: %s does not exist at %s", e.Name, e.Path, rev)
	}
	sha, err := git.Output("subtree", "split", "-q", "--prefix="+e.Path, rev)
	if err != nil {
		return "", err
	}
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("%s: split failed", e.Name)
	}
	return sha, nil
}

// Rejoin records in the monorepo that HEAD's repoPath was published as split. A merge commit with HEAD's unchanged tree, in git-subtree's
// --rejoin format. It gives later pulls the right merge base (otherwise they conflict with our own published changes) and lets later splits
// stop here instead of walking the whole history.
func (e *External) Rejoin(split string) error {
	head, err := git.RevParse("HEAD")
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Split '%s/' into commit '%s'\n\ngit-subtree-dir: %s\ngit-subtree-mainline: %s\ngit-subtree-split: %s\n",
		e.Path, split, e.Path, head, split)
	commit, err := git.Input(msg, "commit-tree", "HEAD^{tree}", "-p", head, "-p", split)
	if err != nil {
		return err
	}
	_, err = git.Output("update-ref", "-m", "pit: rejoin "+e.Name, "HEAD", commit, head)
	return err
}

// Push is the only way pit pushes to an external repo; the pre-push guard lets it through because of PIT_PUSH.
func (e *External) Push(refspec string) error {
	return git.Pass([]string{"PIT_PUSH=1"}, "push", "--quiet", e.Remote, refspec)
}

// FetchLFS runs before merging external commits: it downloads their LFS objects from the external repo. Checkout would otherwise try the
// monorepo's LFS server, and a later push to the monorepo needs them all locally. args select the incoming commits.
func (e *External) FetchLFS(args ...string) error {
	n, err := lfs.PointerCount(args...)
	if err != nil || n == 0 {
		return err
	}
	if err := lfs.Require(e.URL); err != nil {
		return err
	}
	ui.Info("%s: fetching Git LFS objects from %s", e.Name, e.URL)
	if err := lfs.FetchAll(e.Remote, e.Track); err != nil {
		return fmt.Errorf("%s: %w", e.Name, err)
	}
	return nil
}

// CheckLFS runs after importing external commits: it warns about files under repoPath whose storage contradicts the monorepo's
// .gitattributes.
func (e *External) CheckLFS() error {
	issues, err := lfs.Mismatches("HEAD", e.Path, "")
	if err != nil {
		return err
	}
	for _, issue := range issues {
		ui.Warn("%s: %s", e.Name, issue)
	}
	if len(issues) > 0 {
		ui.Warn("%s: %s", e.Name, lfs.Hint(e.Path))
	}
	return nil
}
