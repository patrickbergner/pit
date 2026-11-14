package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// repoStatus is a repo's status read for a command, or the error reading it.
type repoStatus struct {
	repo *workspace.Repo
	*workspace.Status
	err error
}

func readStatuses(targets []workspace.Target) []repoStatus {
	return workspace.Parallel(repos(targets), func(r *workspace.Repo) repoStatus {
		var specs []string
		for _, t := range targets {
			if t.Repo == r {
				specs = t.Pathspecs
			}
		}
		st, err := workspace.ReadStatus(r, specs)
		return repoStatus{r, st, err}
	})
}

func repos(targets []workspace.Target) []*workspace.Repo {
	rs := make([]*workspace.Repo, len(targets))
	for i, t := range targets {
		rs[i] = t.Repo
	}
	return rs
}

// notes lists what is unusual about a repo's state.
func (s repoStatus) notes() []string {
	var n []string
	if s.Operation != "" {
		n = append(n, s.Operation)
	}
	switch {
	case s.Detached:
		n = append(n, "HEAD detached")
	case s.repo.Ext != nil && s.Branch != s.repo.Ext.Branch:
		n = append(n, "expected "+s.repo.Ext.Branch)
	}
	switch {
	case s.Unborn:
		n = append(n, "no commits yet")
	case s.UpstreamGone:
		n = append(n, "upstream "+s.Upstream+" is gone")
	case s.Upstream == "" && !s.Detached && s.repo.HasRemotes():
		n = append(n, "no upstream")
	}
	return n
}

func (s repoStatus) branchLabel() string {
	if s.Detached {
		return "(detached)"
	}
	return s.Branch
}

func (s repoStatus) aheadBehind() string {
	var parts []string
	if s.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", s.Ahead))
	}
	if s.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", s.Behind))
	}
	return strings.Join(parts, " ")
}

func cmdStatus(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("status", args, spec{bools: []string{"fetch"}})
	if err != nil {
		return err
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	if p.on("fetch") {
		if err := fetchAll(repos(targets), nil, true); err != nil {
			return err
		}
	}
	statuses := readStatuses(targets)

	var shown []repoStatus
	failed, quiet := 0, 0
	for _, s := range statuses {
		switch {
		case s.err != nil:
			fmt.Fprintf(os.Stderr, "pit: %s: %s\n", s.repo.Name, s.err)
			failed++
		case s.Clean() && s.Ahead == 0 && s.Behind == 0 && len(s.notes()) == 0:
			quiet++
		default:
			shown = append(shown, s)
		}
	}
	// externals of the selection that aren't cloned yet
	var missing []*workspace.Repo
	if len(p.paths) == 0 {
		candidates := w.Externals
		if p.narrowed() {
			if candidates, err = w.Match(p.repos, p.groups); err != nil {
				return err
			}
		}
		for _, r := range candidates {
			if !r.IsRoot() && !r.Cloned() {
				missing = append(missing, r)
			}
		}
	}

	nameW, branchW, abW := 0, 0, 0
	for _, s := range shown {
		nameW, branchW = max(nameW, len(s.repo.Name)), max(branchW, len(s.branchLabel()))
		abW = max(abW, len([]rune(s.aheadBehind())))
	}
	for _, r := range missing {
		nameW = max(nameW, len(r.Name))
	}
	for _, s := range shown {
		line := ui.Pad(s.repo.Name, nameW) + "  " + ui.Pad(s.branchLabel(), branchW)
		if abW > 0 {
			line += "  " + ui.Pad(s.aheadBehind(), abW)
		}
		if n := s.notes(); len(n) > 0 {
			line += "  (" + strings.Join(n, ", ") + ")"
		}
		fmt.Println(ui.Bold(strings.TrimRight(line, " ")))
		for _, e := range s.Entries {
			path := w.Display(s.repo, e.Path)
			if e.Orig != "" {
				path = w.Display(s.repo, e.Orig) + " -> " + path
			}
			fmt.Printf("  %s %s\n", e.XY, path)
		}
	}
	for _, r := range missing {
		fmt.Printf("%s  not cloned (pit sync)\n", ui.Pad(r.Name, nameW))
	}

	switch {
	case quiet > 0 && len(shown) == 0 && len(missing) == 0 && failed == 0:
		fmt.Printf("%s clean and up to date\n", allRepos(quiet))
	case quiet > 0 && len(shown) > 0:
		fmt.Printf("%s clean and up to date\n", ui.Plural(quiet, "more repo"))
	case quiet > 0:
		fmt.Printf("%s clean and up to date\n", ui.Plural(quiet, "repo"))
	case len(statuses) == 0 && len(missing) == 0:
		fmt.Println("no repos")
	}
	if !p.on("fetch") {
		fetchAgeNote(statuses)
	}
	if failed > 0 {
		return errSilent
	}
	return nil
}

func allRepos(n int) string {
	if n == 1 {
		return "1 repo"
	}
	return fmt.Sprintf("all %d repos", n)
}

// fetchAgeNote points out when ahead/behind are based on an old fetch.
func fetchAgeNote(statuses []repoStatus) {
	var oldest time.Time
	for _, s := range statuses {
		if s.err != nil || s.Upstream == "" {
			continue
		}
		if t := s.repo.LastFetch(); !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	if age := time.Since(oldest); !oldest.IsZero() && age > 24*time.Hour {
		ui.Note("ahead/behind are as of the last fetch, %d days ago for the oldest; pit status --fetch refreshes them", int(age.Hours()/24))
	}
}
