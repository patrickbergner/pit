# pit – Polyrepo Integration Tool

**pit** (Polyrepo Integration Tool) keeps a monorepo and the external repositories in its subdirectories in sync – in both directions,
without ever handing a repository history that isn't its own.

You work in one monorepo. Some of its subdirectories are also standalone repositories (called *externals*). Either side can be public
or private: open-source libraries published from a closed monorepo, components shared with a customer or another team, or subprojects
of an open monorepo that also live on their own. `pit` wraps `git subtree` so that:

- `pit push` publishes new commits touching such a directory to its external repo – as commits that contain only that directory, never any
  other monorepo history;
- `pit pull` merges contributions made directly in the external repos back into the monorepo;
- a pre-push hook blocks every push to an external repo that doesn't go through pit.

The monorepo stays the single source of truth. `pit` is a single native binary with no dependencies besides git.

## Requirements

- `git` on `PATH`, including `git subtree` (part of git's contrib scripts; Git for Windows and most Linux distributions and Homebrew
  ship it).
- `git-lfs`, only if Git LFS is actually used in the monorepo or an external repo. The LFS attribute checks need git ≥ 2.40.

## Installation

Download the zip for your platform from the releases and put the `pit` (`pit.exe`) binary in it anywhere on your `PATH`.

Or install it with Go:

```sh
go install github.com/patrickbergner/pit/cmd/pit@latest
```

or build it:

```sh
./build.sh
```

## Quick start

1. Add `externals.json` at the root of your monorepo:

   ```json
   {
     "externals": {
       "mylib": { "url": "https://github.com/you/mylib.git", "repoPath": "libs/mylib" }
     }
   }
   ```

2. Install the guard hook, once per clone (see [Pre-push guard](#pre-push-guard)):

   ```sh
   pit install-hook
   ```

3. Bring the two sides together:
   - The external repo already has commits: `pit add mylib` imports it into `libs/mylib` (the directory must not exist yet).
   - The external repo is empty: commit `libs/mylib` in the monorepo and `pit push mylib` publishes its history.

4. From then on, work in the monorepo as usual and use `pit status`, `pit pull` and `pit push` instead of `git pull` / `git push`.

## Commands

Run `pit` anywhere inside the monorepo; it finds the root itself.

| Command | What it does |
|---|---|
| `pit list` | Show the configured externals |
| `pit status [--no-fetch] [ext...]` | Commits waiting to be pushed / pulled, for the monorepo and each external |
| `pit pull [ext...]` | Merge the monorepo's upstream first, then new external commits into each `repoPath` |
| `pit push [-n] [-y] [ext...]` | Show the complete plan, ask once, then push: monorepo first, then the external repos |
| `pit add (ext... \| --all)` | First-time import of an external repo into its `repoPath` |
| `pit tag [-m msg] [-r rev] [-y] [--allow-unpublished] <ext> <tag>` | Tag the external commit for monorepo revision `rev` (default `HEAD`) |
| `pit install-hook` | Install the pre-push guard |
| `pit version` | Show the version |

**Without external names**, `status`, `pull` and `push` cover the monorepo *and* all externals. **With names**, they cover only those
externals and leave the monorepo's upstream alone. (`push` still records its bookkeeping merges locally; they go out with the next full
`pit push`.)

### `pit push`

`push` works in two phases. First it fetches, splits and checks everything without changing anything, and prints a plan: the monorepo
commits to push and, for every external, **every commit message that is about to be published to its external repo**. Problems (an external
repo with commits the monorepo doesn't have yet, LFS mismatches, …) are all reported at once. Only after you confirm does it upload LFS
objects, push the monorepo and then each external repo.

- `-n`, `--dry-run` shows the plan and stops.
- `-y`, `--yes` skips the confirmation.

Uncommitted changes are not pushed – `push` pushes `HEAD` and warns if the worktree is dirty. Since commit messages are published as they
are, write them for the external repo's audience when a commit touches an external, or keep other changes in separate commits.

### `pit pull`

Requires a clean worktree. Merges the upstream of the current branch first, then each external's branch into its `repoPath`. It always
merges, never rebases – see [Don't rebase](#dont-rebase).

### `pit tag`

Creates an annotated tag on the external commit that corresponds to a monorepo revision and pushes it to the external repo:

```sh
pit tag -m "Release 1.2.0" mylib v1.2.0
```

In the monorepo the tag is stored as `mylib/v1.2.0`, so tags of different externals (and your monorepo's own tags) don't collide; the
external repo gets plain `v1.2.0`. By default the commit must already be published; `--allow-unpublished` publishes it along with the tag.

## Configuration

`externals.json` at the monorepo root:

```json
{
  "settings": {
    "defaultBranch": "main",
    "remotePrefix": "pit-"
  },
  "externals": {
    "mylib": { "url": "https://github.com/you/mylib.git", "repoPath": "libs/mylib" },
    "tool":  { "url": "git@github.com:you/tool.git", "repoPath": "tools/tool", "branch": "master" }
  }
}
```

| Key | Meaning |
|---|---|
| `settings.defaultBranch` | External repo branch used when an external sets none (default `main`) |
| `settings.remotePrefix` | Prefix of the git remotes pit manages (default `pit-`) |
| `externals.<name>` | Name used on the command line; letters, digits, `.`, `_`, `-` |
| `url` | The external repo (required) |
| `repoPath` | Directory in the monorepo (required) |
| `branch` | Branch in the external repo (optional) |

Unknown keys are rejected, so a typo fails loudly instead of being ignored. The whole `settings` object is optional.

For each external, pit creates and updates a git remote `<remotePrefix><name>` (e.g. `pit-mylib`). These remotes are fetched without tags
and only for their one branch, so the external repos' tags never end up in the monorepo's tag namespace.

## Pre-push guard

An external's remote sits right next to the monorepo's, and one careless `git push pit-mylib main` would hand the external repo the whole
monorepo history. `pit install-hook` installs a pre-push hook that blocks every push whose URL matches one of the externals, unless pit
itself is doing the push.

- URLs are compared after normalization, so `https://`, `ssh://` and `git@host:path` spellings, trailing `.git`, local paths and `file://`
  URLs all match.
- The hook contains the absolute path of the pit binary. If pit can't be found, the hook **fails closed** and blocks all pushes. After
  moving the binary, run `pit install-hook` again.
- An existing pre-push hook is kept as `pre-push.pit-chained` and runs after the guard with the same arguments and input. In repos using Git
  LFS, the LFS hook is set up and chained automatically.
- Running `pit install-hook` again is safe.

Hooks are not cloned, so install it in every clone of the monorepo.

## Git LFS

pit works with and without Git LFS, in the monorepo and in each external repo. Files are published exactly as they are committed (LFS
pointer or regular file); pit makes sure the LFS objects travel along with the pointers, uploading them itself before any ref is pushed.

The catch: an external repo only sees the `.gitattributes` inside its `repoPath`, not the ones at the monorepo root or in directories above.
A file stored in LFS because of a root rule would show up as a bare pointer file in the external repo. `pit push` therefore checks every
file of an external against the external repo's own attributes and refuses to push on a mismatch; it also warns if the monorepo's attributes
would store a file differently than the external repo expects.

To fix a mismatch, make `<repoPath>/.gitattributes` self-contained – either track the patterns there, or opt out of all inherited rules with
this as its first line:

```
* !filter !diff !merge
```

then re-store the files and commit:

```sh
git add --renormalize libs/mylib
git commit -m "Store libs/mylib files as its .gitattributes say"
```

`pit pull` and `pit add` fetch the LFS objects of incoming external commits from the external repo before merging.

## How it works

- **Split.** `git subtree split` turns the history of `repoPath` into a history containing only that directory. It is deterministic: the
  same monorepo revision always gives the same split commit, which is how `status`, `pull`, `push` and `tag` match both sides.
- **Rejoin.** After publishing, pit records a merge commit on `HEAD` that joins the published split commit (like
  `git subtree split --rejoin`; the tree is unchanged). Without it, later pulls would conflict with your own published changes, and every
  split would walk the full history.
- **Push order.** LFS objects first, then the monorepo, then the external repos. If a push fails, pit says what has already been pushed.

### Don't rebase

The rejoin and subtree merge commits are what connect the monorepo to the external histories. Rebasing flattens them, which leads to
conflicts and diverging split commits. Use `pit pull` (it merges), and don't rebase commits that contain pit's merges.

## Development

```sh
./run-tests.sh                   # gofmt check, go vet, go test ./...
./run-tests.sh -v -run TestLFS   # extra arguments go to go test
```

The end-to-end tests build throwaway monorepos and external repos as local `file://` repositories in temp directories; they never touch the
network. They need git, and git-lfs for the LFS tests (skipped otherwise). pit uses the Go standard library only.

## License

MIT – see [LICENSE](LICENSE).
