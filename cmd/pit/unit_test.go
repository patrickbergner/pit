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
	// a message used again in b shortly afterwards doesn't take the place of b's commit made together with a's
	again := []logCommit{commit(b, 220, "W"), commit(a, 100, "W"), commit(b, 100, "W")}
	got = nil
	for _, e := range groupLog(again, 10, []*workspace.Repo{a, b, c}) {
		got = append(got, e.repoList())
	}
	if want := []string{"b bW", "a aW, b bW"}; !slices.Equal(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
	// within an entry, repos follow the workspace order, not the merge order
	if got := groupLog(merged, 1, []*workspace.Repo{c, b, a})[0].repoList(); got != "c cX, a aX" {
		t.Errorf("entry in workspace order c, b, a = %q", got)
	}
}

func TestColorOptions(t *testing.T) {
	rest, color, err := colorOptions([]string{"--no-color", "status", "--color=always", "-r", "a", "--", "--no-color"})
	if err != nil || color != "always" || !slices.Equal(rest, []string{"status", "-r", "a", "--", "--no-color"}) {
		t.Errorf("colorOptions = %q, %q, %v", rest, color, err)
	}
	if _, color, _ := colorOptions([]string{"diff", "--color"}); color != "always" {
		t.Errorf("--color = %q", color)
	}
	if _, color, _ := colorOptions([]string{"diff"}); color != "" {
		t.Errorf("no option = %q", color)
	}
	if _, _, err := colorOptions([]string{"--color=sometimes"}); err == nil {
		t.Error("--color=sometimes accepted")
	}
}

func TestPushArgs(t *testing.T) {
	opts := []string{"--force-with-lease"}
	for _, tc := range []struct {
		step pushStep
		want []string
	}{
		{pushStep{branch: "main", dest: "origin/main"}, []string{"push", "--force-with-lease"}},
		{pushStep{branch: "x", dest: "origin/x", setUp: true}, []string{"push", "-u", "origin", "x", "--force-with-lease"}},
		// the branch to its upstream branch by name, not as push.default would have it
		{pushStep{branch: "main", dest: "up/master", remote: "up", mergeRef: "refs/heads/master"},
			[]string{"push", "up", "refs/heads/main:refs/heads/master", "--force-with-lease"}},
		// with tags: one atomic push, the branch to its upstream branch by name
		{pushStep{branch: "main", dest: "up/master", remote: "up", mergeRef: "refs/heads/master", tags: []string{"v1"}},
			[]string{"push", "--atomic", "up", "refs/heads/main:refs/heads/master", "refs/tags/v1", "--force-with-lease"}},
		{pushStep{branch: "x", dest: "origin/x", setUp: true, remote: "origin", tags: []string{"v1"}},
			[]string{"push", "--atomic", "-u", "origin", "x", "refs/tags/v1", "--force-with-lease"}},
		// only tags
		{pushStep{branch: "main", remote: "origin", tags: []string{"v1", "v2"}}, []string{"push", "origin", "refs/tags/v1", "refs/tags/v2", "--force-with-lease"}},
	} {
		if got := tc.step.args(opts); !slices.Equal(got, tc.want) {
			t.Errorf("args(%+v) = %q, want %q", tc.step, got, tc.want)
		}
	}
	st := pushStep{branch: "main", dest: "origin/main", commits: []string{"a x"}, remote: "origin", tags: []string{"v1"}}
	if got := st.what(); got != "main -> origin/main, 1 commit, tag v1" {
		t.Errorf("what = %q", got)
	}
	st = pushStep{branch: "main", remote: "origin", tags: []string{"v1", "v2"}}
	if got := st.what(); got != "tags v1, v2 -> origin" {
		t.Errorf("what = %q", got)
	}
}

func TestParentsFirst(t *testing.T) {
	names := func(rs []*workspace.Repo) []string {
		var n []string
		for _, r := range rs {
			n = append(n, r.Name)
		}
		return n
	}
	a := &workspace.Repo{Name: "a"}
	b := &workspace.Repo{Name: "b", Parent: a}
	c := &workspace.Repo{Name: "c", Parent: b}
	x := &workspace.Repo{Name: "x"}
	z := &workspace.Repo{Name: "z"}
	for _, tc := range []struct {
		in   []*workspace.Repo
		want []string
	}{
		{[]*workspace.Repo{a, b, z}, []string{"a", "b", "z"}},         // config order where it works
		{[]*workspace.Repo{a, z, b}, []string{"a", "z", "b"}},         // nested externals aren't moved up
		{[]*workspace.Repo{b, x, a, z}, []string{"x", "a", "b", "z"}}, // b right after a
		{[]*workspace.Repo{c, b, x, a}, []string{"x", "a", "b", "c"}}, // deeper nesting, in turn
		{[]*workspace.Repo{c, x, a}, []string{"x", "a", "c"}},         // b not selected: c still after a
		{[]*workspace.Repo{c, x}, []string{"c", "x"}},                 // parents not selected: no constraint
	} {
		if got := names(parentsFirst(tc.in)); !slices.Equal(got, tc.want) {
			t.Errorf("parentsFirst(%v) = %v, want %v", names(tc.in), got, tc.want)
		}
	}
}
