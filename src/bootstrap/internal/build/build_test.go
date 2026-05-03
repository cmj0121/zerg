package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBuildCCNotFound exercises the LookPath-fail branch.
func TestBuildCCNotFound(t *testing.T) {
	t.Setenv("CC", "/nonexistent/definitely-not-a-real-cc")

	src := writeTempZG(t, `print "ok"`)
	err := Build(src)
	if err == nil {
		t.Fatal("expected error when $CC is missing, got nil")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error must mention 'not found in PATH'; got: %v", err)
	}
}

// TestBuildCCInvocationFailure exercises the cc-runs-but-fails branch:
// stderr forwarded, .c left in place, error returned.
func TestBuildCCInvocationFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-cc not portable to windows")
	}

	dir := t.TempDir()
	fakeCC := filepath.Join(dir, "fake-cc")
	script := "#!/bin/sh\necho 'fake-cc: simulated failure' 1>&2\nexit 7\n"
	if err := os.WriteFile(fakeCC, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake-cc: %v", err)
	}
	t.Setenv("CC", fakeCC)

	// Build into an isolated CWD so a stray output binary doesn't clobber
	// the dev workspace.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	src := writeTempZG(t, `print "ok"`)
	err = Build(src)
	if err == nil {
		t.Fatal("expected error when cc exits non-zero, got nil")
	}
	if !strings.Contains(err.Error(), "fake-cc") {
		t.Errorf("error should mention the cc command; got: %v", err)
	}
}

func writeTempZG(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.zg")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("write tempfile: %v", err)
	}
	return path
}
