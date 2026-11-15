// Command pit (Polyrepo Integration Tool) works with a directory tree made of several git repositories as if it were one: the externals
// listed in pit-externals.json are plain clones in subdirectories, and pit runs the everyday git operations across all of them at once.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// version is set from the VERSION file at build time (see build.sh).
var version = "dev"

const usage = `Usage: pit <command> [-r <repo>]... [-g <group>]... [options] [<path>...]

Runs git in all repos of a pit root: the externals in pit-externals.json
(found here or above) and the root directory, if it is a git repo.

Selecting repos (default: all, wherever you run pit):
  -r <repo>                 A repo by name; "." is the root repo
  -g <group>                The externals in a group
  <path>...                 The repos owning these paths
  -r and -g can be repeated and combined.

Colors (default: on in a terminal, off if NO_COLOR is set):
  --color[=<when>]          always, never or auto; also for git's output
  --no-color                Same as --color=never

Setup:
  sync                      Clone missing externals, check the others, and
                            keep them out of the root repo via .gitignore
  list                      Show the externals and whether they are cloned

Changes:
  status [--fetch]          Branch, ahead/behind and changed files per repo
  diff [<git diff options>] Diffs of all repos, paths relative to the root
  add <path>... | -A | -u   Stage, each path in the repo that owns it
  restore [--staged] <path>...
                            Unstage or discard, routed the same way
  commit [-a] [-m <msg>]... [-F <file>] [-n]
                            Commit in every repo with staged changes (-a:
                            with tracked changes), all with the same message
  commit --continue | --abort
                            Finish or forget a pit commit that stopped halfway
  commit --amend [-a] [-m <msg>]
                            Amend the last pit commit in all its repos
  log [-n <count>] [--oneline]
                            One log across all repos; a pit commit is one entry

Remotes:
  fetch [<git fetch options>]
                            Fetch all repos
  pull [<git pull options>] Pull every repo that has an upstream
  push [-n] [-i | -y] [<git push options>]
                            Show what each repo would push, ask, then push;
                            new tags on the pushed branch go along
                            -n: only show; -i: ask; -y: don't ask
                            (default: confirmPush, confirmPushGroups)

Tags:
  tag                       Latest tag and commits since, per repo
  tag -r <repo> <name>      Tag HEAD of the selected repos (-r or -g)
  pushtag [-i | -y] -r <repo> <name>
                            Tag, then push them as pit push does

Anything else:
  each [--] <git args>      Run a git command in each repo
  version                   Show the pit version

Options pit doesn't know go to git, e.g. pit diff --staged,
pit pull --rebase, pit push --force-with-lease.
`

// errSilent makes pit exit with status 1 without printing anything more.
var errSilent = errors.New("")

// interrupted is set on Ctrl-C. The git process running at that moment gets the signal too and stops; pit then stops before the next repo.
var interrupted atomic.Bool

// interruptCh is closed on the first Ctrl-C, ending a pending question.
var interruptCh = make(chan struct{})

func main() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		for range sig {
			if !interrupted.Swap(true) {
				close(interruptCh)
			}
		}
	}()
	code := run(os.Args[1:])
	ui.Reset()
	os.Exit(code)
}

type command struct {
	run func(*workspace.Workspace, []string) error
	// checks: warn about externals that aren't cloned (unless the command lists them itself) and an outdated ignore block
	checks bool
	// locks: the command changes something and takes pit's lock
	locks bool
	// listsMissing: the command shows the externals that aren't cloned itself
	listsMissing bool
}

var commands = map[string]command{
	"sync":    {run: cmdSync, locks: true},
	"list":    {run: cmdList},
	"status":  {run: cmdStatus, checks: true, listsMissing: true},
	"diff":    {run: cmdDiff, checks: true},
	"add":     {run: cmdAdd, checks: true, locks: true},
	"restore": {run: cmdRestore, checks: true, locks: true},
	"commit":  {run: cmdCommit, checks: true, locks: true},
	"log":     {run: cmdLog, checks: true},
	"fetch":   {run: cmdFetch, checks: true},
	"pull":    {run: cmdPull, checks: true, locks: true},
	"push":    {run: cmdPush, checks: true, locks: true},
	"tag":     {run: cmdTag, checks: true}, // locks itself when it tags
	"pushtag": {run: cmdPushTag, checks: true, locks: true},
	"each":    {run: cmdEach, checks: true, locks: true},
}

func run(args []string) int {
	args, color, err := colorOptions(args)
	git.SetColor(ui.SetColor(color))
	if err != nil {
		ui.Error("%s", err)
		return 1
	}
	name := "help"
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	switch name {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "version", "--version":
		fmt.Println("pit", version)
		return 0
	}
	c, ok := commands[name]
	if !ok {
		ui.Error("unknown command %q", name)
		fmt.Fprint(os.Stderr, "\n"+usage)
		return 1
	}
	if err := runCommand(c, args); err != nil {
		if !errors.Is(err, errSilent) {
			ui.Error("%s", err)
		}
		return 1
	}
	return 0
}

// colorOptions takes --color[=<when>] and --no-color out of args, wherever they come before a "--": before the command or among its
// options. The last one counts; color is "" if there is none.
func colorOptions(args []string) (rest []string, color string, err error) {
	for i, a := range args {
		switch {
		case a == "--":
			return append(rest, args[i:]...), color, nil
		case a == "--no-color":
			color = ui.ColorNever
		case a == "--color":
			color = ui.ColorAlways
		case strings.HasPrefix(a, "--color="):
			switch color = strings.TrimPrefix(a, "--color="); color {
			case ui.ColorAlways, ui.ColorNever, ui.ColorAuto:
			default:
				return nil, "", fmt.Errorf("invalid value %q for --color (always, never or auto)", color)
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, color, nil
}

func runCommand(c command, args []string) error {
	if _, err := git.Path(); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	w, err := workspace.Open(cwd)
	if err != nil {
		return err
	}
	if c.checks {
		check(w, !c.listsMissing)
	}
	if c.locks {
		unlock, err := w.Lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	return c.run(w, args)
}

// check warns about what pit sync would fix.
func check(w *workspace.Workspace, uncloned bool) {
	if missing := w.Uncloned(); uncloned && len(missing) > 0 {
		names := make([]string, len(missing))
		for i, r := range missing {
			names[i] = r.Name
		}
		ui.Warn("not cloned yet: %s; run pit sync", strings.Join(names, ", "))
	}
	if w.RootRepo != nil && !w.IgnoreUpToDate() {
		ui.Warn(".gitignore doesn't list the current externals; run pit sync")
	}
}

// stopped reports a Ctrl-C, for loops over repos.
func stopped() error {
	if interrupted.Load() {
		return errors.New("interrupted")
	}
	return nil
}
