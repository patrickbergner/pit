package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

type logCommit struct {
	repo        *workspace.Repo
	short       string
	committed   time.Time
	authored    time.Time
	author      string
	email       string
	message     string
	subjectLine string
}

// logEntry is one commit, or the commits of one pit commit: same author and message, consecutive across repos.
type logEntry struct {
	commits []logCommit
}

var countFlag = regexp.MustCompile(`^-[0-9]+$`)

func cmdLog(w *workspace.Workspace, args []string) error {
	// git's -<n> shorthand
	for i, a := range args {
		if a == "--" {
			break
		}
		if countFlag.MatchString(a) {
			args[i] = "-n" + a[1:]
		}
	}
	p, err := parseArgs("log", args, spec{bools: []string{"oneline"}, values: []string{"n|max-count"}})
	if err != nil {
		return err
	}
	count := 20
	if v := p.last("max-count"); v != "" {
		if count, err = strconv.Atoi(v); err != nil || count < 1 {
			return fmt.Errorf("log: invalid count %q", v)
		}
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}

	type result struct {
		commits []logCommit
		err     error
	}
	results := workspace.Parallel(repos(targets), func(r *workspace.Repo) result {
		var specs []string
		for _, t := range targets {
			if t.Repo == r {
				specs = t.Pathspecs
			}
		}
		c, err := readLog(r, count, specs)
		return result{c, err}
	})
	var lists [][]logCommit
	failed := false
	for i, res := range results {
		if res.err != nil {
			ui.Error("%s: %s", targets[i].Repo.Label(), res.err)
			failed = true
			continue
		}
		lists = append(lists, res.commits)
	}

	for _, e := range groupLog(mergeLogs(lists), count, repos(targets)) {
		printEntry(e, p.on("oneline"))
	}
	if failed {
		return errSilent
	}
	return nil
}

func readLog(r *workspace.Repo, count int, pathspecs []string) ([]logCommit, error) {
	if !git.OK(r.Dir, "rev-parse", "-q", "--verify", "HEAD") {
		return nil, nil // no commits yet
	}
	args := []string{"log", "-z", "-n", strconv.Itoa(count), "--format=%h%x1f%ct%x1f%at%x1f%an%x1f%ae%x1f%B"}
	if len(pathspecs) > 0 {
		args = append(append(args, "--"), pathspecs...)
	}
	out, err := git.Raw(r.Dir, nil, args...)
	if err != nil {
		return nil, err
	}
	var commits []logCommit
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(rec, "\x1f", 6)
		if len(f) != 6 {
			continue
		}
		ct, _ := strconv.ParseInt(f[1], 10, 64)
		at, _ := strconv.ParseInt(f[2], 10, 64)
		msg := strings.TrimRight(f[5], "\n")
		subject, _, _ := strings.Cut(msg, "\n")
		commits = append(commits, logCommit{r, f[0], time.Unix(ct, 0), time.Unix(at, 0), f[3], f[4], msg, subject})
	}
	return commits, nil
}

// mergeLogs merges the repos' logs, newest commit first, keeping each repo's own order.
func mergeLogs(lists [][]logCommit) []logCommit {
	var merged []logCommit
	idx := make([]int, len(lists))
	for {
		best := -1
		for i, l := range lists {
			if idx[i] < len(l) && (best < 0 || l[idx[i]].committed.After(lists[best][idx[best]].committed)) {
				best = i
			}
		}
		if best < 0 {
			return merged
		}
		merged = append(merged, lists[best][idx[best]])
		idx[best]++
	}
}

// groupWindow is how far apart the commits of one pit commit may be. Commit times have a resolution of one second, so the commits of one pit
// commit aren't necessarily adjacent once the logs are merged.
const groupWindow = 10 * time.Minute

// groupLog joins commits of different repos with the same author and message, made within groupWindow, into one entry; up to count entries.
// The commits of an entry are listed in the order of repos (workspace order), not in the order they were merged in, which for commits made
// in the same second is arbitrary.
func groupLog(commits []logCommit, count int, repos []*workspace.Repo) []logEntry {
	entries := groupCommits(commits, count)
	for _, e := range entries {
		slices.SortStableFunc(e.commits, func(a, b logCommit) int {
			return slices.Index(repos, a.repo) - slices.Index(repos, b.repo)
		})
	}
	return entries
}

func groupCommits(commits []logCommit, count int) []logEntry {
	var entries []logEntry
next:
	for _, c := range commits {
		for i := len(entries) - 1; i >= 0; i-- {
			e := &entries[i]
			if e.commits[len(e.commits)-1].committed.Sub(c.committed) > groupWindow {
				break
			}
			if e.commits[0].email == c.email && e.commits[0].message == c.message && !e.has(c.repo) {
				e.commits = append(e.commits, c)
				continue next
			}
		}
		if len(entries) < count {
			entries = append(entries, logEntry{[]logCommit{c}})
		}
	}
	return entries
}

func (e *logEntry) has(r *workspace.Repo) bool {
	for _, c := range e.commits {
		if c.repo == r {
			return true
		}
	}
	return false
}

func (e logEntry) repoList() string {
	parts := make([]string, len(e.commits))
	for i, c := range e.commits {
		parts[i] = c.repo.Label() + " " + c.short
	}
	return strings.Join(parts, ", ")
}

func printEntry(e logEntry, oneline bool) {
	c := e.commits[0]
	if oneline {
		fmt.Printf("%s  %s  %s\n", c.committed.Format("2006-01-02 15:04"), c.subjectLine, ui.Yellow("["+e.repoList()+"]"))
		return
	}
	fmt.Println(ui.Yellow("commit " + e.repoList()))
	fmt.Printf("Author: %s <%s>\n", c.author, c.email)
	fmt.Printf("Date:   %s\n\n", c.authored.Format("Mon Jan 2 15:04:05 2006 -0700"))
	for _, l := range strings.Split(c.message, "\n") {
		fmt.Println(strings.TrimRight("    "+l, " "))
	}
	fmt.Println()
}
