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

// reassignCases are the previously-broken valid programs from the memory-safety
// review: reassigning a Ref (or Ref-holding) target must release the old value and
// retain the new, so the box balance stays ALLOCS==FREE with no use-after-free.
var reassignCases = []struct {
	name       string
	src        string
	wantStdout string
	wantAllocs string // the "ALLOCS=n LIVE=0" the counting allocator must report
}{
	{
		// R3: reassign a Ref binding to another Ref binding. Old Ref(2) is released at
		// the assignment; Ref(1) (now held by both slots) is released twice down to 0.
		name:       "R3-ref-binding",
		src:        "fn main() {\n a := Ref(1)\n mut b := Ref(2)\n b = a\n print deref(b)\n}",
		wantStdout: "1\n",
		wantAllocs: "ALLOCS=2 LIVE=0",
	},
	{
		// R7: reassign a struct-of-Ref binding. a's old inner Ref(1) is released via the
		// struct deep-drop at the assignment; Ref(2) is retained then released to 0.
		name:       "R7-struct-of-ref",
		src:        "struct Box { pub v: Ref[int] }\nfn main() {\n mut a := Box(Ref(1))\n b := Box(Ref(2))\n a = b\n print deref(a.v)\n}",
		wantStdout: "2\n",
		wantAllocs: "ALLOCS=2 LIVE=0",
	},
	{
		// R2: reassign a Ref in a loop. Each iteration releases the old box before
		// binding the new one, so nothing leaks across iterations.
		name:       "R2-loop-reassign",
		src:        "fn main() {\n mut r := Ref(0)\n mut i := 1\n for i < 4 {\n  r = Ref(i)\n  i = i + 1\n }\n print deref(r)\n}",
		wantStdout: "3\n",
		wantAllocs: "ALLOCS=4 LIVE=0",
	},
	{
		// R8: reassign a Ref-typed field (a sub-place). The old field Ref(1) is released
		// in place before the new one is stored; the struct's scope drop frees Ref(2).
		name:       "R8-field-reassign",
		src:        "struct Box { pub v: Ref[int] }\nfn main() {\n mut a := Box(Ref(1))\n a.v = Ref(2)\n print deref(a.v)\n}",
		wantStdout: "2\n",
		wantAllocs: "ALLOCS=2 LIVE=0",
	},
}

// TestReassignBalance links each reassignment program against the counting allocator
// and asserts every box is freed exactly once (LIVE=0): a leak shows LIVE>0, a double
// free shows LIVE<0.
func TestReassignBalance(t *testing.T) {
	for _, tc := range reassignCases {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildCounting(t, tc.src)
			stdout, stderr, err := run(t, bin)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, stderr)
			}
			if stdout != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, tc.wantStdout)
			}
			if !strings.Contains(stderr, tc.wantAllocs) {
				t.Fatalf("balance = %q, want %q", stderr, tc.wantAllocs)
			}
		})
	}
}

// TestReassignMemorySafeASan builds each reassignment program against the real
// runtime under AddressSanitizer + UBSan and asserts a clean run — no
// heap-use-after-free, double-free, or leak. It skips when the sanitizer is
// unavailable (some toolchains lack the runtime).
func TestReassignMemorySafeASan(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("AddressSanitizer runtime unavailable")
	}
	for _, tc := range reassignCases {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildASan(t, cc, tc.src)
			stdout, stderr, err := run(t, bin)
			if err != nil {
				t.Fatalf("ASan reported a memory error (or non-zero exit): %v\n%s", err, stderr)
			}
			if stdout != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q\nsanitizer: %s", stdout, tc.wantStdout, stderr)
			}
			if strings.Contains(stderr, "runtime error") || strings.Contains(stderr, "ERROR: AddressSanitizer") {
				t.Fatalf("sanitizer diagnostic:\n%s", stderr)
			}
		})
	}
}

// buildASan compiles src and links it against the real runtime tree with
// -fsanitize=address,undefined, returning the built binary path.
func buildASan(t *testing.T, cc, src string) string {
	t.Helper()
	code, manifest, diags := build.Compile(src)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("a Ref program must need the runtime\n%s", code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{"-std=c11", "-g", "-fsanitize=address,undefined",
		"-fno-sanitize-recover=all", "-I", dir, "-o", bin, cpath}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc (asan) failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return bin
}

// asanAvailable reports whether the toolchain can build and link a trivial program
// with the AddressSanitizer runtime.
func asanAvailable(t *testing.T, cc string) bool {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.c")
	if err := os.WriteFile(src, []byte("int main(void){return 0;}"), 0o644); err != nil {
		return false
	}
	bin := filepath.Join(dir, "probe.bin")
	cmd := exec.Command(cc, "-fsanitize=address,undefined", "-o", bin, src)
	if err := cmd.Run(); err != nil {
		return false
	}
	return exec.Command(bin).Run() == nil
}
