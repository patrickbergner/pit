package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// loginServer stands for a remote that wants credentials: it answers every request with 401.
func loginServer(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// tellingHelper is a credential helper that has no credentials and says on stderr whether Git Credential Manager would be allowed to ask.
const tellingHelper = `!f() { echo "helper: GCM_INTERACTIVE=$GCM_INTERACTIVE" >&2; exit 1; }; f`

func TestSyncAsksWhereItCanBeAnswered(t *testing.T) {
	s := newSandbox(t)
	url := loginServer(t) + "/locked.git"
	root := s.root(true, ext{"locked", url, "locked"})
	s.git(root, "config", "--global", "credential.helper", tellingHelper)

	// sync's look at the remote before the clone may ask, with git's messages on the terminal; here nobody can answer, so it fails
	out, err := s.pitEnv(root, []string{"GIT_TERMINAL_PROMPT=0"}, "sync")
	if err == nil {
		t.Fatalf("sync succeeded\n%s", out)
	}
	contains(t, out, block("locked", "helper: GCM_INTERACTIVE=\n"), "could not read Username", "git ls-remote "+url+" failed")
}

func TestParallelRunsCantAsk(t *testing.T) {
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	s.git(a, "remote", "set-url", "origin", loginServer(t)+"/a.git")
	s.git(a, "config", "credential.helper", tellingHelper)

	// fetch runs git in parallel, where nothing can be asked: git and Git Credential Manager fail at once, and pit says where to ask
	start := time.Now()
	out := s.pitFails(root, "fetch")
	if time.Since(start) > 20*time.Second {
		t.Errorf("fetch took %s", time.Since(start))
	}
	contains(t, out, block("a", ""), "helper: GCM_INTERACTIVE=never", "terminal prompts disabled",
		"a: git needs credentials there, which it can't ask for while pit runs it in parallel; git fetch in "+a+" asks for them")
}

func TestSecondInterruptEndsGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Ctrl-C can't be sent to a process on Windows; internal/git's TestAbort covers ending git there")
	}
	s := newSandbox(t)
	root := s.standard()
	a := s.path("forge/a")
	started := filepath.ToSlash(s.path("helper-started"))
	s.git(a, "remote", "set-url", "origin", loginServer(t)+"/a.git")
	// a credential helper that never answers and ignores Ctrl-C, like a prompt nobody sees
	s.git(a, "config", "credential.helper", `!f() { trap "" INT; touch '`+started+`'; sleep 30; }; f`)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "pull")
	cmd.Dir, cmd.Env = root, s.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 2 * time.Second // the helper's sleep, left over once git is ended, keeps pit's output open
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for deadline := time.Now().Add(20 * time.Second); !s.exists(s.dir, "helper-started"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatalf("the helper didn't start\n%s", out.String())
		}
	}
	// the first Ctrl-C waits for git, which waits for the helper; pit says so, and the second one ends them
	cmd.Process.Signal(os.Interrupt)
	time.Sleep(1500 * time.Millisecond)
	cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("pit still ran after the second Ctrl-C\n%s", out.String())
	}
	contains(t, out.String(), "git is still running; if it waits for something, such as credentials, Ctrl-C again stops it")
	if s.exists(root, ".pit/lock") {
		t.Error("pit left its lock behind")
	}
	if !strings.Contains(out.String(), "pull failed in a") && !strings.Contains(out.String(), "interrupted") {
		t.Errorf("pit didn't report the stop:\n%s", out.String())
	}
}
