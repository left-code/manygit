package gh

import "testing"

// parsePRs decodes the exact envelope `gh api graphql` emits for the PR search:
// data.search.nodes, each an inline PullRequest fragment. The nested
// author/repository objects are flattened onto PullRequest, and baseRefName /
// headRefName become the BaseRef ← HeadRef the PRs pane draws.
func TestParsePRs(t *testing.T) {
	data := []byte(`{"data":{"search":{"nodes":[
	  {"number":123,"title":"Add AI studio tab","url":"https://github.com/blend-ed/frontend-app-authoring/pull/123",
	   "isDraft":false,"author":{"login":"rabeeh-ta"},"baseRefName":"master","headRefName":"feat/ai-studio",
	   "repository":{"nameWithOwner":"blend-ed/frontend-app-authoring"}},
	  {"number":118,"title":"Fix avatar upload","url":"https://github.com/blend-ed/frontend-app-account/pull/118",
	   "isDraft":true,"author":{"login":"someone"},"baseRefName":"main","headRefName":"fix/avatar",
	   "repository":{"nameWithOwner":"blend-ed/frontend-app-account"}}
	]}}}`)
	prs, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}
	p := prs[0]
	if p.Number != 123 || p.Title != "Add AI studio tab" || p.Author != "rabeeh-ta" ||
		p.RepoSlug != "blend-ed/frontend-app-authoring" || p.IsDraft {
		t.Errorf("PR[0] = %+v", p)
	}
	if p.BaseRef != "master" || p.HeadRef != "feat/ai-studio" {
		t.Errorf("PR[0] branches = %q <- %q, want master <- feat/ai-studio", p.BaseRef, p.HeadRef)
	}
	if !prs[1].IsDraft || prs[1].Author != "someone" {
		t.Errorf("PR[1] = %+v", prs[1])
	}
	if prs[1].BaseRef != "main" || prs[1].HeadRef != "fix/avatar" {
		t.Errorf("PR[1] branches = %q <- %q, want main <- fix/avatar", prs[1].BaseRef, prs[1].HeadRef)
	}
}

// An empty gh result is not an error — it means nothing pending. GraphQL returns
// an empty (or null) nodes array rather than the "[]" the old search JSON gave.
func TestParsePRs_Empty(t *testing.T) {
	for _, in := range []string{
		`{"data":{"search":{"nodes":[]}}}`,
		`{"data":{"search":{"nodes":null}}}`,
		`{"data":{"search":{}}}`,
		"",
		"   \n",
	} {
		prs, err := parsePRs([]byte(in))
		if err != nil {
			t.Errorf("parsePRs(%q): unexpected error %v", in, err)
		}
		if len(prs) != 0 {
			t.Errorf("parsePRs(%q) = %d PRs, want 0", in, len(prs))
		}
	}
}

func TestParsePRs_Malformed(t *testing.T) {
	if _, err := parsePRs([]byte(`{not json`)); err == nil {
		t.Error("malformed JSON should return an error")
	}
}

// A GraphQL 200 can still carry an errors array with partial/absent data (a bad
// query, a rate limit, a missing scope). Silently returning zero PRs would show
// "You're all caught up" over a broken query, so it has to surface as an error.
func TestParsePRs_GraphQLErrors(t *testing.T) {
	data := []byte(`{"data":{"search":null},"errors":[{"message":"Field 'headRefName' doesn't exist"}]}`)
	if _, err := parsePRs(data); err == nil {
		t.Fatal("a GraphQL errors array should return an error")
	} else if got := err.Error(); got == "" {
		t.Error("the error should carry the GraphQL message")
	}
}

// `type: ISSUE` searches can return non-PullRequest nodes, which the inline
// fragment leaves as empty objects. They are not PRs and must not become blank
// rows in the pane.
func TestParsePRs_SkipsNonPRNodes(t *testing.T) {
	data := []byte(`{"data":{"search":{"nodes":[
	  {},
	  {"number":7,"title":"Real one","author":{"login":"a"},"baseRefName":"main","headRefName":"x",
	   "repository":{"nameWithOwner":"o/r"}}
	]}}}`)
	prs, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 7 {
		t.Fatalf("got %+v, want just PR #7", prs)
	}
}

// A PR from a deleted account has author: null. It should still list, not crash
// or drop out — the row simply has no @handle.
func TestParsePRs_NullAuthor(t *testing.T) {
	data := []byte(`{"data":{"search":{"nodes":[
	  {"number":9,"title":"Orphaned","author":null,"baseRefName":"main","headRefName":"y",
	   "repository":{"nameWithOwner":"o/r"}}
	]}}}`)
	prs, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Author != "" || prs[0].Number != 9 {
		t.Fatalf("got %+v, want PR #9 with an empty author", prs)
	}
}
