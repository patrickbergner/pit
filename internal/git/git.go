// Package git runs the git command line in a given repository directory. Failures come back as *Error, which carries what git printed on
// stderr.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// pagerEnv tells git that what it prints goes to a pager, as git's own pager does, so that it colors its output as for a terminal.
var pagerEnv []string

// SetPagerInUse tells git that the output pit shows goes through a pager.
func SetPagerInUse() { pagerEnv = []string{"GIT_PAGER_IN_USE=true"} }

// noPrompts keeps git, and the ssh it connects through, from asking for anything in the runs whose output pit captures, where a prompt
// could neither be seen nor answered and git would wait forever. GIT_TERMINAL_PROMPT covers git's own prompts, not credential helpers; Git
// Credential Manager, the helper of Git for Windows, has a switch of its own, without which it opens a prompt or window nobody sees. ssh
// heeds neither: it asks for a key's passphrase, or whether to trust a new host, on the terminal, which it opens itself whatever git's
// stdin is. Several of those at once, one per repo fetched in parallel, take turns at the one keyboard and turn its echo off, and none
// gets a whole answer. SSH_ASKPASS_REQUIRE=force (OpenSSH 8.4 and later) makes ssh ask its SSH_ASKPASS program instead, and that is pit
// itself, which refuses (see Askpass). Either way git fails at once instead.
var noPrompts = sync.OnceValue(func() []string {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}
	if exe, err := os.Executable(); err == nil {
		// Forward slashes, which the MSYS2 ssh of Git for Windows runs as well as a native ssh does
		env = append(env, "SSH_ASKPASS="+filepath.ToSlash(exe), "SSH_ASKPASS_REQUIRE=force", askpassEnv+"=refuse")
	}
	return env
})

// askpassEnv marks the git runs whose SSH_ASKPASS is pit (see noPrompts).
const askpassEnv = "PIT_ASKPASS"

// refusal starts the message pit prints as askpass program, which NeedsCredentials looks for.
const refusal = "pit: can't ask "

// Askpass handles a run of pit as the askpass program of a git run where nothing can be asked (see noPrompts), and reports whether this
// is one: then it has said so on stderr, which ends up in git's message, and pit exits with 1, which ssh and git take for no answer.
// git itself also asks SSH_ASKPASS, if neither GIT_ASKPASS nor core.askPass is set. Either one passes the question as the only argument;
// that, and not only the variable, tells such a run from a pit started by something git ran, which inherits the variable.
func Askpass(args []string) bool {
	if os.Getenv(askpassEnv) != "refuse" || len(args) != 1 || !strings.Contains(args[0], " ") {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\"%s\" while git runs in the background\n", refusal, strings.TrimRight(args[0], ": "))
	return true
}

// Every git process pit runs belongs to abortCtx, which Abort cancels.
var abortCtx, abortRuns = context.WithCancel(context.Background())

// running counts the git processes running.
var running atomic.Int32

// Abort ends the git processes running, and any started from now on, together with the processes they started, such as a credential
// helper waiting for an answer. It is for a second Ctrl-C, when the first one didn't end them.
func Abort() { abortRuns() }

// Running reports whether a git process is running.
func Running() bool { return running.Load() > 0 }

// command builds git args run in dir ("" for the current directory) with env added to ours. shown is for commands whose output pit shows,
// which follow pit's coloring.
func command(dir string, env []string, args []string, shown bool) (*exec.Cmd, error) {
	bin, err := Path()
	if err != nil {
		return nil, err
	}
	if shown {
		args = append(append([]string(nil), colorArgs...), args...)
		env = append(slices.Clone(env), pagerEnv...)
	}
	cmd := exec.CommandContext(abortCtx, bin, args...)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	// Capturing git's output doesn't wait for a process git started that keeps the output open after git has ended, as a daemon may.
	cmd.WaitDelay = time.Second
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

// run runs cmd and waits for it.
func run(cmd *exec.Cmd) error {
	running.Add(1)
	defer running.Add(-1)
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil // git succeeded; something it started still holds its output
	}
	return err
}

// Raw runs git in dir with stdin (may be nil) and returns its stdout. git can't ask for anything (see noPrompts); that goes for all the
// runners that capture stderr: Raw, Output, Input, OK and Combined.
func Raw(dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd, err := command(dir, noPrompts(), args, false)
	if err != nil {
		return nil, &Error{args, "", err}
	}
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	if err := run(cmd); err != nil {
		return out.Bytes(), &Error{args, errb.String(), err}
	}
	return out.Bytes(), nil
}

// Prompting is Output for commands that may have to ask, such as for credentials: stdin and stderr are pit's own, so that git's prompts
// show and can be answered, and only stdout is captured.
func Prompting(dir string, args ...string) (string, error) {
	cmd, err := command(dir, nil, args, false)
	if err != nil {
		return "", &Error{args, "", err}
	}
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, os.Stderr
	if err := run(cmd); err != nil {
		return "", &Error{args, "", err}
	}
	return strings.TrimRight(out.String(), "\n"), nil
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

// Combined runs git in dir without a terminal: no stdin, no prompts (see noPrompts), stdout and stderr captured together. It is for
// commands that run in parallel, whose output is printed afterwards.
func Combined(dir string, args ...string) ([]byte, error) {
	return combined(dir, false, args)
}

// Asking is Combined for a repo that gets a turn of its own after git needed credentials there in a parallel run: now git, its credential
// helper and ssh may ask for them, on the terminal, which they open themselves, while pit still captures what git prints.
func Asking(dir string, args ...string) ([]byte, error) {
	return combined(dir, true, args)
}

func combined(dir string, asking bool, args []string) ([]byte, error) {
	env := noPrompts()
	if asking {
		env = nil
	}
	cmd, err := command(dir, env, args, true)
	if err != nil {
		return nil, &Error{args, "", err}
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if asking {
		cmd.Stdin = os.Stdin
	}
	if err := run(cmd); err != nil {
		return out.Bytes(), &Error{args, "", err}
	}
	return out.Bytes(), nil
}

// NeedsCredentials reports whether git's message says that it failed for want of credentials it couldn't ask for (see noPrompts): git's,
// Git Credential Manager's, or ssh's for a key's passphrase or a host to trust, which pit refused as its askpass program.
func NeedsCredentials(msg string) bool {
	return strings.Contains(msg, "terminal prompts disabled") || strings.Contains(msg, "user interactivity has been disabled") ||
		strings.Contains(msg, refusal)
}

// Pass runs git in dir attached to the terminal: stdin, stdout and stderr are pit's own.
func Pass(dir string, args ...string) error {
	cmd, err := command(dir, nil, args, true)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := run(cmd); err != nil {
		return &Error{args, "", err}
	}
	return nil
}
