package aigit

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_BareJSON(t *testing.T) {
	p, err := Parse(`{"steps":[{"repo":"blendxapi","args":["rebase","origin/master"]}],"note":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 1 || p.Steps[0].Repo != "blendxapi" {
		t.Fatalf("got %+v", p.Steps)
	}
	if got := p.Steps[0].Command(); got != "git rebase origin/master" {
		t.Errorf("Command() = %q", got)
	}
}

// The CLIs are asked for bare JSON but habitually wrap it in a fence or a
// sentence. Requiring the whole reply to parse would fail on a correct plan.
func TestParse_WrappedInProseAndFences(t *testing.T) {
	for _, in := range []string{
		"Sure! Here's the plan:\n```json\n{\"steps\":[{\"repo\":\"a\",\"args\":[\"pull\"]}]}\n```\nLet me know.",
		"```\n{\"steps\":[{\"repo\":\"a\",\"args\":[\"pull\"]}]}\n```",
		"{\"steps\":[{\"repo\":\"a\",\"args\":[\"pull\"]}]}",
	} {
		p, err := Parse(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if len(p.Steps) != 1 || p.Steps[0].Repo != "a" {
			t.Errorf("%q: got %+v", in, p.Steps)
		}
	}
}

// A brace inside a note (a commit message, say) must not end the object early.
func TestParse_BraceInsideString(t *testing.T) {
	p, err := Parse(`{"steps":[],"note":"skipped commit \"fix {json} parsing\" for now"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Note, "{json}") {
		t.Errorf("note truncated at the inner brace: %q", p.Note)
	}
}

// Empty steps + a note is how the harness declines — "mkdir isn't git" — and must
// parse cleanly rather than erroring.
func TestParse_DeclineIsNotAnError(t *testing.T) {
	p, err := Parse(`{"steps":[],"note":"manygit only runs git, so I can't create that folder."}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 0 || p.Note == "" {
		t.Errorf("got %+v", p)
	}
}

// Junk must be an error, never a silently empty plan — an empty plan reads as
// "nothing to do" when in fact the request failed.
func TestParse_JunkIsAnError(t *testing.T) {
	for _, in := range []string{"", "I couldn't work that out, sorry.", "not json at all"} {
		if _, err := Parse(in); !errors.Is(err, ErrNoJSON) {
			t.Errorf("Parse(%q) err = %v, want ErrNoJSON", in, err)
		}
	}
	if _, err := Parse(`{"steps": [ broken`); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestParse_StepsMustBeRunnable(t *testing.T) {
	if _, err := Parse(`{"steps":[{"repo":"a","args":[]}]}`); err == nil {
		t.Error("a step with no command must be rejected at parse time")
	}
	if _, err := Parse(`{"steps":[{"repo":"","args":["pull"]}]}`); err == nil {
		t.Error("a step naming no repo must be rejected at parse time")
	}
}

func TestPlan_ReposIsDistinctAndOrdered(t *testing.T) {
	p := Plan{Steps: []Step{
		{Repo: "b", Args: []string{"fetch"}},
		{Repo: "a", Args: []string{"fetch"}},
		{Repo: "b", Args: []string{"pull"}},
	}}
	got := p.Repos()
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Errorf("Repos() = %v, want [b a]", got)
	}
}
