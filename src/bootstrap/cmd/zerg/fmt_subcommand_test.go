package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestFmtSubcommand exercises the three modes of `zerg fmt` end-to-end via
// the built CLI binary. It builds the toolchain into the test's tmp dir,
// then drives a small set of fixture .zg files (canonical, non-canonical,
// parse-error) through stdout / -w / --check.
//
// The tests pin the exit-code contract (0 / 1 / 2) — that's the user-
// observable surface, more important than the formatted text itself
// (which the internal/fmt unit tests already lock).
func TestFmtSubcommand(t *testing.T) {
	bin := buildBin(t)

	canonical := "let x := 1\nprint x\n"
	nonCanonical := "let x := 1\nif true {\n  print x\n}\n"
	formattedNonCanonical := "let x := 1\nif true {\n    print x\n}\n"
	parseError := "let x :=\n"

	t.Run("stdout/canonical", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", canonical)
		out, code := runBin(t, bin, "fmt", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stderr=%s", code, out.stderr)
		}
		if out.stdout != canonical {
			t.Fatalf("stdout=%q want %q", out.stdout, canonical)
		}
	})

	t.Run("stdout/non-canonical", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", nonCanonical)
		out, code := runBin(t, bin, "fmt", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if out.stdout != formattedNonCanonical {
			t.Fatalf("stdout=%q want %q", out.stdout, formattedNonCanonical)
		}
	})

	t.Run("write/rewrites in place", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", nonCanonical)
		_, code := runBin(t, bin, "fmt", "-w", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != formattedNonCanonical {
			t.Fatalf("file=%q want %q", got, formattedNonCanonical)
		}
	})

	t.Run("write/canonical idempotent", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", canonical)
		_, code := runBin(t, bin, "fmt", "-w", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != canonical {
			t.Fatalf("file=%q want %q (rewrite changed canonical input)", got, canonical)
		}
	})

	t.Run("check/canonical exits 0", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", canonical)
		_, code := runBin(t, bin, "fmt", "--check", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
	})

	t.Run("check/non-canonical exits 1", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", nonCanonical)
		out, code := runBin(t, bin, "fmt", "--check", path)
		if code != 1 {
			t.Fatalf("exit=%d, want 1; stderr=%s", code, out.stderr)
		}
		if !bytes.Contains([]byte(out.stderr), []byte(path)) {
			t.Fatalf("stderr=%q does not mention path %s", out.stderr, path)
		}
	})

	t.Run("check/parse error exits 2", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", parseError)
		out, code := runBin(t, bin, "fmt", "--check", path)
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stderr=%s", code, out.stderr)
		}
		// Diagnostic envelope: file:line:col: message.
		if !bytes.Contains([]byte(out.stderr), []byte("a.zg:")) {
			t.Fatalf("stderr=%q does not carry file:line:col envelope", out.stderr)
		}
	})

	t.Run("stdout/parse error exits 2", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", parseError)
		_, code := runBin(t, bin, "fmt", path)
		if code != 2 {
			t.Fatalf("exit=%d, want 2", code)
		}
	})

	t.Run("check/multiple files reports each diff", func(t *testing.T) {
		dir := t.TempDir()
		p1 := writeFile(t, dir, "a.zg", nonCanonical)
		p2 := writeFile(t, dir, "b.zg", canonical)
		p3 := writeFile(t, dir, "c.zg", nonCanonical)
		out, code := runBin(t, bin, "fmt", "--check", p1, p2, p3)
		if code != 1 {
			t.Fatalf("exit=%d, want 1", code)
		}
		if !bytes.Contains([]byte(out.stderr), []byte(p1)) {
			t.Fatalf("stderr missing %s: %s", p1, out.stderr)
		}
		if !bytes.Contains([]byte(out.stderr), []byte(p3)) {
			t.Fatalf("stderr missing %s: %s", p3, out.stderr)
		}
		if bytes.Contains([]byte(out.stderr), []byte(p2)) {
			t.Fatalf("canonical %s should not appear in stderr: %s", p2, out.stderr)
		}
	})
}

// TestFmtWriteDataSafety pins the four data-safety properties of `zerg fmt -w`
// added in v0.10 Iter 2: skip-on-equal preserves mtime, source perm bits are
// preserved across rewrites, and symlinks are refused with a focused error.
func TestFmtWriteDataSafety(t *testing.T) {
	bin := buildBin(t)
	canonical := "let x := 1\nprint x\n"
	nonCanonical := "let x := 1\nif true {\n  print x\n}\n"
	formattedNonCanonical := "let x := 1\nif true {\n    print x\n}\n"

	t.Run("skip-on-equal preserves mtime", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", canonical)
		// Backdate so the OS clock can't accidentally tick during the
		// test and produce a coincidentally identical mtime.
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		_, code := runBin(t, bin, "fmt", "-w", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("mtime changed on canonical file: before=%v after=%v",
				before.ModTime(), after.ModTime())
		}
	})

	t.Run("non-canonical write updates content", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", nonCanonical)
		_, code := runBin(t, bin, "fmt", "-w", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != formattedNonCanonical {
			t.Fatalf("file=%q want %q", got, formattedNonCanonical)
		}
	})

	t.Run("preserves permission bits", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits not exercised on Windows")
		}
		dir := t.TempDir()
		path := writeFile(t, dir, "a.zg", nonCanonical)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		_, code := runBin(t, bin, "fmt", "-w", path)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("perm=%#o, want 0o600", got)
		}
	})

	t.Run("symlink is refused", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on Windows; not part of this fix")
		}
		dir := t.TempDir()
		target := writeFile(t, dir, "real.zg", nonCanonical)
		link := filepath.Join(dir, "link.zg")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		out, code := runBin(t, bin, "fmt", "-w", link)
		if code == 0 {
			t.Fatalf("exit=0 on symlink; want non-zero (stderr=%s)", out.stderr)
		}
		if !bytes.Contains([]byte(out.stderr), []byte("symlink")) {
			t.Fatalf("stderr=%q does not mention symlink", out.stderr)
		}
		// Target must be untouched.
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != nonCanonical {
			t.Fatalf("target mutated through symlink: got=%q", got)
		}
	})
}

// runOutput captures both stdout and stderr from a CLI invocation.
type runOutput struct {
	stdout string
	stderr string
}

// runBin invokes bin with args and returns its stdout, stderr, and exit
// code. Any unexpected exec error fails the test outright.
func runBin(t *testing.T, bin string, args ...string) (runOutput, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := runOutput{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return out, 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode()
	}
	t.Fatalf("exec %s: %v", bin, err)
	return out, -1
}

// buildBin compiles the cmd/zerg binary into the test's TempDir and returns
// its absolute path. Sharing across sub-tests via t.Helper is fine — the
// binary is small and this keeps each test self-contained.
func buildBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zerg")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

// writeFile drops content at dir/name and returns the absolute path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
