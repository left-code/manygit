//go:build unix

package tui

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts c in its own process group and makes cancellation signal the
// whole group. exec.CommandContext on its own only kills the direct child, so
// `bash -c "sleep 30 | cat"` would leave the pipeline's grandchildren running
// after the pane had moved on.
func setProcGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
