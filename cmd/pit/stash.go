package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// stashFile records pit's stashes, so that pit stash pop brings back exactly the stashes one pit stash made.
const stashFile = "stash.json"

// stashRecord is pit's record of its stashes, oldest first.
type stashRecord struct {
	Stashes []pitStash `json:"stashes"`
}

// pitStash is one pit stash: the stash it made in each repo that had something to stash.
type pitStash struct {
	Repos []stashRef `json:"repos"`
}

type stashRef struct {
	Repo string `json:"repo"`
	Hash string `json:"hash"` // the stash commit
}

func cmdStash(w *workspace.Workspace, args []string) error {
	sub := "push"
	if len(args) > 0 && slices.Contains([]string{"push", "pop", "apply", "drop", "list"}, args[0]) {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "push":
		return stashPush(w, args)
	case "list":
		return stashList(w, args)
	}
	return stashUse(w, sub, args)
}

// stashPush runs git stash push in each selected repo with changes and records the stashes it made as one pit stash.
func stashPush(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("stash", args, spec{values: []string{"m|message"}, passthrough: true})
	if err != nil {
		return err
	}
	// Ignored files are the clones of the externals inside a repo, and pit's own state in the root repo.
	if hasOpt(p.opts, "-a", "--all") {
		return errors.New("stash: --all would stash ignored files too, which include the clones of externals and pit's .pit; " +
			"-u (--include-untracked) stashes untracked files")
	}
	var rec stashRecord
	if _, err := w.ReadState(stashFile, &rec); err != nil {
		return err
	}
	targets, err := p.targets(w)
	if err != nil {
		return err
	}
	var owning []workspace.Target
	for _, t := range targets {
		if t = ownPathspecs(t); len(p.paths) == 0 || len(t.Pathspecs) > 0 {
			owning = append(owning, t)
		}
	}
	untracked := hasOpt(p.opts, "-u", "--include-untracked")
	var todo []workspace.Target
	var problems []string
	for i, s := range readStatuses(owning) {
		r := owning[i].Repo
		switch {
		case s.err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s", r.Label(), s.err))
		case !s.TrackedChanges() && (!untracked || s.Clean()): // nothing to stash
		case s.Unborn:
			problems = append(problems, r.Label()+": has no commits yet, so git can't stash its changes")
		case s.Conflicts():
			problems = append(problems, r.Label()+": has unresolved conflicts")
		default:
			todo = append(todo, owning[i])
		}
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			ui.Error("%s", pr)
		}
		return errors.New("stash: nothing was stashed")
	}
	if len(todo) == 0 {
		ui.Note("no local changes to save")
		return nil
	}

	gitArgs := append([]string{"stash", "push"}, p.opts...)
	if m := p.vals["message"]; len(m) > 0 {
		gitArgs = append(gitArgs, "-m", strings.Join(m, "\n\n"))
	}
	var made pitStash
	var stashed, pending []string
	var failure error
	for i, t := range todo {
		if failure = stopped(); failure != nil {
			pending = labels(todo[i:])
			break
		}
		before, _ := git.Output(t.Repo.Dir, "rev-parse", "-q", "--verify", "refs/stash")
		header(t.Repo)
		err := git.Pass(t.Repo.Dir, pathspecArgs(slices.Clone(gitArgs), t)...)
		ui.End()
		if after, _ := git.Output(t.Repo.Dir, "rev-parse", "-q", "--verify", "refs/stash"); after != "" && after != before {
			made.Repos = append(made.Repos, stashRef{t.Repo.Name, after})
			stashed = append(stashed, t.Repo.Label())
		}
		if err != nil {
			failure = fmt.Errorf("%s: git stash failed", t.Repo.Label())
			pending = labels(todo[i+1:])
			break
		}
	}
	if len(made.Repos) > 0 {
		rec.Stashes = append(rec.Stashes, made)
		if err := w.WriteState(stashFile, &rec); err != nil {
			return err
		}
	}
	if failure != nil {
		ui.Error("%s", failure)
		if len(stashed) > 0 {
			ui.Note("stashed: %s (pit stash pop brings it back)", strings.Join(stashed, ", "))
		}
		if len(pending) > 0 {
			ui.Note("not stashed: %s", strings.Join(pending, ", "))
		}
		return errSilent
	}
	return nil
}

func labels(targets []workspace.Target) []string {
	l := make([]string, len(targets))
	for i, t := range targets {
		l[i] = t.Repo.Label()
	}
	return l
}

// stashUse pops, applies or drops a pit stash, the newest unless its number is given, in each of its repos.
func stashUse(w *workspace.Workspace, sub string, args []string) error {
	p, err := parseArgs("stash", args, spec{passthrough: true})
	if err != nil {
		return err
	}
	if p.narrowed() {
		return fmt.Errorf("stash %s: covers the repos of the pit stash; -r and -g don't apply", sub)
	}
	var rec stashRecord
	if _, err := w.ReadState(stashFile, &rec); err != nil {
		return err
	}
	n, err := stashNumber(sub, p.paths, len(rec.Stashes))
	if err != nil {
		return err
	}
	idx := len(rec.Stashes) - 1 - n

	// The record is pit's own: since, a stash may have been popped or dropped with git, or its repo removed.
	type step struct {
		ref   stashRef
		repo  *workspace.Repo
		index int // in the repo's stash list
	}
	var steps []step
	for _, ref := range rec.Stashes[idx].Repos {
		r, err := byName(w, ref.Repo)
		if err != nil {
			ui.Warn("%s; leaving its stash %.7s out", err, ref.Hash)
			continue
		}
		i, err := stashIndex(r, ref.Hash)
		switch {
		case err != nil:
			return fmt.Errorf("%s: %s", r.Label(), err)
		case i < 0:
			ui.Warn("%s: its stash %.7s is gone (popped or dropped with git?); leaving it out", r.Label(), ref.Hash)
		default:
			steps = append(steps, step{ref, r, i})
		}
	}

	gitArgs := append([]string{"stash", sub}, p.opts...)
	done := 0
	var failure error
	for _, s := range steps {
		if failure = stopped(); failure != nil {
			break
		}
		header(s.repo)
		err := git.Pass(s.repo.Dir, append(slices.Clone(gitArgs), fmt.Sprintf("stash@{%d}", s.index))...)
		ui.End()
		if err != nil {
			failure = fmt.Errorf("%s: git stash %s failed", s.repo.Label(), sub)
			break
		}
		done++
	}

	// What stays of the pit stash: after pop and drop, the stashes not used yet; git keeps the one whose pop failed.
	var keep []stashRef
	for i, s := range steps {
		if sub == "apply" || i >= done {
			keep = append(keep, s.ref)
		}
	}
	if len(keep) > 0 {
		rec.Stashes[idx].Repos = keep
	} else {
		rec.Stashes = slices.Delete(rec.Stashes, idx, idx+1)
	}
	if len(rec.Stashes) == 0 {
		err = w.RemoveState(stashFile)
	} else {
		err = w.WriteState(stashFile, &rec)
	}
	if err != nil {
		return err
	}
	if failure != nil {
		ui.Error("%s", failure)
		var finished, rest []string
		for i, s := range steps {
			if i < done {
				finished = append(finished, s.repo.Label())
			} else {
				rest = append(rest, s.repo.Label())
			}
		}
		if len(finished) > 0 {
			ui.Note("done: %s", strings.Join(finished, ", "))
		}
		ui.Note("not done: %s; pit stash %s again takes it up once the problem is solved", strings.Join(rest, ", "), sub)
		return errSilent
	}
	return nil
}

// stashNumber turns the argument of pop, apply or drop into the number of a pit stash, newest first as in pit stash list.
func stashNumber(sub string, args []string, count int) (int, error) {
	if count == 0 {
		return 0, fmt.Errorf("stash %s: there is no pit stash", sub)
	}
	if len(args) == 0 {
		return 0, nil
	}
	n, err := strconv.Atoi(args[0])
	if len(args) > 1 || err != nil || n < 0 || n >= count {
		return 0, fmt.Errorf("stash %s: %s is no pit stash; pit stash list shows them, the newest as 0", sub, strings.Join(args, " "))
	}
	return n, nil
}

// stashIndex returns the position of the stash commit hash in the repo's stash list, -1 if it isn't there.
func stashIndex(r *workspace.Repo, hash string) (int, error) {
	out, err := git.Output(r.Dir, "stash", "list", "--format=%H")
	if err != nil || out == "" {
		return -1, err
	}
	return slices.Index(strings.Split(out, "\n"), hash), nil
}

// stashList shows pit's stashes, newest first, numbered for pop, apply and drop, each with the message of its stashes and its repos.
func stashList(w *workspace.Workspace, args []string) error {
	p, err := parseArgs("stash", args, spec{noPaths: true})
	if err != nil {
		return err
	}
	if p.narrowed() {
		return errors.New("stash list: lists all pit stashes; -r and -g don't apply")
	}
	var rec stashRecord
	if _, err := w.ReadState(stashFile, &rec); err != nil {
		return err
	}
	for n := range len(rec.Stashes) {
		st := rec.Stashes[len(rec.Stashes)-1-n]
		subject := ""
		names := make([]string, len(st.Repos))
		for i, ref := range st.Repos {
			names[i] = workspace.Label(ref.Repo)
			r, err := byName(w, ref.Repo)
			if err != nil {
				names[i] += " (gone)"
				continue
			}
			if at, _ := stashIndex(r, ref.Hash); at < 0 {
				names[i] += " (gone)"
			} else if subject == "" {
				subject, _ = git.Output(r.Dir, "log", "-1", "--format=%s", ref.Hash)
			}
		}
		fmt.Printf("%d  %s  %s\n", n, subject, ui.Yellow("["+strings.Join(names, ", ")+"]"))
	}
	return nil
}
