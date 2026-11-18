package main

import (
	"errors"
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
	hash        string
	short       string
	committed   time.Time
	date        string // the author date as git log shows it
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

// logFilters are the git log options that pit log passes on, and whether each takes a value. They choose commits and leave git's output
// alone, which pit reads itself.
var logFilters = map[string]bool{
	"--author": true, "--committer": true, "--since": true, "--after": true, "--until": true, "--before": true, "--grep": true,
	"--all-match": false, "--invert-grep": false, "-i": false, "--regexp-ignore-case": false, "-E": false, "--extended-regexp": false,
	"-F": false, "--fixed-strings": false, "-P": false, "--perl-regexp": false, "--no-merges": false, "--merges": false,
	"--min-parents": true, "--max-parents": true, "--first-parent": false, "-S": true, "-G": true, "--pickaxe-regex": false,
	"--diff-filter": true, "--follow": false,
}

// checkLogFilter tells why pit log can't pass the option o on to git log, if it can't.
func checkLogFilter(o string) error {
	name, _, hasValue := strings.Cut(o, "=")
	if !strings.HasPrefix(o, "--") && len(o) > 2 { // -Sfoo
		name, hasValue = o[:2], true
	}
	takesValue, ok := logFilters[name]
	switch {
	case !ok:
		return fmt.Errorf("log: pit log takes only git log options that choose commits (--author=, --since=, --grep=, --no-merges, ...), "+
			"as it reads git's output itself; %s isn't one", name)
	case takesValue && !hasValue && strings.HasPrefix(name, "--"):
		return fmt.Errorf("log: give %s its value in the same argument (%s=<value>)", name, name)
	case takesValue && !hasValue:
		return fmt.Errorf("log: give %s its value in the same argument (%s<value>)", name, name)
	}
	return nil
}

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
	p, err := parseArgs("log", args, spec{bools: []string{"oneline"}, values: []string{"n|max-count"}, passthrough: true})
	if err != nil {
		return err
	}
	for _, o := range p.opts {
		if err := checkLogFilter(o); err != nil {
			return err
		}
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
	results := eachTarget(targets, func(t workspace.Target) result {
		c, err := readLog(t.Repo, count, p.opts, t.Pathspecs)
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

// readLog reads up to count commits of the repo's log, all if count is 0. extra are git log options and revisions (HEAD if none), pathspecs
// limit the log.
func readLog(r *workspace.Repo, count int, extra, pathspecs []string) ([]logCommit, error) {
	if !git.OK(r.Dir, "rev-parse", "-q", "--verify", "HEAD") {
		return nil, nil // no commits yet
	}
	args := []string{"log", "-z", "--format=%H%x1f%h%x1f%ct%x1f%ad%x1f%an%x1f%ae%x1f%B"}
	if count > 0 {
		args = append(args, "-n", strconv.Itoa(count))
	}
	args = append(args, extra...)
	if len(pathspecs) > 0 {
		args = append(append(args, "--"), pathspecs...)
	}
	out, err := git.Raw(r.Dir, nil, args...)
	if err != nil {
		return nil, err
	}
	var commits []logCommit
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(rec, "\x1f", 7)
		if len(f) != 7 {
			continue
		}
		ct, _ := strconv.ParseInt(f[2], 10, 64)
		msg := strings.TrimRight(f[6], "\n")
		subject, _, _ := strings.Cut(msg, "\n")
		commits = append(commits, logCommit{r, f[0], f[1], time.Unix(ct, 0), f[3], f[4], f[5], msg, subject})
	}
	return commits, nil
}

// findCommit resolves rev, a commit given on the command line, in the repos; it must name a commit in exactly one of them.
func findCommit(repos []*workspace.Repo, rev string) (logCommit, error) {
	if strings.HasPrefix(rev, "-") {
		return logCommit{}, fmt.Errorf("%s is no commit", rev)
	}
	type result struct {
		commits []logCommit
		err     error
	}
	results := workspace.Parallel(repos, func(r *workspace.Repo) result {
		hash, err := git.Output(r.Dir, "rev-parse", "-q", "--verify", rev+"^{commit}")
		if err != nil || hash == "" {
			return result{}
		}
		c, err := readLog(r, 1, []string{hash}, nil)
		return result{c, err}
	})
	var found []logCommit
	for i, res := range results {
		if res.err != nil {
			return logCommit{}, fmt.Errorf("%s: %s", repos[i].Label(), res.err)
		}
		found = append(found, res.commits...)
	}
	switch len(found) {
	case 0:
		return logCommit{}, fmt.Errorf("%s is no commit in any of the repos", rev)
	case 1:
		return found[0], nil
	}
	names := make([]string, len(found))
	for i, c := range found {
		names[i] = c.repo.Label()
	}
	return logCommit{}, fmt.Errorf("%s names a commit in %s; give more of its hash, or select the repo with -r",
		rev, strings.Join(names, ", "))
}

// newestCommit returns the latest commit on HEAD among the repos.
func newestCommit(repos []*workspace.Repo) (logCommit, error) {
	type result struct {
		commits []logCommit
		err     error
	}
	results := workspace.Parallel(repos, func(r *workspace.Repo) result {
		c, err := readLog(r, 1, nil, nil)
		return result{c, err}
	})
	var lists [][]logCommit
	for i, res := range results {
		if res.err != nil {
			return logCommit{}, fmt.Errorf("%s: %s", repos[i].Label(), res.err)
		}
		lists = append(lists, res.commits)
	}
	if merged := mergeLogs(lists); len(merged) > 0 {
		return merged[0], nil
	}
	return logCommit{}, errors.New("no commits in any of the repos")
}

// entryOf returns the pit commit c belongs to: the entry pit log shows it in, found among the repos' commits made around its time. The
// repo of c is read from c on, so that a commit off its HEAD is found too.
func entryOf(c logCommit, repos []*workspace.Repo) (logEntry, error) {
	// an entry has at most one commit per repo, each within groupWindow of the next
	span := time.Duration(len(repos)+1) * groupWindow
	window := []string{"--since=@" + strconv.FormatInt(c.committed.Add(-span).Unix(), 10),
		"--until=@" + strconv.FormatInt(c.committed.Add(span).Unix(), 10)}
	type result struct {
		commits []logCommit
		err     error
	}
	results := workspace.Parallel(repos, func(r *workspace.Repo) result {
		extra := window
		if r == c.repo {
			extra = append(slices.Clone(window), c.hash)
		}
		commits, err := readLog(r, 0, extra, nil)
		return result{commits, err}
	})
	var lists [][]logCommit
	for i, res := range results {
		if res.err != nil {
			return logEntry{}, fmt.Errorf("%s: %s", repos[i].Label(), res.err)
		}
		lists = append(lists, res.commits)
	}
	merged := mergeLogs(lists)
	for _, e := range groupLog(merged, len(merged), repos) {
		if slices.ContainsFunc(e.commits, func(d logCommit) bool { return d.repo == c.repo && d.hash == c.hash }) {
			return e, nil
		}
	}
	return logEntry{[]logCommit{c}}, nil
}

// cmdShow shows a pit commit, the newest one unless a commit of it is given: the entry pit log shows, then the changes of all its repos as
// one diff, as pit diff prints them.
func cmdShow(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("show", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	if len(p.paths) > 1 {
		return errors.New("show: give one commit; pit show finds the commits that belong with it in the other repos")
	}
	repos, err := p.selected(w)
	if err != nil {
		return err
	}
	var c logCommit
	if len(p.paths) == 0 {
		c, err = newestCommit(repos)
	} else {
		c, err = findCommit(repos, p.paths[0])
	}
	if err != nil {
		return err
	}
	e, err := entryOf(c, repos)
	if err != nil {
		return err
	}
	printEntry(e, false)
	blocks := listsFiles(p.opts)
	var failed []string
	for _, d := range e.commits {
		if err := stopped(); err != nil {
			return err
		}
		prefix := d.repo.RootRelative()
		gitArgs := append([]string{"--no-pager", "show", "--format=", "--src-prefix=a/" + prefix, "--dst-prefix=b/" + prefix}, p.opts...)
		if blocks {
			header(d.repo)
		}
		err := git.Pass(d.repo.Dir, append(gitArgs, d.hash)...)
		if blocks {
			ui.End()
		}
		if err != nil {
			failed = append(failed, d.repo.Label())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("show failed in %s", strings.Join(failed, ", "))
	}
	return nil
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
		// the entries started last are the closest in time
		for i := len(entries) - 1; i >= 0; i-- {
			e := &entries[i]
			if e.commits[len(e.commits)-1].committed.Sub(c.committed) > groupWindow {
				continue
			}
			if e.commits[0].email == c.email && e.commits[0].message == c.message && !e.has(c.repo) && !e.betterMatch(c, commits) {
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

// betterMatch reports whether c, which could join the entry, rather belongs with another commit: one with its author and message from a
// repo of the entry that is closer to c in time than the entry's commit of that repo. That is how the same message used again shortly
// afterwards in one repo ("WIP") doesn't take the place of the commit that was made together with c.
func (e *logEntry) betterMatch(c logCommit, commits []logCommit) bool {
	for _, m := range e.commits {
		for _, d := range commits {
			if d.repo == m.repo && d.email == c.email && d.message == c.message &&
				d.committed.Sub(c.committed).Abs() < m.committed.Sub(c.committed).Abs() {
				return true
			}
		}
	}
	return false
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
	fmt.Printf("Date:   %s\n\n", c.date)
	for _, l := range strings.Split(c.message, "\n") {
		fmt.Println(strings.TrimRight("    "+l, " "))
	}
	fmt.Println()
}
