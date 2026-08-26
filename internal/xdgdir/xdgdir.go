// Package xdgdir resolves the base directories manygit stores its config and
// cache files under.
package xdgdir

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigHome returns the directory manygit's config file lives under: the
// value of $XDG_CONFIG_HOME when set, %AppData% on Windows, or ~/.config
// everywhere else.
func ConfigHome() string {
	return homeFor("XDG_CONFIG_HOME", ".config", os.UserConfigDir, runtime.GOOS)
}

// CacheHome returns the directory manygit's cache files live under: the value
// of $XDG_CACHE_HOME when set, %LocalAppData% on Windows, or ~/.cache
// everywhere else.
func CacheHome() string {
	return homeFor("XDG_CACHE_HOME", ".cache", os.UserCacheDir, runtime.GOOS)
}

// homeFor implements the shared lookup: an explicit XDG env var always wins
// (so a user who set one under WSL/MSYS/Cygwin keeps their existing files),
// then the platform default. macOS and Linux both keep the long-standing
// ~/.config and ~/.cache convention manygit already used before Windows
// support existed — os.UserConfigDir would move macOS to ~/Library/Application
// Support, which would silently orphan every existing install's config and
// cache. Windows has no such history, so it gets its native directory via
// windowsDir. goos is threaded through explicitly (rather than read from
// runtime.GOOS in here) so tests can exercise the windows branch on any host.
func homeFor(envVar, unixLeaf string, windowsDir func() (string, error), goos string) string {
	if base := os.Getenv(envVar); base != "" {
		return base
	}
	if goos == "windows" {
		if dir, err := windowsDir(); err == nil {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return unixLeaf
	}
	return filepath.Join(home, unixLeaf)
}
