package aigit

import "strings"

// Rejection is one step the validator refused, with the reason shown to the user.
type Rejection struct {
	Step   Step
	Reason string
}

// Validate checks every step and returns the ones that must not run. An empty
// result means the plan may be offered for confirmation.
//
// This is a BLOCKLIST, not an allowlist: every ordinary git verb — worktree,
// revert, reflog, describe — keeps working, and the list never needs revisiting
// as git grows. What it blocks is the small, stable set of ways a git command can
// stop being just a git command:
//
//   - flags that hand git a shell command to run
//   - flags that move git to a different repo than the one named in the step,
//     which would make the confirm screen show one thing and do another
//   - pushes that destroy history or refs on the remote
//
// It backs up the confirm gate rather than replacing it. It exists because the
// prompt is built from repo content — branch names, tag names, commit messages,
// all from arbitrary repositories — so untrusted text reaches the model that
// writes these commands. The prompt is a request; these checks are a guarantee.
//
// Known limit: a repo's own .git/config can define an alias whose body starts
// with "!", which git runs through a shell. Catching that would require knowing
// git's builtin verbs, i.e. an allowlist, which costs every future verb. The
// confirm screen still shows the command, and the repos here are the user's own.
func Validate(p Plan) []Rejection {
	var out []Rejection
	for _, s := range p.Steps {
		if why := checkStep(s); why != "" {
			out = append(out, Rejection{Step: s, Reason: why})
		}
	}
	return out
}

// checkStep returns why a step is refused, or "" if it is allowed.
func checkStep(s Step) string {
	// --upload-pack / --receive-pack / --exec-path are accepted BOTH as git globals
	// and as options of push, fetch, clone and ls-remote, so position proves
	// nothing — screen them wherever they appear.
	for _, a := range s.Args {
		switch flagName(a) {
		case "--exec-path", "--upload-pack", "--receive-pack":
			return "points git at another binary to execute"
		}
	}
	for i, a := range s.Args {
		if !strings.HasPrefix(a, "-") {
			return checkSub(a, s.Args[i+1:])
		}
		if why := checkGlobalFlag(a); why != "" {
			return why
		}
	}
	return "no git subcommand"
}

// checkGlobalFlag screens the flags that come BEFORE the subcommand. Every one it
// rejects takes a value, so nothing after it can be mistaken for the subcommand;
// the globals it lets through (--no-pager, --paginate, --bare, …) are all boolean.
func checkGlobalFlag(a string) string {
	switch flagName(a) {
	case "-c", "--config-env":
		// git -c alias.x='!sh -c "…"' x  — arbitrary shell.
		return "sets config inline, which can define a shell alias"
	case "-C", "--git-dir", "--work-tree":
		// The step names a repo and manygit runs it there; these would redirect it,
		// so the confirm screen would show one repo and act on another.
		return "redirects git away from the repo this step names"
	}
	return ""
}

// checkSub screens a subcommand and its arguments.
func checkSub(sub string, rest []string) string {
	switch sub {
	case "push":
		for _, a := range rest {
			switch flagName(a) {
			case "-f", "--force", "--force-with-lease", "--force-if-includes":
				return "force-pushes, which rewrites history on the remote"
			case "-d", "--delete":
				return "deletes a remote ref"
			case "--mirror", "--prune":
				return "removes remote refs that are missing locally"
			}
			// A refspec deletes when it has an empty source (":main") and forces
			// when it is prefixed with "+" ("+HEAD:main") — neither says "force".
			if strings.HasPrefix(a, ":") {
				return "deletes a remote ref"
			}
			if strings.HasPrefix(a, "+") && strings.Contains(a, ":") {
				return "force-pushes via a + refspec"
			}
		}
	case "rebase":
		for _, a := range rest {
			if n := flagName(a); n == "-x" || n == "--exec" {
				return "runs a shell command between commits"
			}
		}
	case "bisect":
		if len(rest) > 0 && rest[0] == "run" {
			return "runs an arbitrary command for each revision"
		}
	case "submodule":
		for _, a := range rest {
			if a == "foreach" {
				return "runs a shell command in each submodule"
			}
		}
	case "filter-branch":
		for _, a := range rest {
			if n := flagName(a); strings.HasPrefix(n, "--") && strings.HasSuffix(n, "-filter") {
				return "runs a shell command over every commit"
			}
		}
	}
	return ""
}

// flagName strips an inline value so "--exec-path=/tmp/x" screens as "--exec-path".
func flagName(a string) string {
	if i := strings.IndexByte(a, '='); i > 0 {
		return a[:i]
	}
	return a
}
