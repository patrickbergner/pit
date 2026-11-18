package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A git.bat shim first on PATH must not be run: cmd.exe would eat the '^' and split at the '&'.
func TestResolveSkipsBatchShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files only exist on Windows")
	}
	real, err := resolve()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	shim := "@\"" + real + "\" %*\r\n"
	if err := os.WriteFile(filepath.Join(dir, "git.bat"), []byte(shim), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Ext(got), ".exe") {
		t.Fatalf("resolve() = %s, want a git.exe", got)
	}
	arg := "HEAD^{tree} & echo x"
	out, err := exec.Command(got, "rev-parse", "--sq-quote", arg).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := " '" + arg + "'"; strings.TrimRight(string(out), "\n") != want {
		t.Fatalf("git got %q, want %q", out, want)
	}
}

// Abort ends a git that waits, together with the process it started, which keeps git's output open: here the sleep of an alias.
func TestAbort(t *testing.T) {
	defer func() { abortCtx, abortRuns = context.WithCancel(context.Background()) }()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := Raw(dir, nil, "-c", "alias.wait=!sleep 30", "wait")
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); !Running(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("git didn't start")
		}
	}
	time.Sleep(500 * time.Millisecond) // for git to start the sleep
	start := time.Now()
	Abort()
	select {
	case err := <-done:
		if err == nil {
			t.Error("an aborted git succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("git still ran 10s after Abort")
	}
	t.Logf("git ended %s after Abort", time.Since(start))
	if _, err := Output(dir, "version"); err == nil {
		t.Error("a git started after Abort ran")
	}
}
