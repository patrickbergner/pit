//go:build !windows

package git

import "os"

// killTree ends p. The processes it started got the Ctrl-C too, as they are in the terminal's foreground process group; git runs there to
// read from the terminal, so it can't have a group of its own to end as a whole.
func killTree(p *os.Process) error { return p.Kill() }
