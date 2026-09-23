package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(write(t, `{"externals": {
		"b": {"url": "https://example.com/b", "repoPath": "b/external", "branch": "dev"},
		"a": {"url": "https://example.com/a", "repoPath": "a"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.DefaultBranch != "main" || c.Settings.RemotePrefix != "pit-" {
		t.Errorf("defaults = %+v", c.Settings)
	}
	if got := c.Names(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Names() = %v", got)
	}
	if c.Externals["b"].Branch != "dev" || c.Externals["b"].RepoPath != "b/external" {
		t.Errorf("b = %+v", c.Externals["b"])
	}
}

func TestLoadSettings(t *testing.T) {
	c, err := Load(write(t, `{"settings": {"defaultBranch": "trunk", "remotePrefix": "extRepo-"}, "externals": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.DefaultBranch != "trunk" || c.Settings.RemotePrefix != "extRepo-" {
		t.Errorf("settings = %+v", c.Settings)
	}
}

func TestLoadErrors(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	for name, content := range map[string]string{
		"syntax":        `{ nope`,
		"unknown key":   `{"externals": {"a": {"url": "u", "repo_path": "a"}}}`,
		"unknown block": `{"setting": {}}`,
	} {
		if _, err := Load(write(t, content)); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
