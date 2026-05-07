package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// v12CompileAndRun is the shared compile-and-run helper for the v0.12
// runtime unit tests (chan / wait_group / select / defer). It writes
// `prog` (a complete C TU — runtime constants concatenated with the
// driver source) to a temp file, compiles it with the project default
// CC, runs the resulting binary with `env` overrides, and returns
// (combined output, exit code).
//
// On a non-zero exit the helper returns the combined stdout+stderr so
// callers can assert on diagnostic substrings; on cc failure or any
// non-ExitError run failure it fails the test outright.
func v12CompileAndRun(t *testing.T, prog string, env []string) ([]byte, int) {
	t.Helper()
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	progPath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(progPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog.c: %v", err)
	}
	binPath := filepath.Join(dir, "driver")
	cmd := exec.Command(DefaultCC(), "-Wall", "-Wno-deprecated-declarations",
		"-Wno-unused-function", "-O2", "-pthread", "-o", binPath, progPath)
	cmd.Dir = dir
	var ccErr bytes.Buffer
	cmd.Stderr = &ccErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc failed: %v\nstderr:\n%s", err, ccErr.String())
	}
	cmd = exec.Command(binPath)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return append(out, ee.Stderr...), ee.ExitCode()
		}
		t.Fatalf("driver: %v", err)
	}
	return out, 0
}
