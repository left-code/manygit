package tui

import (
	"strings"
	"testing"

	"manygit/internal/gh"
)

// prModel is a Model with the PRs pane focused and one list loaded, plus two
// discovered repos whose origin slugs can be matched against a PR.
func prModel(t *testing.T, prs ...gh.PullRequest) Model {
	t.Helper()
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.ghProbed, m.ghAvailable, m.prLoaded = true, true, true
	m.focus, m.topView = panelBranches, tvPRs
	m.prMine = prs
	return m
}

// lineWith returns the first rendered line containing needle, ANSI stripped.
func lineWith(s, needle string) string {
	for _, ln := range strings.Split(stripANSI(s), "\n") {
		if strings.Contains(ln, needle) {
			return ln
		}
	}
	return ""
}

// The whole point of the pane is knowing what merges where. A PR row's second
// line names the repo, the branch that repo is checked out to locally, and
// base ← head — base on the left because that's the branch receiving the work.
func TestTUI_PRRowShowsBaseAndHeadBranches(t *testing.T) {
	m := prModel(t, gh.PullRequest{
		Number: 44, Title: "Notification catalog", Author: "zameel7",
		RepoSlug: "blend-ed/blendxmetadata", BaseRef: "main", HeadRef: "feature/notification-settings",
	})
	m.repos[0].status.Slug = "blend-ed/blendxmetadata"
	m.repos[0].status.Branch = "master"

	out := m.renderPRsView(110, 20)

	// Line 1 keeps the identity of the PR; the repo moves down to line 2 so a
	// long title gets the full width.
	first := lineWith(out, "#44")
	if !strings.Contains(first, "@zameel7") || !strings.Contains(first, "Notification catalog") {
		t.Errorf("line 1 should carry number, author and title, got %q", first)
	}

	second := lineWith(out, "blendxmetadata")
	if second == "" {
		t.Fatalf("no line mentions the repo; rendered:\n%s", stripANSI(out))
	}
	if second == first {
		t.Errorf("the repo/branch detail must be its OWN line, not appended to the title: %q", first)
	}
	for _, want := range []string{"blendxmetadata", "(master)", "main", "←", "feature/notification-settings"} {
		if !strings.Contains(second, want) {
			t.Errorf("line 2 missing %q, got %q", want, second)
		}
	}
	if i, j := strings.Index(second, "main"), strings.Index(second, "feature/notification-settings"); i > j {
		t.Errorf("base must sit LEFT of head, got %q", second)
	}
}

// A PR whose repo isn't in the tree has no local branch to name. Omitting the
// parenthesised branch is also the tell that enter and o will say it is not in this tree.
func TestTUI_PRRowOmitsLocalBranchWhenRepoNotCloned(t *testing.T) {
	m := prModel(t, gh.PullRequest{
		Number: 7, Title: "Elsewhere", Author: "a",
		RepoSlug: "other/elsewhere", BaseRef: "main", HeadRef: "feat/x",
	})

	second := lineWith(m.renderPRsView(110, 20), "elsewhere")
	if second == "" {
		t.Fatal("the repo name should still be shown")
	}
	if strings.Contains(second, "(") {
		t.Errorf("no local clone means no (branch), got %q", second)
	}
	if !strings.Contains(second, "main") || !strings.Contains(second, "feat/x") {
		t.Errorf("base ← head should still render without a local clone, got %q", second)
	}
}

// When the local repo is already on the PR's head branch, that PR is checked
// out. Marking it is how you see, at a glance, which of a run of checkouts have
// landed — the flow the pane exists for.
//
// Asserted on the predicate rather than the rendered colour on purpose: lipgloss
// emits no ANSI under `go test` (there is no TTY), so styleGreen.Render(s) == s
// and any assertion on the colour would pass whatever the code did.
func TestTUI_PRRowMarksAlreadyCheckedOut(t *testing.T) {
	onIt := gh.PullRequest{Number: 1, RepoSlug: "o/alpha", BaseRef: "main", HeadRef: "feat/done"}
	notOnIt := gh.PullRequest{Number: 2, RepoSlug: "o/bravo", BaseRef: "main", HeadRef: "feat/pending"}
	absent := gh.PullRequest{Number: 3, RepoSlug: "o/nowhere", BaseRef: "main", HeadRef: "feat/x"}

	m := prModel(t, onIt, notOnIt, absent)
	m.repos[0].status.Slug, m.repos[0].status.Branch = "o/alpha", "feat/done" // on the PR branch
	m.repos[1].status.Slug, m.repos[1].status.Branch = "o/bravo", "master"    // not

	if !m.prCheckedOut(onIt) {
		t.Error("a repo sitting on the PR's head branch should count as checked out")
	}
	if m.prCheckedOut(notOnIt) {
		t.Error("a repo on a different branch must not count as checked out")
	}
	if m.prCheckedOut(absent) {
		t.Error("a PR with no local clone must not count as checked out")
	}

	// A PR with no head ref must never match a repo with no branch read yet.
	m.repos[1].status.Branch = ""
	if m.prCheckedOut(gh.PullRequest{RepoSlug: "o/bravo"}) {
		t.Error("empty head ref must not match an empty local branch")
	}

	if got := lineWith(m.renderPRsView(110, 20), "alpha"); !strings.Contains(got, "(feat/done)") {
		t.Errorf("the local branch should still be shown, got %q", got)
	}
}

// The arrow obeys the same status_glyphs setting as ↑/↓: unicode terminals get
// ←, and `ascii` gets <- so the row can't drift in an ambiguous-width terminal.
func TestTUI_PRRowArrowFollowsGlyphSetting(t *testing.T) {
	pr := gh.PullRequest{Number: 3, RepoSlug: "o/alpha", BaseRef: "main", HeadRef: "feat/x"}

	m := prModel(t, pr)
	if got := lineWith(m.renderPRsView(110, 20), "alpha"); !strings.Contains(got, "←") {
		t.Errorf("unicode glyphs should use ←, got %q", got)
	}

	m2 := prModel(t, pr)
	m2.cfg.StatusGlyphs = "ascii"
	got := lineWith(m2.renderPRsView(110, 20), "alpha")
	if !strings.Contains(got, "<-") {
		t.Errorf("ascii glyphs should use <-, got %q", got)
	}
	if strings.Contains(got, "←") {
		t.Errorf("ascii glyphs must not emit ←, got %q", got)
	}
}

// Two-line rows stacked with no gap turn nine PRs into eighteen dense lines and
// you can't tell where one ends and the next begins. A blank line between them
// makes each PR read as a block.
func TestTUI_PRRowsAreSeparated(t *testing.T) {
	var prs []gh.PullRequest
	for i := 1; i <= 4; i++ {
		prs = append(prs, gh.PullRequest{
			Number: i, Title: "t", Author: "a",
			RepoSlug: "o/r", BaseRef: "main", HeadRef: "feat/x",
		})
	}
	m := prModel(t, prs...)

	lines := strings.Split(strings.TrimRight(stripANSI(m.renderPRsView(110, 30)), "\n"), "\n")
	var idx []int
	for i, ln := range lines {
		if strings.Contains(ln, "#") {
			idx = append(idx, i)
		}
	}
	if len(idx) != 4 {
		t.Fatalf("expected 4 PRs, rendered %d:\n%s", len(idx), strings.Join(lines, "\n"))
	}
	for n := 1; n < len(idx); n++ {
		gap := idx[n] - idx[n-1]
		if gap != 3 {
			t.Errorf("PR %d starts %d lines after PR %d; want 3 (title, detail, blank)", n+1, gap, n)
		}
		if between := strings.TrimSpace(lines[idx[n]-1]); between != "" {
			t.Errorf("no blank line before PR %d, found %q", n+1, between)
		}
	}
}

// Rows are two lines now, so a pane that fit N PRs fits N/2. The caller clamps
// the pane to its height (renderLayout -> clampLines), so the window has to be
// counted in ROWS: an off-by-one leaves a title line whose branch line got
// clamped away — a PR shown with no repo, no base and no head, which is the one
// thing the second line exists to prevent.
func TestTUI_PRRowsWindowByTwoLineRows(t *testing.T) {
	var prs []gh.PullRequest
	for i := 1; i <= 12; i++ {
		prs = append(prs, gh.PullRequest{
			Number: i, Title: "t", Author: "a",
			RepoSlug: "o/r", BaseRef: "main", HeadRef: "feat/x",
		})
	}
	m := prModel(t, prs...)

	// Odd and even heights both, since listH/2 truncates.
	for _, innerH := range []int{4, 5, 8, 9, 12, 13} {
		out := clampLines(stripANSI(m.renderPRsView(110, innerH)), innerH)
		lines := strings.Split(out, "\n")

		titles, details := 0, 0
		for i, ln := range lines {
			if !strings.Contains(ln, "#") {
				continue
			}
			titles++
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "main") {
				t.Errorf("h=%d: PR on line %d has no branch line under it:\n%s", innerH, i, out)
				continue
			}
			details++
		}
		if titles == 0 {
			t.Errorf("h=%d: no PRs rendered at all:\n%s", innerH, out)
		}
		if titles != details {
			t.Errorf("h=%d: %d PR titles but %d branch lines", innerH, titles, details)
		}
	}
}

// The cursor has to stay on screen when it walks past the bottom of a two-line
// window — the same guarantee the one-line list gave.
func TestTUI_PRRowsKeepCursorVisible(t *testing.T) {
	var prs []gh.PullRequest
	for i := 1; i <= 12; i++ {
		prs = append(prs, gh.PullRequest{Number: i, RepoSlug: "o/r", BaseRef: "main", HeadRef: "h"})
	}
	m := prModel(t, prs...)
	m.prCursor = 11

	out := stripANSI(m.renderPRsView(110, 8))
	if !strings.Contains(out, "#12") {
		t.Errorf("the selected PR #12 scrolled out of view:\n%s", out)
	}
}

// Older gh, a cached list, or a PR whose refs came back empty must not render a
// dangling ": ←" with nothing either side.
func TestTUI_PRRowWithoutBranchesDegrades(t *testing.T) {
	m := prModel(t, gh.PullRequest{Number: 5, Title: "No refs", Author: "a", RepoSlug: "o/alpha"})

	second := lineWith(m.renderPRsView(110, 20), "alpha")
	if second == "" {
		t.Fatal("the repo name should still render with no branch info")
	}
	if strings.Contains(second, "←") || strings.Contains(second, ":") {
		t.Errorf("with no base/head there should be no arrow and no colon, got %q", second)
	}
}
