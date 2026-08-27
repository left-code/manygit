//go:build windows

package tui

import (
	"os/exec"
	"sync"
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
//
// The returned func releases the job handle and must be called once the
// process is done, cancelled or not — c.Cancel only fires on cancellation, so
// a command that simply runs to completion would otherwise never close this
// handle. Both paths funnel through the same sync.Once, so whichever happens
// first (normal exit or cancel) is the only one that actually closes it.
func finishProcGroup(c *exec.Cmd) func() {
	noop := func() {}
	if c.Process == nil {
		return noop
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return noop
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
		return noop
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return noop
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return noop
	}
	var once sync.Once
	closeJob := func() { once.Do(func() { windows.CloseHandle(job) }) }
	c.Cancel = func() error {
		closeJob() // KILL_ON_JOB_CLOSE terminates the whole tree
		return nil
	}
	return closeJob
}
