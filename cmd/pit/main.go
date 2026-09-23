// Command pit (Polyrepo Integration Tool) keeps a monorepo and its external subtree repos in sync.
//
// Configuration: externals.json at the root of the repository pit is run in.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/patrickbergner/pit/internal/monorepo"
)

// version is set from the VERSION file at build time (see build.sh).
var version = "dev"

const usage = `Usage: pit <command> [options]

Run inside the monorepo; configuration is read from externals.json at its root.
Without external names, pull/push/status cover the monorepo AND all externals.
With names, they cover only those externals (the monorepo is left alone).

Commands:
  list                                  Show configured externals
  status [--no-fetch] [<external>...]   Show what is waiting to be pushed / pulled
  pull   [<external>...]                Merge upstream changes: monorepo first, then
                                        new external commits into each repoPath
  push   [-n] [-y] [<external>...]      Show the complete plan (monorepo commits and every
                                        external commit message), ask once, then push:
                                        monorepo first, then the external repos
                                          -n, --dry-run  show the plan only
                                          -y, --yes      don't ask for confirmation
  add    (<external>... | --all)        Import an external repo into its repoPath (first time)
  tag    [-m <msg>] [-r <rev>] [-y] [--allow-unpublished] <external> <tag>
                                        Tag the external commit matching monorepo <rev>
                                        (default HEAD); kept locally as <external>/<tag>
  install-hook                          Install a pre-push hook that blocks direct
                                        pushes to external repos; an existing pre-push
                                        hook (e.g. Git LFS's) is kept and chained
  version                               Show the pit version
`

var commands = map[string]func(*monorepo.Repo, []string) error{
	"list":         cmdList,
	"status":       cmdStatus,
	"pull":         cmdPull,
	"push":         cmdPush,
	"add":          cmdAdd,
	"tag":          cmdTag,
	"install-hook": cmdInstallHook,
}

// errSilent makes pit exit with status 1 without printing anything more.
var errSilent = errors.New("")

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	cmd := "help"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "version", "--version":
		fmt.Println("pit", version)
		return 0
	case "hook": // run by the installed pre-push hook
		err = cmdHook(args)
	default:
		c, ok := commands[cmd]
		if !ok {
			fmt.Fprint(os.Stderr, usage)
			return 1
		}
		var r *monorepo.Repo
		if r, err = monorepo.Open(); err == nil {
			err = c(r, args)
		}
	}
	if err != nil {
		if !errors.Is(err, errSilent) {
			fmt.Fprintf(os.Stderr, "pit: %s\n", err)
		}
		return 1
	}
	return 0
}

// parseNames splits args into external names, rejecting unknown options.
func parseNames(cmd string, args []string) ([]string, error) {
	var names []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return nil, fmt.Errorf("%s: unknown option %s", cmd, a)
		}
		names = append(names, a)
	}
	return names, nil
}
