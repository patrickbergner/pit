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
	if err != nil || c.Settings.DefaultBranch != "main" || len(c.Externals) != 0 || !c.Settings.AskBeforePush() {
		t.Errorf("empty config: %+v, %v", c, err)
	}
	for json, want := range map[string]bool{`{"settings": {"confirmPush": false}}`: false, `{"settings": {"confirmPush": true}}`: true} {
		if c, err := Parse([]byte(json)); err != nil || c.Settings.AskBeforePush() != want {
			t.Errorf("%s: AskBeforePush = %v, %v", json, c.Settings.AskBeforePush(), err)
		}
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
