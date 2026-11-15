// Package git runs the git command line in a given repository directory. Failures come back as *Error, which carries what git printed on
// stderr.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Path returns the git executable pit runs, resolved once.
var Path = sync.OnceValues(resolve)

// resolve finds git on PATH. On Windows that may be a git.bat or git.cmd shim, which Go runs through cmd.exe, and cmd.exe mangles arguments:
// '^' is its escape character ("HEAD^{tree}" arrives as "HEAD{tree}") and '&' starts another command. A shim is therefore replaced by the
// git.exe in its exec path, so pit runs the same git the shim would.
func resolve() (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("git not found on PATH")
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".bat" && ext != ".cmd" {
		return path, nil
	}
	if out, err := exec.Command(path, "--exec-path").Output(); err == nil {
		exe := filepath.Join(strings.TrimSpace(string(out)), "git.exe")
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
	}
	return "", fmt.Errorf("git on PATH is the batch file %s, which would mangle pit's arguments, and no git.exe was found in its exec path; "+
		"put a git.exe first on PATH", path)
}

// Error reports a failed git command together with its stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// colorArgs make git color what pit shows of its output like pit colors its own, if that was decided with --color, --no-color or NO_COLOR.
var colorArgs []string

// SetColor makes git color its output that pit shows always or never, by mode "always" or "never"; with any other mode, git decides
// itself.
func SetColor(mode string) {
	switch mode {
	case "always", "never":
		colorArgs = []string{"-c", "color.ui=" + mode}
	default:
		colorArgs = nil
	}
}

// command builds git args run in dir ("" for the current directory) with env added to ours. shown is for commands whose output pit shows,
// which follow pit's coloring.
func command(dir string, env []string, args []string, shown bool) (*exec.Cmd, error) {
	bin, err := Path()
	if err != nil {
		return nil, err
	}
	if shown {
		args = append(append([]string(nil), colorArgs...), args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), env...)
	if dir != "" {
		// The repo is the one in dir, never one further up: a directory that isn't a repo after all must fail, not be taken for the repo
		// around it.
		if abs, err := filepath.Abs(dir); err == nil {
			cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(abs))
		}
	}
	return cmd, nil
}

// repoEnv are the variables that point git at a particular repository, whatever directory it runs in. Git sets them for hooks and
// aliases; inherited from there, they would make pit work on that one repository in every repo's name.
var repoEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_PREFIX", "GIT_GRAFT_FILE", "GIT_SHALLOW_FILE", "GIT_NAMESPACE",
	"GIT_CEILING_DIRECTORIES"}

// environ is pit's environment without repoEnv.
var environ = sync.OnceValue(func() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(repoEnv, strings.ToUpper(name)) {
			env = append(env, kv)
		}
	}
	return env
})

// baseEnv returns a copy of environ to add to.
func baseEnv() []string { return slices.Clone(environ()) }

// Raw runs git in dir with stdin (may be nil) and returns its stdout.
func Raw(dir string, stdin io.Reader, args ...string) ([]byte, error) {
	return raw(dir, nil, stdin, args)
}

// Remote is Output for commands that reach a remote, possibly in parallel: interactive credential prompts are disabled, as for Combined.
func Remote(dir string, args ...string) (string, error) {
	out, err := raw(dir, []string{"GIT_TERMINAL_PROMPT=0"}, nil, args)
	return strings.TrimRight(string(out), "\n"), err
}

func raw(dir string, env []string, stdin io.Reader, args []string) ([]byte, error) {
	cmd, err := command(dir, env, args, false)
	if err != nil {
		return nil, &Error{args, "", err}
	}
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &Error{args, errb.String(), err}
	}
	return out.Bytes(), nil
}

// Output runs git in dir and returns its stdout without trailing newlines.
func Output(dir string, args ...string) (string, error) {
	out, err := Raw(dir, nil, args...)
	return strings.TrimRight(string(out), "\n"), err
}

// Input is Output with the given stdin.
func Input(dir, stdin string, args ...string) (string, error) {
	out, err := Raw(dir, strings.NewReader(stdin), args...)
	return strings.TrimRight(string(out), "\n"), err
}

// OK reports whether git exits with status 0.
func OK(dir string, args ...string) bool {
	_, err := Raw(dir, nil, args...)
	return err == nil
}

// Combined runs git in dir without a terminal: no stdin, interactive credential prompts disabled, stdout and stderr captured together. It
// is for commands that run in parallel, whose output is printed afterwards.
func Combined(dir string, args ...string) ([]byte, error) {
	cmd, err := command(dir, []string{"GIT_TERMINAL_PROMPT=0"}, args, true)
	if err != nil {
		return nil, &Error{args, "", err}
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &Error{args, "", err}
	}
	return out.Bytes(), nil
}

// Pass runs git in dir attached to the terminal: stdin, stdout and stderr are pit's own.
func Pass(dir string, args ...string) error {
	cmd, err := command(dir, nil, args, true)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return &Error{args, "", err}
	}
	return nil
}
