package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A headline is one line of harness output shaped "Title: detail". Splitting on
// the FIRST colon gives the changelog-style heading and its explanation; a
// headline with no colon is all heading, which is what an older cached feed
// looks like and what the harness sometimes returns anyway.
func TestSplitHeadline(t *testing.T) {
	cases := []struct{ in, head, detail string }{
		{"PostHog funnel instrumentation lands: course-creation stage transitions tracked",
			"PostHog funnel instrumentation lands", "course-creation stage transitions tracked"},
		{"Upstream API standardization: ADRs 0027/0034/0036 applied, home v3/v4",
			"Upstream API standardization", "ADRs 0027/0034/0036 applied, home v3/v4"},
		{"Flashcards record reviews on attempt", "Flashcards record reviews on attempt", ""},
		{"", "", ""},
		{"Trailing colon:", "Trailing colon", ""},
		{":leading colon", ":leading colon", ""}, // not a title — keep it whole
	}
	for _, c := range cases {
		head, detail := splitHeadline(c.in)
		if head != c.head || detail != c.detail {
			t.Errorf("splitHeadline(%q) = (%q, %q), want (%q, %q)", c.in, head, detail, c.head, c.detail)
		}
	}
}

// wrapWords breaks on spaces, never mid-word — lipgloss's own Width() hard-wraps
// mid-word, which is exactly the trap the keysBody column fell into.
func TestWrapWords(t *testing.T) {
	got := wrapWords("the quick brown fox jumps over the lazy dog", 12)
	for _, ln := range got {
		if lipgloss.Width(ln) > 12 {
			t.Errorf("line %q exceeds width 12", ln)
		}
	}
	if strings.Join(got, " ") != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("wrapping lost or reordered words: %q", got)
	}

	// A word longer than the measure still has to appear, on its own line.
	long := wrapWords("short supercalifragilisticexpialidocious end", 10)
	if !strings.Contains(strings.Join(long, "|"), "supercalifragilisticexpialidocious") {
		t.Errorf("an over-long word was dropped: %q", long)
	}
	if len(wrapWords("", 10)) != 0 {
		t.Error("empty input should wrap to no lines")
	}
	if got := wrapWords("anything", 0); len(got) != 1 || got[0] != "anything" {
		t.Errorf("a non-positive width should pass the text through, got %q", got)
	}
}

// The news overlay reads as a changelog: a heading per item, its explanation
// indented beneath, and a blank line between items so ten of them don't run
// together as one grey block.
func TestTUI_NewsOverlayIsSpacedLikeAChangelog(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 40)
	m.newsFeed = []string{
		"PostHog funnel instrumentation lands: course-creation stage transitions tracked, every backend event now tagged by deployment",
		"Attendance goes end-to-end: manual marking with optimistic UI, backed by new DDN staff permissions",
		"Flashcards record reviews on attempt",
	}

	lines := m.newsLines()
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = strings.TrimRight(stripANSI(l), " ")
	}
	joined := strings.Join(plain, "\n")

	for _, want := range []string{
		"PostHog funnel instrumentation lands",
		"Attendance goes end-to-end",
		"Flashcards record reviews on attempt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing heading %q in:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "course-creation stage transitions tracked") {
		t.Errorf("the detail text was dropped:\n%s", joined)
	}

	// A blank line before every heading except the first.
	headAt := []int{}
	for i, l := range plain {
		if strings.Contains(l, "PostHog funnel") || strings.Contains(l, "Attendance goes") ||
			strings.Contains(l, "Flashcards record") {
			headAt = append(headAt, i)
		}
	}
	if len(headAt) != 3 {
		t.Fatalf("expected 3 headings, found %d in:\n%s", len(headAt), joined)
	}
	for _, i := range headAt[1:] {
		if strings.TrimSpace(plain[i-1]) != "" {
			t.Errorf("no blank line before the heading on line %d:\n%s", i, joined)
		}
	}

	// Nothing may exceed the readable measure — that is the point of wrapping.
	for _, l := range plain {
		if w := lipgloss.Width(l); w > m.newsColW()+4 {
			t.Errorf("line is %d cells wide, past the %d measure: %q", w, m.newsColW(), l)
		}
	}
}

// Scrolling is by rendered line, not by headline: an entry is several lines now,
// so clamping to len(newsFeed) would stop j a long way short of the bottom.
func TestTUI_NewsOverlayScrollsByRenderedLine(t *testing.T) {
	cfg, repos := twoRepos(t)
	m := loadAll(t, New(cfg, "", repos, nil), 120, 12)
	for i := 0; i < 10; i++ {
		m.newsFeed = append(m.newsFeed,
			"Headline number that is fairly long: with an explanation after it that will certainly need wrapping at this width")
	}
	m.showNews = true

	total := len(m.newsLines())
	if total <= len(m.newsFeed) {
		t.Fatalf("10 headlines with details should render more than 10 lines, got %d", total)
	}

	rk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	for i := 0; i < total+20; i++ {
		mm, _ := m.Update(rk("j"))
		m = mm.(Model)
	}
	if m.newsOffset != total-1 {
		t.Errorf("j should reach the last rendered line %d, stopped at %d", total-1, m.newsOffset)
	}
	if v := stripANSI(m.View()); strings.Count(v, "\n") > 12 {
		t.Errorf("the overlay overflowed a 12-row terminal")
	}
}

// A cache written by an older prompt has headlines with no "Title: detail"
// shape, so it must be discarded rather than rendered as headings with no
// explanation for a whole newsTTL.
func TestNewsCacheRejectsAnOlderFormat(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	saveNewsCache(cachedNews{Days: 3, Sig: "sig", Headlines: []string{"old style headline"}}) // Format 0
	if _, ok := loadNewsCache(); ok {
		t.Error("a cache with no format stamp should be rejected")
	}

	saveNewsCache(cachedNews{Days: 3, Sig: "sig", Format: newsFormat, Headlines: []string{"New: shape"}})
	c, ok := loadNewsCache()
	if !ok || len(c.Headlines) != 1 {
		t.Fatalf("a current-format cache should load, got ok=%v %+v", ok, c)
	}
}
