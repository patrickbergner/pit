package main

import (
	"fmt"
	"slices"
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

// eachTarget runs f for each target as workspace.Parallel does for repos, and returns the results in the targets' order. Targets have a
// repo each.
func eachTarget[T any](targets []workspace.Target, f func(workspace.Target) T) []T {
	rs := repos(targets)
	return workspace.Parallel(rs, func(r *workspace.Repo) T { return f(targets[slices.Index(rs, r)]) })
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

	shown := map[*workspace.Repo]repoStatus{}
	failed, quiet := 0, 0
	for _, s := range statuses {
		switch {
		case s.err != nil:
			ui.Error("%s: %s", s.repo.Label(), s.err)
			failed++
		case s.Clean() && s.Ahead == 0 && s.Behind == 0 && len(s.notes()) == 0:
			quiet++
		default:
			shown[s.repo] = s
		}
	}
	// externals of the selection that aren't cloned yet
	missing := map[*workspace.Repo]bool{}
	if len(p.paths) == 0 {
		candidates := w.Externals
		if p.narrowed() {
			if candidates, err = w.Match(p.repos, p.groups); err != nil {
				return err
			}
		}
		for _, r := range candidates {
			if !r.IsRoot() && !r.Cloned() {
				missing[r] = true
			}
		}
	}

	// a block per repo with something to report, in workspace order
	for _, r := range w.All() {
		if s, ok := shown[r]; ok {
			header(r)
			s.print(w)
			ui.End()
		} else if missing[r] {
			header(r)
			fmt.Println(ui.Yellow("not cloned") + " (pit sync)")
			ui.End()
		}
	}

	switch {
	case quiet > 0 && len(shown) == 0 && len(missing) == 0 && failed == 0:
		fmt.Printf("%s clean and up to date\n", all(quiet, "repo"))
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

// print shows the repo's status: branch, ahead/behind and notes in one line, then the changed files in git status --short format.
func (s repoStatus) print(w *workspace.Workspace) {
	line := ui.Green(s.branchLabel())
	if s.Detached {
		line = ui.Red(s.branchLabel())
	}
	if ab := s.aheadBehind(); ab != "" {
		line += "  " + ab
	}
	if n := s.notes(); len(n) > 0 {
		line += "  " + ui.Yellow("("+strings.Join(n, ", ")+")")
	}
	fmt.Println(line)
	for _, e := range s.Entries {
		path := w.Display(s.repo, e.Path)
		if e.Orig != "" {
			path = w.Display(s.repo, e.Orig) + " -> " + path
		}
		fmt.Printf("  %s %s\n", statusLetters(e), path)
	}
}

// statusLetters colors an entry's XY as git status does: the index's letter green, the worktree's red, both red for untracked and
// unmerged paths.
func statusLetters(e workspace.Entry) string {
	if e.Untracked() || e.Conflicted() {
		return ui.Red(e.XY)
	}
	return ui.Green(e.XY[:1]) + ui.Red(e.XY[1:])
}

// all returns "1 repo" / "all 3 repos".
func all(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("all %d %ss", n, word)
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
