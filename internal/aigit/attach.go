package aigit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// refRe matches an @-reference in a request: "@scripts/update-all.sh". It stops
// at whitespace, and trims trailing sentence punctuation so "…@build.sh, then"
// references build.sh rather than "build.sh,".
var refRe = regexp.MustCompile(`@([^\s]+)`)

const (
	// maxAttachBytes caps one file. A shell script is a few KB; anything far
	// larger is not what this is for, and the whole prompt still has to fit.
	maxAttachBytes = 32 << 10
	// maxAttachments caps how many files one request can pull in.
	maxAttachments = 4
)

// Attachment is a referenced file's contents, or the reason it has none.
type Attachment struct {
	Ref  string // as typed, without the @
	Body string
	Err  string // non-empty when the file was not read; shown to the user
}

// Refs pulls the @-references out of a request, in order, without duplicates.
func Refs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range refRe.FindAllStringSubmatch(s, -1) {
		ref := strings.TrimRight(m[1], ".,;:!?)")
		if ref != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// Attach reads each reference relative to root.
//
// A reference is refused unless it resolves to a path INSIDE root. That matters
// because the contents are sent to an external AI CLI: without the check,
// "@../../.ssh/id_rsa" or "@/etc/passwd" would quietly exfiltrate whatever the
// user could read. Absolute paths and anything climbing out with ".." are
// rejected by name, and the resolved path is re-checked against root afterwards
// so a symlink cannot smuggle one out either.
func Attach(root string, refs []string) []Attachment {
	var out []Attachment
	for i, ref := range refs {
		if i >= maxAttachments {
			out = append(out, Attachment{Ref: ref,
				Err: fmt.Sprintf("not read — at most %d files per request", maxAttachments)})
			continue
		}
		out = append(out, attachOne(root, ref))
	}
	return out
}

func attachOne(root, ref string) Attachment {
	bad := func(why string) Attachment { return Attachment{Ref: ref, Err: why} }

	if filepath.IsAbs(ref) {
		return bad("not read — only paths inside the tree can be referenced")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return bad("not read — cannot resolve the scan root")
	}
	full := filepath.Join(absRoot, filepath.Clean(ref))
	if !underRoot(absRoot, full) {
		return bad("not read — outside the tree")
	}
	// Re-check after following symlinks, so a link inside the tree cannot point
	// out of it. A broken link falls through to the read error below.
	if real, err := filepath.EvalSymlinks(full); err == nil && !underRoot(absRoot, real) {
		return bad("not read — resolves outside the tree")
	}

	info, err := os.Stat(full)
	if err != nil {
		return bad("not read — no such file in the tree")
	}
	if info.IsDir() {
		return bad("not read — that is a directory")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return bad("not read — " + err.Error())
	}
	body, truncated := string(b), false
	if len(b) > maxAttachBytes {
		body, truncated = string(b[:maxAttachBytes]), true
	}
	a := Attachment{Ref: ref, Body: body}
	if truncated {
		a.Body += "\n… (truncated)"
	}
	return a
}

func underRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// renderAttachments formats the files for the prompt, or "" when there are none.
func renderAttachments(atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nReferenced files:\n")
	for _, a := range atts {
		if a.Err != "" {
			fmt.Fprintf(&b, "\n--- %s: %s\n", a.Ref, a.Err)
			continue
		}
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n--- end %s ---\n", a.Ref, a.Body, a.Ref)
	}
	return b.String()
}
