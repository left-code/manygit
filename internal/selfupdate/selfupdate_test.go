package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestNewerThan(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.1", "v0.1.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"0.2.0", "v0.1.0", true},     // mixed v-prefix
		{"v0.2.0", "0.1.0-dev", true}, // dev current is older than any release
		{"garbage", "v0.1.0", false},  // unparseable latest never wins
	}
	for _, c := range cases {
		if got := NewerThan(c.latest, c.current); got != c.want {
			t.Errorf("NewerThan(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.1.0":     true,
		"0.1.0":      true,
		"0.1.0-dev":  false,
		"v1.2.3-rc1": false,
		"dev":        false,
		"":           false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q)=%v want %v", v, got, want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := assetName("darwin", "arm64"); got != "manygit_darwin_arm64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
	if got := assetName("linux", "amd64"); got != "manygit_linux_amd64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
	if got := assetName("windows", "amd64"); got != "manygit_windows_amd64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
}

func TestBinaryNameFor(t *testing.T) {
	if got := binaryNameFor("windows"); got != "manygit.exe" {
		t.Errorf("binaryNameFor(windows) = %q, want manygit.exe", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := binaryNameFor(goos); got != "manygit" {
			t.Errorf("binaryNameFor(%s) = %q, want manygit", goos, got)
		}
	}
}

func TestAggregate_SplitsByOSIncludingWindows(t *testing.T) {
	rs := []Release{{
		Tag:         "v1.0.0",
		PublishedAt: "2026-01-01T00:00:00Z",
		Assets: []Asset{
			{Name: "manygit_linux_amd64.tar.gz", DownloadCount: 3},
			{Name: "manygit_darwin_arm64.tar.gz", DownloadCount: 2},
			{Name: "manygit_windows_amd64.tar.gz", DownloadCount: 5},
			{Name: "checksums.txt", DownloadCount: 100}, // not a binary; excluded
		},
	}}
	s := aggregate(rs, 10)
	if s.BinaryDownloads != 10 {
		t.Errorf("BinaryDownloads = %d, want 10", s.BinaryDownloads)
	}
	want := map[string]int{"linux": 3, "darwin": 2, "windows": 5}
	for goos, n := range want {
		if s.ByOS[goos] != n {
			t.Errorf("ByOS[%s] = %d, want %d", goos, s.ByOS[goos], n)
		}
	}
}

// TestReplaceExecutableFor_Windows exercises the windows rename-aside sequence
// on ordinary files (no locked-image behavior to simulate, but the sequencing
// — old exe moved to .old, new binary takes its name — is plain os.Rename and
// verifiable on any OS).
func TestReplaceExecutableFor_Windows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "manygit.exe")
	tmpName := filepath.Join(dir, ".manygit-new-abc")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpName, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutableFor(tmpName, exe, "windows"); err != nil {
		t.Fatalf("replaceExecutableFor: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("reading replaced exe: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("exe content = %q, want %q", got, "new")
	}
	if _, err := os.Stat(tmpName); !os.IsNotExist(err) {
		t.Errorf("tmpName should have been consumed by the rename, stat err = %v", err)
	}
}

func TestReplaceExecutableFor_Unix(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "manygit")
	tmpName := filepath.Join(dir, ".manygit-new-abc")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpName, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutableFor(tmpName, exe, "linux"); err != nil {
		t.Fatalf("replaceExecutableFor: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("reading replaced exe: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("exe content = %q, want %q", got, "new")
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  manygit_linux_amd64.tar.gz\ndef456  manygit_darwin_arm64.tar.gz\n"
	if got := checksumFor(sums, "manygit_darwin_arm64.tar.gz"); got != "def456" {
		t.Errorf("checksumFor = %q", got)
	}
	if got := checksumFor(sums, "nope.tar.gz"); got != "" {
		t.Errorf("checksumFor(missing) = %q, want empty", got)
	}
}

func TestExtractBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/echo fake-binary\n")
	// include a decoy file plus the real binary
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {"manygit", payload}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(f.data)
	}
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes(), "manygit")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("extracted %q, want %q", got, payload)
	}
	if _, err := extractBinary(buf.Bytes(), "missing"); err == nil {
		t.Error("extractBinary(missing) should error")
	}
}
