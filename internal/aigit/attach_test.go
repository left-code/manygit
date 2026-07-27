package aigit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRefs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"@scripts/update-all.sh only the frontend apps", []string{"scripts/update-all.sh"}},
		{"compare @a.sh and @b.sh", []string{"a.sh", "b.sh"}},
		{"@a.sh, then @a.sh again", []string{"a.sh"}}, // deduped, punctuation trimmed
		{"run @build.sh.", []string{"build.sh"}},
		{"no references here", nil},
		{"an email like a@b.com is not a path we care about", []string{"b.com"}},
	} {
		got := Refs(tc.in)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Refs(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAttach_ReadsAFileInTheTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ngit -C apps/web pull --ff-only\n"
	if err := os.WriteFile(filepath.Join(root, "scripts", "update-all.sh"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Attach(root, []string{"scripts/update-all.sh"})
	if len(got) != 1 || got[0].Err != "" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Body != body {
		t.Errorf("body = %q, want %q", got[0].Body, body)
	}
}

// The contents go to an external AI CLI, so a reference that escapes the tree is
// an exfiltration path, not just a bad filename. Every one of these must refuse.
func TestAttach_RefusesAnythingOutsideTheTree(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"../secret.txt",
		"../../etc/passwd",
		"scripts/../../secret.txt",
		outside, // absolute
	} {
		got := Attach(root, []string{ref})
		if len(got) != 1 {
			t.Fatalf("%q: got %d attachments", ref, len(got))
		}
		if got[0].Err == "" {
			t.Errorf("%q was READ — it must be refused", ref)
		}
		if strings.Contains(got[0].Body, "PRIVATE") {
			t.Errorf("%q leaked file contents", ref)
		}
	}
}

// A symlink inside the tree pointing out of it is the same leak wearing a hat.
func TestAttach_RefusesSymlinkOutOfTheTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := t.TempDir()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "innocent.sh")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot symlink here: %v", err)
	}
	got := Attach(root, []string{"innocent.sh"})
	if got[0].Err == "" || strings.Contains(got[0].Body, "PRIVATE") {
		t.Errorf("a symlink out of the tree was followed: %+v", got[0])
	}
}

func TestAttach_MissingFileSaysSo(t *testing.T) {
	got := Attach(t.TempDir(), []string{"scripts/nope.sh"})
	if got[0].Err == "" {
		t.Fatal("a missing file must report why, not read empty")
	}
}

func TestAttach_CapsSizeAndCount(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", maxAttachBytes+5000)
	if err := os.WriteFile(filepath.Join(root, "big.sh"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Attach(root, []string{"big.sh"})
	if len(got[0].Body) > maxAttachBytes+64 {
		t.Errorf("body is %d bytes — the cap did not apply", len(got[0].Body))
	}
	if !strings.Contains(got[0].Body, "truncated") {
		t.Error("a truncated file must say so, or the model reasons about half a script silently")
	}

	var many []string
	for i := 0; i < maxAttachments+3; i++ {
		many = append(many, "f.sh")
	}
	// Refs dedupes, so build the over-cap list directly.
	res := Attach(root, many)
	read := 0
	for _, a := range res {
		if strings.Contains(a.Err, "at most") {
			read++
		}
	}
	if read == 0 {
		t.Error("the attachment count cap never fired")
	}
}

func TestPrompt_IncludesReferencedFiles(t *testing.T) {
	atts := []Attachment{
		{Ref: "scripts/update-all.sh", Body: "git -C apps/web pull\n"},
		{Ref: "gone.sh", Err: "not read — no such file in the tree"},
	}
	p := Prompt(ctx(), "@scripts/update-all.sh only the frontend apps", atts)
	for _, want := range []string{
		"scripts/update-all.sh",
		"git -C apps/web pull",
		"not read — no such file in the tree",
		"INSTRUCTIONS TO READ", // never executed verbatim
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

func TestNames_CompletesScriptsWithTheAt(t *testing.T) {
	c := ctx()
	c.Scripts = []string{"scripts/update-all.sh"}
	if got := Complete(c.Names(), "@scripts/upd"); got != "ate-all.sh" {
		t.Errorf("Complete = %q, want %q", got, "ate-all.sh")
	}
}
