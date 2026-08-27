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

// finishProcGroup is a no-op on unix: setProcGroup already armed c.Cancel
// before Start, and unix process groups pick up every descendant automatically
// (they inherit their parent's pgid), so there's nothing left to attach once
// the process exists. See procgroup_windows.go, which needs the process handle
// Start produces and so can't do its equivalent setup any earlier, and so
// returns a cleanup func of its own to release it.
func finishProcGroup(c *exec.Cmd) func() { return func() {} }
