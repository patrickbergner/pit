# pit – Polyrepo Integration Tool

**pit** lets you work with a directory tree made of several git repositories as if it were one.

List the repositories in a `pit-externals.json` at the top of the tree. `pit sync` clones each into its subdirectory, and from then on
pit runs the everyday git operations across all of them: one `pit status`, one `pit commit` with the same message in every or some of the repos with changes, one `pit push`.

- Each external stays a plain clone. `cd` into it and use git directly whenever you like.
- pit adds no remotes, hooks or refs and never rewrites history. The only thing it writes into a repo are the ignore entries that keep
  the externals' clones out of it. Everything else is commands you could have typed yourself.
- A single native binary with no dependencies besides git.

## Installation

Download the zip for your platform from the releases and put `pit` anywhere on your `PATH`, or build it with

```sh
./build.sh
```

pit needs `git` on `PATH`. On Windows, a `git.bat` or `git.cmd` shim is fine.

**Shell completion** of commands, options, repo names (`-r`) and groups (`-g`): add the line for your shell to its startup file.

```sh
source <(pit completion bash)                              # ~/.bashrc, also for Git Bash
pit completion powershell | Out-String | Invoke-Expression  # $PROFILE
```

## Quick start

1. Create `pit-externals.json` in the directory that should hold the repos:

   ```json
   {
     "externals": {
       "app":  { "url": "https://github.com/you/app.git", "path": "app" },
       "core": { "url": "git@github.com:you/core.git",    "path": "libs/core" }
     }
   }
   ```

2. Clone everything:

   ```sh
   pit sync
   ```

3. Work as usual, then from anywhere in the tree:

   ```sh
   pit status
   pit add -A
   pit commit -m "Rename the config option everywhere"
   pit push
   ```

## The pit root

The directory holding `pit-externals.json` is the **root**. pit finds it by walking up from the current directory, so it works from any
subdirectory, including from inside an external. The root can be a git repo itself, or a plain directory:

```
workspace/                 <- the root; here a git repo itself
  pit-externals.json
  .gitignore               <- pit keeps the externals' paths in here
  .pit/                    <- pit's local state, gitignored
  README.md                <- belongs to the root repo
  app/                     <- external "app"
  libs/
    notes.md               <- belongs to the root repo
    core/                  <- external "core"
    util/                  <- external "util"
```

- pit works on all **externals** in config order, plus the **root repo** if the root is a git repo. The root repo is selected as `.`
  (`-r .`), shows up as `(pit root)`, and always comes last.
- A file belongs to the external whose path contains it (the innermost one, if [externals are nested](#nested-externals)), otherwise to
  the root repo.
- A repo is the one in its directory, never one further up. If the root is a plain directory inside some other git repo, pit doesn't
  work with that repo and keeps nothing out of it; `pit sync` warns about it. To make it the root repo, put `pit-externals.json` into
  its top directory.

## Commands

Every command covers **all repos, wherever in the tree you run it**. To narrow that down:

- `-r <repo>` selects a repo by name; `-r .` is the root repo.
- `-g <group>` selects the externals in a [group](#groups).
- Paths select the repos owning them. A directory covers every external below it: `pit add .` at the root stages in all repos,
  `pit status libs` shows only the repos under `libs/`.

`-r` and `-g` can be repeated and combined and select the repos matching any of them. Together with paths, only the selected repos
owning those paths remain.

| Command | What it does |
|---|---|
| `pit sync` | Clone missing externals, check existing ones, keep them out of the repos holding them |
| `pit list` | A table of the externals: path, branch, groups, whether they are cloned, URL |
| `pit tree` | The externals in the directory tree, with their sibling directories |
| `pit status [--fetch]` | Branch, ahead/behind and changed files per repo |
| `pit diff [<options>]` | The diffs of all repos, as one diff of the whole tree |
| `pit grep [<options>] <pattern> [<path>...]` | Search the tracked files of all repos |
| `pit add <path>...` / `pit add -A` | Stage, each path in the repo that owns it |
| `pit restore [--staged] <path>...` | Unstage or discard changes, routed the same way |
| `pit rm [--recursive] [--cached] <path>...` | Remove files and stage that, routed the same way, once all repos pass git's checks |
| `pit mv <source>... <destination>` | Move or rename files in the repo that owns them |
| `pit commit [-a] [-m <msg>]... [-F <file>] [-n] [<path>...]` | Commit in every repo with something to commit, all with the same message |
| `pit commit --continue` / `--abort` | Finish, or forget, a pit commit that stopped halfway |
| `pit commit --amend [-a] [-m <msg> \| --no-edit]` | Amend the last pit commit |
| `pit revert [-m <msg> \| --no-edit] <commit>` | Revert a pit commit, as one new pit commit |
| `pit stash [push \| pop \| apply \| drop \| list]` | Stash in every repo with changes, and bring back exactly those stashes |
| `pit log [-n <count>] [--oneline] [<filters>]` | One log across all repos |
| `pit show [<options>] [<commit>]` | A pit commit, with the changes of all its repos as one diff |
| `pit graph [<log options>] [<revision>...] [-- <path>...]` | The history of the repo you are in, as `git log --graph` draws it |
| `pit changes <commit> [<revision>] [-- <path>...]` | The files a commit changed, or that differ between it and another revision |
| `pit fetch` | Fetch all repos |
| `pit pull` | Pull all repos that have an upstream |
| `pit push [-n] [-i \| -y]` | Show what each repo would push, new tags included, ask repo by repo, then push |
| `pit tag` | The latest tag of each repo and the commits since |
| `pit tag -r <repo> <name>` | Tag HEAD of the selected repos |
| `pit pushtag [-i \| -y] -r <repo> <name>` | Tag, then push as `pit push` does |
| `pit each [--] <git args>` | Run any git command in each repo |
| `pit completion bash \| powershell` | Print the script for [shell completion](#installation) |
| `pit version` | Show the version |

- **Git options.** Options pit doesn't know are passed on to git: `pit diff --staged`, `pit pull --rebase`,
  `pit push --force-with-lease`. Give an option its value in the same argument (`--source=HEAD~1`, `-U5`). `-r` and `-g` are pit's, so
  use the long form of git options with those letters (`pit pull --rebase`, not `-r`).
- **Help.** `-h` or `--help` after any command but `each` shows pit's help.
- **Paths** in output are relative to the current directory, as git prints them.
- **Order and locking.** `status`, `log`, `grep` and `fetch` work on the repos in parallel. Everything that changes something runs repo by
  repo, in config order, root last, and holds a lock (`.pit/lock`) so two pit commands don't interleave.
- **Credentials.** git asks for them where you can answer: in `pull`, `push`, `sync` and everything else whose git output you see as it
  comes. Where git runs in the background, as in `fetch` and `status --fetch`, it can't ask, and fails at once instead of waiting for
  an answer nobody can give; pit says where to enter them. This covers git's own prompts and Git Credential Manager, the credential
  helper of Git for Windows (`GCM_INTERACTIVE=never`).
- **Ctrl-C** stops before the next repo, once the git that runs has stopped too. A git that keeps waiting, for example for credentials in
  a window you don't see, is ended by a second Ctrl-C, together with what it started; pit then stops as after any other failure.

### Output and colors

Most commands show each repo's output in a block: a header with the repo's name (and its path, if that differs), the output, an empty
line.

```
$ pit pull
─── app ────────────────────────────────────────────────────────────────────────
Already up to date.

─── core (libs/core) ───────────────────────────────────────────────────────────
Updating 1a2b3c4..5d6e7f8
Fast-forward
 README.md | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

```

`diff`, `log` and `show` stay one diff and one log of the whole tree. `add` and `restore` are silent and use blocks only with `-p` /
`-i`, when git asks hunk by hunk.

Output to a terminal is lightly colored. `NO_COLOR` set to anything turns that off; `--color[=always|never|auto]` or `--no-color`,
before or after the command, decides it either way, also for the git output pit shows (`pit --color diff | less -R`).

**Pager.** In a terminal, `diff`, `grep`, `log` and `show` page their output, as git does: with the pager git would use (`GIT_PAGER`,
`core.pager`, `PAGER`, else `less`), run through the shell, with `LESS=FRX` unless `LESS` is set. On Windows, pit finds the shell and
`less` in the Git for Windows installation git comes from. `--no-pager`, before or after the command, prints straight to the terminal; a
pager of `cat` does the same.

### `pit sync`

For each external:

- **Directory missing or empty:** cloned, with the configured branch checked out. An empty remote repo is cloned too, with that branch
  started. git may ask for credentials there, under the external's header, before cloning: pit first looks at the remote's branches.
- **Already a git repo:** pit checks that `origin` is the configured URL (`https://`, `ssh://` and `git@host:path` spellings and a
  trailing `.git` don't matter) and warns if not. Nothing is changed.
- **Exists, but isn't empty and isn't a repo:** an error for this external. pit never overwrites or adopts content.

It also updates the ignore entries that keep the externals out of the repos holding them: the root repo's
[`.gitignore`](#keeping-the-externals-out-of-the-root-repo), and the `.git/info/exclude` of an external with
[others inside](#nested-externals).

To add or remove an external, edit `pit-externals.json` and run `pit sync`; if the root is a git repo, commit the config together with
`.gitignore`. The directory of a removed external is reported and left as it is.

All other commands warn when an external isn't cloned or ignore entries are out of date. `pit add` refuses to stage in a repo with
outdated entries, since it would pick up the clones inside it.

### `pit status`

```
$ pit status
─── app ────────────────────────────────────────────────────────────────────────
main  ↑2 ↓1
   M app/src/main.go
  ?? app/notes.md

─── core (libs/core) ───────────────────────────────────────────────────────────
feature/x  (expected main)
   M libs/core/README.md

─── (pit root) ─────────────────────────────────────────────────────────────────
main  ↑1
   M pit-externals.json

9 more repos clean and up to date
```

Per repo: the branch, commits ahead (↑) and behind (↓) its upstream, and the changed files as in `git status --short`. Notes in
parentheses point out the unusual: another branch than the configured one, a detached HEAD, a merge or rebase in progress, a missing or
deleted upstream, no commits yet.

Ahead/behind are as of the last fetch; `--fetch` fetches first. pit says so when the last fetch is more than a day old.

### `pit diff`

Runs `git diff` in every repo with each repo's location in the paths (`a/libs/core/README.md`), so the output reads as one diff of the
whole tree. Diff options are passed on (`--staged`, `--exit-code`, ...). Options that list files instead of a patch (`--stat`,
`--name-only`, `--name-status`, ...) give a block per repo, as git prints those paths relative to the repo.

### `pit grep`

Runs `git grep` in all repos at once: `pit grep -n "TODO" libs`. The first argument is the pattern, unless `-e` gives the patterns
(`pit grep -e foo -e bar`); further arguments are paths, routed as everywhere. git grep's options are passed on (`-i`, `-w`, `-l`,
`--untracked`, ...), with their values in the same argument (`-A3`). As git prints the paths relative to the repo, each repo with matches
gets a block. Like git grep, pit grep fails if nothing matches anywhere.

### `pit rm`

Runs `git rm` for each path in the repo that owns it, so each removal is staged where it belongs: `pit rm libs/core/old.go notes.md`.

- **Checked first.** In one repo, git rm removes nothing if one of its paths fails git's checks: a path that matches nothing, a file with
  changes that aren't committed, a directory without `-r`. pit extends that to all repos: it runs `git rm --dry-run` in each first, and if
  one fails, it shows git's reasons and removes nothing anywhere.
- **`--recursive`** removes directories. It is git rm's `-r`, which has no long form, while `-r` selects repos in pit. Other options go to
  git: `--cached` only untracks the files and keeps them, `-f` removes files with changes, `-n` only shows what would be removed, `-q`
  doesn't list it.
- **Files, not externals.** A path that is an external's directory or contains externals would remove everything in them, so pit refuses
  it. To drop an external, remove it from `pit-externals.json` and run `pit sync`.
- git rm lists the removed files relative to its repo, so each repo gets a block.

### `pit mv`

Runs `git mv` in the repo that owns the paths, with the paths as that repo sees them, so `pit mv libs/core/old.go libs/core/new.go` works
from anywhere in the tree. Options go to git: `-f`, `-k`, `-n`, `-v`.

- **Within one repo.** A move has one destination, so the sources and the destination must belong to the same repo; git can't move files
  between repos. For that, move the files yourself and stage both sides: `pit add libs/core/util.go libs/util/util.go` stages the removal
  in `core` and the new file in `util`. The history stays behind in `core`.
- **Files, not externals.** To move an external, change its `path` in `pit-externals.json`, move its directory and run `pit sync`. A
  directory holding externals is refused too: git mv renames it as a whole, clones included, and the config would no longer match.
- pit reports all problems at once and then moves nothing. git mv itself checks every source before it moves any.
- git mv is silent; with `-v` or `-n`, its list of moves comes in a block for the repo.

### `pit commit`

One message, one commit per repo:

1. **Collect** the repos with staged changes, or with `-a`, with changes to tracked files.
2. **Check** before anything is committed: every repo must be readable, on a branch, without unresolved conflicts or an operation
   (merge, rebase, ...) in progress, and a git identity must be configured. All problems are listed at once, and nothing is committed.
3. **Message** from `-m` (several are joined as paragraphs), `-F <file>`, or git's editor, opened once with the changes of every repo.
4. **Commit** in each repo, one after the other, root last. Each repo's hooks run as usual; `-n` (`--no-verify`) skips them.

**Paths** commit just those, as `git commit <paths>` does: their changes, staged or not, in the repos owning them, while other staged
changes stay staged. `pit commit -m "Fix the parser" libs/core/parse.go app/main.go` commits in `core` and `app`; a directory covers the
externals below it. A path git doesn't know, such as a new file, is refused up front; `pit add` it first.

The commits are independent: nothing links them besides message, author and time, and there is no cross-repo atomicity. If a commit
fails halfway, typically because of a hook, pit stops and lists what was committed and what wasn't. Then:

- `pit commit --continue` commits the remaining repos with the same message, once the problem is fixed.
- `pit commit --abort` forgets the interrupted commit; the commits already made stay.

**Amending.** `pit commit --amend` amends the commits of the last pit commit, offering their message in the editor (or use `-m`, or
`--no-edit`). Repos with newly staged changes get a fresh commit with the same message. pit refuses if one of the repos has moved on
since or the commit is already pushed there. To amend anything else, use git in that repo.

### `pit revert`

Reverts a pit commit as one new pit commit: `pit revert 1a2b3c4`, with a commit of any of its repos, as `pit log` shows them. pit finds
the commits that belong with it in the other repos, the way `pit log` groups them, then runs `git revert --no-commit` and commits in
each, all with the same message: as git words it, `Revert "<subject>"`, with each repo's reverted commit, in the editor unless `-m`, `-F`
or `--no-edit` decide it. `-r` and `-g` limit the revert to some of the repos.

Every repo must be clean, on a branch and in no operation; merge commits are refused, as they need `git revert -m`. It is a pit commit
like any other: if a revert conflicts, pit stops there; resolve the conflicts and `git add` the files in that repo, then
`pit commit --continue` commits it and reverts the rest. `pit commit --amend` amends it.

### `pit stash`

`pit stash [push] [-m <msg>] [-u] [<path>...]` runs `git stash push` in each repo with changes and records which repos it stashed in, in
`.pit/`, as one pit stash. `pit stash pop` brings back exactly those stashes, whatever else is stashed in the repos. Options go to git;
paths limit the stash, as with git. `-u` stashes untracked files too; `--all` is refused, as the ignored files it would stash include the
clones of externals and pit's `.pit/`.

- `pit stash list` shows the pit stashes, newest first, numbered from 0, each with its message and repos.
- `pit stash pop`, `apply` and `drop` use the newest pit stash, or the one with the given number: `pit stash pop 1`.
- A stash popped or dropped with git since is left out with a warning. If git can't pop a stash, typically because of a conflict, pit
  stops there and keeps the rest of the pit stash for the next `pit stash pop`; git keeps that repo's stash, as always.

### `pit log`

Merges the logs of all repos, newest first; 20 entries unless you give `-n <count>` (or `-<count>`):

```
commit app 1a2b3c4, core 5d6e7f8, (pit root) 9a8b7c6
Author: You <you@example.com>
Date:   Sat Nov 14 10:23:45 2026 +0100

    Rename the config option everywhere
```

Commits in different repos with the same author and message, made within ten minutes of each other, are one entry: what `pit commit`
produced. The grouping comes from the histories alone, so it is the same on every machine and for commits made with git. `--oneline`
shows one line per entry; paths narrow the log, as with `git log -- <path>`.

**Filters.** The git log options that choose commits are passed on, with their values in the same argument: `--author=`,
`--committer=`, `--since=` / `--after=`, `--until=` / `--before=`, `--grep=` (with `--all-match`, `--invert-grep`, `-i`, `-E`, `-F`,
`-P`), `-S<string>`, `-G<regex>`, `--diff-filter=`, `--merges`, `--no-merges`, `--min-parents=`, `--max-parents=`, `--first-parent` and
`--follow`. Options that change git's output, like `--stat` or `--format`, are refused, as pit reads that output itself; `pit show`
shows a commit's changes.

```sh
pit log --oneline --author=me --since=1.week
```

### `pit show`

Shows a pit commit: its entry as `pit log` shows it, then the changes of all its repos as one diff with each repo's location in the
paths, as `pit diff` prints them. Without an argument, it is the newest one; `pit show 1a2b3c4` shows the one that commit of any repo is
part of. A commit name like `HEAD` that resolves in several repos needs `-r` (`pit show -r core HEAD~1`); `-r` and `-g` also limit the
repos shown. Diff options are passed on, and those that list files (`--stat`, `--name-only`, ...) give a block per repo.

### `pit graph`, `pit changes`

These two show the history of a single repo: the one the current directory is in (an external, or the root repo outside of them), or
the ones `-r` and `-g` select, each in a block. Their arguments are git's, in git's order: options and revisions, then `--` and paths,
relative to the current directory as everywhere.

`pit graph` draws the history as `git log --graph --decorate --oneline` does, with git's colors for the branch lines, branch names and
tags. Options go to git: `--all` for every branch, `-n 30`, `--author=...`, `main..feature/x`; `--format=` or `--pretty=` replace the
one-line default.

```
$ cd libs/core && pit graph --all
─── core (libs/core) ───────────────────────────────────────────────────────────
*   3c4d5e6 (HEAD -> main, origin/main) Merge branch 'feature/x'
|\
| * 2b3c4d5 (feature/x) Add the x parser
|/
* 1a2b3c4 Initial commit
```

`pit changes <commit>` lists the files a commit changed, compared with the commit before it: the first parent of a merge, nothing for
a repo's first commit. `pit changes <commit> <revision>` lists the files that differ between the two, as in `pit changes 1a2b3c4 HEAD`.
Names like `HEAD~2` or `main` are the repo's you are in; a commit hash found only in another repo is taken from there. The list shows
git's letters (`A`dded, `M`odified, `D`eleted, `R`enamed with `old -> new`) with paths relative to the current directory; `--stat`,
`--numstat`, `--name-only` or `-p` show git's own output instead.

```
$ pit changes 3c4d5e6
─── core (libs/core) ───────────────────────────────────────────────────────────
from 1a2b3c4 Initial commit
to   3c4d5e6 Merge branch 'feature/x'
  A parser/x.go
  M README.md
```

### `pit fetch`, `pit pull`

- `pit fetch` fetches all repos in parallel. Terminal prompts for credentials are disabled meanwhile; credential helpers work as usual.
- `pit pull` runs `git pull` in every repo that has an upstream, honoring your git config (`pull.rebase`, `pull.ff`) and the options
  you pass (`--rebase`, `--ff-only`, ...). A repo that fails, e.g. with a conflict, doesn't stop the others; the summary lists it, and
  you resolve it there with git.

### `pit push`

**1. The plan.** pit fetches, then shows a block per repo with the branch, where it goes, the commits and the new tags:

```
─── app ────────────────────────────────────────────────────────────────────────
main -> origin/main
  1a2b3c4 Release 1.4.1
tag v1.4.1 -> origin
```

- The branch goes to its upstream branch, as shown, whatever `push.default` says. A branch that never had an upstream is pushed to
  `origin` with `-u`.
- A repo that has diverged from its upstream is marked `(can't push)`; `pit pull` it first. `--force`, `--force-with-lease` and
  `--force-if-includes` lift that check.
- A branch whose upstream is gone, typically deleted on the remote once merged, is not published again. pit skips it, or marks it
  `(can't push)` if it has commits that are on no remote branch; `git push -u` in that repo publishes it again.
- **Tags** on the pushed branch that the remote doesn't have yet go along, in one atomic push with the branch, so a tag never reaches
  the remote without its commits. A repo with nothing new but a tag is pushed, too. Tags that aren't on the branch stay local, and pit
  mentions them. `--tags` and `--follow-tags`, which push tags by git's rules instead, are refused.

**2. The questions.** pit asks repo by repo; repos that can't be pushed are skipped:

```
(1/3) Push app: main -> origin/main, 2 commits, tag v1.4.1? [y,n,a,s,?] y
(2/3) Push core: feature/x -> origin/feature/x, 1 commit? [y,n,a,s,?] n
(3/3) Push (pit root): main -> origin/main, 1 commit? [y,n,a,s,?] y
```

`y` pushes the repo, `n` doesn't, `a` pushes it and all remaining ones, `s` skips it and all remaining ones. All questions come before
the first push: Ctrl-C or a closed stdin pushes nothing at all.

Whether pit asks:

- `confirmPush` in the [settings](#configuration), on by default; `confirmPushGroups` overrides it per group, and with the key `"."`
  for the root repo. Repos that need no question are pushed along with the ones you answer `y` for; pit names them before asking.
- `-y` (`--yes`) pushes without asking, `-i` (`--interactive`) asks about every repo, whatever the config says.
- **Without any question, a repo that can't be pushed stops the whole push**: nobody chose to leave it behind, and related commits in
  the other repos shouldn't go out without it. `pit push -i` lets you push the others anyway.

**3. The pushes.** The chosen repos are pushed one by one, root last. If one fails, pit stops and lists what was pushed and what
wasn't. pit exits with an error whenever a repo was left out because of a problem, so a partial push never looks complete.

`-n` (`--dry-run`) shows the plan only.

### `pit tag`, `pit pushtag`

Without a name, `pit tag` shows each repo's latest tag on HEAD and the commits since, i.e. which repos have unreleased changes:

```
$ pit tag -g libs
─── core (libs/core) ───────────────────────────────────────────────────────────
v1.4.0, 3 commits since

─── util (libs/util) ───────────────────────────────────────────────────────────
v0.2.1

2 more repos without tags
```

- `pit tag -r core v1.4.1` tags HEAD of the repos selected with `-r` or `-g`. Each repo has versions of its own, so the repos must be
  named. All are checked first (a commit to tag, no tag of that name yet); if one can't be tagged, none is. The tags are lightweight.
- `pit pushtag -r core v1.4.1` tags, then pushes those repos as `pit push` does (`-i` and `-y` work, too), the tag along with any
  commits not pushed yet. It refuses a name the remote already has a tag of. If you answer `n`, the tag stays local, and a later
  `pit push` publishes it.

### `pit each`

The escape hatch for everything pit doesn't wrap. It runs the git command in each repo, with a block per repo, and fails if it failed
anywhere:

```sh
pit each -- switch -c feature/x
pit each -r app -r core -- tag v1.0
pit each stash
```

## Configuration

`pit-externals.json`:

```json
{
  "settings": {
    "defaultBranch": "main",
    "confirmPush": true,
    "confirmPushGroups": { "libs": false, ".": false }
  },
  "externals": {
    "app":  { "url": "https://github.com/you/app.git", "path": "app", "branch": "master" },
    "core": { "url": "git@github.com:you/core.git",    "path": "libs/core", "groups": ["libs", "go"] },
    "util": { "url": "git@github.com:you/util.git",    "path": "libs/util", "groups": ["libs"] }
  }
}
```

| Key | Meaning |
|---|---|
| `settings.defaultBranch` | Branch of externals that set none (default `main`) |
| `settings.confirmPush` | Whether `pit push` asks repo by repo before pushing (default `true`); `-y` / `-i` override it |
| `settings.confirmPushGroups` | `confirmPush` for the externals of these groups; if an external's groups disagree, asking wins. The key `"."` is for the root repo, which is in no group |
| `externals.<name>` | Name used with `-r` and in output; letters, digits, `.`, `_`, `-` |
| `url` | Clone URL (required) |
| `path` | Directory relative to the root, with forward slashes (required) |
| `branch` | Branch `pit sync` checks out, and that `pit status` expects (optional) |
| `groups` | The groups the external belongs to, for `-g` (optional) |

- The externals keep the order they have in the file.
- Unknown keys are rejected, so a typo fails loudly. The `settings` object is optional.
- Paths must be unique and stay inside the root. A URL, a branch or a directory in a path must not start with `-`.

### Groups

An external can belong to any number of groups. A group exists once some external lists it; its name uses the same characters as an
external's. `-g <group>` works with every command:

```sh
pit sync -g libs                     # clone only the libs
pit status -g libs
pit commit -g libs -r . -m "Bump the Go version"   # the libs and the root repo
pit each -g go -- tag v1.4.0
```

The root repo belongs to no group; add `-r .` to include it. Externals of a group that aren't cloned yet are left out, except by `sync`,
`list`, `tree` and `status`.

## Keeping the externals out of the root repo

If the root is a git repo, `pit sync` maintains a block in its `.gitignore`:

```
# >>> pit externals - managed by pit, do not edit
/.pit/
/app/
/libs/core/
/libs/util/
# <<< pit externals
```

- The entries are exact, anchored paths: `libs/notes.md` stays part of the root repo while `libs/core/` is ignored.
- Only the block is rewritten. Lines of your own that got into it are moved below it.
- It's `.gitignore` rather than `.git/info/exclude` so that it is versioned and works in every clone of the root repo.
- Ignore rules don't affect tracked files. If the root repo tracks files in an external's path, `pit sync` refuses to clone that
  external and tells you how to untrack them.
- Externals inside other externals aren't listed here; the external holding them keeps them out.

## Nested externals

An external can lie inside another one, for instance to add a repo of your own to a directory of a repo you use:

```
workspace/
  pit-externals.json
  app/                     <- external "app"
    plugins/
      base/                <- belongs to app
      extra/               <- belongs to app
      myplugin/            <- external "myplugin", inside app
```

```json
{
  "externals": {
    "app":      { "url": "git@github.com:you/app.git",      "path": "app" },
    "myplugin": { "url": "git@github.com:you/myplugin.git", "path": "app/plugins/myplugin" }
  }
}
```

Every file belongs to the innermost external holding it: `app/plugins/base/x.txt` and new files in `app/plugins/` belong to `app`,
everything in `app/plugins/myplugin/` to `myplugin`. `pit add app/plugins` stages in both. Nesting can go deeper.

To keep `myplugin`'s clone out of `app`, `pit sync` maintains a block in the `.git/info/exclude` of `app`, which git reads like a
`.gitignore`:

```
# >>> pit externals - managed by pit, do not edit
/plugins/myplugin/
# <<< pit externals
```

- The block lists the externals directly inside `app`. Externals further inside are kept out by the external holding them.
- `.git/info/exclude` belongs to the clone and isn't versioned: the history of `app` and other clones of it are unaffected. `myplugin`
  is your addition, not part of `app`.
- With the block, git and pit leave `plugins/myplugin/` alone in `app`. Without it, `git status` in `app` would list it as untracked,
  and `git add -A` would stage the clone as an embedded repository.
- `pit sync` clones `app` first and writes the block before it clones `myplugin`, whatever the order in the config: an external listed
  before the one it lies inside waits until right after that one. All others keep their config order.
  `pit sync -r myplugin` refuses while `app` isn't cloned.
- `myplugin` can only go where `app` tracks no files; otherwise `pit sync` refuses to clone it.
- When `myplugin` is removed from the config, `pit sync` removes its entry and leaves the clone as it is. `app` then sees it as
  untracked.
- If `app` is a linked worktree, the block is in the main worktree's `.git/info/exclude`, which all its worktrees share.

**Make sure `app` never gets files in `plugins/myplugin`**, on any branch. To git, ignored files are expendable: switching `app` to a
branch that has files there overwrites `myplugin`'s files with them, and switching back deletes them. Ignore rules only decide what git
picks up, not what it may write.

## Local state

pit keeps a few files in `.pit/` next to `pit-externals.json`, local to your checkout and gitignored:

| File | Purpose |
|---|---|
| `COMMIT_EDITMSG` | The message of the latest pit commit; survives a failed commit for `--continue` |
| `commit-in-progress.json` | Which repos of an interrupted pit commit are done, and which are still to do |
| `last-commit.json` | The commits of the last pit commit, for `--amend` |
| `stash.json` | Which stash of which repo each pit stash is |
| `lock` | Held while a command changes something |

Deleting `.pit` loses at most an interrupted commit's progress, the ability to amend the last pit commit through pit, and which stashes
belong to which pit stash; the stashes themselves stay in the repos. Since you may use git directly at any time, pit checks what it
recorded against the repos before acting on it. A `lock` left behind by a pit that was killed can simply be deleted.

## What pit deliberately doesn't do

- **No pinning.** The root repo doesn't record which commits of the externals belong together; externals follow their branches. A fresh
  `pit sync` gives you the current tip of every external, not a reproducible snapshot.
- **No atomicity** across repos for commit, pull or push, only checks up front that catch the foreseeable failures.
- **No branch management** beyond reporting; `pit each -- switch ...` does the job.
- **No special handling** for Git LFS, submodules or hooks. Each clone has its own setup, and git deals with it.

## Development

```sh
./run-tests.sh                      # gofmt check, go vet, go test ./...
./run-tests.sh -v -run TestCommit   # extra arguments go to go test
```

The end-to-end tests in `cmd/pit` build throwaway pit roots and remotes as local `file://` repositories in temp directories; they need
only git and never touch the network. pit uses the Go standard library only.

## License

MIT – see [LICENSE](LICENSE).
