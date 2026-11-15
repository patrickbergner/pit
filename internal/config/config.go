// Package config loads pit-externals.json: the externals, i.e. git repositories that are cloned into subdirectories of the directory holding
// the file.
//
// Unknown keys are rejected, so a typo fails loudly instead of being silently ignored. The externals keep the order they have in the file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// FileName is the config file; the directory holding it is the pit root.
const FileName = "pit-externals.json"

// StateDir is pit's local, gitignored state directory next to the config file.
const StateDir = ".pit"

type Config struct {
	Settings  Settings
	Externals []External // in file order
}

type Settings struct {
	DefaultBranch     string          `json:"defaultBranch"`     // default "main"
	ConfirmPush       *bool           `json:"confirmPush"`       // default true; read with AskBeforePush
	ConfirmPushGroups map[string]bool `json:"confirmPushGroups"` // overrides ConfirmPush for the externals in these groups
}

// AskBeforePush reports whether pit push asks before pushing a repo in the given groups (none for the root repo): as confirmPushGroups says
// for its groups, asking if they disagree, otherwise as confirmPush says (default true).
func (s Settings) AskBeforePush(groups []string) bool {
	overridden := false
	for _, g := range groups {
		if ask, ok := s.ConfirmPushGroups[g]; ok {
			if ask {
				return true
			}
			overridden = true
		}
	}
	return !overridden && (s.ConfirmPush == nil || *s.ConfirmPush)
}

type External struct {
	Name   string   `json:"-"`
	URL    string   `json:"url"`
	Path   string   `json:"path"`   // relative to the root, forward slashes
	Branch string   `json:"branch"` // default Settings.DefaultBranch
	Groups []string `json:"groups"` // for selecting externals with -g
}

var validName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse decodes and validates a config.
func Parse(data []byte) (*Config, error) {
	var raw struct {
		Settings  Settings        `json:"settings"`
		Externals json.RawMessage `json:"externals"`
	}
	if err := decodeStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	c := &Config{Settings: raw.Settings}
	if c.Settings.DefaultBranch == "" {
		c.Settings.DefaultBranch = "main"
	}
	if len(raw.Externals) > 0 && string(raw.Externals) != "null" {
		exts, err := decodeExternals(raw.Externals)
		if err != nil {
			return nil, err
		}
		c.Externals = exts
	}
	for i := range c.Externals {
		if c.Externals[i].Branch == "" {
			c.Externals[i].Branch = c.Settings.DefaultBranch
		}
	}
	return c, c.validate()
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected content after the top-level object")
	}
	return nil
}

// decodeExternals reads the externals object member by member to keep the file's order.
func decodeExternals(data []byte) ([]External, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("invalid JSON: \"externals\" must be an object")
	}
	var exts []External
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		name := tok.(string)
		var member json.RawMessage
		if err := dec.Decode(&member); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		var e External
		if err := decodeStrict(member, &e); err != nil {
			return nil, fmt.Errorf("external %q: invalid JSON: %v", name, err)
		}
		if seen[name] {
			return nil, fmt.Errorf("external %q is defined twice", name)
		}
		seen[name] = true
		e.Name = name
		exts = append(exts, e)
	}
	return exts, nil
}

func (c *Config) validate() error {
	byKey := map[string]string{} // lower-cased path -> name
	for _, e := range c.Externals {
		if !validName.MatchString(e.Name) {
			return fmt.Errorf("external %q: invalid name (letters, digits, '.', '_', '-'; not starting with '.' or '-')", e.Name)
		}
		if e.URL == "" || e.Path == "" {
			return fmt.Errorf("external %q: needs \"url\" and \"path\"", e.Name)
		}
		if err := checkPath(e.Path); err != nil {
			return fmt.Errorf("external %q: path %q %v", e.Name, e.Path, err)
		}
		for i, g := range e.Groups {
			if !validName.MatchString(g) {
				return fmt.Errorf("external %q: invalid group name %q (letters, digits, '.', '_', '-'; not starting with '.' or '-')", e.Name, g)
			}
			if slices.Contains(e.Groups[:i], g) {
				return fmt.Errorf("external %q: group %q is listed twice", e.Name, g)
			}
		}
		key := strings.ToLower(e.Path) // case-insensitive, so the config means the same on every file system
		if other, ok := byKey[key]; ok {
			return fmt.Errorf("externals %q and %q have the same path %q", other, e.Name, e.Path)
		}
		byKey[key] = e.Name
	}
	for _, a := range c.Externals {
		for _, b := range c.Externals {
			if a.Name != b.Name && strings.HasPrefix(strings.ToLower(b.Path), strings.ToLower(a.Path)+"/") {
				return fmt.Errorf("external %q (%s) lies inside external %q (%s)", b.Name, b.Path, a.Name, a.Path)
			}
		}
	}
	groups := c.Groups()
	for _, g := range slices.Sorted(maps.Keys(c.Settings.ConfirmPushGroups)) {
		if !slices.Contains(groups, g) {
			known := "no external has groups"
			if len(groups) > 0 {
				known = "groups: " + strings.Join(groups, ", ")
			}
			return fmt.Errorf("settings.confirmPushGroups: no external is in group %q (%s)", g, known)
		}
	}
	return nil
}

func checkPath(p string) error {
	switch {
	case strings.Contains(p, `\`):
		return errors.New("must use forward slashes")
	case strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':'):
		return errors.New("must be relative to the directory of " + FileName)
	case strings.HasSuffix(p, "/"):
		return errors.New("must not end with '/'")
	}
	for i, seg := range strings.Split(p, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return errors.New("must be a plain relative path without empty, '.' or '..' segments")
		case strings.EqualFold(seg, ".git"):
			return errors.New("must not contain a .git directory")
		case i == 0 && strings.EqualFold(seg, StateDir):
			return errors.New("must not lie in pit's " + StateDir + " directory")
		}
	}
	return nil
}

// Groups returns all group names, in the order they first appear.
func (c *Config) Groups() []string {
	var groups []string
	for _, e := range c.Externals {
		for _, g := range e.Groups {
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}
	}
	return groups
}

// Find returns the external with the given name.
func (c *Config) Find(name string) (*External, bool) {
	for i := range c.Externals {
		if c.Externals[i].Name == name {
			return &c.Externals[i], true
		}
	}
	return nil, false
}
