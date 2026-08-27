// Package discover finds git repositories under a root directory.
package discover

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Repo is a discovered git repository.
type Repo struct {
	Path  string // absolute path to the repo working tree
	Name  string // base name of Path
	Group string // parent dir relative to root, or "(root)"
}

// Options controls the walk.
type Options struct {
	MaxDepth int
	Prune    map[string]bool
}

// DefaultPrune is the set of directory names never descended into.
func DefaultPrune() map[string]bool {
	set := map[string]bool{}
	for _, n := range []string{
		".git", "node_modules", "vendor", "venv", ".venv",
		"__pycache__", ".tox", ".mypy_cache", ".pytest_cache",
		"dist", "build", ".next", ".cache", "site-packages",
		"target", ".idea", ".vscode",
	} {
		set[n] = true
	}
	return set
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

func makeRepo(root, dir string) Repo {
	group := "(root)"
	if dir != root {
		if rel, err := filepath.Rel(root, filepath.Dir(dir)); err == nil && rel != "." && rel != "" {
			group = rel
		}
	}
	return Repo{Path: dir, Name: filepath.Base(dir), Group: group}
}

// Discover walks root up to opts.MaxDepth, collecting every directory that
// contains a .git entry. It keeps descending past found repos (so repos nested
// inside a root repo's working tree are found) but never descends into pruned
// directory names, and never follows symlinks.
func Discover(root string, opts Options) ([]Repo, error) {
	root = filepath.Clean(root)
	if opts.Prune == nil {
		opts.Prune = DefaultPrune()
	}

	var repos []Repo
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > opts.MaxDepth {
			return
		}
		if isGitRepo(dir) {
			repos = append(repos, makeRepo(root, dir))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() { // skips files and symlinks (DirEntry.IsDir is lstat-based)
				continue
			}
			if opts.Prune[e.Name()] {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	walk(root, 0)

	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Group != repos[j].Group {
			return repos[i].Group < repos[j].Group
		}
		return repos[i].Name < repos[j].Name
	})
	return repos, nil
}

// Script is a runnable script discovered under the root.
type Script struct {
	Path string // absolute path
	Name string // path relative to root, e.g. "scripts/sync-edx.sh"
}

// Scripts finds runnable scripts under root up to maxDepth directory levels (a
// file directly in root is depth 1, in root/scripts/ is depth 2), pruning the
// same junk directories as Discover. Results are sorted by relative name. See
// scriptExtensionsFor for which files count.
func Scripts(root string, maxDepth int, prune map[string]bool) []Script {
	root = filepath.Clean(root)
	if prune == nil {
		prune = DefaultPrune()
	}
	exts := scriptExtensionsFor(runtime.GOOS)
	var out []Script
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				if !prune[name] {
					walk(filepath.Join(dir, name), depth+1)
				}
				continue
			}
			full := filepath.Join(dir, name)
			if !looksLikeScript(name, full, e, exts) {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				rel = name
			}
			out = append(out, Script{Path: full, Name: rel})
		}
	}
	walk(root, 1)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scriptExtensionsFor returns the extensions that always count as a runnable
// script, regardless of the executable bit: *.sh everywhere, plus — only on
// Windows — the two native Windows script kinds (*.ps1, *.cmd/*.bat).
// internal/tui commands.go's scriptInvocation is the only thing that knows how
// to run those, and it only has powershell/cmd.exe to invoke on Windows; on
// Linux/macOS neither exists on PATH, so listing them there would offer
// scripts the runner can't actually start.
func scriptExtensionsFor(goos string) []string {
	if goos == "windows" {
		return []string{".sh", ".ps1", ".cmd", ".bat"}
	}
	return []string{".sh"}
}

// looksLikeScript reports whether a file should be listed as a runnable
// script: anything with an exts suffix, or an extensionless executable whose
// first bytes are a "#!" shebang (catches helpers like scripts/sync-all
// without pulling in binaries or dotfiles).
func looksLikeScript(name, full string, e os.DirEntry, exts []string) bool {
	lower := strings.ToLower(name)
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	if strings.Contains(name, ".") {
		return false
	}
	// Windows has no executable bit to check — Mode() there never sets any of
	// 0o111 — so an extensionless shebang script is never picked up on it. That
	// matches the platform: Windows can't run such a file directly either way.
	info, err := e.Info()
	if err != nil || info.Mode()&0o111 == 0 {
		return false
	}
	f, err := os.Open(full)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	n, _ := io.ReadFull(f, buf)
	return n == 2 && buf[0] == '#' && buf[1] == '!'
}
