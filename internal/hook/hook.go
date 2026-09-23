// Package hook is the pre-push guard. It blocks direct pushes to the external repos: pushing a monorepo branch or tag there would publish the
// whole monorepo history. Only pit (which pushes split commits and sets PIT_PUSH=1) may push to them.
//
// The installed hook is a POSIX sh stub that calls `pit hook pre-push`, then any pre-push hook that was there before (e.g. Git LFS's), kept
// as pre-push.pit-chained.
package hook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/patrickbergner/pit/internal/config"
	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/lfs"
	"github.com/patrickbergner/pit/internal/ui"
)

// Marker identifies the guard in .git/hooks/pre-push.
const Marker = "pit-pre-push-guard"

// script is the installed hook: POSIX sh, so it runs wherever git does. It fails closed: without pit, pushes are blocked rather than
// unguarded.
const script = `#!/bin/sh
# ` + Marker + ` - written by 'pit install-hook'; re-run that after moving pit.
# Blocks direct pushes to the external repos in externals.json (only pit may push there),
# then runs the pre-push hook that was here before (e.g. Git LFS's), if any.
pit=%s
[ -x "$pit" ] || pit=pit
if ! command -v "$pit" >/dev/null 2>&1; then
  echo "pre-push: pit not found; put it on PATH or re-run 'pit install-hook'" >&2
  exit 1
fi
"$pit" hook pre-push "$@" || exit 1
if [ -x "$0.pit-chained" ]; then exec "$0.pit-chained" "$@"; fi
`

// Install writes the guard as the current repo's pre-push hook, running the pit binary at exe. Whatever pre-push hook was there before is
// kept as pre-push.pit-chained. Safe to re-run.
func Install(exe string) error {
	hooks, err := git.Output("rev-parse", "--git-path", "hooks")
	if err != nil {
		return err
	}
	target := filepath.Join(hooks, "pre-push")
	chained := target + ".pit-chained"
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return err
	}

	if content, err := os.ReadFile(target); err == nil && !strings.Contains(string(content), Marker) {
		if exists(chained) {
			return fmt.Errorf("both %s and %s exist; merge them manually", target, chained)
		}
		if err := os.Rename(target, chained); err != nil {
			return err
		}
		ui.Info("kept the existing pre-push hook as %s; the guard runs it", chained)
	}

	// With the guard in place, 'git lfs install' can no longer add its own pre-push hook, so add it now if this repo uses LFS.
	if !exists(chained) && lfs.Available() && git.OK("grep", "-q", "--untracked", "-e", "filter=lfs", "--", ":(glob)**/.gitattributes") {
		_ = os.Remove(target) // our own guard, rewritten below
		if _, err := git.Output("lfs", "update"); err != nil {
			ui.Warn("git lfs update failed; LFS hooks may be missing: %v", err)
		}
		if exists(target) {
			if err := os.Rename(target, chained); err != nil {
				return err
			}
			ui.Info("installed Git LFS's pre-push hook as %s; the guard runs it", chained)
		}
	}

	content := fmt.Sprintf(script, shQuote(filepath.ToSlash(exe)))
	if err := os.WriteFile(target, []byte(content), 0o755); err != nil {
		return err
	}
	ui.Info("installed %s (runs %s)", target, exe)
	return nil
}

// External returns the name of the external whose external repo url is, or "" if none. It reads externals.json of the repo around the working
// directory; without one there are no external repos.
func External(url string) (string, error) {
	root, err := git.Output("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(filepath.Join(root, config.FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	target := NormalizeURL(url, root)
	for _, name := range cfg.Names() {
		if u := cfg.Externals[name].URL; u != "" && NormalizeURL(u, root) == target {
			return name, nil
		}
	}
	return "", nil
}

var (
	scpLike     = regexp.MustCompile(`^[^/]+:[^0-9]`)
	windowsPath = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
)

// NormalizeURL reduces the many spellings of a repo URL to one form, e.g.
//
//	git@github.com:You/foo.git, https://github.com/you/foo, ssh://git@github.com/you/foo/
//	-> github.com/you/foo
//
// Local paths and file:// URLs become absolute paths (relative ones resolved against root). Like git, anything without "://" and without a
// ":" before the first "/" is a local path.
func NormalizeURL(u, root string) string {
	local := windowsPath.MatchString(u)
	if rest, ok := strings.CutPrefix(u, "file://"); ok {
		u, local = rest, true
		if windowsPath.MatchString(strings.TrimPrefix(u, "/")) { // file:///C:/x
			u = strings.TrimPrefix(u, "/")
		}
	}
	if !local && !strings.Contains(u, "://") {
		colon, slash := strings.Index(u, ":"), strings.IndexAny(u, `/\`)
		local = colon < 0 || (slash >= 0 && slash < colon)
	}
	if local {
		if rest, ok := strings.CutPrefix(u, "~"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				u = home + rest
			}
		}
		if !filepath.IsAbs(u) {
			u = filepath.Join(root, u)
		}
		u = filepath.ToSlash(filepath.Clean(u))
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" { // case-insensitive file systems
			u = strings.ToLower(u)
		}
	} else {
		if _, rest, ok := strings.Cut(u, "://"); ok { // scheme
			u = rest
		}
		if at := strings.Index(u, "@"); at >= 0 && (strings.Index(u, "/") < 0 || at < strings.Index(u, "/")) {
			u = u[at+1:] // user@
		}
		if scpLike.MatchString(u) { // scp-style host:path
			u = strings.Replace(u, ":", "/", 1)
		}
		u = strings.ToLower(u) // GitHub paths are case-insensitive
	}
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	return strings.TrimSuffix(u, "/")
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
