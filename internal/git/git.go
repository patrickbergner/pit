// Package git runs the git command line. Failures come back as *Error, which carries what git printed on stderr.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

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

// Raw runs git with stdin (may be nil) and returns its stdout.
func Raw(stdin io.Reader, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &Error{args, errb.String(), err}
	}
	return out.Bytes(), nil
}

// Output runs git and returns its stdout without trailing newlines.
func Output(args ...string) (string, error) {
	out, err := Raw(nil, args...)
	return strings.TrimRight(string(out), "\n"), err
}

// Input is Output with the given stdin.
func Input(stdin string, args ...string) (string, error) {
	out, err := Raw(strings.NewReader(stdin), args...)
	return strings.TrimRight(string(out), "\n"), err
}

// OK reports whether git exits with status 0.
func OK(args ...string) bool {
	_, err := Raw(nil, args...)
	return err == nil
}

// Code returns git's exit status; the error is for git not running at all.
func Code(args ...string) (int, error) {
	_, err := Raw(nil, args...)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	default:
		return -1, err
	}
}

// Pass runs git with its output going straight to the user; env is added to ours.
func Pass(env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// RevParse resolves rev to an object name.
func RevParse(rev string) (string, error) { return Output("rev-parse", "--verify", "-q", rev) }

// IsAncestor reports whether commit a is an ancestor of (or equal to) commit b.
func IsAncestor(a, b string) bool { return OK("merge-base", "--is-ancestor", a, b) }

// RevCount counts the commits in a revision range.
func RevCount(rng string) (int, error) {
	out, err := Output("rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}
