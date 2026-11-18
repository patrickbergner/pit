package git

import (
	"os"
	"os/exec"
	"strconv"
)

// killTree ends p and the processes it started. Killing git.exe alone would leave its helpers running, such as a credential manager waiting
// for an answer, and they would keep git's output open.
func killTree(p *os.Process) error {
	if err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(p.Pid)).Run(); err != nil {
		return p.Kill()
	}
	return nil
}
