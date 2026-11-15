# pit – Polyrepo Integration Tool

**pit** lets you work with a directory tree made of several git repositories as if it were one.

You list the repositories in a `pit-externals.json` at the top of the tree. pit clones each of them into its subdirectory as a plain
clone, and from then on runs the everyday git operations across all of them at once: one `pit status` for the whole tree, one
`pit commit` that commits in every repo with changes using the same message, one `pit push`.

Each external stays an ordinary clone. `cd` into one and use git directly whenever you like; pit doesn't mind. pit adds no remotes, hooks,
refs or files to any repo and never rewrites history. It only runs commands you could have typed yourself in each repo.

`pit` is a single native binary with no dependencies besides git.

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

pit needs `git` on `PATH`. On Windows, a `git.bat` or `git.cmd` shim on `PATH` is fine: pit runs the `git.exe` from its exec path instead,
since cmd.exe would mangle some arguments.

## Quick start

1. Create `pit-externals.json` in the directory that should hold the repos:

   ```json
   {
     "externals": {
       "mylib": { "url": "https://github.com/you/mylib.git", "path": "libs/mylib" },
       "tool":  { "url": "git@github.com:you/tool.git",      "path": "tools/tool" }
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
subdirectory, including from inside an external.

The root can be a git repo itself, or just a plain directory:

```
forge/                     <- the root; here a git repo itself
  pit-externals.json
  .gitignore               <- pit keeps the externals' paths in here
  .pit/                    <- pit's local state, gitignored
  README.md                <- belongs to the root repo
  environment/             <- external "environment"
  pit/
    notes.md               <- belongs to the root repo
    public/                <- external "pit"
```

pit works on all **externals** in config order, plus the **root repo** if the root is a git repo. The root repo is selected as `.` (`-r .`),
shows up as `(pit root)` in output, and always comes last. Every file belongs to the external whose path contains it, otherwise to the
root repo (or to no repo, if the root is not a git repo).

## Commands

Every command covers **all repos, wherever in the tree you run it**, just as git covers the whole repo from any subdirectory. You can narrow
that down:

- `-r <repo>` selects a repo by name; `-r .` is the root repo.
- `-g <group>` selects the externals in a [group](#groups).
- Paths select the repos owning them. A directory covers every external below it, so `pit add .` at the root stages in all repos, and
  `pit status libs` shows only the repos under `libs/`.

`-r` and `-g` can be repeated and combined, and select the repos matching any of them: `pit commit -g libs -r . -m "..."` commits in the
`libs` externals and the root repo. Paths together with `-r` or `-g` select only the selected repos owning those paths.

Paths in output are relative to the current directory, like git prints them. Options pit doesn't know itself are passed on to git, e.g.
`pit diff --staged`, `pit pull --rebase`, `pit push --force-with-lease`. Give git options their value in the same argument
(`--source=HEAD~1`, `-U5`).

| Command | What it does |
|---|---|
| `pit sync` | Clone missing externals, check existing ones, update the root's `.gitignore` |
| `pit list` | A table of the externals with path, branch, groups, whether they are cloned and URL; the root directory |
| `pit tree` | The directories from the root down to the externals and their siblings, with branch, groups and whether the externals are cloned |
| `pit status [--fetch]` | Branch, ahead/behind and changed files per repo |
| `pit diff [<options>]` | The diffs of all repos, as one diff of the whole tree |
| `pit add <path>...` / `pit add -A` | Stage, each path in the repo that owns it |
| `pit restore [--staged] <path>...` | Unstage or discard changes, routed the same way |
| `pit commit [-a] [-m <msg>]... [-F <file>] [-n]` | Commit in every repo with something to commit, all with the same message |
| `pit commit --continue` / `--abort` | Finish, or forget, a pit commit that stopped halfway |
| `pit commit --amend [-a] [-m <msg> \| --no-edit]` | Amend the last pit commit |
| `pit log [-n <count>] [--oneline]` | One log across all repos |
| `pit fetch` | Fetch all repos |
| `pit pull` | Pull all repos that have an upstream |
| `pit push [-n] [-i \| -y]` | Show what each repo would push, new tags included, ask repo by repo, then push |
| `pit tag` | The latest tag of each repo and the commits since |
| `pit tag -r <repo> <name>` | Tag HEAD of the selected repos |
| `pit pushtag [-i \| -y] -r <repo> <name>` | Tag, then push as `pit push` does |
| `pit each [--] <git args>` | Run any git command in each repo |
| `pit version` | Show the version |

`status`, `log` and `fetch` work on the repos in parallel. Everything that changes something runs repo by repo, in config order, root
last, and holds a lock (`.pit/lock`) so two pit commands don't interleave.

### Output and colors

Commands that go through the repos show each repo's output in a block of its own: a header line with the repo's name (and its path, if
that differs) ruled out to column 80, the output, then an empty line.

```
$ pit pull
─── pit (pit/public) ───────────────────────────────────────────────────────────
Already up to date.

─── bond (bond/public) ─────────────────────────────────────────────────────────
Updating 1a2b3c4..5d6e7f8
Fast-forward
 README.md | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

```

`sync`, `status`, `fetch`, `pull`, `push`, `commit`, `tag`, `pushtag` and `each` all do this; `add` and `restore` do it only
with `-p` / `-i`, when git asks hunk by hunk. `diff` and `log` stay one diff and one log of the whole tree.

pit colors its output lightly, when it goes to a terminal: the headers, the branches and changes in `status`, questions, warnings and
errors. On Windows, that's a console as well as Git Bash's mintty. `NO_COLOR` set to anything turns colors off. `--color[=always|never|auto]`
or `--no-color`, before or after the command, decides it either way; that goes for the output of git that pit shows, too
(`pit --color diff | less -R`).

### `pit sync`

For each external:

- **Directory missing or empty:** clones it, checking out the configured branch. An empty remote repo is cloned too, with that branch
  started.
- **Already a git repo:** checks that its `origin` is the configured URL (`https://`, `ssh://` and `git@host:path` spellings and a
  trailing `.git` don't matter) and warns if not. Nothing is changed.
- **Exists, but isn't empty and isn't a repo:** an error for this external. pit never overwrites or adopts content.

Then it updates the root repo's `.gitignore` (see [below](#keeping-the-externals-out-of-the-root-repo)). An external removed from the config
is reported, and its directory is left as it is. Externals that were cloned already, with the configured origin, are summed up in one
line.

To add or remove an external, edit `pit-externals.json` and run `pit sync`. If the root is a git repo, commit the config together with
`.gitignore`.

All other commands warn when an external isn't cloned yet or the `.gitignore` is out of date. `pit add` refuses to stage in the root repo
until `pit sync` has fixed the `.gitignore`, since the root repo would otherwise pick up the externals' clones.

### `pit status`

```
$ pit status
─── pit (pit/public) ───────────────────────────────────────────────────────────
main  ↑2 ↓1
   M pit/public/cmd/pit/main.go
  ?? pit/public/concept.md

─── bond (bond/public) ─────────────────────────────────────────────────────────
feature/x  (expected main)
   M bond/public/README.md

─── (pit root) ─────────────────────────────────────────────────────────────────
main  ↑1
   M pit-externals.json

9 more repos clean and up to date
```

Per repo: branch, commits ahead (↑) and behind (↓) its upstream, and the changed files in `git status --short` format. Notes point
out what's unusual: a branch other than the configured one, a detached HEAD, a merge or rebase in progress, a missing or deleted upstream,
a repo without commits. Clean repos with nothing to note are summed up in the last line.

Ahead/behind are as of the last fetch. `--fetch` fetches first; pit also mentions it when the last fetch is more than a day old.

### `pit diff`

Runs `git diff` in every repo and prefixes each repo's paths with its location below the root (`a/pit/public/cmd/pit/main.go`), so the
output reads like one diff of the whole tree. `--staged`, `--stat`, `--exit-code` and other diff options are passed on. pit doesn't
start a pager; pipe the output into one if you like.

### `pit commit`

The core of pit: one message, one commit per repo.

1. **Collect.** The repos with staged changes, or with `-a`, with changes to tracked files. If there are none, pit says so.
2. **Check**, before anything is committed: no repo may have a detached HEAD, unresolved conflicts or an operation (merge, rebase, ...)
   in progress, and a git identity must be configured. All problems are listed at once, and nothing is committed.
3. **Message.** From `-m` (several are joined as paragraphs, as with git), `-F <file>`, or from git's editor, opened once with a list
   of the changes in every repo.
4. **Commit** in each repo, one after the other, root last. Each repo's own hooks run as usual; `-n` (`--no-verify`) skips them.
5. **Report** git's summary of each commit (`[main 1a2b3c4] ...`, the files changed) in the repo's block, along with what its hooks
   printed.

The commits are independent. There is no cross-repo atomicity, and pit doesn't pretend there is. If a commit fails halfway, typically
because of a hook, pit stops and lists what was committed and what wasn't. Fix the problem, then

```sh
pit commit --continue
```

commits the remaining repos with the same message. `pit commit --abort` forgets the interrupted commit instead; the commits already made
stay.

**Amending.** pit remembers the commits of the last pit commit. `pit commit --amend` amends exactly those, offering their message in the
editor (or takes `-m`, or keeps it with `--no-edit`). Repos with newly staged changes that weren't part of it get a fresh commit with the
same message. pit refuses if one of the repos has moved on since, or the commit has already been pushed there. To amend anything else, use
git in that repo.

In the repos, nothing links the commits of one pit commit besides their message, author and time.

### `pit log`

Merges the logs of all repos, newest first, 20 entries unless you give `-n <count>` (or `-<count>`):

```
commit pit 1a2b3c4, bond 5d6e7f8, (pit root) 9a8b7c6
Author: You <you@example.com>
Date:   Sat Nov 14 10:23:45 2026 +0100

    Rename the config option everywhere
```

Commits in different repos with the same author and message, made within ten minutes of each other, are shown as one entry: what
`pit commit` produced. The grouping comes from the histories alone, so it looks the same on every machine and for commits made with git.
`--oneline` shows one line per entry. Paths narrow the log, as with `git log -- <path>`.

### `pit fetch`, `pit pull`

`pit fetch` fetches all repos in parallel. Terminal prompts for credentials are disabled while doing so; credential helpers work as usual.

`pit pull` runs `git pull` in every repo that has an upstream, honoring your git config (`pull.rebase`, `pull.ff`); `--rebase`,
`--ff-only` and other options are passed on. A repo that fails, e.g. with a conflict or local changes in the way, doesn't stop the others.
The summary lists what needs attention, and you resolve it in that repo with git.

### `pit push`

`pit push` fetches first, then shows the plan: a block per repo with the branch, where it goes, the commits about to be pushed, and the
new tags. A repo that has diverged from its upstream is marked `(can't push)`, since git would reject its push; `pit pull` it first.
(Passing `--force`, `--force-with-lease` or `--force-if-includes` lifts that check.)

```
─── pit (pit/public) ───────────────────────────────────────────────────────────
main -> origin/main
  1a2b3c4 Release 1.4.1
tag v1.4.1 -> origin
```

**Tags** go along with the branch: the tags on the pushed branch that its remote doesn't have yet, which pit finds out with
`git ls-remote`. A repo with nothing new but a tag gets pushed, too. Branch and tags go in one atomic push, so a tag never reaches the
remote without the commits it's on, and a release never starts from a commit the branch doesn't have. Tags that aren't on the branch
stay local; pit mentions them. That's why `--tags` and `--follow-tags`, which push tags by git's rules instead, are refused.

Next, pit asks repo by repo whether to push it. Repos that can't be pushed are skipped, and you're still asked about all the others:

```
(1/3) Push pit: main -> origin/main, 2 commits, tag v1.4.1? [y,n,a,s,?] y
(2/3) Push bond: feature/x -> origin/feature/x, 1 commit? [y,n,a,s,?] n
(3/3) Push (pit root): main -> origin/main, 1 commit? [y,n,a,s,?] y
```

`y` pushes the repo, `n` doesn't, `a` pushes it and all remaining ones, `s` skips it and all remaining ones (repos answered with `y` before
are still pushed). All questions come before the first push, so nothing has been pushed until the last answer, and Ctrl-C during the
questions pushes nothing at all.
Without an answer (stdin closed), pit pushes nothing.

Asking is on by default. `"confirmPush": false` in the [settings](#configuration) turns it off. `confirmPushGroups` overrides it for
the externals in some groups, e.g. `{"private": false}` pushes the `private` externals without asking while pit still asks about the
others. The root repo is in no group; the key `"."` overrides it for the root repo. Repos that need no question are pushed along with
the ones you answer `y` for (pit names them before asking); Ctrl-C or no answer still pushes nothing. On the command line, `-y`
(`--yes`) pushes without asking and `-i` (`--interactive`) asks about every repo, whatever the config says. **Without any question, a
repo that can't be pushed stops the whole push**: nobody chose to leave it behind, and related commits in the other repos shouldn't go
out without it. `pit push -i` lets you push the others anyway.

Then it pushes the chosen repos one by one, root last. If one fails, pit stops and lists what was pushed and what wasn't. A branch without
upstream is pushed to `origin` with `-u`. `-n` (`--dry-run`) shows the plan only, without asking. pit exits with an error whenever a repo
was left out because of a problem, so a partial push never looks complete.

### `pit tag`, `pit pushtag`

Without a name, `pit tag` shows each repo's latest tag on HEAD and how many commits came since, i.e. which repos have unreleased
changes:

```
$ pit tag -g public
─── pit (pit/public) ───────────────────────────────────────────────────────────
v1.4.0, 3 commits since

─── bond (bond/public) ─────────────────────────────────────────────────────────
v0.2.1

2 more repos without tags
```

`pit tag -r pit v1.4.1` tags HEAD of the repos selected with `-r` or `-g`. Each repo has versions of its own, so pit doesn't tag all
repos unless you name them. All of them are checked first (a commit to tag, no tag of that name yet), and if one can't be tagged, none
is. The tags are lightweight, like `git tag v1.4.1`.

`pit pushtag -r pit v1.4.1` tags, then pushes those repos as `pit push` does, asking as `confirmPush` says (`-i` and `-y` work, too).
The new tag goes along with any commits not pushed yet. It refuses a name the remote already has a tag of. If you answer `n`, the tag stays
local, and a later `pit push` publishes it.

### `pit each`

The escape hatch for everything pit doesn't wrap:

```sh
pit each -- switch -c feature/x
pit each -r mylib -r tool -- tag v1.0
pit each stash
```

Runs the git command in each repo, its output in a block per repo, and fails if it failed anywhere.

## Configuration

`pit-externals.json`:

```json
{
  "settings": {
    "defaultBranch": "main",
    "confirmPush": true,
    "confirmPushGroups": { "go": false, ".": false }
  },
  "externals": {
    "mylib": { "url": "https://github.com/you/mylib.git", "path": "libs/mylib", "groups": ["libs", "go"] },
    "tool":  { "url": "git@github.com:you/tool.git",      "path": "tools/tool", "branch": "master" }
  }
}
```

| Key | Meaning |
|---|---|
| `settings.defaultBranch` | Branch of externals that set none (default `main`) |
| `settings.confirmPush` | Whether `pit push` asks repo by repo before pushing (default `true`); `-y` / `-i` override it |
| `settings.confirmPushGroups` | `confirmPush` for the externals of these groups, e.g. `{"private": false}`; for an external in several of them that disagree, asking wins. The key `"."` is for the root repo, which is in no group |
| `externals.<name>` | Name used with `-r` and in output; letters, digits, `.`, `_`, `-` |
| `url` | Clone URL (required) |
| `path` | Directory relative to the root, with forward slashes (required) |
| `branch` | Branch `pit sync` checks out, and that `pit status` expects (optional) |
| `groups` | Names of the groups the external belongs to, for `-g` (optional) |

The externals keep the order they have in the file. Unknown keys are rejected, so a typo fails loudly instead of being ignored. Paths must
stay inside the root, must be unique, and an external can't lie inside another one. The whole `settings` object is optional.

### Groups

An external can belong to any number of groups. There is nothing to declare: a group exists once some external lists it. Group names
use the same characters as external names, and an unknown group on the command line is an error that lists the existing ones.

`-g <group>` works with every command:

```sh
pit sync -g work                     # clone only the work repos
pit status -g libs
pit commit -g libs -m "Bump the Go version"
pit each -g go -- tag v1.4.0
```

The root repo belongs to no group; add `-r .` to include it. Externals of a group that aren't cloned yet are left out, except by
`pit sync`, `pit list`, `pit tree` and `pit status`, which deal with them.

## Keeping the externals out of the root repo

If the root is a git repo, `pit sync` maintains a block in its `.gitignore`:

```
# >>> pit externals - managed by pit, do not edit
/.pit/
/environment/
/pit/public/
# <<< pit externals
```

- The entries are exact, anchored paths: `pit/notes.md` stays part of the root repo while `pit/public/` is ignored.
- Only the block is rewritten; the rest of the file stays as it is.
- It's `.gitignore` rather than `.git/info/exclude` because it is versioned and works in every clone of the root repo.
- An ignore rule doesn't affect files that are already tracked. If the root repo tracks files in an external's path, `pit sync` refuses to
  clone that external and tells you how to untrack them.

## Local state

pit keeps a few files in `.pit/` next to `pit-externals.json`. The directory is local to your checkout and gitignored.

| File | Purpose |
|---|---|
| `COMMIT_EDITMSG` | The message of the latest pit commit; survives a failed commit for `--continue` |
| `commit-in-progress.json` | Which repos of an interrupted pit commit are done, and which are still to do |
| `last-commit.json` | The commits of the last pit commit, for `--amend` |
| `lock` | Held while a command changes something |

None of it is needed to understand the repos. Deleting `.pit` loses at most an interrupted commit's progress and the ability to amend the
last pit commit through pit. Since you may use git directly at any time, pit checks what it recorded against the repos before acting on
it. A `lock` left behind by a crashed pit can simply be deleted.

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

The end-to-end tests in `cmd/pit` build throwaway pit roots and remote repositories as local `file://` repositories in temp directories;
they never touch the network and need only git. pit uses the Go standard library only.

## License

MIT – see [LICENSE](LICENSE).
