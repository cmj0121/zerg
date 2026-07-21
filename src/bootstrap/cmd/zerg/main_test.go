package main

import (
	"os"
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

func TestRunFmtToStdout(t *testing.T) {
	src := writeSrc(t, "fn main() { print 1 + 2 }")
	if code := runFmt(&FmtCmd{File: src}); code != 0 {
		t.Errorf("fmt run = %d, want 0", code)
	}
	// stdout mode must not touch the file
	got, err := os.ReadFile(src)
	if err != nil || string(got) != "fn main() { print 1 + 2 }" {
		t.Errorf("source file changed without --write: %q (%v)", got, err)
	}
}

func TestRunFmtWrite(t *testing.T) {
	src := writeSrc(t, "fn main() { print 1 + 2 }")
	if code := runFmt(&FmtCmd{File: src, Write: true}); code != 0 {
		t.Fatalf("fmt --write run = %d, want 0", code)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := "fn main() {\n\tprint 1 + 2\n}\n"
	if string(got) != want {
		t.Fatalf("rewritten file = %q, want %q", got, want)
	}
	// a second --write run is a no-op on canonical input
	if code := runFmt(&FmtCmd{File: src, Write: true}); code != 0 {
		t.Fatalf("second fmt --write run = %d, want 0", code)
	}
	again, _ := os.ReadFile(src)
	if string(again) != want {
		t.Fatalf("canonical file changed on rerun: %q", again)
	}
}

func TestRunFmtParseError(t *testing.T) {
	if code := runFmt(&FmtCmd{File: writeSrc(t, "fn f( {")}); code != 1 {
		t.Errorf("parse-error run = %d, want 1", code)
	}
}

func TestRunFmtReadError(t *testing.T) {
	if code := runFmt(&FmtCmd{File: "/no/such/file.zg"}); code != 1 {
		t.Errorf("missing-file run = %d, want 1", code)
	}
}
