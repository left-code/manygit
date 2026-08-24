# `!` Shell Passthrough Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Press `!`, type a bash command, and run it in the highlighted repo with its output streaming live into the existing `[7 Output]` pane.

**Architecture:** `!` becomes a third producer for the Output pane, alongside the Scripts runner (`enter`) and the AI harness (`:`). It reuses the existing streaming pipeline wholesale — `bash`, stdout+stderr merged into one `io.Pipe`, a `bufio.Scanner` pumped one line per `tea.Cmd`, staleness guarded by `Model.outputRun` — so auto-follow, the `Output*` tab marker, the repo fingerprint probe and the end-of-run re-stat all come for free. The one genuinely new capability is cancellation: both the script runner and the shell line move to `exec.CommandContext` with a process group, and the kill switch travels back to the Model in a new `runStartMsg`.

**Tech Stack:** Go 1.24, Bubble Tea, Lip Gloss. No new dependencies. Browser demo is vanilla ES5-style JS, no build step.

**Spec:** No separate spec file — this feature was specified conversationally. The four binding decisions the user made are recorded verbatim in Global Constraints below, and the design rationale lives in this plan's task notes.

---

## Global Constraints

- **DO NOT COMMIT.** The repo's `CLAUDE.md` states: *"Don't commit on your own — the user is the default committer. Make the file changes and stop."* This **overrides** the writing-plans skill's default "frequent commits" step. Every task therefore ends with **"Stop and hand off"**, not `git commit`. Read-only `git status` / `diff` / `log` is fine.
- **Decision — mode:** capture into `[7 Output]`. **Not** a TUI-suspending passthrough. Commands run non-interactively with no TTY; `vim`, `top` and `sudo` prompts will not work, and that is accepted.
- **Decision — scope:** the **highlighted repo only**, one command at a time. No fan-out across repos.
- **Decision — visibility (user's exact words):** *"when `!` is done the bottom bar should show the name of the repo: and the cursor for typing like the user should know visually he is running bash commands inside that folder which he is selected now."* The repo name must be visible while typing **and** in the output.
- **Decision — cancellation:** `esc` / `ctrl+c` kill a running command.
- **Decision — safety:** **no confirm, no command blocklist.** `!` is an explicit shell escape. The command is echoed into the pane as a record of what ran.
- **Key `!` is free** as a binding. It is already used as a *status glyph* (red `!` = "no upstream") in `view.go:121`/`129` — that is a display string, not a binding, and does not conflict.
- **The `?` keys overlay column is `Width(10)` and HARD-WRAPS.** Any key label longer than 10 cells breaks mid-word. `TestTUI_KeyColumnFitsEveryLabel` enforces this.
- **Seven documentation mirrors**, not four. The four key tables (`view.go:keysColumns`, `README.md`, `docs/index.html`, `docs/llms.txt`) **plus** `view.go:footer()`, the three **Safety** sections, and `CLAUDE.md`'s divergence count.
- **The browser demo must be updated in the same change** (`CLAUDE.md`: "the landing-page demo mirrors the TUI"). A browser has no bash, so `!` becomes the **fifth** intentional divergence and must be disclosed on the page.
- **Copy strings are ported verbatim** between Go and `docs/assets/demo.js`. Change the Go first, then re-port.
- **All `docs/` asset paths stay relative** (Pages serves from the `/manygit/` subpath).

## Verification commands

```bash
go build ./...                       # must stay clean
go test ./internal/tui/ -run TestShell -v
go test ./...                        # full suite
node --check docs/assets/demo.js     # demo syntax
cd docs && python3 -m http.server 8765   # drive the demo by hand
```

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/tui/commands.go` | Process spawning + streaming | Add `streamCmd`, `startShellCmd`, `exitText`; rewrite `startScriptCmd` on top of `streamCmd` |
| `internal/tui/procgroup_unix.go` | Process-group kill (unix) | **Create** |
| `internal/tui/procgroup_other.go` | No-op fallback | **Create** |
| `internal/tui/messages.go` | Message types | Add `runStartMsg` |
| `internal/tui/model.go` | State | Add `shell*` fields, `outputKind` |
| `internal/tui/update.go` | Keys + message handling | `case "!"`, `handleShellPromptKey`, `runShellLine`, `killRunning`, cancel keys, `runStartMsg` handler, `takeOutputPane` signature |
| `internal/tui/view.go` | Rendering | `shellPromptLine`, `statusOrFilterLine`, `bottomHint`, `footer`, `keysColumns` |
| `internal/tui/shell_test.go` | Tests for the whole feature | **Create** |
| `README.md`, `docs/index.html`, `docs/llms.txt` | Keys + Safety docs | Modify |
| `docs/assets/demo.js` | Browser port | Modify |
| `CLAUDE.md` | Divergence count 4 → 5 | Modify |

---

### Task 1: Cancellable streaming core

The existing `startScriptCmd` cannot be stopped — there is no `Kill` anywhere in the codebase. This task makes **both** producers cancellable and factors out the shared body. The kill switch must ride out on its **own** message emitted the instant the process starts, *before* the first read blocks: `!sleep 30` prints nothing, so a cancel func attached to the first output line would never arrive.

`exec.CommandContext` alone only kills the direct child. `bash -c "sleep 30 | cat"` spawns grandchildren that would survive, so the process gets its own group and the whole group is signalled.

**Files:**
- Create: `internal/tui/procgroup_unix.go`, `internal/tui/procgroup_other.go`, `internal/tui/shell_test.go`
- Modify: `internal/tui/commands.go:116-148`, `internal/tui/messages.go:105-114`
- Modify (test helper): `internal/tui/output_test.go:17-38`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `runStartMsg{run int; cancel context.CancelFunc; scanner *bufio.Scanner}`
  - `startShellCmd(line, dir string, run int) tea.Cmd`
  - `startScriptCmd(path string, run int) tea.Cmd` (unchanged signature, new internals)
  - `streamCmd(c *exec.Cmd, cancel context.CancelFunc, run int) tea.Msg`
  - `exitText(err error) string`
  - `setProcGroup(c *exec.Cmd)`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/shell_test.go`:

```go
package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// startRun runs a start command far enough to get the runStartMsg out of it,
// mirroring what the Update loop does with the first message of a run.
func startRun(t *testing.T, c tea.Cmd) runStartMsg {
	t.Helper()
	if c == nil {
		t.Fatal("nil start command")
	}
	msg := c()
	st, ok := msg.(runStartMsg)
	if !ok {
		t.Fatalf("a run must open with runStartMsg, got %T", msg)
	}
	if st.cancel == nil {
		t.Fatal("runStartMsg must carry a cancel func")
	}
	if st.scanner == nil {
		t.Fatal("runStartMsg must carry its scanner")
	}
	return st
}

// A `!` line runs through bash, merges stdout and stderr, and reports a clean exit.
func TestShellStreamsCombinedOutput(t *testing.T) {
	lines, err := drainScript(t, startShellCmd("echo one; echo two >&2; echo three", t.TempDir(), 0))
	if err != nil {
		t.Errorf("clean exit should have a nil error, got %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, w := range []string{"one", "two", "three"} {
		if !strings.Contains(joined, w) {
			t.Errorf("captured output %v is missing %q", lines, w)
		}
	}
}

// The command runs in the directory it was given, not manygit's own cwd.
func TestShellRunsInTheGivenDir(t *testing.T) {
	dir := t.TempDir()
	lines, err := drainScript(t, startShellCmd("pwd", dir, 0))
	if err != nil {
		t.Fatalf("pwd failed: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("want one line of output, got %v", lines)
	}
	// macOS resolves t.TempDir() under /private/var; compare resolved paths.
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(lines[0]))
	if got != want {
		t.Errorf("command ran in %q, want %q", got, want)
	}
}

// A non-zero exit surfaces as the terminal error, and exitText names the code.
func TestShellNonZeroExitIsReported(t *testing.T) {
	lines, err := drainScript(t, startShellCmd("echo boom; exit 3", t.TempDir(), 0))
	if err == nil {
		t.Fatal("a non-zero exit should surface as the terminal error")
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Errorf("want [boom] before the failure, got %v", lines)
	}
	if got := exitText(err); got != "exited 3" {
		t.Errorf("exitText = %q, want %q", got, "exited 3")
	}
	if got := exitText(nil); got != "exited 0" {
		t.Errorf("exitText(nil) = %q, want %q", got, "exited 0")
	}
}

// The kill switch must arrive BEFORE any output does — a command that prints
// nothing for a minute still has to be cancellable.
func TestShellCancelReachesUsBeforeAnyOutput(t *testing.T) {
	st := startRun(t, startShellCmd("sleep 30", t.TempDir(), 0))
	st.cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			om, ok := readScriptLine(st.scanner, st.run)().(scriptOutMsg)
			if !ok || om.done {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop a silent command")
	}
}

// Cancelling kills the whole process group, not just bash: a pipeline's
// grandchildren must not outlive the run.
func TestShellCancelKillsTheProcessGroup(t *testing.T) {
	st := startRun(t, startShellCmd("sleep 30 | cat", t.TempDir(), 0))
	st.cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			om, ok := readScriptLine(st.scanner, st.run)().(scriptOutMsg)
			if !ok || om.done {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel left the pipeline's grandchildren running")
	}
}

// A command that cannot start reports the failure instead of hanging.
func TestShellStartFailureIsReported(t *testing.T) {
	_, err := drainScript(t, startShellCmd("exit 127", filepath.Join(t.TempDir(), "nope"), 0))
	if err == nil {
		t.Error("a bad working directory should surface as an error")
	}
	var ee *exec.ExitError
	_ = ee // the error may be a start error rather than an exit error; either is fine
}
```

- [ ] **Step 2: Update the shared test helper so it consumes the new opening message**

In `internal/tui/output_test.go`, replace `drainScript` (lines 14-38) with:

```go
// drainScript runs a start command to completion, mirroring the Update loop:
// runStartMsg opens the run, then each scriptOutMsg drives the next read until
// done. Returns the captured lines and the terminal error (a non-zero exit or a
// read error; nil on clean exit).
func drainScript(t *testing.T, first tea.Cmd) ([]string, error) {
	t.Helper()
	if first == nil {
		t.Fatal("nil start command")
	}
	msg := first()
	if st, ok := msg.(runStartMsg); ok {
		msg = readScriptLine(st.scanner, st.run)()
	}
	var lines []string
	for {
		om, ok := msg.(scriptOutMsg)
		if !ok {
			t.Fatalf("expected scriptOutMsg, got %T", msg)
		}
		if om.done {
			return lines, om.err
		}
		lines = append(lines, om.line)
		if om.scanner == nil {
			t.Fatal("non-done scriptOutMsg is missing its scanner")
		}
		msg = readScriptLine(om.scanner, om.run)()
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestShell' -v`
Expected: FAIL to compile — `undefined: runStartMsg`, `undefined: startShellCmd`, `undefined: exitText`.

- [ ] **Step 4: Add the message type**

In `internal/tui/messages.go`, add `"context"` to the imports and insert above `scriptOutMsg` (line 105):

```go
// runStartMsg opens a streamed run — a script from the Scripts pane, or a `!`
// shell line. It carries the kill switch back to the Model and is emitted the
// moment the process STARTS, before the first read blocks: a command that prints
// nothing for a minute (`!sleep 30`) must still be cancellable, so the cancel
// func cannot ride on the first output line.
type runStartMsg struct {
	run     int // the run this belongs to (Model.outputRun at start)
	cancel  context.CancelFunc
	scanner *bufio.Scanner
}
```

- [ ] **Step 5: Add the process-group helpers**

Create `internal/tui/procgroup_unix.go`:

```go
//go:build unix

package tui

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts c in its own process group and makes cancellation signal the
// whole group. exec.CommandContext on its own only kills the direct child, so
// `bash -c "sleep 30 | cat"` would leave the pipeline's grandchildren running
// after the pane had moved on.
func setProcGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
```

Create `internal/tui/procgroup_other.go`:

```go
//go:build !unix

package tui

import "os/exec"

// setProcGroup is a no-op off unix: exec.CommandContext's own kill of the direct
// child is all that is available. manygit ships linux and darwin binaries only
// (see .goreleaser.yaml); this file exists so the package still builds elsewhere.
func setProcGroup(c *exec.Cmd) {}
```

- [ ] **Step 6: Rewrite the runners in `internal/tui/commands.go`**

Add `"context"`, `"errors"` and `"strconv"` to the imports if absent. Replace `startScriptCmd` (lines 116-137) with:

```go
// startScriptCmd runs a script with `bash` in the background (non-interactive),
// in the script's own directory.
func startScriptCmd(path string, run int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		c := exec.CommandContext(ctx, "bash", path)
		c.Dir = filepath.Dir(path)
		return streamCmd(c, cancel, run)
	}
}

// startShellCmd runs one shell line (`!`) with `bash -c` in dir — the repo under
// the cursor. Identical plumbing to a script run; the only difference is how the
// command was built, which is why both go through streamCmd.
func startShellCmd(line, dir string, run int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		c := exec.CommandContext(ctx, "bash", "-c", line)
		c.Dir = dir
		return streamCmd(c, cancel, run)
	}
}

// streamCmd starts c with stdout+stderr merged into one pipe and hands the
// reader back to the Update loop. The process's exit status is delivered to the
// reader via CloseWithError, so a non-zero exit surfaces as scanner.Err() at EOF
// (no shared state, no race).
//
// It returns runStartMsg rather than the first line on purpose: reading would
// block until the command printed something, and the cancel func has to be
// reachable before then.
func streamCmd(c *exec.Cmd, cancel context.CancelFunc, run int) tea.Msg {
	setProcGroup(c)
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		cancel()
		return scriptOutMsg{run: run, done: true, err: err}
	}
	// Deliver the exit status to the reader: a non-zero exit surfaces as
	// scanner.Err() at EOF. If the TUI quits mid-stream this goroutine is
	// abandoned, but the child then gets SIGPIPE on its next write and exits.
	go func() { pw.CloseWithError(c.Wait()) }()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines (1 MiB)
	return runStartMsg{run: run, cancel: cancel, scanner: sc}
}

// exitText names how a `!` run finished. A non-zero exit reads as the shell
// would report it; anything else (could not start, pipe failure) shows the error.
func exitText(err error) string {
	if err == nil {
		return "exited 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "exited " + strconv.Itoa(ee.ExitCode())
	}
	return err.Error()
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestShell|TestScriptStreaming' -v`
Expected: PASS, including the pre-existing `TestScriptStreamingRealProcess`.

- [ ] **Step 8: Verify nothing else broke**

Run: `go build ./... && go test ./...`
Expected: all packages `ok`. The Update loop does not handle `runStartMsg` yet, so a *live* script run would stall — that is fixed in Task 2 and is not observable from the tests.

- [ ] **Step 9: Stop and hand off**

Do **not** commit (see Global Constraints). Report: files changed, test output, and that live script runs are wired up in Task 2.

---

### Task 2: Pane ownership — `outputKind` and the `runStartMsg` handler

The Output pane's end-of-run report currently hardcodes script wording (`"ran a.sh"` / `"script a.sh failed"`). A shell line needs `exited 0` / `exited 3` / `cancelled`, so the pane records **which producer** owns it. This task also wires `runStartMsg` into `Update`, restoring live script runs, and makes a handover kill the process it supersedes instead of orphaning it.

**Files:**
- Modify: `internal/tui/model.go` (fields), `internal/tui/update.go:339-375` (message handling), `internal/tui/update.go:1544-1554` (`takeOutputPane`), plus its 3 call sites at `update.go:585`, `update.go:1060`, `update.go:1650`
- Test: `internal/tui/shell_test.go`

**Interfaces:**
- Consumes: `runStartMsg`, `exitText` (Task 1).
- Produces:
  - `type outputKind int` with `outScript`, `outShell`, `outAI`
  - `Model.outputKind outputKind`, `Model.shellCancel context.CancelFunc`, `Model.shellKilled bool`, `Model.shellRepo string`
  - `takeOutputPane(kind outputKind, title string)` — **signature changed**
  - `(*Model).killRunning()`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/shell_test.go`:

```go
// The Update loop must adopt the kill switch from runStartMsg and then start
// pumping the reader.
func TestShell_UpdateAdoptsTheCancelFunc(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.takeOutputPane(outShell, "alpha $ echo hi")

	var called bool
	mm, cmd := m.Update(runStartMsg{run: m.outputRun, cancel: func() { called = true }})
	m = mm.(Model)
	if m.shellCancel == nil {
		t.Fatal("Update should store the run's cancel func")
	}
	if cmd == nil {
		t.Error("Update should start pumping the reader after runStartMsg")
	}
	m.killRunning()
	if !called {
		t.Error("killRunning should invoke the stored cancel func")
	}
	if m.shellCancel != nil {
		t.Error("killRunning should clear the cancel func")
	}
}

// A runStartMsg from a superseded run is cancelled on arrival rather than
// orphaned: its takeOutputPane ran before the process existed, so nothing else
// can stop it.
func TestShell_SupersededStartIsCancelledNotOrphaned(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.takeOutputPane(outShell, "alpha $ one")

	var called bool
	mm, _ := m.Update(runStartMsg{run: m.outputRun - 1, cancel: func() { called = true }})
	m = mm.(Model)
	if !called {
		t.Error("a superseded run's process must be cancelled, not left running")
	}
	if m.shellCancel != nil {
		t.Error("a superseded run must not become the pane's cancel func")
	}
}

// A finished shell run reports its exit status; a script still reports "ran".
func TestShell_DoneWordsItselfByProducer(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	m.takeOutputPane(outShell, "alpha $ false")
	m.shellRepo = "alpha"
	run := m.outputRun
	mm, _ := m.Update(scriptOutMsg{run: run, done: true, err: &exec.ExitError{}})
	m = mm.(Model)
	if !strings.Contains(stripANSI(m.statusLine), "alpha") {
		t.Errorf("a shell run's report should name the repo, got %q", stripANSI(m.statusLine))
	}
	if m.outputRunning {
		t.Error("done should clear outputRunning")
	}

	m.takeOutputPane(outScript, "a.sh")
	mm, _ = m.Update(scriptOutMsg{run: m.outputRun, done: true})
	m = mm.(Model)
	if got := stripANSI(m.statusLine); !strings.Contains(got, "ran a.sh") {
		t.Errorf("a script run should still report %q, got %q", "ran a.sh", got)
	}
}

// Handing the pane to a new producer stops whatever was running in it.
func TestShell_HandoverKillsThePreviousRun(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellCancel = func() { called = true }
	m.takeOutputPane(outScript, "a.sh")
	if !called {
		t.Error("taking the pane should kill the run it supersedes")
	}
	if m.shellKilled {
		t.Error("takeOutputPane should reset shellKilled for the new run")
	}
}
```

Add `"os/exec"` to the test file's imports if not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestShell_' -v`
Expected: FAIL to compile — `undefined: outShell`, `undefined: outScript`, `killRunning`, and `too many arguments in call to takeOutputPane`.

- [ ] **Step 3: Add the state**

In `internal/tui/model.go`, add `"context"` to the imports. Above the `Model` struct, add:

```go
// outputKind is which producer currently owns the Output pane. The pane is
// shared by three of them and they report completion differently: a script
// "ran", a `!` line "exited N", the AI harness prints its own report.
type outputKind int

const (
	outScript outputKind = iota
	outShell
	outAI
)
```

Inside `Model`, immediately after the `outputRun int` field (line ~119), add:

```go
	outputKind outputKind // which producer owns the pane; words the end-of-run report

	// `!` shell mode. shellCmd is the line being typed; shellRepo/shellDir are
	// the repo it will run in, SNAPSHOTTED when `!` opened — the same reasoning
	// as aiNames: a background fetch or a rescan must not move the target out
	// from under a half-typed command.
	//
	// shellCancel is the running command's kill switch (nil when nothing runs),
	// adopted from runStartMsg. shellKilled distinguishes "we stopped it" from a
	// real non-zero exit, so a cancel doesn't get reported as a crash.
	shellPrompting bool
	shellCmd       string
	shellRepo      string
	shellDir       string
	shellCancel    context.CancelFunc
	shellKilled    bool

	// Prompt history, up/down — in-memory only, exactly like aiHistory: a scratch
	// convenience for this session, and shell lines are not worth persisting to
	// disk by surprise. shellHistIdx == len(shellHistory) means "not browsing".
	shellHistory []string
	shellHistIdx int
	shellDraft   string
```

- [ ] **Step 4: Change `takeOutputPane` and add `killRunning`**

In `internal/tui/update.go`, replace the body of `takeOutputPane` (line 1544) with:

```go
func (m *Model) takeOutputPane(kind outputKind, title string) {
	m.killRunning() // stop what we're superseding rather than orphaning it
	m.outputRun++
	m.aiRun++
	m.outputKind = kind
	m.outputTitle = title
	m.outputLines = nil
	m.outputOffset = 0
	m.outputRunning = true
	m.shellKilled = false
	m.probing = false
}

// killRunning stops the command currently streaming into the Output pane, if
// there is one. Safe to call at any time: cancel() is idempotent and harmless
// after the process has already exited.
func (m *Model) killRunning() {
	if m.shellCancel == nil {
		return
	}
	m.shellKilled = true
	m.shellCancel()
	m.shellCancel = nil
}
```

Update its three existing call sites:
- `update.go:585` (confirmPlan `y`): `m.takeOutputPane(outAI, "running "+plural(len(plan.Steps), "command"))`
- `update.go:1060` (`handleAIPromptKey` enter): `m.takeOutputPane(outAI, m.cfg.Harness+": "+req)`
- `update.go:1650` (`runSelectedScript`): `m.takeOutputPane(outScript, vs[m.scriptCursor].Name)`

- [ ] **Step 5: Handle `runStartMsg` and re-word the done branch**

In `internal/tui/update.go`, insert a new case immediately before `case scriptOutMsg:` (line 351):

```go
	case runStartMsg:
		// The pane may already have moved on — takeOutputPane could not have
		// killed this one, because the process did not exist yet when it ran.
		if msg.run != m.outputRun {
			msg.cancel()
			return m, nil
		}
		m.shellCancel = msg.cancel
		return m, readScriptLine(msg.scanner, msg.run)
```

Then, inside `case scriptOutMsg:`, replace the status-building block in the `if msg.done` branch with:

```go
			m.outputRunning = false
			m.probing = false // the next tick finds it disarmed and stops
			m.shellCancel = nil
			var s string
			switch {
			case m.outputKind == outShell && m.shellKilled:
				s = styleOrange.Render(m.shellRepo + ": cancelled")
			case m.outputKind == outShell:
				s = styleGreen.Render(m.shellRepo + ": " + exitText(msg.err))
				if msg.err != nil {
					s = styleRed.Render(m.shellRepo + ": " + exitText(msg.err))
				}
			case msg.err != nil:
				s = styleRed.Render("script " + m.outputTitle + " failed: " + msg.err.Error())
			default:
				s = styleGreen.Render("ran " + m.outputTitle)
			}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestShell' -v`
Expected: PASS.

- [ ] **Step 7: Verify the full suite and a live script run**

Run: `go build ./... && go test ./...`
Expected: all `ok`. Then run `./manygit` in a folder with a `.sh` script, focus Scripts (`2`), press `enter`, and confirm output still streams — this exercises the `runStartMsg` handler end to end.

- [ ] **Step 8: Stop and hand off**

Do **not** commit.

---

### Task 3: `!` opens a prompt bound to the highlighted repo

The user's explicit requirement: the repo name must be visible while typing, so it is never ambiguous which folder the command will run in.

**Files:**
- Modify: `internal/tui/update.go:566-577` (modal precedence), `internal/tui/update.go:892` (add `case "!"` before `case ":"`), `internal/tui/view.go:826-836` (`statusOrFilterLine`)
- Test: `internal/tui/shell_test.go`

**Interfaces:**
- Consumes: the `shell*` fields (Task 2).
- Produces: `(m Model) handleShellPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd)`, `(m Model) shellPromptLine() string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/shell_test.go`:

```go
// `!` opens the prompt bound to the repo under the cursor, and the bottom bar
// shows that repo's name so you can see which folder you are about to run in.
func TestShell_BangOpensPromptBoundToTheCursorRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m = mm.(Model)
	if !m.shellPrompting {
		t.Fatal("! should open the shell prompt")
	}
	want := m.repos[0].repo.Name
	if m.shellRepo != want {
		t.Errorf("prompt bound to %q, want the cursor repo %q", m.shellRepo, want)
	}
	if m.shellDir != m.repos[0].repo.Path {
		t.Errorf("prompt bound to dir %q, want %q", m.shellDir, m.repos[0].repo.Path)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, want+" !") {
		t.Errorf("the bottom bar must show %q while typing; view had none", want+" !")
	}
}

// Typing lands in the command, and the prompt renders it after the repo name.
func TestShell_TypingShowsInThePromptLine(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m = press(t, m, "!")
	m = typeIn(t, m, "git status -sb")

	if m.shellCmd != "git status -sb" {
		t.Errorf("shellCmd = %q, want %q", m.shellCmd, "git status -sb")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "git status -sb") {
		t.Error("the prompt line should render what has been typed")
	}
}

// While the shell prompt is open, keys that would otherwise open an overlay must
// land in the command instead — the same rule aiPrompting follows.
func TestShell_PromptSwallowsOverlayKeys(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m = press(t, m, "!")
	m = typeIn(t, m, "grep -n ? x")

	if m.showHelp || m.showGraph || m.showNews {
		t.Error("? and g must not open an overlay while the shell prompt is up")
	}
	if m.shellCmd != "grep -n ? x" {
		t.Errorf("shellCmd = %q, want the literal typed text", m.shellCmd)
	}
}

// esc abandons the prompt without running anything.
func TestShell_EscClosesThePrompt(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m = press(t, m, "!")
	m = typeIn(t, m, "rm -rf /")

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.shellPrompting || m.shellCmd != "" {
		t.Error("esc should abandon the shell prompt and clear the line")
	}
	if m.outputRunning {
		t.Error("esc must not run the command")
	}
}
```

Add these helpers to the same file:

```go
// press sends one key (given as its String() form) and returns the new Model.
func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return mm.(Model)
}

// typeIn sends s one rune at a time, using KeySpace for spaces exactly as
// bubbletea does — a handler that only matches KeyRunes silently eats spaces.
func typeIn(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		mm, _ := m.Update(k)
		m = mm.(Model)
	}
	return m
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestShell_Bang|TestShell_Typing|TestShell_PromptSwallows|TestShell_EscCloses' -v`
Expected: FAIL — `! should open the shell prompt`.

- [ ] **Step 3: Add the modal guard**

In `internal/tui/update.go`, inside `handleKey`, insert immediately after the `m.filtering` guard (line 569) and **before** the `m.aiPrompting` guard:

```go
	// Above the overlay guards for the same reason aiPrompting is: a `?` or `g`
	// inside a shell command is part of the command, not a request for an overlay.
	if m.shellPrompting {
		return m.handleShellPromptKey(msg)
	}
```

- [ ] **Step 4: Add the `!` case**

In `internal/tui/update.go`, insert immediately before `case ":":` (line 892):

```go
	case "!":
		// A shell line in the highlighted repo. Bound to that repo up front and
		// shown in the prompt, so there is never a question which folder it lands
		// in — and so a background fetch cannot move it mid-type.
		r := m.currentVisible(vis)
		if r == nil {
			return m, m.setStatus(styleOrange.Render("no repo selected"))
		}
		m.shellPrompting = true
		m.shellCmd = ""
		m.shellRepo = r.repo.Name
		m.shellDir = r.repo.Path
		m.shellHistIdx = len(m.shellHistory)
```

- [ ] **Step 5: Add the key handler**

In `internal/tui/update.go`, add below `handleAIPromptKey` (after line ~1073). History and enter are stubbed here and completed in Tasks 4 and 5:

```go
// handleShellPromptKey owns every key while the `!` line is open. Like
// handleAIPromptKey it switches on msg.Type — a closed set — so nothing from the
// normal keymap can leak into a half-typed command.
//
// There is deliberately no tab-completion: `:` completes against a known
// vocabulary of repo and branch names, but a shell line has no such list, and a
// tab that silently did nothing would read as broken.
func (m Model) handleShellPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.shellPrompting = false
		m.shellCmd = ""
	case tea.KeyBackspace:
		if msg.Alt { // opt+backspace on macOS
			m.shellCmd = dropWord(m.shellCmd)
			break
		}
		if r := []rune(m.shellCmd); len(r) > 0 {
			m.shellCmd = string(r[:len(r)-1]) // by rune, so multi-byte survives
		}
	case tea.KeyCtrlW:
		m.shellCmd = dropWord(m.shellCmd)
	case tea.KeyCtrlU:
		m.shellCmd = ""
	case tea.KeyRunes, tea.KeySpace:
		// KeySpace is matched explicitly: bubbletea re-types a lone space out of
		// KeyRunes, so a handler that only matched KeyRunes would eat every space.
		m.shellCmd += string(msg.Runes)
	}
	return m, nil
}
```

- [ ] **Step 6: Render the prompt line**

In `internal/tui/view.go`, add above `aiPromptLine` (line ~845):

```go
// shellPromptLine renders the `!` input. The repo name leads it because that is
// the whole point of the line: you can see which folder the command will run in
// before you commit to running it.
func (m Model) shellPromptLine() string {
	return styleGroup.Render(m.shellRepo+" !") + " " + m.shellCmd + "_"
}
```

And in `statusOrFilterLine` (line 826), add as the **first** check:

```go
	if m.shellPrompting {
		return m.shellPromptLine()
	}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestShell -v`
Expected: PASS.

- [ ] **Step 8: Verify the full suite**

Run: `go build ./... && go test ./...`
Expected: all `ok`.

- [ ] **Step 9: Stop and hand off**

Do **not** commit.

---

### Task 4: Prompt history with up / down

Mirrors `historyStep` for `:` exactly, including the draft round-trip: the first `up` stashes what you were typing so `down` can put it back.

**Files:**
- Modify: `internal/tui/update.go` (`handleShellPromptKey`, add `shellHistoryStep`)
- Test: `internal/tui/shell_test.go`

**Interfaces:**
- Consumes: `shellHistory`, `shellHistIdx`, `shellDraft` (Task 2).
- Produces: `(*Model) shellHistoryStep(d int)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/shell_test.go`:

```go
// up walks back through this session's commands and down returns, restoring the
// half-typed draft at the end — up-then-down is always a round trip.
func TestShell_HistoryUpDownRoundTrips(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m.shellHistory = []string{"ls", "git status"}
	m.shellHistIdx = len(m.shellHistory)

	m = press(t, m, "!")
	m = typeIn(t, m, "half")

	up := func() { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp}); m = mm.(Model) }
	down := func() { mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}); m = mm.(Model) }

	up()
	if m.shellCmd != "git status" {
		t.Errorf("first up = %q, want the most recent command", m.shellCmd)
	}
	up()
	if m.shellCmd != "ls" {
		t.Errorf("second up = %q, want the older command", m.shellCmd)
	}
	up() // clamps, no wraparound
	if m.shellCmd != "ls" {
		t.Errorf("up past the oldest = %q, want it to clamp at %q", m.shellCmd, "ls")
	}
	down()
	down()
	if m.shellCmd != "half" {
		t.Errorf("down back to the end = %q, want the stashed draft %q", m.shellCmd, "half")
	}
}

// With no history at all, up and down do nothing.
func TestShell_HistoryEmptyIsANoOp(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m = press(t, m, "!")
	m = typeIn(t, m, "pwd")

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = mm.(Model)
	if m.shellCmd != "pwd" {
		t.Errorf("up with no history changed the line to %q", m.shellCmd)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestShell_History -v`
Expected: FAIL — `first up = "half", want the most recent command`.

- [ ] **Step 3: Add the history walk**

In `internal/tui/update.go`, add below `handleShellPromptKey`:

```go
// shellHistoryStep walks the `!` history: -1 older, +1 newer, both clamped (no
// wraparound). Entering the history stashes the half-typed line in shellDraft so
// walking back to the end restores it — up-then-down is always a round trip.
// Same semantics as historyStep for `:`; kept separate so the two histories
// never bleed into each other.
func (m *Model) shellHistoryStep(d int) {
	if len(m.shellHistory) == 0 {
		return
	}
	if m.shellHistIdx == len(m.shellHistory) {
		m.shellDraft = m.shellCmd
	}
	i := m.shellHistIdx + d
	if i < 0 {
		i = 0
	}
	if i > len(m.shellHistory) {
		i = len(m.shellHistory)
	}
	m.shellHistIdx = i
	if i == len(m.shellHistory) {
		m.shellCmd = m.shellDraft
		return
	}
	m.shellCmd = m.shellHistory[i]
}
```

- [ ] **Step 4: Wire the keys**

In `handleShellPromptKey`, add before `case tea.KeyRunes, tea.KeySpace:`:

```go
	case tea.KeyUp:
		m.shellHistoryStep(-1)
	case tea.KeyDown:
		m.shellHistoryStep(+1)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestShell -v`
Expected: PASS.

- [ ] **Step 6: Stop and hand off**

Run `go build ./... && go test ./...` first (expected: all `ok`). Do **not** commit.

---

### Task 5: `enter` runs the command into the Output pane

**Files:**
- Modify: `internal/tui/update.go` (`handleShellPromptKey` enter case, add `runShellLine`)
- Test: `internal/tui/shell_test.go`

**Interfaces:**
- Consumes: `startShellCmd` (Task 1), `takeOutputPane(outShell, …)` / `killRunning` (Task 2), the prompt state (Task 3).
- Produces: `(m Model) runShellLine() (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/shell_test.go`:

```go
// enter takes the Output pane, focuses it, echoes the command with its repo, and
// returns a run command.
func TestShell_EnterRunsIntoTheOutputPane(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	repo := m.repos[0].repo.Name

	m = press(t, m, "!")
	m = typeIn(t, m, "echo hi")
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	if cmd == nil {
		t.Fatal("enter should return a run command")
	}
	if m.shellPrompting || m.shellCmd != "" {
		t.Error("enter should close the prompt and clear the line")
	}
	if m.focus != panelBottom || m.bottomView != bvOutput {
		t.Errorf("enter should focus the Output view, got focus=%d view=%d", m.focus, m.bottomView)
	}
	if !m.outputRunning || m.outputKind != outShell {
		t.Errorf("enter should mark a shell run, running=%v kind=%d", m.outputRunning, m.outputKind)
	}
	echo := repo + " $ echo hi"
	if len(m.outputLines) != 1 || stripANSI(m.outputLines[0]) != echo {
		t.Errorf("the pane should open with the echoed command %q, got %v", echo, m.outputLines)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "7 Output*") {
		t.Error("a running shell command should mark the Output tab")
	}
	if n := len(m.shellHistory); n != 1 || m.shellHistory[0] != "echo hi" {
		t.Errorf("enter should record the command in history, got %v", m.shellHistory)
	}
}

// An empty line does nothing at all — no run, no history entry.
func TestShell_EnterOnAnEmptyLineDoesNothing(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	m = press(t, m, "!")
	m = typeIn(t, m, "   ")

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd != nil {
		t.Error("an empty command should not run anything")
	}
	if m.outputRunning {
		t.Error("an empty command should not take the Output pane")
	}
	if len(m.shellHistory) != 0 {
		t.Errorf("an empty command should not enter history, got %v", m.shellHistory)
	}
	if m.shellPrompting {
		t.Error("enter should still close the prompt")
	}
}

// Repeating a command does not add a duplicate history entry.
func TestShell_HistorySkipsAnImmediateRepeat(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)
	for i := 0; i < 2; i++ {
		m = press(t, m, "!")
		m = typeIn(t, m, "pwd")
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mm.(Model)
	}
	if len(m.shellHistory) != 1 {
		t.Errorf("an immediate repeat should not duplicate, got %v", m.shellHistory)
	}
}

// End to end against a real bash: the streamed lines land in the pane.
func TestShell_RealCommandStreamsIntoThePane(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	m = press(t, m, "!")
	m = typeIn(t, m, "echo alpha-line")
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	// tea.Batch returns an opaque cmd; drive the runner directly instead.
	_ = cmd
	lines, err := drainScript(t, startShellCmd("echo alpha-line", m.shellDir, m.outputRun))
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if len(lines) != 1 || lines[0] != "alpha-line" {
		t.Errorf("want [alpha-line], got %v", lines)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestShell_Enter|TestShell_HistorySkips|TestShell_Real' -v`
Expected: FAIL — `enter should return a run command`.

- [ ] **Step 3: Implement the runner**

In `internal/tui/update.go`, add below `shellHistoryStep`:

```go
// runShellLine runs what was typed at `!` in the repo the prompt was bound to.
// There is no confirm and no blocklist: `!` is an explicit shell escape, the way
// vim's `:!` is, and a y/N on every `git status` would make it useless. What it
// does instead is leave a record — the command is echoed into the pane, with the
// repo it ran in, before a single line of output arrives.
func (m Model) runShellLine() (tea.Model, tea.Cmd) {
	line := strings.TrimSpace(m.shellCmd)
	m.shellPrompting = false
	m.shellCmd = ""
	if line == "" {
		return m, nil
	}
	if m.shellDir == "" {
		return m, m.setStatus(styleOrange.Render("no repo selected"))
	}
	if n := len(m.shellHistory); n == 0 || m.shellHistory[n-1] != line {
		m.shellHistory = append(m.shellHistory, line)
	}
	m.shellHistIdx = len(m.shellHistory)

	echo := m.shellRepo + " $ " + line
	m.takeOutputPane(outShell, echo)
	m.setBottomView(bvOutput)
	m.appendOutput(styleTitle.Render(echo))
	// A shell line can do anything to any repo, so the Repos pane follows it live
	// exactly as it follows a script.
	return m, tea.Batch(startShellCmd(line, m.shellDir, m.outputRun), m.startRepoProbe())
}
```

- [ ] **Step 4: Wire enter**

In `handleShellPromptKey`, add before `case tea.KeyUp:`:

```go
	case tea.KeyEnter:
		return m.runShellLine()
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestShell -v`
Expected: PASS.

- [ ] **Step 6: Verify by hand**

Run `go build -o manygit . && ./manygit`, press `!`, type `git status -sb`, press enter. Confirm: the bottom bar showed the repo name while typing; the pane opens with `<repo> $ git status -sb` in the accent colour; output streams; the tab reads `7 Output*` while running then `7 Output`; the status line reports `<repo>: exited 0`.

- [ ] **Step 7: Stop and hand off**

Run `go build ./... && go test ./...` (expected: all `ok`). Do **not** commit.

---

### Task 6: Cancelling a running command

`esc` is deliberately scoped to "the Output pane is focused". Its existing job is "back out one layer, innermost first", and if a long command were running in the background while you were reading a diff, an unscoped `esc` would kill the command instead of closing the diff. `ctrl+c` is unscoped because that is what it means in a shell — but `q` still always quits, so you are never trapped.

**Files:**
- Modify: `internal/tui/update.go:663` (`ctrl+c`), `internal/tui/update.go:826-853` (`esc`), `internal/tui/view.go:453-470` (`bottomHint`)
- Test: `internal/tui/shell_test.go`

**Interfaces:**
- Consumes: `killRunning`, `shellCancel` (Task 2).
- Produces: nothing new.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tui/shell_test.go`:

```go
// esc kills a running command when the Output pane is the thing you are looking
// at, and the pane says so.
func TestShell_EscCancelsARunningCommand(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellRepo = "alpha"
	m.setBottomView(bvOutput)
	m.shellCancel = func() { called = true }

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if !called {
		t.Error("esc should kill the running command")
	}
	if !m.shellKilled {
		t.Error("esc should mark the run as killed so done reports 'cancelled'")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "esc: cancel") {
		t.Error("the Output pane should advertise esc: cancel while running")
	}
}

// esc must NOT reach past a diff to kill a background command: its normal
// back-out-one-layer job wins when you are looking at something else.
func TestShell_EscDoesNotCancelFromAnotherPane(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellCancel = func() { called = true }
	m.setBottomView(bvChanges)
	m.changeShowDiff = true

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if called {
		t.Error("esc from the Changes diff must close the diff, not kill the command")
	}
	if m.changeShowDiff {
		t.Error("esc should still have closed the diff")
	}
}

// ctrl+c kills a running command instead of quitting; with nothing running it
// still quits.
func TestShell_CtrlCCancelsThenQuits(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellRepo = "alpha"
	m.shellCancel = func() { called = true }

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = mm.(Model)
	if !called {
		t.Error("ctrl+c should kill the running command")
	}
	if cmd != nil {
		t.Error("ctrl+c must not quit while a command is running")
	}

	m.outputRunning = false
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("with nothing running, ctrl+c should still quit")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestShell_Esc|TestShell_CtrlC' -v`
Expected: FAIL — `esc should kill the running command`.

- [ ] **Step 3: Add the `ctrl+c` guard**

In `internal/tui/update.go`, replace `case "q", "ctrl+c":` at line 663 with:

```go
	case "ctrl+c":
		// Shell-like: while something is running, ctrl+c stops it rather than
		// quitting. `q` always quits, so this can never trap anyone.
		if m.outputRunning && m.shellCancel != nil {
			m.killRunning()
			return m, m.setStatus(styleOrange.Render(m.shellRepo + ": cancelled"))
		}
		return m, tea.Quit
	case "q":
```

Keep whatever body followed the original case attached to `case "q":`.

- [ ] **Step 4: Add the `esc` case**

In `internal/tui/update.go`, inside the `case "esc":` switch (line 830), add as the **first** branch — the switch's order is the nesting, and a running command is the innermost layer:

```go
		case m.outputRunning && m.shellCancel != nil &&
			m.focus == panelBottom && m.bottomView == bvOutput:
			// Scoped to the Output pane on purpose: esc's job everywhere else is
			// "back out one layer", and killing a background command from inside a
			// diff would be a nasty surprise. ctrl+c is the unscoped one.
			m.killRunning()
			return m, m.setStatus(styleOrange.Render(m.shellRepo + ": cancelled"))
```

- [ ] **Step 5: Advertise it in the pane hint**

In `internal/tui/view.go`, inside `bottomHint`'s switch (line 458), add as the **first** case:

```go
	case m.bottomView == bvOutput && m.outputRunning && m.shellCancel != nil:
		h = "esc: cancel"
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run TestShell -v`
Expected: PASS.

- [ ] **Step 7: Verify by hand**

Run `./manygit`, press `!`, type `sleep 30`, enter, then `esc`. Expect: status reads `<repo>: cancelled`, the `Output*` marker clears, and `ps aux | grep sleep` shows nothing left behind. Repeat with `sleep 30 | cat` to confirm the process group is killed.

- [ ] **Step 8: Stop and hand off**

Run `go build ./... && go test ./...` (expected: all `ok`). Do **not** commit.

---

### Task 7: In-app key reference and footer

Mirror 1 of 7, plus the always-visible footer strip.

**Files:**
- Modify: `internal/tui/view.go:810-823` (`footer`), `internal/tui/view.go:1212` (`keysColumns` left column)
- Test: `internal/tui/shell_test.go`

**Interfaces:** none new.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/shell_test.go`:

```go
// `!` must appear in the always-visible footer and in the ? keys reference,
// or the feature is undiscoverable.
func TestShell_IsDocumentedInTheApp(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 100, 30)

	if got := stripANSI(m.footer()); !strings.Contains(got, "! shell") {
		t.Errorf("the footer should advertise ! shell, got %q", got)
	}

	left, right := m.keysColumns()
	joined := stripANSI(strings.Join(append(left, right...), "\n"))
	if !strings.Contains(joined, "!") {
		t.Error("the ? keys reference should list !")
	}
	if !strings.Contains(joined, "shell") {
		t.Error("the ? keys reference should describe ! as a shell")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestShell_IsDocumented -v`
Expected: FAIL — `the footer should advertise ! shell`.

- [ ] **Step 3: Update the footer**

In `internal/tui/view.go:821-822`, change the footer string so `! shell` sits next to `: ai`:

```go
	return styleDim.Render(
		enter + " | z zoom | g graph | n news | t tags | F changed | s sync | p push | d/D discard | o open | r refetch | ! shell | : ai | ? help | q quit")
```

- [ ] **Step 4: Add the keys row**

In `internal/tui/view.go`, in `keysColumns`'s `left` slice, insert immediately after the `kr(":", …)` line (line 1212):

```go
		kr("!", "shell in the > repo — esc cancels, up/down recalls"),
```

The key label `!` is 1 cell, far inside the `Width(10)` column.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestShell|TestTUI_KeyColumn' -v`
Expected: PASS, including the pre-existing `TestTUI_KeyColumnFitsEveryLabel`.

- [ ] **Step 6: Verify by hand at the minimum width**

Run `./manygit` in an 80-column terminal, press `?`. Confirm the `!` row renders on one line and nothing wraps mid-word.

- [ ] **Step 7: Stop and hand off**

Run `go build ./... && go test ./...` (expected: all `ok`). Do **not** commit.

---

### Task 8: Written documentation — keys and safety

Mirrors 2, 3 and 4, plus the three **Safety** sections. Those currently say the only directly-offered destructive action is `d`/`D`, "which always asks first" — an unguarded `!` makes that false, and correcting it is not optional.

The existing "never through a shell" claims are all scoped to what the **AI harness** may produce and stay true: `!` is the user typing, not the model. Do **not** delete them.

**Files:**
- Modify: `README.md:93-122` (keys), `README.md:196-204` (safety)
- Modify: `docs/index.html:484-613` (keys), `docs/index.html:788-800` (safety)
- Modify: `docs/llms.txt:88-118` (keys), `docs/llms.txt:159-171` (safety)

**Interfaces:** none.

- [ ] **Step 1: Add the `!` row to the README key table**

In `README.md`, insert a new row immediately after the `` `:` `` row (line 117):

```markdown
| `!` | **shell in the highlighted repo** — type a bash command and it runs there, with its output streaming into the Output pane. The bottom bar shows which repo you are in while you type. `esc` (or `ctrl+c`) cancels a running command; `↑`/`↓` recall this session's earlier commands; `alt+backspace` / `ctrl+w` delete a word, `ctrl+u` the line. Runs non-interactively, so `vim`, `top` and anything else needing a terminal will not work |
```

- [ ] **Step 2: Correct the README safety paragraph**

In `README.md:195-196`, the sentence currently reads *"The only destructive action it offers directly is discarding a repo's changes (`d` / `D`), which always asks you to confirm first."* Replace that sentence with:

```markdown
The only destructive action it offers *on its own* is discarding a repo's changes
(`d` / `D`), which always asks you to confirm first. `!` is the exception, by
design: it runs whatever bash command you type in the highlighted repo, with no
confirm and no blocklist — it is a shell escape, and it does exactly what you
typed. The command is echoed into the Output pane, with the repo it ran in, so
there is always a record.
```

- [ ] **Step 3: Add the `!` row to the site key table**

In `docs/index.html`, insert immediately after the `:` row (which ends at line 596):

```html
              <tr>
                <td class="kt__k"><b>!</b></td>
                <td>
                  Shell in the highlighted repo. Type a bash command — <i>git status -sb</i>,
                  <i>npm test</i> — and it runs there, streaming into the Output pane. The
                  bottom bar shows which repo you are in while you type, and
                  <b>esc</b> cancels a running command. Runs non-interactively, so
                  <code>vim</code> and <code>top</code> will not work
                </td>
              </tr>
```

- [ ] **Step 4: Correct the site safety section**

In `docs/index.html:788-792`, the paragraph ends *"…and the one destructive action it offers directly is discarding a repo's changes with <b>d</b> / <b>D</b>, which always asks first."* Replace that trailing clause with:

```html
              and the one destructive action it offers on its own is discarding a repo's
              changes with <b>d</b> / <b>D</b>, which always asks first. <b>!</b> is the
              deliberate exception: it runs the bash command you type, in the highlighted
              repo, with no confirm and no blocklist — a shell escape does what you typed.
              The command is echoed into the Output pane with the repo it ran in, so there
              is a record of it.
```

- [ ] **Step 5: Add the `!` bullet to `llms.txt`**

In `docs/llms.txt`, insert after the `:` entry (which ends at line 109):

```
- `!` — shell in the highlighted repo: type a bash command and it runs there, with its
  combined stdout and stderr streaming into the Output pane. The bottom bar shows the
  repo name while you type, so it is always clear which folder the command lands in.
  `esc` or `ctrl+c` cancels a running command, `up`/`down` recall this session's earlier
  commands. No confirm and no blocklist — it is a shell escape and runs exactly what you
  typed. Non-interactive (no TTY), so `vim`, `top` and `sudo` prompts do not work.
```

- [ ] **Step 6: Correct the `llms.txt` safety section**

In `docs/llms.txt:162-164`, replace *"The only destructive action it offers directly is discarding a repo's changes (`d` / `D`), which always asks first."* with:

```
The only destructive action it offers on its own is discarding a repo's changes
(`d` / `D`), which always asks first. `!` is the deliberate exception: it runs the
bash command you type in the highlighted repo, with no confirm and no blocklist,
because a shell escape that second-guessed you would be useless. The command is
echoed into the Output pane with the repo it ran in, so there is a record.
```

- [ ] **Step 7: Verify the four key tables agree**

Run:

```bash
grep -n '`!`' README.md docs/llms.txt
grep -n 'kt__k"><b>!' docs/index.html
grep -n 'kr("!"' internal/tui/view.go
```

Expected: at least one hit in each of the four files.

- [ ] **Step 8: Check the page still renders**

Run `cd docs && python3 -m http.server 8765`, open `http://localhost:8765`, and confirm the Keys table shows the `!` row and the page has no horizontal overflow at 320px wide.

- [ ] **Step 9: Stop and hand off**

Do **not** commit.

---

### Task 9: Browser demo port, disclosure, and the divergence count

`CLAUDE.md` requires the demo to mirror the TUI. A browser has no bash, so `!` joins `q`, `o`, `esc` and `:` as an intentional divergence — the **fifth**. `CLAUDE.md` says "exactly four places" and numbers them 1-4; that count is load-bearing prose and becomes stale.

Everything *around* the fake must be real and ported, exactly as it is for `:`: the input line, the repo binding, history, the echo, the streaming, the `Output*` marker and `esc` to cancel.

**Files:**
- Modify: `docs/assets/demo.js` (state ~line 351, `statusOrFilter` ~1022, `keysBody` ~1157, `handleKey` ~2050 and ~2209)
- Modify: `docs/index.html:308-312` (the on-page disclosure)
- Modify: `CLAUDE.md` (divergence list 4 → 5)

**Interfaces:**
- Consumes: `takeOutputPane` (demo.js:1575), `curRepo` (511), `setStatus` (603), `announce` (1149), `d`/`gp`/`yl` style helpers.
- Produces: `cannedShell(cmd, repo)`, `shellPromptLine()`, `handleShellPromptKey(k)`, `runShellLine()`.

- [ ] **Step 1: Add the state**

In `docs/assets/demo.js`, after the `aiHistory` line (~353), add:

```js
    // `!` shell mode. The demo's FIFTH intentional divergence: a browser has no
    // shell, so a handful of commands answer from a script and everything else
    // says so plainly. The page says the shell is fake, exactly as it says the
    // git is fake. Everything around it is real and ported: the repo binding,
    // the input, the history, the echoed command, the streaming, and esc.
    shellPrompting: false, shellCmd: "", shellRepo: "",
    shellHistory: [], shellHistIdx: 0, shellDraft: "", shellRunning: false,
```

- [ ] **Step 2: Add the canned shell and the prompt line**

Add above `handleKey` (~line 2045):

```js
  /* -- `!` shell mode (canned) --------------------------------------------
     Port of internal/tui/update.go handleShellPromptKey + runShellLine. The ONE
     thing that is faked is the command's output, because a browser has no bash
     — the fifth intentional divergence, and the page says so on screen. */

  function cannedShell(cmd, repo) {
    var c = cmd.trim();
    if (/^git\s+status/.test(c)) return ["## main...origin/main", " M internal/tui/update.go", "?? notes.txt"];
    if (/^git\s+log/.test(c)) return ["a1b2c3d  wire the shell pane", "d4e5f6a  fix the tab bar"];
    if (/^git\s+branch/.test(c)) return ["* main", "  feat/shell"];
    if (/^ls(\s|$)/.test(c)) return ["README.md", "go.mod", "internal", "main.go"];
    if (/^pwd$/.test(c)) return ["~/code/" + repo];
    if (/^echo\s+/.test(c)) return [c.replace(/^echo\s+/, "")];
    return null;
  }

  function shellPromptLine() {
    return gp(S.shellRepo + " !") + " " + esc(S.shellCmd) + "_";
  }

  function shellHistoryStep(n) {
    if (!S.shellHistory.length) return;
    if (S.shellHistIdx === S.shellHistory.length) S.shellDraft = S.shellCmd;
    var i = Math.min(Math.max(S.shellHistIdx + n, 0), S.shellHistory.length);
    S.shellHistIdx = i;
    S.shellCmd = i === S.shellHistory.length ? S.shellDraft : S.shellHistory[i];
  }

  function handleShellPromptKey(k) {
    if (k === "Escape") { S.shellPrompting = false; S.shellCmd = ""; return; }
    if (k === "Enter") { runShellLine(); return; }
    if (k === "Backspace") { S.shellCmd = S.shellCmd.slice(0, -1); return; }
    if (k === "ArrowUp") { shellHistoryStep(-1); return; }
    if (k === "ArrowDown") { shellHistoryStep(1); return; }
    if (k.length === 1) S.shellCmd += k;
  }

  function runShellLine() {
    var line = S.shellCmd.trim(), repo = S.shellRepo;
    S.shellPrompting = false;
    S.shellCmd = "";
    if (!line) return;
    if (!S.shellHistory.length || S.shellHistory[S.shellHistory.length - 1] !== line) {
      S.shellHistory.push(line);
    }
    S.shellHistIdx = S.shellHistory.length;

    var echo = repo + " $ " + line;
    takeOutputPane(echo);
    S.shellRunning = true;
    S.focus = "bottom";
    S.bottomView = "output";
    var run = S.outputRun;
    var body = cannedShell(line, repo);
    // RAW strings, never HTML: renderOutput colours by regex on the raw line and
    // escapes everything else. Pushing markup here would render it literally.
    var lines = [echo].concat(
      body ? body : ["this demo has no shell — try git status, ls, pwd or echo"]
    );
    var i = 0;
    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    (function step() {
      if (run !== S.outputRun) return; // superseded
      if (i >= lines.length) {
        S.outputRunning = false;
        S.shellRunning = false;
        setStatus(body ? gr(repo + ": exited 0") : d(repo + ": nothing ran — the shell is faked here"));
        render();
        return;
      }
      var n = reduce ? lines.length : 1;
      for (var k = 0; k < n && i < lines.length; k++, i++) {
        var atBottom = S.outputOffset >= S.outputLines.length - 1;
        S.outputLines.push(lines[i]);
        if (atBottom) S.outputOffset = S.outputLines.length - 1;
      }
      render();
      setTimeout(step, reduce ? 0 : 55);
    })();
  }
```

- [ ] **Step 3: Teach `renderOutput` to accent the echoed command**

The Go stores pre-styled lines (`styleTitle.Render(echo)`); the demo stores raw
text and colours it by regex at render time. The existing rule accents lines
starting with `$`, which `runScript` satisfies (`"$ " + title`) but the shell
echo (`alpha $ git status`) does not. In `docs/assets/demo.js:952`, widen it:

```js
      var cls = /^(\S+\s)?\$\s/.test(l) ? "a"
```

Leave the remaining branches untouched.

- [ ] **Step 4: Wire the prompt into the status line and the modal chain**

In `statusOrFilter()` (~line 1022), add as the **first** check:

```js
    if (S.shellPrompting) return shellPromptLine();
```

In the same function's footer string, add `! shell` before `: ai`:

```js
    return d(enter + " | z zoom | g graph | n news | t tags | F changed | s sync | p push | d/D discard | o open | r refetch | ! shell | : ai | ? help | q quit");
```

In `handleKey` (~line 2050), add as the **first** guard, above `S.aiPrompting`:

```js
    if (S.shellPrompting) { handleShellPromptKey(k); return; }
```

- [ ] **Step 5: Add the `!` case and esc-cancel**

In `handleKey`'s switch, add immediately before `case ":":` (~line 2209):

```js
      case "!": {
        var sr = curRepo();
        if (!sr) { setStatus(d("no repo selected")); break; }
        S.shellPrompting = true; S.shellCmd = ""; S.shellRepo = sr.n;
        S.shellHistIdx = S.shellHistory.length;
        announce("Shell prompt open in " + sr.n + ". Type a command, enter runs it.");
        break;
      }
```

In the existing `case "Escape":` handling of the main switch, add a cancel branch as the first condition (matching the Go's scoping to a focused, running Output pane):

```js
        if (S.shellRunning && S.focus === "bottom" && S.bottomView === "output") {
          S.outputRun++; // supersede the streaming timer
          S.outputRunning = false; S.shellRunning = false;
          S.outputLines.push("— cancelled —"); // raw, per Step 3
          setStatus(og(S.shellRepo + ": cancelled"));
          break;
        }
```

`og` is `demo.js`'s orange helper (line 434) — the counterpart of the Go's
`styleOrange`. `setStatus` takes HTML, unlike `outputLines`, which takes raw text.

- [ ] **Step 6: Add the keys-overlay row**

In `keysBody`'s `left` array (~line 1157), add next to the other prompt keys:

```js
      kr("!", "shell in the > repo — esc cancels, up/down recalls"),
```

- [ ] **Step 7: Update the on-page disclosure**

In `docs/index.html:309-312`, replace the sentence with:

```html
            <span
              >A working copy of the interface, running here. The git is fake, and
              <b>:</b>'s AI reply and <b>!</b>'s shell output are scripted — the keys
              are the real keys.</span
            >
```

- [ ] **Step 8: Update the divergence count in `CLAUDE.md`**

In `CLAUDE.md`, change *"The demo intentionally diverges in exactly four places"* to *"exactly five places"* and append a fifth numbered item:

```markdown
  5. **`!` prints canned output instead of running a shell.** A browser has no
     bash, so `cannedShell()` answers a few command shapes and everything else
     says the shell is faked. Everything around it is real and ported: the repo
     binding, the input line, the history, the echoed `<repo> $ <cmd>`, the
     streaming into the Output pane, and `esc` to cancel. The page says the
     shell output is scripted, the same way it says the git is fake — if you
     extend `cannedShell`, keep that label true.
```

Also update the "FOUR mirrors of the key table" note to record that `view.go:footer()` and the three Safety sections move with a key change too.

- [ ] **Step 9: Check the demo's syntax**

Run: `node --check docs/assets/demo.js`
Expected: no output (valid).

- [ ] **Step 10: Drive the demo by hand**

Run `cd docs && python3 -m http.server 8765`, open `http://localhost:8765`, click the terminal, then:
- Press `!` — the bottom bar shows `<repo> !` with a cursor.
- Type `git status -sb`, press enter — the pane echoes `<repo> $ git status -sb` and streams the canned lines; the tab reads `7 Output*` then `7 Output`.
- Press `!`, `↑` — the previous command comes back.
- Press `!`, type `sleep 30`, enter, then `esc` — the run cancels.
- Press `!`, type `xyzzy`, enter — the pane says the demo has no shell.
- Press `?` — the `!` row is listed.

Confirm: no console errors, and no horizontal overflow at 320px.

- [ ] **Step 11: Stop and hand off**

Run the full verification set:

```bash
go build ./... && go test ./... && node --check docs/assets/demo.js
```

Expected: all packages `ok`, demo valid. Do **not** commit — report what changed and let the user review.

---

## Self-Review

**1. Spec coverage** — every decision from Global Constraints maps to a task:

| Decision | Task |
|---|---|
| Capture into `[7 Output]`, not a TUI-suspending passthrough | 1, 5 |
| Highlighted repo only | 3, 5 |
| Repo name visible while typing **and** in the output | 3 (prompt line), 5 (echo) |
| `esc` / `ctrl+c` cancel | 1 (kill switch), 2 (adoption), 6 (keys) |
| No confirm, no blocklist | 5 (`runShellLine` — explicit, with the echo as the record) |
| Seven documentation mirrors | 7 (in-app + footer), 8 (README/html/llms + 3 safety), 9 (CLAUDE.md) |
| Demo mirrors the TUI, divergence disclosed | 9 |

**2. Placeholder scan** — no `TBD`, no "add error handling", no "similar to Task N". Every code step carries the actual code. Every helper named in the demo tasks was resolved against `docs/assets/demo.js` while writing this plan and verified to exist: `d` (429), `gr` (430), `og` (434), `gp` (436), `ti` (438, the `styleTitle` counterpart), `sp` (428), `esc` (423), `curRepo` (511), `setStatus` (603), `announce` (1149), `takeOutputPane` (1575). Nothing is left for the implementer to guess.

**3. Type consistency** — checked across tasks:
- `takeOutputPane(kind outputKind, title string)` is defined in Task 2 and called with that signature in Task 2 (3 existing sites) and Task 5.
- `runStartMsg{run, cancel, scanner}` is defined in Task 1 and consumed in Task 1's test helper and Task 2's handler.
- `startShellCmd(line, dir string, run int) tea.Cmd` — defined Task 1, called Task 5.
- `exitText(err error) string` — defined Task 1, used Task 2.
- `killRunning()` — defined Task 2, used Task 2 (`takeOutputPane`) and Task 6 (both keys).
- `shellHistoryStep(d int)` — defined Task 4, wired Task 4.
- `outShell` / `outScript` / `outAI` used consistently.
- `m.shellRepo` is read by the done-branch in Task 2 and set in Task 3; Task 2's test sets it directly so the tasks stay independently testable.

**Known ordering constraint:** Task 1 leaves live script runs stalled (nothing handles `runStartMsg` until Task 2). Tasks 1 and 2 must land together before any manual run. This is called out in Task 1 Step 8.

---

## Out of scope — a pre-existing demo bug found while planning

**Not part of this feature. Do not fix it inside these tasks.** Recorded because
it was discovered while tracing `renderOutput`, and because Task 9 Step 3 has to
work around it.

`docs/assets/demo.js` uses two incompatible conventions for `S.outputLines`:

- `runScript` (line 1612) pushes **raw text**.
- The `:` harness path (lines 1892, 1900, 1903, 1905-1906, 1911, 1914, 1918, 1921, 1933, 1954) pushes **HTML** from `d()`, `gp()`, `rd()`, `yl()`, `gr()`.

`renderOutput` (line 960) resolves a colour class by regex against the raw line
and falls back to `esc(l)`. HTML that matches no regex is therefore escaped and
shown literally. Verified:

```
input : d("asking claude... (scripted in this demo)")
cls   : ""
output: &lt;span class=&quot;d&quot;&gt;asking claude... (scripted in this demo)&lt;/span&gt;
```

So pressing `:` on the live page prints `<span class="d">asking claude...
(scripted in this demo)</span>` as visible text instead of dim grey, and the same
applies to the plan transcript and the `[y/N]` confirm line.

The Go does not have this problem — it stores pre-styled lines and
`renderOutputView` wraps them with lipgloss, which is ANSI-aware.

**Suggested fix (separate change):** pick one convention. The smaller edit is to
let `renderOutput` pass a line through unescaped when it is already markup, but
the cleaner fix — and the one that matches the Go — is to keep `outputLines` raw
and move all colouring into `renderOutput`'s regex table. Either way it wants its
own before/after check on the page, which is why it is not bundled here.

This plan's Task 9 sidesteps the bug entirely by pushing **raw** strings and
extending the colour regex instead.
