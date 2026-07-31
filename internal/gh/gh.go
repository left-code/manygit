// Package gh is a thin wrapper over the GitHub CLI (`gh`). manygit shells out to
// gh for pull-request info and checkout, reusing gh's own auth — it holds no
// GitHub token of its own (mirroring the harness package's use of the AI CLIs).
//
// Everything degrades gracefully: if gh is missing or logged out, the calls
// return an error and the TUI simply omits the GitHub features. `@me` is a
// server-side search qualifier, so it does not depend on the gh version.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PullRequest is one open pull request as returned by the PR search.
type PullRequest struct {
	Number   int    // PR number within its repo
	Title    string // PR title
	Author   string // author login (author.login); "" if the account was deleted
	RepoSlug string // "owner/repo" (repository.nameWithOwner)
	URL      string // html URL
	IsDraft  bool   // draft PRs are shown dimmed
	BaseRef  string // branch the PR merges INTO (baseRefName)
	HeadRef  string // branch being merged (headRefName)
}

// searchPR is the raw JSON shape of one search result node. Author is a pointer
// because GitHub returns author: null for a deleted account, and a non-PR node
// (a `type: ISSUE` search can return issues) decodes to the zero value — both
// have to survive decoding rather than panic or become a blank row.
type searchPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Draft  bool   `json:"isDraft"`
	Base   string `json:"baseRefName"`
	Head   string `json:"headRefName"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// searchResult is the `gh api graphql` envelope. A GraphQL request can answer
// 200 with a populated errors array and null data, so errors must be inspected —
// decoding only `data` would turn a broken query into a silent "no PRs".
type searchResult struct {
	Data struct {
		Search struct {
			Nodes []searchPR `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// prSearchQuery asks for the same open PRs `gh search prs` used to return, plus
// the two branch names — which the REST-backed `gh search prs --json` cannot
// provide at any version (its field set has no baseRefName/headRefName). Going
// through GraphQL keeps it to ONE request per list; the alternative, a
// `gh pr view` per result, would be up to 50.
const prSearchQuery = `query($q: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n) {
    nodes {
      ... on PullRequest {
        number
        title
        url
        isDraft
        baseRefName
        headRefName
        author { login }
        repository { nameWithOwner }
      }
    }
  }
}`

// prSearchLimit is how many PRs each list fetches, matching the old
// `gh search prs --limit=50`.
const prSearchLimit = 50

// run executes gh in dir (or the process cwd when dir=="") and returns trimmed
// stdout, wrapping a non-zero exit with its stderr.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Available reports whether the gh binary is on PATH. It does NOT check auth —
// use Login for that.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// Login returns the authenticated user's login (via `gh api user`) and whether
// the call succeeded. A failure means gh is missing or not logged in; either way
// the GitHub features are unavailable. Preferred over parsing `gh auth status`,
// whose text format has changed across gh versions.
func Login(ctx context.Context) (string, bool) {
	out, err := run(ctx, "", "api", "user", "--jq", ".login")
	if err != nil || out == "" {
		return "", false
	}
	return out, true
}

// MyOpenPRs returns the current user's open PRs (any review state).
func MyOpenPRs(ctx context.Context) ([]PullRequest, error) {
	return searchPRs(ctx, "is:pr is:open author:@me")
}

// ReviewRequestedPRs returns open PRs whose review has been requested from the
// current user.
func ReviewRequestedPRs(ctx context.Context) ([]PullRequest, error) {
	return searchPRs(ctx, "is:pr is:open review-requested:@me")
}

// searchPRs runs the PR search as one GraphQL request and decodes the result.
// `@me` is a server-side search qualifier, so this does not depend on knowing
// the login locally.
func searchPRs(ctx context.Context, query string) ([]PullRequest, error) {
	out, err := run(ctx, "", "api", "graphql",
		"-f", "query="+prSearchQuery,
		"-f", "q="+query,
		"-F", "n="+strconv.Itoa(prSearchLimit))
	if err != nil {
		return nil, err
	}
	return parsePRs([]byte(out))
}

// parsePRs decodes the `gh api graphql` envelope into PullRequests. Pure (no
// exec), so it is unit-testable without a live gh.
func parsePRs(data []byte) ([]PullRequest, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var res searchResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("gh: parse prs: %w", err)
	}
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("gh: search prs: %s", res.Errors[0].Message)
	}
	nodes := res.Data.Search.Nodes
	prs := make([]PullRequest, 0, len(nodes))
	for _, r := range nodes {
		if r.Number == 0 {
			continue // not a PullRequest — the inline fragment left it empty
		}
		login := ""
		if r.Author != nil {
			login = r.Author.Login
		}
		prs = append(prs, PullRequest{
			Number:   r.Number,
			Title:    r.Title,
			Author:   login,
			RepoSlug: r.Repository.NameWithOwner,
			URL:      r.URL,
			IsDraft:  r.Draft,
			BaseRef:  r.Base,
			HeadRef:  r.Head,
		})
	}
	return prs, nil
}

// Checkout checks out pull request `number` in the repo at dir using
// `gh pr checkout` — which resolves the head branch (including forks) and sets
// up tracking. gh reads the repo from dir's origin remote, so dir must be the
// local clone whose origin matches the PR's repository. The caller must ensure a
// clean working tree; gh refuses a checkout that would clobber local changes.
func Checkout(ctx context.Context, dir string, number int) error {
	_, err := run(ctx, dir, "pr", "checkout", fmt.Sprintf("%d", number))
	return err
}
