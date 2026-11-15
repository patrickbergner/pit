package config

import (
	"strings"
	"testing"
)

func TestParseKeepsOrderAndDefaults(t *testing.T) {
	c, err := Parse([]byte(`{
		"settings": {"defaultBranch": "trunk"},
		"externals": {
			"zeta":  {"url": "u1", "path": "z"},
			"alpha": {"url": "u2", "path": "a/b", "branch": "dev"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Externals) != 2 || c.Externals[0].Name != "zeta" || c.Externals[1].Name != "alpha" {
		t.Fatalf("externals = %+v", c.Externals)
	}
	if c.Externals[0].Branch != "trunk" || c.Externals[1].Branch != "dev" {
		t.Errorf("branches = %s, %s", c.Externals[0].Branch, c.Externals[1].Branch)
	}
	if e, ok := c.Find("alpha"); !ok || e.Path != "a/b" {
		t.Errorf("Find(alpha) = %+v, %v", e, ok)
	}

	c, err = Parse([]byte(`{}`))
	if err != nil || c.Settings.DefaultBranch != "main" || len(c.Externals) != 0 || !c.Settings.AskBeforePush(nil) {
		t.Errorf("empty config: %+v, %v", c, err)
	}
	for json, want := range map[string]bool{`{"settings": {"confirmPush": false}}`: false, `{"settings": {"confirmPush": true}}`: true} {
		if c, err := Parse([]byte(json)); err != nil || c.Settings.AskBeforePush(nil) != want {
			t.Errorf("%s: AskBeforePush = %v, %v", json, c.Settings.AskBeforePush(nil), err)
		}
	}
}

func TestConfirmPushGroups(t *testing.T) {
	const exts = `"externals": {
		"x": {"url": "u", "path": "x", "groups": ["private"]},
		"y": {"url": "u", "path": "y", "groups": ["public", "work"]}
	}`
	root := []string{RootKey}
	for _, tc := range []struct {
		settings string
		groups   []string
		want     bool
	}{
		{`{"confirmPushGroups": {"private": false}}`, []string{"private"}, false},
		{`{"confirmPushGroups": {"private": false}}`, []string{"public", "work"}, true}, // not overridden: confirmPush, default true
		{`{"confirmPushGroups": {"private": false}}`, root, true},                       // the root repo is in no group
		{`{"confirmPush": false, "confirmPushGroups": {"work": true}}`, []string{"public", "work"}, true},
		{`{"confirmPush": false, "confirmPushGroups": {"work": true}}`, root, false},
		{`{"confirmPushGroups": {"public": false, "work": false}}`, []string{"public", "work"}, false},
		{`{"confirmPushGroups": {"public": false, "work": true}}`, []string{"public", "work"}, true}, // they disagree: asking wins
		// the root repo by its own key
		{`{"confirmPushGroups": {"private": false, ".": false}}`, root, false},
		{`{"confirmPushGroups": {".": false}}`, []string{"private"}, true},
		{`{"confirmPush": false, "confirmPushGroups": {".": true}}`, root, true},
	} {
		c, err := Parse([]byte(`{"settings": ` + tc.settings + `, ` + exts + `}`))
		if err != nil {
			t.Fatalf("%s: %v", tc.settings, err)
		}
		if got := c.Settings.AskBeforePush(tc.groups); got != tc.want {
			t.Errorf("%s: AskBeforePush(%q) = %v, want %v", tc.settings, tc.groups, got, tc.want)
		}
	}

	for _, tc := range []struct{ json, want string }{
		{`{"settings": {"confirmPushGroups": {"privat": false}}, ` + exts + `}`,
			`no external is in group "privat" (groups: private, public, work; "." for the root repo)`},
		{`{"settings": {"confirmPushGroups": {"private": false}}}`, `no external is in group "private" (no external has groups; "." for`},
		{`{"settings": {"confirmPushGroups": ["private"]}, ` + exts + `}`, "invalid JSON"},
	} {
		if _, err := Parse([]byte(tc.json)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %v, want an error containing %q", tc.json, err, tc.want)
		}
	}
	// the root repo's key needs no groups
	if _, err := Parse([]byte(`{"settings": {"confirmPushGroups": {".": false}}}`)); err != nil {
		t.Error(err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`{"externals": {"x": {"url": "u", "path": "x", "repoPath": "x"}}}`, "unknown field"},
		{`{"setting": {}}`, "unknown field"},
		{`{"externals": []}`, "must be an object"},
		{`{} {}`, "unexpected content"},
		{`{"externals": {"x": {"url": "u"}}}`, `needs "url" and "path"`},
		{`{"externals": {"x": {"url": "u", "path": "x"}, "x": {"url": "u", "path": "y"}}}`, "defined twice"},
		{`{"externals": {".": {"url": "u", "path": "x"}}}`, "invalid name"},
		{`{"externals": {"-x": {"url": "u", "path": "x"}}}`, "invalid name"},
		{`{"externals": {"x": {"url": "u", "path": "/x"}}}`, "must be relative"},
		{`{"externals": {"x": {"url": "u", "path": "C:/x"}}}`, "must be relative"},
		{`{"externals": {"x": {"url": "u", "path": "a\\x"}}}`, "forward slashes"},
		{`{"externals": {"x": {"url": "u", "path": "a/../x"}}}`, "'..'"},
		{`{"externals": {"x": {"url": "u", "path": "a//x"}}}`, "empty"},
		{`{"externals": {"x": {"url": "u", "path": "x/"}}}`, "end with"},
		{`{"externals": {"x": {"url": "u", "path": "a/.git/x"}}}`, ".git"},
		{`{"externals": {"x": {"url": "u", "path": ".pit/x"}}}`, ".pit"},
		{`{"externals": {"x": {"url": "u", "path": "Lib"}, "y": {"url": "u", "path": "lib"}}}`, "same path"},
		{`{"externals": {"x": {"url": "u", "path": "lib"}, "y": {"url": "u", "path": "lib/sub"}}}`, "lies inside"},
	} {
		_, err := Parse([]byte(tc.json))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %v, want an error containing %q", tc.json, err, tc.want)
		}
	}
}

func TestGroups(t *testing.T) {
	c, err := Parse([]byte(`{"externals": {
		"x": {"url": "u", "path": "x", "groups": ["work", "go"]},
		"y": {"url": "u", "path": "y"},
		"z": {"url": "u", "path": "z", "groups": ["go", "tools"]}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Groups(); strings.Join(got, ",") != "work,go,tools" {
		t.Errorf("Groups = %v", got)
	}
	for _, tc := range []struct{ json, want string }{
		{`{"externals": {"x": {"url": "u", "path": "x", "groups": ["a b"]}}}`, "invalid group name"},
		{`{"externals": {"x": {"url": "u", "path": "x", "groups": [""]}}}`, "invalid group name"},
		{`{"externals": {"x": {"url": "u", "path": "x", "groups": ["a", "a"]}}}`, "listed twice"},
		{`{"externals": {"x": {"url": "u", "path": "x", "groups": "a"}}}`, "invalid JSON"},
	} {
		if _, err := Parse([]byte(tc.json)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %v, want an error containing %q", tc.json, err, tc.want)
		}
	}
}

func TestParseAllowsSiblingPrefixes(t *testing.T) {
	// "lib" and "library" share a string prefix but neither lies inside the other
	if _, err := Parse([]byte(`{"externals": {"x": {"url": "u", "path": "lib"}, "y": {"url": "u", "path": "library"}}}`)); err != nil {
		t.Error(err)
	}
}
