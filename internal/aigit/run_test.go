package aigit

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rabeeh-ta/manygit/internal/git"
)

func TestExecute_RunsInOrder(t *testing.T) {
	var got []string
	res := Execute(Plan{Steps: []Step{
		{Repo: "a", Args: []string{"fetch"}},
		{Repo: "b", Args: []string{"pull"}},
	}}, func(repo string, args ...string) (string, error) {
		got = append(got, repo+":"+args[0])
		return "", nil
	})
	if len(got) != 2 || got[0] != "a:fetch" || got[1] != "b:pull" {
		t.Errorf("ran %v, want [a:fetch b:pull]", got)
	}
	for _, r := range res {
		if !r.OK() {
			t.Errorf("%s should have succeeded", r.Step.Command())
		}
	}
}

// The whole reason for stopping: a conflict must leave ONE repo to unpick, not
// five. Everything after the failure must be reported as skipped, not attempted.
func TestExecute_StopsAtFirstFailure(t *testing.T) {
	boom := errors.New("CONFLICT")
	var attempted []string
	res := Execute(Plan{Steps: []Step{
		{Repo: "a", Args: []string{"rebase", "master"}},
		{Repo: "b", Args: []string{"rebase", "master"}},
		{Repo: "c", Args: []string{"rebase", "master"}},
		{Repo: "d", Args: []string{"rebase", "master"}},
	}}, func(repo string, args ...string) (string, error) {
		attempted = append(attempted, repo)
		if repo == "b" {
			return "", boom
		}
		return "", nil
	})

	if len(attempted) != 2 {
		t.Fatalf("attempted %v — must stop after the failure", attempted)
	}
	if !res[0].OK() {
		t.Error("a should have succeeded")
	}
	if !errors.Is(res[1].Err, boom) {
		t.Errorf("b should carry the failure, got %v", res[1].Err)
	}
	if !res[2].Skipped || !res[3].Skipped {
		t.Error("c and d must be reported as skipped, not silently dropped")
	}
	if res[2].OK() {
		t.Error("a skipped step is not a success")
	}
}

// End to end against a real repository, through the same git.Run the TUI uses —
// so this would catch argv being mangled or the working directory being wrong.
func TestExecute_AgainstRealRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	sh := func(wd string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wd
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo := filepath.Join(dir, "r")
	sh(dir, "init", "-q", "-b", "main", "r")
	sh(repo, "config", "user.email", "t@t")
	sh(repo, "config", "user.name", "t")
	sh(repo, "commit", "-q", "--allow-empty", "-m", "first")

	run := func(name string, args ...string) (string, error) {
		return git.Run(filepath.Join(dir, name), args...)
	}

	// A tag, then a deliberately impossible checkout, then a step that must be skipped.
	res := Execute(Plan{Steps: []Step{
		{Repo: "r", Args: []string{"tag", "v9.9.9"}},
		{Repo: "r", Args: []string{"checkout", "no-such-branch"}},
		{Repo: "r", Args: []string{"tag", "v8.8.8"}},
	}}, run)

	if !res[0].OK() {
		t.Fatalf("tag should have succeeded: %v", res[0].Err)
	}
	if res[1].Err == nil {
		t.Fatal("checking out a missing branch should fail")
	}
	if !res[2].Skipped {
		t.Error("the step after the failure must be skipped")
	}

	tags, err := git.Run(repo, "tag", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if tags != "v9.9.9" {
		t.Errorf("tags = %q — want only v9.9.9 (v8.8.8 must never have run)", tags)
	}
}

// Multi-word arguments must survive as ONE argv element. If they were ever joined
// into a shell string this would create a differently-named tag, or fail.
func TestExecute_ArgvIsNotAShellString(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "r")
	for _, a := range [][]string{
		{"init", "-q", "-b", "main", "r"},
	} {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", a, err, out)
		}
	}
	for _, a := range [][]string{
		{"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		if _, err := git.Run(repo, a...); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := git.Run(repo, "tag", "-a", "v1.0.0", "-m", "release v1.0.0; echo pwned"); err != nil {
		t.Fatal(err)
	}
	msg, err := git.Run(repo, "tag", "-n99", "--list", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	// The whole message, semicolon and all, is data — not two commands.
	if want := "release v1.0.0; echo pwned"; !strings.Contains(msg, want) {
		t.Errorf("tag message = %q, want it to contain %q verbatim", msg, want)
	}
}
