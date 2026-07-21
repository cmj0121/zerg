package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCmpErr(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	if cmpErr(a, b) != a {
		t.Errorf("cmpErr(a, b) should return a")
	}
	if cmpErr(nil, b) != b {
		t.Errorf("cmpErr(nil, b) should return b")
	}
	if cmpErr(nil, nil) != nil {
		t.Errorf("cmpErr(nil, nil) should return nil")
	}
}

func writeSrc(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.zg")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return p
}

func TestRunEmitStages(t *testing.T) {
	src := "fn main() {\n  print 1 + 2\n}"
	for _, emit := range []string{"tokens", "ast", "c"} {
		if code := run(&CLI{File: writeSrc(t, src), Emit: emit, Output: "a.out"}); code != 0 {
			t.Errorf("run(--emit %s) = %d, want 0", emit, code)
		}
	}
}

func TestRunParseError(t *testing.T) {
	if code := run(&CLI{File: writeSrc(t, "fn f( {"), Emit: "c"}); code != 1 {
		t.Errorf("parse-error run = %d, want 1", code)
	}
}

func TestRunSemaError(t *testing.T) {
	// print of an undefined name passes the parser but fails sema.
	if code := run(&CLI{File: writeSrc(t, "fn main() { print x }"), Emit: "c"}); code != 1 {
		t.Errorf("sema-error run = %d, want 1", code)
	}
}

func TestRunReadError(t *testing.T) {
	if code := run(&CLI{File: "/no/such/file.zg", Emit: "c"}); code != 1 {
		t.Errorf("missing-file run = %d, want 1", code)
	}
}

func TestRunTokensDiagnostic(t *testing.T) {
	// an unsupported construct makes the lexer report a diagnostic.
	if code := run(&CLI{File: writeSrc(t, "fn f() { x := `ls` }"), Emit: "tokens"}); code != 1 {
		t.Errorf("tokens dump with diagnostic = %d, want 1", code)
	}
}

func TestRunBuildAndKeepC(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	out := filepath.Join(t.TempDir(), "prog")
	src := writeSrc(t, "fn main() {\n  print 6 * 7\n}")
	if code := run(&CLI{File: src, Emit: "bin", CC: cc, Output: out, KeepC: true}); code != 0 {
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

func TestRunCCFailure(t *testing.T) {
	// a non-existent C compiler makes linking fail.
	src := writeSrc(t, "fn main() { print 1 }")
	if code := run(&CLI{File: src, Emit: "bin", CC: "cc-does-not-exist", Output: filepath.Join(t.TempDir(), "x")}); code != 1 {
		t.Errorf("bad-cc run = %d, want 1", code)
	}
}
