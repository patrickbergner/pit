package main

import (
	"slices"
	"testing"
	"time"

	"github.com/patrickbergner/pit/internal/workspace"
)

func TestParseArgs(t *testing.T) {
	commit := spec{bools: []string{"a|all", "amend"}, values: []string{"m|message"}, noPaths: true}
	p, err := parseArgs("commit", []string{"-am", "msg", "--message=two", "-r", "x", "--repo=y", "--amend", "-g", "g1", "--group=g2"}, commit)
	if err != nil {
		t.Fatal(err)
	}
	if !p.on("all") || !p.on("amend") || !slices.Equal(p.vals["message"], []string{"msg", "two"}) || !slices.Equal(p.repos, []string{"x", "y"}) ||
		!slices.Equal(p.groups, []string{"g1", "g2"}) {
		t.Errorf("parsed = %+v", p)
	}
	p, err = parseArgs("commit", []string{"-mvalue"}, commit)
	if err != nil || p.last("message") != "value" {
		t.Errorf("-mvalue: %+v, %v", p, err)
	}
	for _, bad := range [][]string{{"--nope"}, {"-x"}, {"-m"}, {"path"}} {
		if _, err := parseArgs("commit", bad, commit); err == nil {
			t.Errorf("parseArgs(%q) succeeded", bad)
		}
	}

	// passthrough: unknown options go to git, paths stay paths, "--" ends options
	diff := spec{passthrough: true}
	p, err = parseArgs("diff", []string{"--staged", "-U5", "-r", "a", "x.go", "--", "-weird"}, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.opts, []string{"--staged", "-U5"}) || !slices.Equal(p.paths, []string{"x.go", "-weird"}) || !slices.Equal(p.repos, []string{"a"}) {
		t.Errorf("diff parsed = %+v", p)
	}

	// gitArgs: positionals stay in order with the options
	pull := spec{passthrough: true, gitArgs: true}
	p, err = parseArgs("pull", []string{"-X", "theirs", "--rebase", "origin"}, pull)
	if err != nil || !slices.Equal(p.opts, []string{"-X", "theirs", "--rebase", "origin"}) {
		t.Errorf("pull parsed = %+v, %v", p, err)
	}
}

func TestNormalizeURL(t *testing.T) {
	same := [][]string{
		{"https://github.com/you/repo.git", "https://github.com/you/repo", "ssh://git@github.com/you/repo.git", "git@github.com:you/repo",
			"HTTPS://GitHub.com/you/repo/"},
		{"file:///C:/x/repo.git", "file:///c:/x/repo"},
	}
	for _, group := range same {
		for _, u := range group {
			if normalizeURL(u) != normalizeURL(group[0]) {
				t.Errorf("normalizeURL(%s) = %s, want %s", u, normalizeURL(u), normalizeURL(group[0]))
			}
		}
	}
	if normalizeURL("https://github.com/you/repo") == normalizeURL("https://github.com/you/other") {
		t.Error("different repos compare equal")
	}
}

func TestGroupLog(t *testing.T) {
	a, b, c := &workspace.Repo{Name: "a"}, &workspace.Repo{Name: "b"}, &workspace.Repo{Name: "c"}
	t0 := time.Unix(1_000_000, 0)
	commit := func(r *workspace.Repo, secs int, msg string) logCommit {
		return logCommit{repo: r, short: r.Name + msg, committed: t0.Add(time.Duration(secs) * time.Second), email: "me", message: msg}
	}
	merged := []logCommit{
		commit(a, 100, "X"),
		commit(b, 100, "Y"), // same second, but another message
		commit(c, 99, "X"),  // joins a's X despite b's commit in between
		commit(a, 90, "X"),  // same repo: a new entry
		commit(b, -1000, "X"),
	}
	entries := groupLog(merged, 10, []*workspace.Repo{a, b, c})
	var got []string
	for _, e := range entries {
		got = append(got, e.repoList())
	}
	// b's old X is too far from both earlier X entries to join them
	want := []string{"a aX, c cX", "b bY", "a aX", "b bX"}
	if !slices.Equal(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
	if n := len(groupLog(merged, 2, []*workspace.Repo{a, b, c})); n != 2 {
		t.Errorf("count 2 gave %d entries", n)
	}
	// within an entry, repos follow the workspace order, not the merge order
	if got := groupLog(merged, 1, []*workspace.Repo{c, b, a})[0].repoList(); got != "c cX, a aX" {
		t.Errorf("entry in workspace order c, b, a = %q", got)
	}
}
