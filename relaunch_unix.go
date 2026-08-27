//go:build !windows

package main

import "syscall"

// relaunch replaces the running process image with exe/argv/env — the
// self-updater's re-exec into the binary it just downloaded. It only returns
// on failure; on success the calling process is gone.
func relaunch(exe string, argv, env []string) error {
	return syscall.Exec(exe, argv, env)
}
