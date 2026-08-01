package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
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
// end-to-end assertion the value-only tests share.
//
// It materializes the runtime when the emitted C asks for it. It used to link the one
// file bare, which made "does this program need the runtime?" a question the caller had
// to answer and none of them did: an example that reached a checked conversion or a
// parse failed here as `'zergrt.h' file not found`, which reads as a broken example
// rather than as a test that declined to link what the program needs.
func compileAndRun(t *testing.T, cc, code string) string {
	t.Helper()
	tmp := t.TempDir()
	cpath := filepath.Join(tmp, "out.c")
	bpath := filepath.Join(tmp, "out.bin")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	cargs := []string{"-std=c11", "-o", bpath, cpath}
	if strings.Contains(code, `#include "zergrt.h"`) {
		cfiles, err := runtime.Materialize(tmp)
		if err != nil {
			t.Fatalf("materialize runtime: %v", err)
		}
		cargs = append([]string{"-std=c11", "-I", tmp, "-o", bpath, cpath}, cfiles...)
	}
	if out, err := exec.Command(cc, cargs...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- C ---\n%s", err, out, code)
	}
	out, err := exec.Command(bpath).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	return string(out)
}
