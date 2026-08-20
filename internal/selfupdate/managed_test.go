package selfupdate

import (
	"testing"
	"time"
)

// A managed install is one where something other than manygit owns the binary
// on disk. ManagedBy names that owner, or returns "" for a self-managed install.
func TestManagedBy(t *testing.T) {
	cases := []struct {
		name           string
		exePath        string
		ldflag, modVer string
		want           string
	}{
		{"cask on apple silicon", "/opt/homebrew/Caskroom/manygit/1.1.3/manygit", "v1.1.3", "v1.1.3", "homebrew"},
		{"cask on intel", "/usr/local/Caskroom/manygit/1.1.3/manygit", "v1.1.3", "v1.1.3", "homebrew"},
		{"cask under a custom prefix", "/Users/rt/brew/Caskroom/manygit/1.1.3/manygit", "v1.1.3", "v1.1.3", "homebrew"},

		// Regression: since Go 1.24 a plain `go build` on a clean tagged tree —
		// exactly what GoReleaser runs — stamps the VCS tag into build info. A
		// released binary must stay self-managed despite that, or every curl
		// install silently loses self-update.
		{"curl install of a release build", "/home/rt/.local/bin/manygit", "v1.1.3", "v1.1.3", ""},
		{"curl install, no vcs stamping", "/home/rt/.local/bin/manygit", "v1.1.3", "(devel)", ""},

		{"local go build stays self-managed", "/mnt/dev/manygit/manygit", "0.1.0-dev", "(devel)", ""},
		{"local build of a dirty tree", "/mnt/dev/manygit/manygit", "0.1.0-dev", "v1.1.3+dirty", ""},
		{"go install to GOPATH bin", "/home/rt/go/bin/manygit", "0.1.0-dev", "v1.1.3", "go"},
		{"go install to a custom GOBIN", "/opt/tools/manygit", "0.1.0-dev", "v1.1.3", "go"},
		{"no build info is self-managed", "/home/rt/.local/bin/manygit", "0.1.0-dev", "", ""},
		{"caskroom outranks everything", "/opt/homebrew/Caskroom/manygit/1.1.3/manygit", "0.1.0-dev", "v1.1.3", "homebrew"},
		{"a repo dir merely named caskroom does not count", "/home/rt/caskroom/manygit", "v1.1.3", "v1.1.3", ""},
	}
	for _, c := range cases {
		if got := ManagedBy(c.exePath, c.ldflag, c.modVer); got != c.want {
			t.Errorf("%s: ManagedBy(%q,%q,%q)=%q want %q", c.name, c.exePath, c.ldflag, c.modVer, got, c.want)
		}
	}
}

// An update notice tells the user a newer release exists and names the command
// that installs it, without applying anything. Self-managed installs get no
// notice — they get the interactive self-update prompt instead.
func TestUpdateNotice(t *testing.T) {
	cases := []struct {
		name      string
		managedBy string
		want      string
	}{
		{
			name:      "homebrew names the brew command",
			managedBy: Homebrew,
			want:      "manygit v1.2.0 is available (you have v1.1.3).\nRun: brew upgrade manygit",
		},
		{
			name:      "go install names the module path",
			managedBy: GoInstall,
			want:      "manygit v1.2.0 is available (you have v1.1.3).\nRun: go install github.com/rabeeh-ta/manygit@latest",
		},
		{
			name:      "self-managed gets no notice",
			managedBy: SelfManaged,
			want:      "",
		},
		{
			name:      "an owner we have no command for gets no notice",
			managedBy: "aptitude",
			want:      "",
		},
	}
	for _, c := range cases {
		if got := UpdateNotice(c.managedBy, "v1.2.0", "v1.1.3"); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A managed install cannot act on the notice from inside manygit, so it is rate
// limited: printing it every launch turns it into wallpaper.
func TestShouldNotify(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	const day = 24 * time.Hour

	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"never notified before", time.Time{}, true},
		{"notified 25 hours ago", now.Add(-25 * time.Hour), true},
		{"notified an hour ago", now.Add(-time.Hour), false},
		{"notified a minute ago", now.Add(-time.Minute), false},
		{"notified exactly one interval ago", now.Add(-day), true},
		{"marker is in the future from a clock change", now.Add(48 * time.Hour), true},
	}
	for _, c := range cases {
		if got := ShouldNotify(c.last, now, day); got != c.want {
			t.Errorf("%s: ShouldNotify(%v, now, 24h)=%v want %v", c.name, c.last, got, c.want)
		}
	}
}

// A GoReleaser build carries its version in an ldflag; a `go install` build
// carries it in the module's build info and leaves the ldflag at its default.
func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name             string
		ldflag, buildLog string
		want             string
	}{
		{"goreleaser build", "v1.1.3", "(devel)", "v1.1.3"},
		{"go install reads its version from build info", "0.1.0-dev", "v1.1.3", "v1.1.3"},
		{"local go build stays a dev build", "0.1.0-dev", "(devel)", "0.1.0-dev"},
		{"no build info at all", "0.1.0-dev", "", "0.1.0-dev"},
		{"the ldflag wins when both are releases", "v1.1.3", "v1.0.0", "v1.1.3"},
	}
	for _, c := range cases {
		if got := ResolveVersion(c.ldflag, c.buildLog); got != c.want {
			t.Errorf("%s: ResolveVersion(%q,%q)=%q want %q", c.name, c.ldflag, c.buildLog, got, c.want)
		}
	}
}
