package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rabeeh-ta/manygit/internal/aigit"
	"github.com/rabeeh-ta/manygit/internal/discover"
)

// One Output pane, two producers. The script runner stamps its messages with
// outputRun and the AI harness stamps its replies with aiRun, and each used to
// bump only its own counter when it took the pane — so the other one's messages
// still passed their staleness check and wrote into a pane they no longer owned.
//
// paneModel is a Model with a script already streaming into the Output pane.
func paneModel(t *testing.T) Model {
	t.Helper()
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.scripts = []discover.Script{{Name: "long.sh", Path: "/bin/true"}}
	m.scriptCursor = 0
	m.runSelectedScript()
	m.appendOutput("line from the script")
	return m
}

func outputText(m Model) string { return stripANSI(strings.Join(m.outputLines, "\n")) }

// A script is still streaming when the user confirms an AI plan. The AI owns the
// pane now, so the script's remaining lines must not land in it.
func TestTUI_ScriptOutputDoesNotLeakIntoAnAIRun(t *testing.T) {
	m := paneModel(t)
	scriptRun := m.outputRun

	// Confirm a plan: the AI takes the Output pane.
	m.confirmPlan = true
	m.pendingPlan = aigit.Plan{Steps: []aigit.Step{{Repo: "alpha", Args: []string{"status"}}}}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = mm.(Model)
	if m.confirmPlan {
		t.Fatal("y should have accepted the plan")
	}

	// The script process is still alive and still emitting.
	mm, _ = m.Update(scriptOutMsg{run: scriptRun, line: "LEAKED from the script"})
	m = mm.(Model)

	if strings.Contains(outputText(m), "LEAKED") {
		t.Errorf("a superseded script wrote into the AI's Output pane:\n%s", outputText(m))
	}
}

// The mirror image: an AI request is in flight when the user runs a script. The
// script owns the pane, so the reply must be dropped rather than wiping it.
func TestTUI_AIReplyDoesNotOverwriteANewScriptsOutput(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.scripts = []discover.Script{{Name: "s.sh", Path: "/bin/true"}}
	m.scriptCursor = 0

	m.aiRun++ // a request went out
	pending := m.aiRun

	m.runSelectedScript() // ...and the user starts a script instead
	m.appendOutput("output that belongs to the script")

	mm, _ := m.Update(aiPlanMsg{run: pending, plan: aigit.Plan{Note: "REPLY"}})
	m = mm.(Model)

	got := outputText(m)
	if strings.Contains(got, "REPLY") {
		t.Errorf("a superseded AI reply overwrote the running script's output:\n%s", got)
	}
	if !strings.Contains(got, "belongs to the script") {
		t.Errorf("the script's own output was discarded:\n%s", got)
	}
	if !m.outputRunning {
		t.Error("the stale reply cleared outputRunning, so the script looks finished")
	}
}

// Executing an AI plan runs git across repos, so the Repos pane should follow it
// live exactly as it does for a script. Taking the pane supersedes whatever run
// the probe belonged to, so the new owner has to arm its own.
func TestTUI_AIRunArmsTheRepoProbe(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)

	m.confirmPlan = true
	m.pendingPlan = aigit.Plan{Steps: []aigit.Step{{Repo: "alpha", Args: []string{"fetch"}}}}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = mm.(Model)

	if !m.probing || m.probeRun != m.outputRun {
		t.Errorf("an AI run should arm the repo probe (probing=%v probeRun=%d outputRun=%d)",
			m.probing, m.probeRun, m.outputRun)
	}
	for i, r := range m.repos {
		if r.fp == 0 {
			t.Errorf("repo %d has no fingerprint baseline for the AI run", i)
		}
	}

	// ...and finishing it disarms, the same as a script finishing does.
	mm, _ = m.Update(aiDoneMsg{run: m.aiRun})
	if g := mm.(Model); g.probing {
		t.Error("the probe should stop when the AI run finishes")
	}
}

// Taking the pane must not leave a probe chain running for the superseded run —
// it would tick forever with no owner, and its next tick is dropped anyway.
func TestTUI_TakingTheOutputPaneStopsTheOldProbe(t *testing.T) {
	m := paneModel(t)
	if !m.probing {
		t.Fatal("the script should have armed the probe")
	}
	oldRun := m.probeRun

	m.takeOutputPane("something else")

	if m.probing && m.probeRun == oldRun {
		t.Error("the superseded run's probe is still marked live")
	}
	if m.outputRun == oldRun {
		t.Error("taking the pane must supersede the previous run")
	}
}
