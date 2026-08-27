package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rabeeh-ta/manygit/internal/xdgdir"
)

// Owners a managed install can report. SelfManaged means manygit owns its own
// binary and may replace it in place; anything else means a package manager
// owns the file and manygit must not touch it.
const (
	SelfManaged = ""
	Homebrew    = "homebrew"
	GoInstall   = "go"
)

// caskroomSegment is the directory Homebrew stages a cask's files into,
// $(brew --caskroom)/<token>/<version>/. What lands in $(brew --prefix)/bin is
// a symlink to a file underneath it, which is why exePath must arrive already
// resolved. Matched case-sensitively so an ordinary directory named "caskroom"
// doesn't register as a Homebrew install.
const caskroomSegment = "/Caskroom/"

// ManagedBy names whatever owns the manygit binary on disk, or SelfManaged when
// manygit owns it itself.
//
// exePath must already be resolved through symlinks. ldflagVersion is what the
// linker stamped into main.version — the value before ResolveVersion touches it.
// mainModuleVersion is debug.ReadBuildInfo's main module version.
//
// The `go install` test is deliberately NOT "build info holds a real version".
// Since Go 1.24 a plain `go build` on a clean tagged tree stamps the VCS tag into
// build info too, and that is precisely what GoReleaser runs — so keying off it
// alone would classify every released binary, curl installs included, as a
// `go install` and disable self-update for everyone. What actually separates them
// is the ldflag: only a release build has one stamped, and `go install` cannot set
// ldflags. A dev-default ldflag beside a real module version is `go install`.
//
// Ownership is derived at runtime rather than baked in at build time because the
// Homebrew cask and the curl installer serve the same release archive, so no
// build-time value could tell them apart. See
// docs/adr/0001-runtime-install-ownership-detection.md.
func ManagedBy(exePath, ldflagVersion, mainModuleVersion string) string {
	if strings.Contains(exePath, caskroomSegment) {
		return Homebrew
	}
	if !IsRelease(ldflagVersion) && IsRelease(mainModuleVersion) {
		return GoInstall
	}
	return SelfManaged
}

// upgradeCommands maps a managed install's owner to the command that upgrades it.
// An owner absent from this map gets no update notice rather than a guess.
//
// The Homebrew entry leads with `brew update` for a reason that cost a user a
// confused afternoon. manygit ships as a cask in a third-party tap, and a tap is
// an ordinary git clone under Library/Taps — it is NOT part of the JSON API that
// `brew upgrade` refreshes on its own (that covers homebrew-core and
// homebrew-cask only). So a plain `brew upgrade manygit` compares the installed
// version against whatever cask the clone happens to hold, which may be weeks
// old, and prints "the latest version is already installed" — making manygit
// look like it invented the release it had just correctly detected. Only
// `brew update` pulls the tap. `--cask` is explicit so the token can never be
// read as a formula.
var upgradeCommands = map[string]string{
	Homebrew:  "brew update && brew upgrade --cask manygit",
	GoInstall: "go install github.com/rabeeh-ta/manygit@latest",
}

// UpdateNotice returns what a managed install is told when a newer release
// exists: what is available, and the command that installs it. Nothing is
// applied — a managed install must never replace its own binary.
//
// It returns "" for a self-managed install, which takes the interactive
// self-update path instead, and for any owner without a known command.
func UpdateNotice(managedBy, latest, current string) string {
	cmd, ok := upgradeCommands[managedBy]
	if !ok {
		return ""
	}
	return fmt.Sprintf("manygit %s is available (you have %s).\nRun: %s", latest, current, cmd)
}

// ShouldNotify reports whether enough time has passed since the last update
// notice to show another. A zero last means none has ever been shown.
//
// A last in the future — a clock change, or a cache copied between machines —
// counts as due. Silencing notices until the clock catches up would be a worse
// failure than showing one early.
func ShouldNotify(last, now time.Time, every time.Duration) bool {
	if last.After(now) {
		return true
	}
	return now.Sub(last) >= every
}

// ResolveVersion picks the version manygit reports for itself.
//
// GoReleaser stamps the real version into ldflagVersion and leaves build info at
// "(devel)". `go install <pkg>@<version>` does the opposite: the ldflag keeps its
// source default and the version arrives in the module's build info. When neither
// is a release this is a local `go build`, which stays a dev build — and a dev
// build never checks for updates.
func ResolveVersion(ldflagVersion, buildInfoVersion string) string {
	if IsRelease(ldflagVersion) {
		return ldflagVersion
	}
	if IsRelease(buildInfoVersion) {
		return buildInfoVersion
	}
	return ldflagVersion
}

// noticeMarkerPath records when the last update notice was shown. It sits beside
// the changelog marker in the cache dir, and holds an RFC3339 timestamp.
func noticeMarkerPath() string {
	return filepath.Join(xdgdir.CacheHome(), "manygit", "notified-at")
}

// LastNotified reads when the last update notice was shown. A missing or
// unreadable marker yields the zero time, which ShouldNotify treats as due.
func LastNotified() time.Time {
	b, err := os.ReadFile(noticeMarkerPath())
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// MarkNotified records that a notice was just shown. Failures are ignored — an
// unwritable cache dir means the next launch notices again, which is harmless.
func MarkNotified(t time.Time) {
	p := noticeMarkerPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(t.Format(time.RFC3339)), 0o644)
}
