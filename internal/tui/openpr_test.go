package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"manygit/internal/gh"
)

// `o` opens what you are looking at. In the PRs pane that is the highlighted
// PR's repo — opening whatever the Repos cursor happens to be on instead is just
// wrong, and silently so, because both are plausible repos.
//
// Asserted on the resolved target rather than the returned command: running it
// would actually launch the editor.
func TestTUI_OpenTargetsTheHighlightedPRsRepo(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.cursor = 0 // Repos cursor sits on repo 0...
	m.repos[1].status.Slug = "acme/bravo"
	m.prMine = []gh.PullRequest{{Number: 7, RepoSlug: "acme/bravo", BaseRef: "main", HeadRef: "feat/x"}}

	// ...while the PRs pane is focused on a PR belonging to repo 1.
	m.focus, m.topView, m.prCursor = panelBranches, tvPRs, 0
	path, missing := m.openTarget()
	if missing != "" {
		t.Fatalf("the PR's repo is present, got missing=%q", missing)
	}
	if path != m.repos[1].repo.Path {
		t.Errorf("o opened %q, want the PR's repo %q", path, m.repos[1].repo.Path)
	}

	// Away from the PRs pane it still follows the Repos cursor.
	m.focus, m.topView = panelRepos, tvBranches
	if path, _ := m.openTarget(); path != m.repos[0].repo.Path {
		t.Errorf("outside the PRs pane o should open the cursor repo, got %q", path)
	}

	// The Branches sub-view is the same pane but not the PR list.
	m.focus, m.topView = panelBranches, tvBranches
	if path, _ := m.openTarget(); path != m.repos[0].repo.Path {
		t.Errorf("on the Branches sub-view o should open the cursor repo, got %q", path)
	}
}

// A PR in a repo you haven't cloned has nothing to open. Say which repo, and say
// it in terms of the tree manygit scanned — not a silent no-op, and not the
// wrong repo opened instead.
func TestTUI_OpenPRWithNoLocalCloneExplainsItself(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.focus, m.topView = panelBranches, tvPRs
	m.prMine = []gh.PullRequest{{Number: 9, RepoSlug: "acme/not-cloned"}}

	path, missing := m.openTarget()
	if path != "" {
		t.Errorf("nothing should be opened, got %q", path)
	}
	if missing != "acme/not-cloned" {
		t.Errorf("the missing repo should be named, got %q", missing)
	}

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := stripANSI(mm.(Model).statusLine)
	if !strings.Contains(got, "acme/not-cloned") {
		t.Errorf("the status should name the repo, got %q", got)
	}
	for _, want := range []string{"isn't in this tree", "open"} {
		if !strings.Contains(got, want) {
			t.Errorf("status %q should mention %q", got, want)
		}
	}
	if cmd == nil {
		t.Error("the status needs its expiry timer")
	}
}

// enter and o fail for the identical reason, so they say the identical thing —
// only the verb differs. Two wordings for one condition is how a UI teaches you
// that they are different problems.
func TestTUI_MissingCloneMessageIsSharedByEnterAndOpen(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.focus, m.topView = panelBranches, tvPRs
	m.prMine = []gh.PullRequest{{Number: 9, RepoSlug: "acme/nope"}}

	press := func(k string) string {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		return stripANSI(mm.(Model).statusLine)
	}
	openMsg := press("o")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	checkoutMsg := stripANSI(mm.(Model).statusLine)

	for _, s := range []string{openMsg, checkoutMsg} {
		if !strings.Contains(s, "acme/nope isn't in this tree") {
			t.Errorf("expected the shared phrasing, got %q", s)
		}
	}
	if !strings.Contains(openMsg, "open") || !strings.Contains(checkoutMsg, "check out") {
		t.Errorf("the verb should differ: open=%q checkout=%q", openMsg, checkoutMsg)
	}
}
