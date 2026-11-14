package main

import (
	"fmt"
	"strings"

	"github.com/patrickbergner/pit/internal/workspace"
)

// spec describes a command's options. Every command accepts -r/--repo and -g/--group.
type spec struct {
	bools       []string // e.g. "a|all"
	values      []string // e.g. "m|message"
	passthrough bool     // unknown options are handed to git instead of rejected
	noPaths     bool     // no positional arguments
	gitArgs     bool     // positional arguments are git's too (remotes, refspecs) and stay in order with the options
}

type parsed struct {
	bools  map[string]bool     // by long name (or short, if there is none)
	vals   map[string][]string // ditto
	repos  []string            // -r
	groups []string            // -g
	opts   []string            // options for git
	paths  []string
}

func (p *parsed) on(name string) bool { return p.bools[name] }

func (p *parsed) last(name string) string {
	if v := p.vals[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

// lookup maps every spelling of an option ("a", "all") to its canonical name.
func lookup(defs []string) map[string]string {
	m := map[string]string{}
	for _, d := range defs {
		names := strings.Split(d, "|")
		canon := names[len(names)-1]
		for _, n := range names {
			m[n] = canon
		}
	}
	return m
}

func parseArgs(cmd string, args []string, s spec) (*parsed, error) {
	bools := lookup(s.bools)
	values := lookup(append([]string{"r|repo", "g|group"}, s.values...))
	p := &parsed{bools: map[string]bool{}, vals: map[string][]string{}}
	setValue := func(name, v string) {
		switch name {
		case "repo":
			p.repos = append(p.repos, v)
		case "group":
			p.groups = append(p.groups, v)
		default:
			p.vals[name] = append(p.vals[name], v)
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func(opt string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s: option %s needs a value", cmd, opt)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--" && s.gitArgs:
			p.opts = append(p.opts, args[i:]...)
			i = len(args)
		case a == "--":
			p.paths = append(p.paths, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			if c, ok := bools[name]; ok && !hasVal {
				p.bools[c] = true
			} else if c, ok := values[name]; ok {
				if !hasVal {
					v, err := next(a)
					if err != nil {
						return nil, err
					}
					val = v
				}
				setValue(c, val)
			} else if s.passthrough {
				p.opts = append(p.opts, a)
			} else {
				return nil, fmt.Errorf("%s: unknown option %s", cmd, a)
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// short options, possibly bundled as in -am "message"
			if !s.passthrough || knownShort(a[1:], bools, values) {
				for j := 1; j < len(a); j++ {
					o := a[j : j+1]
					if c, ok := bools[o]; ok {
						p.bools[c] = true
					} else if c, ok := values[o]; ok {
						val := a[j+1:]
						if val == "" {
							v, err := next("-" + o)
							if err != nil {
								return nil, err
							}
							val = v
						}
						setValue(c, val)
						break
					} else {
						return nil, fmt.Errorf("%s: unknown option -%s", cmd, o)
					}
				}
			} else {
				p.opts = append(p.opts, a)
			}
		case s.gitArgs:
			p.opts = append(p.opts, a)
		default:
			p.paths = append(p.paths, a)
		}
	}
	if s.noPaths && len(p.paths) > 0 {
		return nil, fmt.Errorf("%s: unexpected argument %q", cmd, p.paths[0])
	}
	return p, nil
}

// knownShort reports whether a bundle of short options starts with one pit knows; for passthrough commands anything else goes to git.
func knownShort(bundle string, bools, values map[string]string) bool {
	o := bundle[:1]
	_, b := bools[o]
	_, v := values[o]
	return (b && len(bundle) == 1) || v
}

// narrowed reports whether -r or -g select repos.
func (p *parsed) narrowed() bool { return len(p.repos) > 0 || len(p.groups) > 0 }

// selected returns the repos selected with -r and -g (together: the repos matching either), or all cloned repos. A repo named with -r must
// be cloned; a group's members that aren't cloned yet are left out.
func (p *parsed) selected(w *workspace.Workspace) ([]*workspace.Repo, error) {
	if !p.narrowed() {
		return w.Cloned(), nil
	}
	if _, err := w.Named(p.repos); err != nil {
		return nil, err
	}
	matched, err := w.Match(p.repos, p.groups)
	if err != nil {
		return nil, err
	}
	var repos []*workspace.Repo
	for _, r := range matched {
		if r.Cloned() {
			repos = append(repos, r)
		}
	}
	return repos, nil
}

// targets returns the selected repos, narrowed to the ones owning the paths if any are given.
func (p *parsed) targets(w *workspace.Workspace) ([]workspace.Target, error) {
	repos, err := p.selected(w)
	if err != nil {
		return nil, err
	}
	if len(p.paths) == 0 {
		targets := make([]workspace.Target, len(repos))
		for i, r := range repos {
			targets[i] = workspace.Target{Repo: r}
		}
		return targets, nil
	}
	routed, err := w.Route(p.paths)
	if err != nil {
		return nil, err
	}
	if !p.narrowed() {
		return routed, nil
	}
	keep := map[*workspace.Repo]bool{}
	for _, r := range repos {
		keep[r] = true
	}
	var targets []workspace.Target
	for _, t := range routed {
		if keep[t.Repo] {
			targets = append(targets, t)
		}
	}
	return targets, nil
}

// pathspecArgs appends "--" and the pathspecs, if any.
func pathspecArgs(args []string, t workspace.Target) []string {
	if len(t.Pathspecs) == 0 {
		return args
	}
	return append(append(args, "--"), t.Pathspecs...)
}
