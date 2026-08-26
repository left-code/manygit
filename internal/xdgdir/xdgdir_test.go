package xdgdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigHome_XDGEnvWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	if got := ConfigHome(); got != "/xdg/config" {
		t.Errorf("ConfigHome = %q, want /xdg/config", got)
	}
}

func TestCacheHome_XDGEnvWins(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	if got := CacheHome(); got != "/xdg/cache" {
		t.Errorf("CacheHome = %q, want /xdg/cache", got)
	}
}

func TestHome_NonWindowsFallsBackToDotDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir in this environment")
	}
	windowsDirCalled := false
	got := homeFor("XDG_CONFIG_HOME", ".config", func() (string, error) {
		windowsDirCalled = true
		return "", nil
	}, "linux")
	want := filepath.Join(home, ".config")
	if got != want {
		t.Errorf("home = %q, want %q", got, want)
	}
	if windowsDirCalled {
		t.Error("windowsDir should never be consulted off windows")
	}
}

func TestHome_WindowsPrefersWindowsDirOverDotDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got := homeFor("XDG_CONFIG_HOME", ".config", func() (string, error) {
		return `C:\Users\me\AppData\Roaming`, nil
	}, "windows")
	want := `C:\Users\me\AppData\Roaming`
	if got != want {
		t.Errorf("home = %q, want %q", got, want)
	}
}

func TestHome_WindowsFallsBackWhenWindowsDirErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir in this environment")
	}
	got := homeFor("XDG_CONFIG_HOME", ".config", func() (string, error) {
		return "", os.ErrNotExist
	}, "windows")
	want := filepath.Join(home, ".config")
	if got != want {
		t.Errorf("home = %q, want %q", got, want)
	}
}
