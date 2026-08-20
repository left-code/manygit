# manygit

A terminal UI for a whole tree of git repos. This glossary records the terms
this project has settled on, so the code, the docs, and the landing page all
use one word per concept.

## Distribution and updates

**Managed install**:
An install where something other than manygit owns the binary file on disk —
Homebrew, or the Go toolchain. manygit must never replace its own file here.
_Avoid_: packaged install, brew install, external install

**Self-managed install**:
An install where manygit owns its own binary and may replace it in place. What
the curl installer produces.
_Avoid_: manual install, standalone install, direct install

**Managed by**:
Which package manager owns a given install — empty for self-managed, otherwise
the manager's name. Derived at runtime from where the binary sits, never baked
in at build time, because every install method receives the same release archive.
_Avoid_: channel (means a release track: stable/beta), install method, packager,
origin

**Self-update**:
manygit downloading a newer release and replacing its own binary. Legal only for
a self-managed install.
_Avoid_: auto-update, in-app update

**Update notice**:
Telling the user a newer release exists and naming the command that installs it,
without applying anything. What a managed install gets in place of a self-update.
_Avoid_: update prompt, update nag
