package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
