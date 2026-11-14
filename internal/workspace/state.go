package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/patrickbergner/pit/internal/config"
)

// StatePath returns the path of a file in pit's local state directory.
func (w *Workspace) StatePath(name string) string {
	return filepath.Join(w.Root, config.StateDir, name)
}

// ReadState decodes a JSON state file into v; found is false if it doesn't exist.
func (w *Workspace) ReadState(name string, v any) (found bool, err error) {
	data, err := os.ReadFile(w.StatePath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("%s is damaged (%v); delete it", w.StatePath(name), err)
	}
	return true, nil
}

// WriteState stores v as a JSON state file, replacing it atomically.
func (w *Workspace) WriteState(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.WriteStateFile(name, append(data, '\n'))
}

// WriteStateFile stores a state file, replacing it atomically.
func (w *Workspace) WriteStateFile(name string, data []byte) error {
	path := w.StatePath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveState deletes a state file; a missing one is fine.
func (w *Workspace) RemoveState(name string) error {
	err := os.Remove(w.StatePath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

const lockFile = "lock"

// Lock takes pit's lock for commands that change something, so two pit runs don't interleave. The returned function releases it.
func (w *Workspace) Lock() (func(), error) {
	path := w.StatePath(lockFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("another pit command is running in %s (if none is, delete %s)", w.Root, path)
	} else if err != nil {
		return nil, err
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()
	return func() { os.Remove(path) }, nil
}
