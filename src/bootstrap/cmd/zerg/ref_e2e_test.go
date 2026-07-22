package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// refProgram exercises the whole Phase 1d iteration-2 surface: construct a Ref
// (move), copy it (retain), read it (deref), del one holder (release), and let the
// scope release the survivor — so the single allocation is freed exactly once.
const refProgram = "fn main() {\n" +
	"  r := Ref(7)\n" +
	"  s := r\n" +
	"  print deref(s)\n" +
	"  del s\n" +
	"}"

// TestRefRunsEndToEnd builds and runs a Ref program through the real driver (which
// links the embedded runtime) and checks it prints the boxed value.
func TestRefRunsEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	out := filepath.Join(t.TempDir(), "prog")
	src := writeSrc(t, refProgram)
	if code := runBuild(&BuildCmd{File: src, Emit: "bin", CC: "cc", Output: out}); code != 0 {
		t.Fatalf("ref build run = %d, want 0", code)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run ref binary: %v\n%s", err, got)
	}
	if string(got) != "7\n" {
		t.Fatalf("ref output = %q, want %q", got, "7\n")
	}
}

// TestRefFreesExactlyOnce is the runtime observation the design calls for: it links
// the emitted Ref program against an instrumented allocator that counts live
// allocations, and asserts the program allocates its box once and leaves nothing
// live — i.e. the Ref is freed exactly once (a leak would show LIVE>0, a double
// free LIVE<0). This is the portable stand-in for LeakSanitizer, which macOS lacks.
func TestRefFreesExactlyOnce(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, manifest, diags := build.Compile(refProgram)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.NeedsRuntime {
		t.Fatal("a Ref program must need the runtime")
	}

	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	// Replace the shipped allocator with a counting one (same zrt_alloc/zrt_free
	// symbols) that reports the live-allocation balance at process exit.
	if err := os.WriteFile(filepath.Join(dir, "alloc.c"), []byte(countingAllocC), 0o644); err != nil {
		t.Fatalf("write instrumented allocator: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, cpath}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	// stdout carries the program's own output; the allocation balance is on stderr.
	cmd := exec.Command(bin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	if string(stdout) != "7\n" {
		t.Fatalf("ref output = %q, want %q", stdout, "7\n")
	}
	if !strings.Contains(stderr.String(), "ALLOCS=1 LIVE=0") {
		t.Fatalf("allocation balance = %q, want one alloc freed exactly once (ALLOCS=1 LIVE=0)", stderr.String())
	}
}

// countingAllocC is a drop-in replacement for the runtime's alloc.c that counts
// allocations and frees and reports the balance at exit, so a test can observe that
// every Ref allocation is freed exactly once.
const countingAllocC = `
#include "zergrt.h"
#include <stdlib.h>
#include <stdio.h>
static long g_live;
static long g_allocs;
void *zrt_alloc(size_t n) {
    void *p = malloc(n);
    if (p == NULL) { zrt_abort("out of memory"); }
    g_live++;
    g_allocs++;
    return p;
}
void zrt_free(void *p) {
    if (p != NULL) { g_live--; free(p); }
}
__attribute__((destructor)) static void zrt_alloc_report(void) {
    fprintf(stderr, "ALLOCS=%ld LIVE=%ld\n", g_allocs, g_live);
}
`
