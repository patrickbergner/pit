pit - Polyrepo Integration Tool

pit lets you work with a directory tree made of several git repositories
as if it were one. The repositories listed in pit-externals.json are
cloned into their subdirectories as plain clones; pit then runs status,
diff, add, commit, log, pull and push across all of them at once. A pit
commit commits in every repo with changes, all with the same message.

Each external stays an ordinary clone: cd into it and use git directly
whenever you like.

Requirements:
  git on PATH.

Installation:
  Put pit anywhere on your PATH.

Quick start:
  1. Create pit-externals.json in the directory that should hold the repos:
       {
         "externals": {
           "mylib": { "url": "https://github.com/you/mylib.git", "path": "libs/mylib" }
         }
       }
  2. Clone everything:                 pit sync
  3. Then, anywhere in the tree:       pit status
                                       pit add -A
                                       pit commit -m "message"
                                       pit push

If the directory holding pit-externals.json is a git repo itself, it is
included as the root repo, "(pit root)" in output and "." with -r, and pit
sync keeps the externals out of it via its .gitignore. An external can
also lie inside another one; pit sync then keeps it out of that one via
the other's .git/info/exclude, which leaves the other's history alone.

Commands:
  sync                        Clone missing externals, update .gitignore
  list                        Show the externals
  tree                        Show the externals in their directory tree
  status [--fetch]            Branch, ahead/behind and changes per repo
  diff [<git diff options>]   Diffs of all repos as one diff
  grep [<options>] <pattern> [<path>...]
                              Search the tracked files of all repos
  add <path>... | -A          Stage, each path in the repo that owns it
  restore [--staged] <path>...
  rm [--recursive] [--cached] <path>...
                              Remove files, routed the same way, once all
                              repos pass git's checks. --recursive is git
                              rm's -r, as -r selects repos.
  mv <source>... <dest>       Move or rename files in the repo that owns
                              them; between repos, move them yourself and
                              pit add both paths.
  commit [-a] [-m <msg>] [<path>...]
                              Commit in every repo with the same message;
                              with paths, just those
  commit --continue | --abort Finish or forget an interrupted pit commit
  commit --amend              Amend the last pit commit
  revert <commit>             Revert the pit commit that <commit> is part
                              of, as one new pit commit
  stash [push | pop | apply | drop | list]
                              Stash in every repo with changes; pop brings
                              back exactly those stashes
  log [-n <count>] [--oneline] [--author=<who>] [--since=<when>] ...
  show [<commit>]             A pit commit as one diff of all its repos
  graph [<git log options>]   The history of the repo you are in, as
                              git log --graph draws it
  changes <commit> [<revision>]
                              The files the commit changed, or that differ
                              between it and the revision (e.g. HEAD)
  fetch | pull
  push [-n] [-i | -y]         Show what each repo would push, ask repo by
                              repo, then push. Asking is on unless
                              "settings": {"confirmPush": false}; -y skips
                              it, -i forces it. A repo that diverged from
                              its upstream is skipped when asking; without
                              asking, it stops the whole push. New tags on
                              the pushed branch go along.
  tag                         Latest tag and commits since, per repo
  tag -r <repo> <name>        Tag HEAD of the selected repos
  pushtag -r <repo> <name>    Tag, then push as pit push does
  each [--] <git args>        Run any git command in each repo
  completion bash | powershell
                              Print the shell's completion script; load it
                              with source <(pit completion bash), or
                              pit completion powershell | Out-String |
                              Invoke-Expression
  version

Every command covers all repos, wherever you run it; -r <repo>,
-g <group> or paths narrow it. Groups are assigned per external in the
config: "groups": ["work", "go"]. Options pit doesn't know are passed on
to git. In a terminal, diff, grep, log and show page their output with
git's pager; --no-pager turns that off.

Where git runs in the background (fetch, status --fetch), it can't ask for
credentials and fails at once; pit says where to enter them. Ctrl-C stops
before the next repo; a second Ctrl-C also ends a git that keeps waiting.

Full documentation: https://github.com/patrickbergner/pit
