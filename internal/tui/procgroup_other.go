//go:build !unix

package tui

import "os/exec"

// setProcGroup is a no-op off unix: exec.CommandContext's own kill of the direct
// child is all that is available. manygit ships linux and darwin binaries only
// (see .goreleaser.yaml); this file exists so the package still builds elsewhere.
func setProcGroup(c *exec.Cmd) {}
