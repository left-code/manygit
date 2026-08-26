//go:build !unix && !windows

package tui

import "os/exec"

// setProcGroup and finishProcGroup are no-ops here: exec.CommandContext's own
// kill of the direct child is all that is available. manygit ships linux,
// darwin and windows binaries (see .goreleaser.yaml); this file exists so the
// package still builds on anything else Go can target (plan9, js/wasm, ...).
func setProcGroup(c *exec.Cmd)    {}
func finishProcGroup(c *exec.Cmd) {}
