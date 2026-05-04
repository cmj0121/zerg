package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 codegen tests — Unit 5 deliverables.
//
// The contracts being asserted:
//
//   * The list struct carries `cap` alongside `data` and `len` so the
//     per-shape push helper can grow without a per-call realloc.
//   * The per-shape `<T>_push` helper exists with cap-doubling semantics.
//   * `push(xs, v)` lowers to a call into `<T>_push`, NOT to an inline
//     realloc-by-one.
//   * Implicit deep-copy is dropped on bind / fn-arg pass / return; only
//     `clone(xs)` (and the per-shape `_copy` helper bodies internal use)
//     remain as call-sites of `<T>_copy`.
// ---------------------------------------------------------------------------

// TestCgenListStructHasCapField — the emitted list struct definition includes
// a cap field for v0.3 push growth.
func TestCgenListStructHasCapField(t *testing.T) {
	out := mustEmit(t, "let xs := [1, 2]\n")
	if !strings.Contains(out, "size_t cap;") {
		t.Errorf("list struct missing cap field; got:\n%s", out)
	}
}

// TestCgenListLitInitializesCap — list literals construct with cap == len.
func TestCgenListLitInitializesCap(t *testing.T) {
	out := mustEmit(t, "let xs := [1, 2, 3]\n")
	if !strings.Contains(out, "__l.len = 3; __l.cap = 3;") {
		t.Errorf("list literal should set cap = len; got:\n%s", out)
	}
}

// TestCgenPushHelperEmitted — the per-shape <T>_push helper is declared and
// defined for any list[T] shape used by the program.
func TestCgenPushHelperEmitted(t *testing.T) {
	src := `mut xs := [1, 2]
push(xs, 3)
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"static void zerg_list_int64_t_push(",
		"if (xs->len == xs->cap)",
		"xs->cap == 0 ? 4 : xs->cap * 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing push helper piece %q; got:\n%s", want, out)
		}
	}
}

// TestCgenPushLowersToHelperCall — `push(xs, v)` lowers to a call into
// `<T>_push` rather than the v0.3-Unit3 inline realloc-by-one.
func TestCgenPushLowersToHelperCall(t *testing.T) {
	src := `mut xs := [1, 2]
push(xs, 3)
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_list_int64_t_push(&z_xs,") {
		t.Errorf("push should lower to <T>_push helper call; got:\n%s", out)
	}
	// The old inline realloc-on-every-push path must be gone.
	if strings.Contains(out, "(z_xs.len + 1) * sizeof(") {
		t.Errorf("push still uses inline realloc-by-one; got:\n%s", out)
	}
}

// TestCgenNoImplicitCopyOnBind — let-binding of a composite does NOT wrap
// the rhs in `<T>_copy`. Was the v0.2 / pre-Unit-5 behaviour.
func TestCgenNoImplicitCopyOnBind(t *testing.T) {
	src := `let xs := [1, 2]
let ys := xs
print ys[0]
`
	out := mustEmit(t, src)
	if strings.Contains(out, "zerg_list_int64_t_copy(z_xs)") {
		t.Errorf("let-binding should NOT call <T>_copy on rhs; got:\n%s", out)
	}
	if !strings.Contains(out, "zerg_list_int64_t z_ys = z_xs;") {
		t.Errorf("let ys := xs should bind directly; got:\n%s", out)
	}
}

// TestCgenNoImplicitCopyOnFnArg — fn-call composite arg is NOT wrapped in
// `<T>_copy` at the call site.
func TestCgenNoImplicitCopyOnFnArg(t *testing.T) {
	src := `fn first(ys: list[int]) -> int {
return ys[0]
}
let xs := [1, 2]
print first(xs)
`
	out := mustEmit(t, src)
	if strings.Contains(out, "zerg_list_int64_t_copy(z_xs)") {
		t.Errorf("fn-call arg should NOT call <T>_copy; got:\n%s", out)
	}
	if !strings.Contains(out, "z_first(z_xs)") {
		t.Errorf("fn call should pass z_xs directly; got:\n%s", out)
	}
}

// TestCgenNoImplicitCopyOnReturn — fn return of a composite does NOT wrap
// the value in `<T>_copy`.
func TestCgenNoImplicitCopyOnReturn(t *testing.T) {
	src := `fn make() -> list[int] {
let xs := [1, 2, 3]
return xs
}
let ys := make()
print ys[0]
`
	out := mustEmit(t, src)
	if strings.Contains(out, "return zerg_list_int64_t_copy(") {
		t.Errorf("return should NOT wrap composite in <T>_copy; got:\n%s", out)
	}
}

// TestCgenCloneStillUsesCopyHelper — the explicit `clone(xs)` call IS the
// only call-site of the per-shape copy helper at v0.3.
func TestCgenCloneStillUsesCopyHelper(t *testing.T) {
	src := `let xs := [1, 2]
let ys := clone(xs)
print ys[0]
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "zerg_list_int64_t_copy(z_xs)") {
		t.Errorf("clone(xs) should call <T>_copy; got:\n%s", out)
	}
	// Only ONE call-site (in the clone), plus the two declaration/
	// definition lines. Total 3 occurrences of the helper name.
	count := strings.Count(out, "zerg_list_int64_t_copy")
	if count != 3 {
		t.Errorf("expected 3 occurrences of zerg_list_int64_t_copy (decl + def + 1 call), got %d in:\n%s", count, out)
	}
}

// TestCgenPushGrowExecutesCorrectly — end-to-end: compile a program that
// pushes past its initial cap, run it, and verify the output. Cap-doubling
// growth must preserve existing values across realloc.
func TestCgenPushGrowExecutesCorrectly(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	src := `mut xs := [1]
push(xs, 2)
push(xs, 3)
push(xs, 4)
push(xs, 5)
print xs
print len(xs)
`
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v", err)
	}
	c := exec.Command(binPath)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	want := "[ 1, 2, 3, 4, 5 ]\n5\n"
	if stdout.String() != want {
		t.Errorf("output mismatch:\n  got:  %q\n  want: %q", stdout.String(), want)
	}
}

// TestCgenPushHundredElementsExecutes — stress test with cap-doubling under
// many growth events. Verifies the helper is correct and the realloc path
// preserves state across many doublings.
func TestCgenPushHundredElementsExecutes(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	src := `mut xs := [0]
for i in 1..100 {
push(xs, i)
}
print len(xs)
print xs[0]
print xs[99]
`
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v", err)
	}
	c := exec.Command(binPath)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	want := "100\n0\n99\n"
	if stdout.String() != want {
		t.Errorf("output mismatch:\n  got:  %q\n  want: %q", stdout.String(), want)
	}
}
