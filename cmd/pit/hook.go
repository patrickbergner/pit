package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/patrickbergner/pit/internal/hook"
	"github.com/patrickbergner/pit/internal/monorepo"
)

// cmdInstallHook installs the pre-push guard, pointing it at this very binary.
func cmdInstallHook(_ *monorepo.Repo, args []string) error {
	if len(args) > 0 {
		return errors.New("usage: pit install-hook")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return hook.Install(exe)
}

// cmdHook implements `pit hook pre-push <remote> <url>`, called by the installed hook.
func cmdHook(args []string) error {
	if len(args) != 3 || args[0] != "pre-push" {
		return errors.New("usage: pit hook pre-push <remote> <url>")
	}
	remote, url := args[1], args[2]
	if os.Getenv("PIT_PUSH") == "1" {
		return nil
	}
	name, err := hook.External(url)
	if err != nil {
		return err
	}
	if name != "" {
		fmt.Fprintf(os.Stderr, "pre-push: '%s' (%s) is the external repo of '%s'.\n", remote, url, name)
		fmt.Fprintln(os.Stderr, "pre-push: use 'pit push' or 'pit tag' instead.")
		return errSilent
	}
	return nil
}
