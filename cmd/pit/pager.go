package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/patrickbergner/pit/internal/git"
	"github.com/patrickbergner/pit/internal/ui"
)

// stdoutTerminal reports whether stdout is a terminal, which is when pit pages; tests replace it.
var stdoutTerminal = func() bool { return ui.Terminal(os.Stdout) }

// pagerQuit is set when the pager ends before pit is done writing to it, as when the user quits it early; commands then stop.
var pagerQuit atomic.Bool

// pager is a running pager that pit's output goes through.
type pager struct {
	in             *os.File // the pager's input, which os.Stdout (and os.Stderr) point to while it runs
	stdout, stderr *os.File // the real ones
	done           chan struct{}
}

// startPager sends stdout, and stderr if that is a terminal too, through the pager git would use (GIT_PAGER, core.pager, PAGER, else
// less), the way git runs its own: through the shell, with LESS=FRX and LV=-c unless they are set. It returns nil when stdout isn't a
// terminal, the pager is cat or none could be started; then the output goes where it went.
func startPager(root string) *pager {
	if !stdoutTerminal() {
		return nil
	}
	name, err := git.Output(root, "var", "GIT_PAGER")
	if name = strings.TrimSpace(name); err != nil || name == "" || name == "cat" {
		return nil
	}
	cmd := pagerCommand(name)
	if cmd == nil {
		return nil
	}
	for _, kv := range []string{"LESS=FRX", "LV=-c"} {
		if k, _, _ := strings.Cut(kv, "="); os.Getenv(k) == "" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil
	}
	p := &pager{in: w, stdout: os.Stdout, stderr: os.Stderr, done: make(chan struct{})}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil
	}
	r.Close()
	go func() {
		cmd.Wait()
		pagerQuit.Store(true)
		close(p.done)
	}()
	os.Stdout = w
	if ui.Terminal(p.stderr) {
		os.Stderr = w
	}
	git.SetPagerInUse()
	return p
}

// stop ends the pager's input, waits until the user has quit it and gives pit its stdout and stderr back. It reports whether the pager
// had quit before, without showing everything.
func (p *pager) stop() (quitEarly bool) {
	quitEarly = pagerQuit.Load()
	p.in.Close()
	<-p.done
	os.Stdout, os.Stderr = p.stdout, p.stderr
	return quitEarly
}

// pagerCommand returns the command that runs the pager: through sh, as git runs it, which on Windows is the sh of Git for Windows, with
// its usr/bin on PATH for less. Without a shell, a pager that is a plain program name runs directly.
func pagerCommand(name string) *exec.Cmd {
	sh, bin := shell()
	if sh == "" {
		if strings.ContainsAny(name, " \t'\"|&;<>()$`\\*?[#~=%") {
			return nil
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return nil
		}
		cmd := exec.Command(path)
		cmd.Env = os.Environ()
		return cmd
	}
	cmd := exec.Command(sh, "-c", name)
	cmd.Env = os.Environ()
	if bin != "" {
		cmd.Env = append(cmd.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return cmd
}

// shell finds sh: on PATH, or on Windows in the Git for Windows installation that git comes from, whose usr/bin it also returns.
func shell() (sh, bin string) {
	if path, err := exec.LookPath("sh"); err == nil {
		return path, ""
	}
	if runtime.GOOS != "windows" {
		return "", ""
	}
	gitExe, err := git.Path()
	if err != nil {
		return "", ""
	}
	// git.exe is in cmd/, bin/, mingw64/bin/ or mingw64/libexec/git-core/ of the installation
	for dir, i := filepath.Dir(gitExe), 0; i < 4; dir, i = filepath.Dir(dir), i+1 {
		bin := filepath.Join(dir, "usr", "bin")
		if _, err := os.Stat(filepath.Join(bin, "sh.exe")); err == nil {
			return filepath.Join(bin, "sh.exe"), bin
		}
	}
	return "", ""
}
