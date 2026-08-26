package tui

import "testing"

func TestScriptInvocation(t *testing.T) {
	cases := []struct {
		path     string
		wantProg string
		wantArgs []string
	}{
		{"scripts/sync.sh", "bash", []string{"scripts/sync.sh"}},
		{"scripts/sync-all", "bash", []string{"scripts/sync-all"}}, // extensionless shebang script
		{"scripts/deploy.ps1", "powershell", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "scripts/deploy.ps1"}},
		{"scripts/build.cmd", "cmd", []string{"/C", "scripts/build.cmd"}},
		{"scripts/build.BAT", "cmd", []string{"/C", "scripts/build.BAT"}}, // case-insensitive
	}
	for _, c := range cases {
		prog, args := scriptInvocation(c.path)
		if prog != c.wantProg || !slicesEqual(args, c.wantArgs) {
			t.Errorf("scriptInvocation(%q) = %q, %v; want %q, %v", c.path, prog, args, c.wantProg, c.wantArgs)
		}
	}
}

func TestShellInvocationFor(t *testing.T) {
	cases := []struct {
		name     string
		haveBash bool
		goos     string
		wantProg string
		wantArgs []string
	}{
		{"bash on PATH, linux", true, "linux", "bash", []string{"-c", "git status"}},
		{"bash on PATH, windows", true, "windows", "bash", []string{"-c", "git status"}},
		{"no bash, windows falls back to cmd", false, "windows", "cmd", []string{"/C", "git status"}},
		{"no bash, non-windows surfaces bash's own error", false, "linux", "bash", []string{"-c", "git status"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, args := shellInvocationFor("git status", c.haveBash, c.goos)
			if prog != c.wantProg || !slicesEqual(args, c.wantArgs) {
				t.Errorf("shellInvocationFor = %q, %v; want %q, %v", prog, args, c.wantProg, c.wantArgs)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
