package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// findCC locates a C compiler for the tests that link and RUN what the backend
// emitted. A test that cannot find one skips rather than fails, so the suite still
// passes on a machine with no toolchain.
func findCC() string {
	for _, name := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// compileAndRun links emitted C with cc and returns the program's stdout — the
// end-to-end assertion the value-only tests share (no runtime units needed).
func compileAndRun(t *testing.T, cc, code string) string {
	t.Helper()
	tmp := t.TempDir()
	cpath := filepath.Join(tmp, "out.c")
	bpath := filepath.Join(tmp, "out.bin")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	if out, err := exec.Command(cc, "-std=c11", "-o", bpath, cpath).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- C ---\n%s", err, out, code)
	}
	out, err := exec.Command(bpath).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	return string(out)
}
