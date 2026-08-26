//go:build windows

package tui

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setProcGroup is a no-op before Start on Windows — see finishProcGroup, which
// does the real work once the process (and its handle) exists.
func setProcGroup(c *exec.Cmd) {}

// finishProcGroup puts c's freshly-started process into a Windows Job Object
// configured to kill everything in it when the job handle closes, and arms
// c.Cancel to close that handle. exec.CommandContext on its own only kills the
// direct child, so `cmd /C "timeout 30 & type nul"` would leave the pipeline's
// grandchildren running after the pane had moved on — this is the Windows
// equivalent of procgroup_unix.go's process-group SIGKILL.
//
// It has to run after Start, unlike the unix version: assigning a process to a
// job needs a handle to that process, which doesn't exist until Start creates
// it. A child inherits its parent's job automatically at the moment it's
// created, so assigning the top-level process right after Start still catches
// every descendant it goes on to spawn.
func finishProcGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return
	}
	c.Cancel = func() error {
		return windows.CloseHandle(job) // KILL_ON_JOB_CLOSE terminates the whole tree
	}
}
