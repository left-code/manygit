package aigit

// Result is what happened to one step.
type Result struct {
	Step    Step
	Err     error  // nil when the command succeeded
	Output  string // git's own output, trimmed; shown only when it failed
	Skipped bool   // an earlier step failed, so this one never ran
}

// OK reports whether the step ran and succeeded.
func (r Result) OK() bool { return r.Err == nil && !r.Skipped }

// Runner executes one git command in a repo. Injected so the executor can be
// tested against real temp repos — or a stub — without importing the TUI.
type Runner func(repo string, args ...string) (string, error)

// Execute runs the plan in order and STOPS at the first failure, marking the
// remaining steps skipped rather than attempting them.
//
// Stopping is the whole point. These are rebases and merges: carrying on past a
// conflict leaves several repos mid-rebase at once, which is far worse to unpick
// than the single broken repo you get by halting. The skipped entries are
// returned rather than dropped so the Output pane can say what did not happen.
//
// Callers must have run Validate and taken the user's confirmation first.
func Execute(p Plan, run Runner) []Result {
	out := make([]Result, 0, len(p.Steps))
	failed := false
	for _, s := range p.Steps {
		if failed {
			out = append(out, Result{Step: s, Skipped: true})
			continue
		}
		text, err := run(s.Repo, s.Args...)
		out = append(out, Result{Step: s, Err: err, Output: text})
		if err != nil {
			failed = true
		}
	}
	return out
}
