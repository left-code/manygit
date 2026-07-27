// Package aigit turns a natural-language request into a checked plan of git
// commands. It holds everything about the `:` harness mode that isn't UI: the
// plan types, parsing the harness's reply, validating it, and assembling the
// context the harness needs. Nothing here imports Bubble Tea, so the risky half
// of the feature — "would this plan be allowed to run?" — is testable by feeding
// it strings.
package aigit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Step is one git invocation against one repo. Args is argv for git, never a
// shell string: manygit execs git with these as separate arguments, so pipes,
// redirection and && cannot be expressed by construction.
type Step struct {
	Repo string   `json:"repo"`
	Args []string `json:"args"`
}

// Command renders the step the way the confirm screen shows it.
func (s Step) Command() string { return "git " + strings.Join(s.Args, " ") }

// Plan is the harness's reply. Steps may be empty: that means the harness is
// declining or asking, and Note carries the one line it is allowed to say.
type Plan struct {
	Steps []Step `json:"steps"`
	Note  string `json:"note"`
}

// Repos lists the distinct repos a plan touches, in first-mentioned order.
func (p Plan) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range p.Steps {
		if !seen[s.Repo] {
			seen[s.Repo] = true
			out = append(out, s.Repo)
		}
	}
	return out
}

// ErrNoJSON means the harness replied with something that wasn't a plan at all.
var ErrNoJSON = errors.New("no JSON object in the harness reply")

// Parse pulls the plan out of a harness reply. The CLIs are asked for bare JSON
// but sometimes wrap it in prose or a ``` fence, so this finds the first balanced
// JSON object rather than requiring the whole reply to parse. A reply with no
// object at all is an error — never a silently empty plan, which would look like
// "nothing to do" when in fact the request failed.
func Parse(out string) (Plan, error) {
	raw, ok := firstJSONObject(out)
	if !ok {
		return Plan{}, ErrNoJSON
	}
	var p Plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Plan{}, fmt.Errorf("harness reply was not a valid plan: %w", err)
	}
	for i, s := range p.Steps {
		if strings.TrimSpace(s.Repo) == "" {
			return Plan{}, fmt.Errorf("step %d names no repo", i+1)
		}
		if len(s.Args) == 0 {
			return Plan{}, fmt.Errorf("step %d for %q has no command", i+1, s.Repo)
		}
	}
	p.Note = strings.TrimSpace(p.Note)
	return p, nil
}

// firstJSONObject returns the first balanced {...} run in s, ignoring braces that
// appear inside JSON strings (a commit message in a note can contain one).
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// nothing — braces inside strings don't count
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
