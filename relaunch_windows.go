//go:build windows

package main

import (
	"os"
	"os/exec"
)

// relaunch starts exe/argv/env as a child, inherits this process's console so
// the child can keep prompting on it, waits for it to exit, and then exits
// with the same code. Windows has no execve — unlike syscall.Exec on Unix,
// the running process image can't be replaced in place, so the old process
// briefly outlives the new one instead of vanishing into it.
func relaunch(exe string, argv, env []string) error {
	cmd := exec.Command(exe, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	if ee, ok := runErr.(*exec.ExitError); ok {
		os.Exit(ee.ExitCode())
	}
	if runErr != nil {
		return runErr
	}
	os.Exit(0)
	return nil // unreachable
}
