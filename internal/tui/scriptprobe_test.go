package tui

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/discover"
)

// writeFile drops a file into a repo the way a script would.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fastMsgs runs cmd, follows any tea.Batch it expands into, and collects every
// message produced within d. Commands that haven't answered by then — the
// tea.Tick timers a handler batches alongside real work — are dropped rather
// than waited on, so a handler that batches a 4s status-expiry timer stays
// testable.
func fastMsgs(t *testing.T, cmd tea.Cmd, d time.Duration) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	var (
		mu  sync.Mutex
		out []tea.Msg
		wg  sync.WaitGroup
		run func(tea.Cmd)
	)
	run = func(c tea.Cmd) {
		defer wg.Done()
		res := make(chan tea.Msg, 1)
		go func() { res <- c() }()
		select {
		case msg := <-res:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, child := range batch {
					if child == nil {
						continue
					}
					wg.Add(1)
					go run(child)
				}
				return
			}
			mu.Lock()
			out = append(out, msg)
			mu.Unlock()
		case <-time.After(d):
		}
	}
	wg.Add(1)
	run(cmd)
	wg.Wait()
	return out
}

// applyAll feeds every statusMsg back into the model, the way the Bubble Tea
// loop would.
func applyAll(m Model, msgs []tea.Msg) Model {
	for _, msg := range msgs {
		if st, ok := msg.(statusMsg); ok {
			mm, _ := m.Update(st)
			m = mm.(Model)
		}
	}
	return m
}

// The complaint this closes: a script switches branches in several repos and the
// Repos pane keeps showing the old ones until you go there and press r. When the
// script ends every repo is re-read, so pane 1 tells the truth without being
// asked.
//
// It has to be a full re-stat, not the fingerprint probe: a script can also
// change files without ever invoking git, and the probe is deliberately blind to
// that (see TestFingerprint_IgnoresWorkingTreeEdits).
func TestTUI_ScriptCompletionRefreshesEveryRepoRow(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	for _, r := range m.repos {
		if got := currentBranch(r.status); got != "master" {
			t.Fatalf("fixture should start on master, got %q", got)
		}
	}

	// What a script does behind manygit's back.
	gitCmd(t, m.repos[0].repo.Path, "checkout", "-q", "-b", "release/2.4")
	writeFile(t, m.repos[1].repo.Path, "untracked.txt", "x\n") // no git involved

	m.outputRunning = true
	mm, cmd := m.Update(scriptOutMsg{run: m.outputRun, done: true})
	m = applyAll(mm.(Model), fastMsgs(t, cmd, time.Second))

	if got := currentBranch(m.repos[0].status); got != "release/2.4" {
		t.Errorf("repo 0 still shows %q after the script ended; want release/2.4", got)
	}
	if got := m.repos[1].status.DirtyCount; got != 1 {
		t.Errorf("repo 1 dirty count = %d, want 1 — a script can dirty a tree without running git", got)
	}
}

// Mid-run, only repos that actually moved are re-read. A full sweep is 6-8 git
// subprocesses per repo (~170ms across 28 repos measured); doing that on a timer
// while a script runs is exactly the stutter this is meant to avoid.
func TestTUI_ScriptProbeRefreshesOnlyRepositoriesThatMoved(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	a, b := m.repos[0].repo.Path, m.repos[1].repo.Path
	m.repos[0].fp, m.repos[1].fp = 100, 200

	changed := m.probeChanged(map[string]int64{a: 999, b: 200})
	if len(changed) != 1 || changed[0] != a {
		t.Fatalf("only the moved repo should be re-read, got %v", changed)
	}
	if m.repos[0].fp != 999 {
		t.Errorf("the moved repo's fingerprint should be updated, got %d", m.repos[0].fp)
	}
	if m.repos[1].fp != 200 {
		t.Errorf("an unmoved repo's fingerprint should be left alone, got %d", m.repos[1].fp)
	}

	// A second sample with nothing new must ask for no work at all.
	if again := m.probeChanged(map[string]int64{a: 999, b: 200}); len(again) != 0 {
		t.Errorf("a quiet tick should trigger no re-reads, got %v", again)
	}
}

// A repo whose baseline was never taken (added by a rescan mid-script, or a path
// missing from the sample) must not be reported as changed — that would fire a
// Status() for every repo on the first tick.
func TestTUI_ScriptProbeIgnoresUnbaselinedRepos(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	a := m.repos[0].repo.Path
	m.repos[0].fp = 0 // never baselined
	m.repos[1].fp = 7 // baselined, but absent from the sample below

	if changed := m.probeChanged(map[string]int64{a: 555}); len(changed) != 0 {
		t.Errorf("an unbaselined repo must not count as changed, got %v", changed)
	}
	if m.repos[0].fp != 555 {
		t.Errorf("it should still adopt the baseline, got %d", m.repos[0].fp)
	}
	if m.repos[1].fp != 7 {
		t.Errorf("a repo absent from the sample must keep its fingerprint, got %d", m.repos[1].fp)
	}
}

// The probe exists only for the duration of a script. If it kept re-arming, an
// idle manygit would stat every repo forever for nothing.
func TestTUI_ScriptProbeStopsWithTheScript(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)

	m.probing, m.outputRun = true, 4
	if _, cmd := m.Update(repoProbeMsg{run: 4}); cmd == nil {
		t.Error("while a script runs the probe should re-arm")
	}

	// The script ending is what stops it.
	mm, _ := m.Update(scriptOutMsg{run: 4, done: true})
	m = mm.(Model)
	if m.probing {
		t.Error("the probe should stop when the script ends")
	}
	if _, cmd := m.Update(repoProbeMsg{run: 4}); cmd != nil {
		t.Error("once the script has ended the probe must not re-arm")
	}

	// A superseded run's tick is dropped — bubbletea cannot cancel a pending
	// Tick, so the guard is the run counter (the scriptOutMsg idiom).
	m.probing = true
	if _, cmd := m.Update(repoProbeMsg{run: 3}); cmd != nil {
		t.Error("a tick from a superseded run must not re-arm")
	}
}

// Every tick re-arms itself, so arming a second probe for a run that already has
// one leaves two self-sustaining chains and permanently doubles the stat rate —
// and it takes no contrivance to get there: the AI-harness paths also set
// outputRunning without bumping outputRun.
func TestTUI_ScriptProbeArmsAtMostOnce(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)

	if cmd := m.startRepoProbe(); cmd == nil {
		t.Fatal("the first arm should return a probe command")
	}
	if cmd := m.startRepoProbe(); cmd != nil {
		t.Error("arming a second probe while one is live would double the tick rate")
	}

	// After the script ends, the next one may arm again.
	mm, _ := m.Update(scriptOutMsg{run: m.outputRun, done: true})
	m = mm.(Model)
	if cmd := m.startRepoProbe(); cmd == nil {
		t.Error("a new script should be able to arm the probe again")
	}
}

// Starting a script while one is still streaming is unguarded (handleKey's
// panelScripts case) and runSelectedScript bumps outputRun precisely to
// supersede the old one. The probe has to follow: the in-flight tick still
// carries the OLD run, so it is dropped without re-arming, and if the new run
// declines to arm as well the second script gets no live updates at all — the
// whole feature, silently off, for its entire duration.
func TestTUI_ScriptProbeRearmsForASecondScriptStartedMidRun(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.scripts = []discover.Script{{Name: "a.sh", Path: "/bin/true"}, {Name: "b.sh", Path: "/bin/true"}}

	m.scriptCursor = 0
	if cmd := m.runSelectedScript(); cmd == nil {
		t.Fatal("script A should start")
	}
	runA := m.outputRun

	// B starts before A has finished — nothing stops that.
	m.scriptCursor = 1
	if cmd := m.runSelectedScript(); cmd == nil {
		t.Fatal("script B should start")
	}
	if m.outputRun == runA {
		t.Fatal("starting B should supersede A's run")
	}

	// A's pending tick is dropped, correctly, and must not re-arm.
	if _, cmd := m.Update(repoProbeMsg{run: runA}); cmd != nil {
		t.Error("the superseded run's tick must not re-arm")
	}
	// So the only chain that can still be ticking is one B armed for itself.
	// Asserting on probeRun rather than on Update(repoProbeMsg{run: current}):
	// that message would be answered whether or not a chain exists to send it,
	// so it proves nothing about whether one was ever scheduled.
	if m.probeRun != m.outputRun {
		t.Errorf("no probe armed for the second script (probeRun=%d, outputRun=%d): "+
			"its Repos pane will never refresh mid-run", m.probeRun, m.outputRun)
	}
}

// Each run gets its own baseline. Reusing the previous script's fingerprints
// would make everything it changed look unchanged to the new run.
func TestTUI_ScriptProbeRebaselinesForEachRun(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.scripts = []discover.Script{{Name: "a.sh", Path: "/bin/true"}, {Name: "b.sh", Path: "/bin/true"}}

	m.scriptCursor = 0
	m.runSelectedScript()
	for _, r := range m.repos {
		r.fp = 12345 // pretend A's baseline went stale
	}

	m.scriptCursor = 1
	m.runSelectedScript()
	for i, r := range m.repos {
		if r.fp == 12345 {
			t.Errorf("repo %d kept the previous run's stale fingerprint", i)
		}
	}
}

// The baseline is taken when the script starts, not on the first tick — anything
// the script changed in its first second would otherwise be baked into the
// baseline and never reported.
func TestTUI_ScriptStartBaselinesFingerprints(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.scripts = []discover.Script{{Name: "noop.sh", Path: "/bin/true"}}
	m.scriptCursor = 0

	if cmd := m.runSelectedScript(); cmd == nil {
		t.Fatal("running a script should return a command")
	}
	for i, r := range m.repos {
		if r.fp == 0 {
			t.Errorf("repo %d has no fingerprint baseline after the script started", i)
		}
	}
	if !m.outputRunning {
		t.Error("the script should be marked as running")
	}
}
