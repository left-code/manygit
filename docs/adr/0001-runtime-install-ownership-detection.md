# Install ownership is detected at runtime, not baked in at build time

manygit refuses to replace its own binary when a package manager owns the file, so
it has to know how it was installed. It works this out at runtime, by resolving its
own executable through symlinks and checking whether the result sits under
Homebrew's `Caskroom` directory — deliberately **not** from a build-time ldflag,
because the Homebrew cask and the `curl | bash` installer download the *same*
release archive. No value baked in at build time can tell those two installs apart.

## Considered options

- **Two GoReleaser builds**, one carrying a `homebrew` ldflag, with its own archive
  for the cask to point at. Rejected: doubles the release artifacts from four to
  eight, and splits the per-asset download counts that `manygit stats` aggregates
  (`internal/selfupdate.aggregate` counts `.tar.gz` assets per release).
- **A marker file written by the cask's postflight hook.** The hook already exists
  for the quarantine `xattr`, so this is nearly free. Rejected: the marker outlives
  the install. Uninstall via brew, reinstall via curl, and a stale file goes on
  claiming Homebrew owns a binary it no longer does.
- **Runtime path detection.** Chosen: derived fresh every launch, adds no release
  artifacts and no persisted state that can drift out of date.

## Consequences

- `selfupdate.Apply` already calls `filepath.EvalSymlinks` on its own path. That
  same resolution is what makes detection work at all, since Homebrew links
  `$(brew --prefix)/bin/manygit` to `$(brew --caskroom)/manygit/<version>/manygit`.
- The failure modes are not symmetric, and the dangerous one is the false
  *positive*. Missing a Homebrew install degrades to the pre-existing behaviour
  (manygit self-updates and desyncs Homebrew's records). Wrongly reporting a
  self-managed install as managed disables self-update for everyone who installed
  via the script — which is what keying `go install` detection off build info
  alone would have done, since Go 1.24 stamps the VCS tag there for the plain
  `go build` that GoReleaser runs. Detection keys off the linker-stamped ldflag
  instead, and `TestManagedBy` pins that case.
- This does not generalise for free. Every future package manager (`.deb`, `.rpm`)
  needs its own path signature added deliberately.
