package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeSrc(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.zg")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return p
}

func TestRunBuildEmitStages(t *testing.T) {
	src := "fn main() {\n  print 1 + 2\n}"
	if code := runBuild(&BuildCmd{File: writeSrc(t, src), Emit: "c", Output: "a.out"}); code != 0 {
		t.Errorf("runBuild(--emit c) = %d, want 0", code)
	}
}

func TestRunBuildParseError(t *testing.T) {
	if code := runBuild(&BuildCmd{File: writeSrc(t, "fn f( {"), Emit: "c"}); code != 1 {
		t.Errorf("parse-error run = %d, want 1", code)
	}
}

func TestRunBuildSemaError(t *testing.T) {
	// print of an undefined name passes the parser but fails sema.
	if code := runBuild(&BuildCmd{File: writeSrc(t, "fn main() { print x }"), Emit: "c"}); code != 1 {
		t.Errorf("sema-error run = %d, want 1", code)
	}
}

func TestRunBuildReadError(t *testing.T) {
	if code := runBuild(&BuildCmd{File: "/no/such/file.zg", Emit: "c"}); code != 1 {
		t.Errorf("missing-file run = %d, want 1", code)
	}
}

func TestRunBuildAndKeepC(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	out := filepath.Join(t.TempDir(), "prog")
	src := writeSrc(t, "fn main() {\n  print 6 * 7\n}")
	if code := runBuild(&BuildCmd{File: src, Emit: "bin", CC: cc, Output: out, KeepC: true}); code != 0 {
		t.Fatalf("build run = %d, want 0", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}
	if _, err := os.Stat(out + ".c"); err != nil {
		t.Fatalf("--keep-c did not keep the .c file: %v", err)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run binary: %v", err)
	}
	if string(got) != "42\n" {
		t.Fatalf("binary output = %q, want 42", string(got))
	}
}

// TestRunBuildResultNilMainLinksRuntime is the end-to-end runtime path: a
// 'fn main() -> Result[nil]' program has a non-empty Manifest, so the driver
// materializes the embedded src/runtime tree and links it. The built binary runs
// through the zrt_run entry shim and exits 0 on the Ok path.
func TestRunBuildResultNilMainLinksRuntime(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	out := filepath.Join(t.TempDir(), "prog")
	src := writeSrc(t, "fn main() -> Result[nil] {\n  nop\n}")
	if code := runBuild(&BuildCmd{File: src, Emit: "bin", CC: cc, Output: out}); code != 0 {
		t.Fatalf("runtime-linked build run = %d, want 0", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}
	if err := exec.Command(out).Run(); err != nil {
		t.Fatalf("runtime program should exit 0, got %v", err)
	}
}

func TestRunBuildCCFailure(t *testing.T) {
	// a non-existent C compiler makes linking fail.
	src := writeSrc(t, "fn main() { print 1 }")
	if code := runBuild(&BuildCmd{File: src, Emit: "bin", CC: "cc-does-not-exist", Output: filepath.Join(t.TempDir(), "x")}); code != 1 {
		t.Errorf("bad-cc run = %d, want 1", code)
	}
}
