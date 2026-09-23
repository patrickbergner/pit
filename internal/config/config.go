// Package config loads externals.json from the monorepo's root: the externals, i.e. subdirectories of the monorepo that are also published
// as standalone external repos.
//
// Unknown keys are rejected, so a typo fails loudly instead of being silently ignored.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// FileName is the config file at the monorepo's root.
const FileName = "externals.json"

type Config struct {
	Settings  Settings            `json:"settings"`
	Externals map[string]External `json:"externals"`
}

type Settings struct {
	DefaultBranch string `json:"defaultBranch"` // default "main"
	RemotePrefix  string `json:"remotePrefix"`  // default "pit-"
}

type External struct {
	URL      string `json:"url"`
	RepoPath string `json:"repoPath"` // directory in the monorepo
	Branch   string `json:"branch"`   // default Settings.DefaultBranch
}

// Load reads the config at path; a missing file yields an error wrapping fs.ErrNotExist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("config not found: %s: %w", path, err)
	} else if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %v", path, err)
	}
	if c.Settings.DefaultBranch == "" {
		c.Settings.DefaultBranch = "main"
	}
	if c.Settings.RemotePrefix == "" {
		c.Settings.RemotePrefix = "pit-"
	}
	return &c, nil
}

// Names returns the externals' names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Externals))
	for n := range c.Externals {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
