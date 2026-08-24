package tui

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/rabeeh-ta/manygit/internal/discover"
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

// drainToEOF pumps a run's reader until EOF, reporting whether it got there
// before the deadline. Used to prove a cancel actually stopped the process.
func drainToEOF(st runStartMsg, within time.Duration) bool {
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
		return true
	case <-time.After(within):
		return false
	}
}

// shellModel is a loaded two-repo Model, the starting point for every prompt
// test here. Keystrokes go through the package's existing key()/typeIn() helpers
// (ai_test.go), which already send a lone space as KeySpace the way bubbletea does.
func shellModel(t *testing.T) Model {
	t.Helper()
	cfg, repos := twoRepos(t)
	return loadAll(t, New(cfg, "", repos, nil), 100, 30)
}

/* -- Task 1: the streaming core ------------------------------------------- */

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
	if !drainToEOF(st, 10*time.Second) {
		t.Fatal("cancel did not stop a silent command")
	}
}

// Cancelling kills the whole process group, not just bash: a pipeline's
// grandchildren must not outlive the run.
func TestShellCancelKillsTheProcessGroup(t *testing.T) {
	st := startRun(t, startShellCmd("sleep 30 | cat", t.TempDir(), 0))
	st.cancel()
	if !drainToEOF(st, 10*time.Second) {
		t.Fatal("cancel left the pipeline's grandchildren running")
	}
}

// A command that cannot start reports the failure instead of hanging.
func TestShellStartFailureIsReported(t *testing.T) {
	if _, err := drainScript(t, startShellCmd("echo hi", filepath.Join(t.TempDir(), "nope"), 0)); err == nil {
		t.Error("a bad working directory should surface as an error")
	}
}

/* -- Task 2: pane ownership ------------------------------------------------ */

// The Update loop must adopt the kill switch from runStartMsg and then start
// pumping the reader.
func TestShell_UpdateAdoptsTheCancelFunc(t *testing.T) {
	m := shellModel(t)
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
	m := shellModel(t)
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
	m := shellModel(t)

	m.takeOutputPane(outShell, "alpha $ false")
	m.shellLoc = "grp/alpha"
	mm, _ := m.Update(scriptOutMsg{run: m.outputRun, done: true, err: &exec.ExitError{}})
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
	m := shellModel(t)

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

/* -- Task 3: the prompt ---------------------------------------------------- */

// `!` opens the prompt bound to the repo under the cursor, and the bottom bar
// shows that repo's name so you can see which folder you are about to run in.
func TestShell_BangOpensPromptBoundToTheCursorRepo(t *testing.T) {
	m := shellModel(t)

	m = key(m, "!")
	if !m.shellPrompting {
		t.Fatal("! should open the shell prompt")
	}
	// twoReposIn puts the repos at <root>/grp/<name>, so the location the prompt
	// shows is the group-qualified path, not the bare name.
	want := shellLocation(m.repos[0].repo)
	if want != "grp/"+m.repos[0].repo.Name {
		t.Fatalf("fixture assumption broken: shellLocation = %q", want)
	}
	if m.shellLoc != want {
		t.Errorf("prompt bound to %q, want the cursor repo %q", m.shellLoc, want)
	}
	if m.shellDir != m.repos[0].repo.Path {
		t.Errorf("prompt bound to dir %q, want %q", m.shellDir, m.repos[0].repo.Path)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, shellPromptLead+want) {
		t.Errorf("the bottom bar must show %q while typing; view had none", shellPromptLead+want)
	}
}

// Typing lands in the command, and the prompt renders it after the repo name.
func TestShell_TypingShowsInThePromptLine(t *testing.T) {
	m := shellModel(t)
	m = key(m, "!")
	m = typeIn(m, "git status -sb")

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
	m := shellModel(t)
	m = key(m, "!")
	m = typeIn(m, "grep -n ? x")

	if m.showHelp || m.showGraph || m.showNews {
		t.Error("? and g must not open an overlay while the shell prompt is up")
	}
	if m.shellCmd != "grep -n ? x" {
		t.Errorf("shellCmd = %q, want the literal typed text", m.shellCmd)
	}
}

// esc abandons the prompt without running anything, and lands on Repos (pane 1).
func TestShell_EscLeavesTheShellAndLandsOnRepos(t *testing.T) {
	m := shellModel(t)
	m.focus = panelBottom // somewhere other than Repos, so the landing is observable
	m = key(m, "!")
	m = typeIn(m, "rm -rf /")

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.shellPrompting || m.shellCmd != "" {
		t.Error("esc should leave the shell and clear the line")
	}
	if m.outputRunning {
		t.Error("esc must not run the command")
	}
	if m.focus != panelRepos {
		t.Errorf("esc should land on Repos (pane 1), got focus=%d", m.focus)
	}
}

// esc leaves the shell but does NOT kill a command that is still running — that
// is ctrl+c's job. "Only get out of this mode."
func TestShell_EscLeavingDoesNotCancelTheRunningCommand(t *testing.T) {
	m := shellModel(t)
	m = key(m, "!")
	m = typeIn(m, "sleep 30")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	var called bool
	m.shellCancel = func() { called = true }

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if called {
		t.Error("esc must not kill the running command — only leave the shell")
	}
	if !m.outputRunning {
		t.Error("the command should still be running after esc")
	}
	if m.shellPrompting {
		t.Error("esc should have left the shell")
	}
	if m.focus != panelRepos {
		t.Errorf("esc should land on Repos (pane 1), got focus=%d", m.focus)
	}
}

// The prompt names where you are, relative to the scanned root. "(root)" is a
// Repos-pane label, not a path segment, so a top-level repo shows just its name.
func TestShell_LocationIsRootRelative(t *testing.T) {
	for _, c := range []struct{ group, name, want string }{
		{"apps", "api-gateway", "apps/api-gateway"},
		{"(root)", "dotfiles", "dotfiles"},
		{"", "loose", "loose"},
	} {
		if got := shellLocation(discover.Repo{Group: c.group, Name: c.name}); got != c.want {
			t.Errorf("shellLocation(%q, %q) = %q, want %q", c.group, c.name, got, c.want)
		}
	}
}

// A narrow terminal shortens the path from the LEFT, so the repo you are in
// stays readable. It must never be shortened from the right.
func TestShell_LocationShortensFromTheLeft(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"apps/api-gateway", 40, "apps/api-gateway"}, // fits: untouched
		{"apps/api-gateway", 16, "apps/api-gateway"}, // exactly fits
		{"apps/api-gateway", 12, "…/api-gateway"[1:]},
		{"apps/api-gateway", 1, "…"},
		{"apps/api-gateway", 0, ""},
	} {
		got := trimLeftTo(c.in, c.max)
		if len([]rune(got)) > c.max {
			t.Errorf("trimLeftTo(%q, %d) = %q — longer than the budget", c.in, c.max, got)
		}
		if c.max >= len([]rune(c.in)) && got != c.in {
			t.Errorf("trimLeftTo(%q, %d) = %q — should be untouched when it fits", c.in, c.max, got)
		}
		if c.max > 1 && c.max < len([]rune(c.in)) {
			if !strings.HasPrefix(got, "…") {
				t.Errorf("trimLeftTo(%q, %d) = %q — a cut must be marked with a leading …", c.in, c.max, got)
			}
			if !strings.HasSuffix(c.in, strings.TrimPrefix(got, "…")) {
				t.Errorf("trimLeftTo(%q, %d) = %q — must keep the TAIL of the path", c.in, c.max, got)
			}
		}
	}
}

// The location budget comes from the terminal width alone. If it depended on
// what had been typed, the prompt would resize under the cursor mid-command.
func TestShell_LocationBudgetIgnoresWhatYouTyped(t *testing.T) {
	m := shellModel(t)
	m = key(m, "!")

	widthOf := func(mm Model) int {
		line := stripANSI(mm.shellPromptLine())
		return strings.Index(line, " ") // the lead+location run up to the first space
	}
	before := widthOf(m)
	m = typeIn(m, "git log --oneline --graph --decorate --all")
	if after := widthOf(m); after != before {
		t.Errorf("the prompt resized while typing: %d -> %d cells", before, after)
	}

	// Wider terminal, no less room for the path.
	narrow, wide := shellLocBudget(80), shellLocBudget(120)
	if wide < narrow {
		t.Errorf("a wider terminal should not shrink the path budget: 80=%d 120=%d", narrow, wide)
	}
	if shellLocBudget(80) < 14 {
		t.Errorf("at the documented 80-col minimum, %q must still fit, budget=%d",
			"apps/api-gateway", shellLocBudget(80))
	}
}

/* -- Task 4: history ------------------------------------------------------- */

// up walks back through this session's commands and down returns, restoring the
// half-typed draft at the end — up-then-down is always a round trip.
func TestShell_HistoryUpDownRoundTrips(t *testing.T) {
	m := shellModel(t)
	m.shellHistory = []string{"ls", "git status"}
	m.shellHistIdx = len(m.shellHistory)

	m = key(m, "!")
	m = typeIn(m, "half")

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
	m := shellModel(t)
	m = key(m, "!")
	m = typeIn(m, "pwd")

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = mm.(Model)
	if m.shellCmd != "pwd" {
		t.Errorf("up with no history changed the line to %q", m.shellCmd)
	}
}

/* -- Task 5: running it ---------------------------------------------------- */

// enter takes the Output pane, focuses it, echoes the command with its repo, and
// returns a run command.
func TestShell_EnterRunsIntoTheOutputPane(t *testing.T) {
	m := shellModel(t)
	loc := shellLocation(m.repos[0].repo)

	m = key(m, "!")
	m = typeIn(m, "echo hi")
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	if cmd == nil {
		t.Fatal("enter should return a run command")
	}
	// The prompt STAYS OPEN: this is a shell you sit in, not a one-shot. Only esc
	// leaves it.
	if !m.shellPrompting {
		t.Error("enter should leave the prompt open for the next command")
	}
	if m.shellCmd != "" {
		t.Errorf("enter should clear the line, got %q", m.shellCmd)
	}
	if m.focus != panelBottom || m.bottomView != bvOutput {
		t.Errorf("enter should focus the Output view, got focus=%d view=%d", m.focus, m.bottomView)
	}
	if !m.outputRunning || m.outputKind != outShell {
		t.Errorf("enter should mark a shell run, running=%v kind=%d", m.outputRunning, m.outputKind)
	}
	echo := loc + " $ echo hi"
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
	m := shellModel(t)
	m = key(m, "!")
	m = typeIn(m, "   ")

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
	if !m.shellPrompting {
		t.Error("an empty enter should just give you a fresh prompt, not leave the shell")
	}
}

// Repeating a command does not add a duplicate history entry.
func TestShell_HistorySkipsAnImmediateRepeat(t *testing.T) {
	m := shellModel(t)
	m = key(m, "!") // opened once — the prompt stays open between runs
	for i := 0; i < 2; i++ {
		m = typeIn(m, "pwd")
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mm.(Model)
	}
	if len(m.shellHistory) != 1 {
		t.Errorf("an immediate repeat should not duplicate, got %v", m.shellHistory)
	}
}

// Two commands in a row without re-opening: the prompt is a place you stay.
func TestShell_RunsSeveralCommandsWithoutReopening(t *testing.T) {
	m := shellModel(t)
	m = key(m, "!")

	for _, cmd := range []string{"pwd", "ls"} {
		m = typeIn(m, cmd)
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mm.(Model)
		if !m.shellPrompting {
			t.Fatalf("prompt closed after %q", cmd)
		}
	}
	if len(m.shellHistory) != 2 || m.shellHistory[0] != "pwd" || m.shellHistory[1] != "ls" {
		t.Errorf("both commands should be in history, got %v", m.shellHistory)
	}
	// Both the key and the symbol must be literal once you are typing: `!` is the
	// key that opened this, `$` is what the prompt renders as — neither may be
	// swallowed mid-command.
	for _, want := range []string{"echo $HOME", "find . ! -name x"} {
		m.shellCmd = ""
		m = typeIn(m, want)
		if m.shellCmd != want {
			t.Errorf("typed text should be literal: got %q, want %q", m.shellCmd, want)
		}
	}
}

// End to end against a real bash: the streamed lines land in the pane.
func TestShell_RealCommandStreamsIntoThePane(t *testing.T) {
	m := shellModel(t)

	m = key(m, "!")
	m = typeIn(m, "echo alpha-line")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	// tea.Batch returns an opaque cmd; drive the runner directly instead.
	lines, err := drainScript(t, startShellCmd("echo alpha-line", m.shellDir, m.outputRun))
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if len(lines) != 1 || lines[0] != "alpha-line" {
		t.Errorf("want [alpha-line], got %v", lines)
	}
}

/* -- Task 6: cancelling ---------------------------------------------------- */

// esc kills a running command when the Output pane is the thing you are looking
// at, and the pane says so.
func TestShell_EscCancelsARunningCommand(t *testing.T) {
	m := shellModel(t)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellLoc = "grp/alpha"
	m.setBottomView(bvOutput)
	m.shellCancel = func() { called = true }

	// The hint has to be up BEFORE the cancel — afterwards there is nothing left
	// to cancel and it correctly disappears.
	if v := stripANSI(m.View()); !strings.Contains(v, "ctrl+c: cancel") {
		t.Error("the Output pane should advertise ctrl+c: cancel while running")
	}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if !called {
		t.Error("esc should kill the running command")
	}
	if !m.shellKilled {
		t.Error("esc should mark the run as killed so done reports 'cancelled'")
	}
	if !strings.Contains(stripANSI(m.statusLine), "cancelled") {
		t.Errorf("esc should report the cancel, got %q", stripANSI(m.statusLine))
	}
	if v := stripANSI(m.View()); strings.Contains(v, "ctrl+c: cancel") {
		t.Error("the cancel hint should go once there is nothing left to cancel")
	}
}

// esc must NOT reach past a diff to kill a background command: its normal
// back-out-one-layer job wins when you are looking at something else.
func TestShell_EscDoesNotCancelFromAnotherPane(t *testing.T) {
	m := shellModel(t)

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
	m := shellModel(t)

	var called bool
	m.takeOutputPane(outShell, "alpha $ sleep 30")
	m.shellLoc = "grp/alpha"
	m.shellCancel = func() { called = true }

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = mm.(Model)
	if !called {
		t.Error("ctrl+c should kill the running command")
	}
	// The returned cmd is setStatus's expiry tick, not tea.Quit — assert on what
	// is observable instead of on cmd being nil, which it never is.
	if !strings.Contains(stripANSI(m.statusLine), "cancelled") {
		t.Errorf("ctrl+c should report the cancel rather than quit, got %q", stripANSI(m.statusLine))
	}

	// With nothing running it must still quit.
	m.outputRunning = false
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("with nothing running, ctrl+c should still quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("with nothing running, ctrl+c should return tea.Quit, got %T", cmd())
	}
}

// Scripts became cancellable in the same change, so a cancelled script must be
// named by its script, not by whatever repo an earlier `!` happened to bind.
func TestShell_CancellingAScriptNamesTheScript(t *testing.T) {
	m := shellModel(t)
	m.shellLoc = "grp/alpha" // stale from an earlier `!`

	m.takeOutputPane(outScript, "deploy.sh")
	m.setBottomView(bvOutput)
	m.shellCancel = func() {}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	got := stripANSI(m.statusLine)
	if !strings.Contains(got, "deploy.sh") {
		t.Errorf("a cancelled script should name the script, got %q", got)
	}
	if strings.Contains(got, "alpha") {
		t.Errorf("a cancelled script must not name a stale `!` repo, got %q", got)
	}

	// And the end-of-run report agrees rather than calling it a failure.
	mm, _ = m.Update(scriptOutMsg{run: m.outputRun, done: true, err: &exec.ExitError{}})
	m = mm.(Model)
	if got := stripANSI(m.statusLine); !strings.Contains(got, "cancelled") {
		t.Errorf("a killed run should report cancelled, not failed, got %q", got)
	}
}

// End to end through the real program loop, which is the only thing that
// exercises the whole chain: `!` -> enter -> startShellCmd -> runStartMsg ->
// readScriptLine -> appendOutput -> render. Driving Update directly (as the
// tests above do) would still pass if the runStartMsg handler were missing, and
// the app would silently stream nothing.
func TestShell_EndToEndThroughTheProgramLoop(t *testing.T) {
	cfg, repos := twoRepos(t)
	tm := teatest.NewTestModel(t, New(cfg, "", repos, nil), teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alpha"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	for _, r := range "echo streamed-ok" {
		ty := tea.KeyRunes
		if r == ' ' {
			ty = tea.KeySpace
		}
		tm.Send(tea.KeyMsg{Type: ty, Runes: []rune{r}})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("streamed-ok"))
	}, teatest.WithDuration(10*time.Second))

	// esc first: the prompt is still open, so a bare `q` would be typed into it
	// rather than quitting. That is exactly the trap a real user would hit too.
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

/* -- Task 7: discoverability ----------------------------------------------- */

// `!` must appear in the always-visible footer and in the ? keys reference,
// or the feature is undiscoverable.
func TestShell_IsDocumentedInTheApp(t *testing.T) {
	m := shellModel(t)

	if got := stripANSI(m.footer()); !strings.Contains(got, "! shell") {
		t.Errorf("the footer should advertise ! shell, got %q", got)
	}

	left, right := m.keysColumns()
	joined := stripANSI(strings.Join(append(left, right...), "\n"))
	if !strings.Contains(joined, "shell") {
		t.Error("the ? keys reference should describe ! as a shell")
	}
}
