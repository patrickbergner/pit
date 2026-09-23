pit - Polyrepo Integration Tool

pit keeps a monorepo and the external repositories in its subdirectories in
sync, in both directions, without ever handing a repository history that
isn't its own. It wraps git subtree: pit push publishes commits touching a
directory to its external repo as commits containing only that directory,
pit pull merges contributions made in the external repos back, and a
pre-push hook blocks every push to an external repo that bypasses pit.

Requirements:
  git on PATH, including git subtree (Git for Windows, most Linux
  distributions and Homebrew ship it). git-lfs only if Git LFS is used;
  the LFS checks need git >= 2.40.

Installation:
  Put pit anywhere on your PATH.

Quick start:
  1. Add externals.json at the root of your monorepo:
       {
         "externals": {
           "mylib": { "url": "https://github.com/you/mylib.git", "repoPath": "libs/mylib" }
         }
       }
  2. Install the guard hook, once per clone:   pit install-hook
  3. Either import an existing external repo:  pit add mylib
     or publish libs/mylib to an empty one:    pit push mylib
  4. From then on use pit status, pit pull and pit push instead of
     git pull / git push.

Commands:
  list                                  Show the configured externals
  status [--no-fetch] [ext...]          Commits waiting to be pushed / pulled
  pull   [ext...]                       Merge the monorepo's upstream, then new
                                        external commits into each repoPath
  push   [-n] [-y] [ext...]             Show the full plan, ask once, then push:
                                        monorepo first, then the external repos
  add    (ext... | --all)               First-time import of an external repo
  tag    [-m msg] [-r rev] [-y] [--allow-unpublished] <ext> <tag>
                                        Tag the external commit for monorepo rev
  install-hook                          Install the pre-push guard
  version                               Show the version

Without external names, status, pull and push cover the monorepo and all
externals; with names, only those externals.

Don't rebase commits containing pit's merges; pit pull always merges.

Full documentation: https://github.com/patrickbergner/pit
