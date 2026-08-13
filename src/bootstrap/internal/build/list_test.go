package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// countingAllocC replaces the materialized alloc.c with a counting wrapper: every
// zrt_alloc/zrt_free (all runtime heap traffic flows through this one seam) adjusts
// a live-allocation tally, and an atexit hook prints it to stderr. macOS ships no
// LeakSanitizer, so a leak-free run is proven by asserting the tally returns to zero
// rather than by ASan; ASan+UBSan still catch double-free / use-after-scope.
const countingAllocC = `#include "zergrt.h"
#include <stdlib.h>
#include <stdio.h>
static long zrt_live_allocs = 0;
static void zrt_report_balance(void) { fprintf(stderr, "ZRT_ALLOC_BALANCE=%ld\n", zrt_live_allocs); }
void *zrt_alloc(size_t n) {
	static int hooked = 0;
	if (!hooked) { hooked = 1; atexit(zrt_report_balance); }
	void *p = malloc(n);
	if (p == NULL && n != 0) { zrt_abort("out of memory"); }
	if (p != NULL) { zrt_live_allocs++; }
	return p;
}
void zrt_free(void *p) { if (p != NULL) { zrt_live_allocs--; } free(p); }
`

// runProgramRTBalanced compiles+links src under ASan+UBSan against the runtime with
// the counting allocator (countingAllocC) swapped in, runs it, and asserts both a
// clean exit and a zero alloc/free balance (no leak). It returns stdout so the caller
// can assert the program's output. Use it for the non-POD teardown paths where a leak
// would otherwise pass silently on macOS.
func runProgramRTBalanced(t *testing.T, src string) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	// Overwrite the materialized alloc.c (a returned core unit) with the counting
	// version; the paths in cfiles are unchanged, so the link line is the same.
	if err := os.WriteFile(filepath.Join(dir, "alloc.c"), []byte(countingAllocC), 0o644); err != nil {
		t.Fatalf("write counting alloc.c: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{
		"-std=c11", "-fsanitize=address,undefined", "-fno-sanitize-recover=all",
		"-I", dir, "-o", bin, cpath,
	}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s\n--- generated C ---\n%s", err, stderr.String(), code)
	}
	if !strings.Contains(stderr.String(), "ZRT_ALLOC_BALANCE=0\n") {
		t.Fatalf("alloc/free NOT balanced (leak): stderr=%q\n--- generated C ---\n%s", stderr.String(), code)
	}
	return stdout.String()
}

// RUN-based tests for the built-in list[T] container (docs/code/collections.md), one per
// implemented slice L1-L8. Every program is compiled to C, linked against the
// materialized runtime under ASan+UBSan (via runProgramRT / runProgramRTAbort), and
// executed, so a passing test asserts a clean exit + exact stdout with no memory
// error. The abort path is exercised separately because it exits non-zero by design.

// runProgramRTAbort compiles+links src under ASan+UBSan, runs it EXPECTING a non-zero
// exit (an abort — e.g. an out-of-range index = IndexError), and returns the combined
// output so the caller can assert the abort message. It mirrors runProgramRT but
// inverts the exit expectation.
func runProgramRTAbort(t *testing.T, src string) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
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
	args := append([]string{
		"-std=c11", "-fsanitize=address,undefined", "-fno-sanitize-recover=all",
		"-I", dir, "-o", bin, cpath,
	}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit (abort), got a clean run\n%s", out)
	}
	return string(out)
}

// TestListBindAndDrop (L1): a list binds, indexes, and drops at scope exit (the
// runtime-linked compile under ASan proves the drop-env runs with no error).
func TestListBindAndDrop(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [1, 2, 3]\n\tprint xs[0]\n\tprint xs[2]\n}\n")
	if got != "1\n3\n" {
		t.Fatalf("list bind/index = %q, want %q", got, "1\n3\n")
	}
}

// TestListIndexAbort (L2): an out-of-range index aborts with IndexError ("index out
// of range") and a non-zero exit, not undefined behaviour.
func TestListIndexAbort(t *testing.T) {
	got := runProgramRTAbort(t, "fn main() {\n\txs := [1, 2]\n\tprint xs[5]\n}\n")
	if !strings.Contains(got, "index out of range") {
		t.Fatalf("expected an index-out-of-range abort, got %q", got)
	}
}

// TestListLen (L3): `.len()` yields the element count.
func TestListLen(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [10, 20, 30, 40]\n\tprint xs.len()\n}\n")
	if got != "4\n" {
		t.Fatalf("list len = %q, want %q", got, "4\n")
	}
}

// TestListAppendAndSet (L4): a `mut` list appends (growing) and edits in place via
// `xs[i] = v`; `.len()` and index reflect both.
func TestListAppendAndSet(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tmut xs := [10, 20]\n\txs.append(30)\n\txs[1] = 99\n"+
		"\tprint xs.len()\n\tprint xs[0]\n\tprint xs[1]\n\tprint xs[2]\n}\n")
	if got != "3\n10\n99\n30\n" {
		t.Fatalf("list append/set = %q, want %q", got, "3\n10\n99\n30\n")
	}
}

// TestListAppendImmutableGate (L4): appending to a plain (non-mut) list is rejected.
func TestListAppendImmutableGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\txs := [1, 2]\n\txs.append(3)\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "immutable list") {
		t.Fatalf("expected an immutable-list append gate, got %v", diags)
	}
}

// TestListSetImmutableGate (L4): `xs[i] = v` on a plain list is rejected.
func TestListSetImmutableGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\txs := [1, 2]\n\txs[0] = 9\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "immutable") {
		t.Fatalf("expected an immutable-list set gate, got %v", diags)
	}
}

// TestListOfRef (L5): a list of Ref[int] deep-copies (retain) on a value copy and
// releases every element at both holders' scope exit — ASan proves no double-free.
func TestListOfRef(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [Ref(1), Ref(2), Ref(3)]\n"+
		"\tys := xs\n\tprint deref(xs[0])\n\tprint deref(ys[2])\n\tprint xs.len()\n}\n")
	if got != "1\n3\n3\n" {
		t.Fatalf("list[Ref] = %q, want %q", got, "1\n3\n3\n")
	}
}

// TestListOfStruct (L5): a list of a POD struct copies element-wise (memcpy fast
// path) and reads a field back.
func TestListOfStruct(t *testing.T) {
	got := runProgramRT(t, "struct Point {\n\tpub x: int\n\tpub y: int\n}\n"+
		"fn main() {\n\tps := [Point(1, 2), Point(3, 4)]\n\tqs := ps\n"+
		"\tprint ps[1].x\n\tprint qs[0].y\n}\n")
	if got != "3\n2\n" {
		t.Fatalf("list[Point] = %q, want %q", got, "3\n2\n")
	}
}

// TestListNested (L5): a list[list[int]] deep-copies its inner lists on a value copy
// and drops them recursively at scope exit — ASan proves no leak / use-after-free.
func TestListNested(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\ta := [1, 2]\n\tb := [3, 4, 5]\n"+
		"\txss := [a, b]\n\tws := xss\n\tprint xss.len()\n\tprint xss[1].len()\n\tprint ws[0].len()\n}\n")
	if got != "2\n3\n2\n" {
		t.Fatalf("list[list] = %q, want %q", got, "2\n3\n2\n")
	}
}

// TestListForIn (L6): `for x in xs` walks in index order; a POD `for mut x` edits in
// place.
func TestListForIn(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [1, 2, 3]\n\tfor x in xs { print x }\n"+
		"\tmut ys := [1, 2, 3]\n\tfor mut y in ys { y = y * 10 }\n\tfor y in ys { print y }\n}\n")
	if got != "1\n2\n3\n10\n20\n30\n" {
		t.Fatalf("list for-in = %q, want %q", got, "1\n2\n3\n10\n20\n30\n")
	}
}

// TestListForInFrozenAppend (L6): appending to a list while iterating it is rejected.
func TestListForInFrozenAppend(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tmut xs := [1, 2, 3]\n\tfor x in xs { xs.append(x) }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "iterating") {
		t.Fatalf("expected a frozen-during-iteration gate, got %v", diags)
	}
}

// TestListForInFrozenRebind (L6): rebinding a list while iterating it is rejected.
func TestListForInFrozenRebind(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tmut xs := [1, 2, 3]\n\tfor x in xs { xs = [9] }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "iterating") {
		t.Fatalf("expected a frozen-rebind gate, got %v", diags)
	}
}

// TestListGet (L7): `.get(i)` yields T? — the value on a good index, the empty case on
// a bad one, read here through the `??` default.
func TestListGet(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [5, 6, 7]\n\tprint xs.get(1) ?? -1\n\tprint xs.get(9) ?? -1\n}\n")
	if got != "6\n-1\n" {
		t.Fatalf("list get = %q, want %q", got, "6\n-1\n")
	}
}

// TestListFillList (L8): the fill form `[v; N]` in list position builds N copies.
func TestListFillList(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs := [7; 4]\n\tprint xs.len()\n\tprint xs[3]\n}\n")
	if got != "4\n7\n" {
		t.Fatalf("list fill = %q, want %q", got, "4\n7\n")
	}
}

// TestListForInRefDrop (L6): a plain `for x in xs` over a list[Ref[int]] — a
// spec-required read path — copies (retains) each element into the loop var and
// releases it at the END of EACH iteration, not at function end. Regression guard for
// the missing per-iteration unwind mark (the loop var's drop was deferred, re-pushing
// the same stack slot every turn → multi-release of the last element, leak of the
// earlier ones). Asserts the sum and a zero alloc/free balance under ASan.
func TestListForInRefDrop(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\txs := [Ref(1), Ref(2), Ref(3)]\n"+
		"\tmut sum := 0\n\tfor x in xs { sum = sum + deref(x) }\n\tprint sum\n}\n")
	if got != "6\n" {
		t.Fatalf("for-in over list[Ref] sum = %q, want %q", got, "6\n")
	}
}

// TestListForInRefNoOtherRefLocal (L6): the same read path in a program that binds NO
// other bare Ref local (xs is a list, and every Ref(n) is unnamed). The loop var over
// a list[Ref] is itself a Ref-bearing local that schedules the zg_ref_drop thunk, so
// the thunk must be emitted for this program — otherwise cc fails on an undeclared
// zg_ref_drop. Regression guard for programHasRefLocal ignoring the for-in loop var.
func TestListForInRefNoOtherRefLocal(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\txs := [Ref(4), Ref(5)]\n"+
		"\tfor x in xs { print deref(x) }\n}\n")
	if got != "4\n5\n" {
		t.Fatalf("for-in over list[Ref], no other Ref local = %q, want %q", got, "4\n5\n")
	}
}

// TestListForInStructRef (L6): the read path over a list of a struct that holds a Ref.
// Each element copies (deep-copies the Ref field) into the loop var and drops it per
// iteration through the struct's drop-env thunk; ASan + a zero balance prove no leak
// or double-free.
func TestListForInStructRef(t *testing.T) {
	got := runProgramRTBalanced(t, "struct Box {\n\tpub r: Ref[int]\n}\n"+
		"fn main() {\n\txs := [Box(Ref(10)), Box(Ref(20))]\n"+
		"\tmut sum := 0\n\tfor x in xs { sum = sum + deref(x.r) }\n\tprint sum\n}\n")
	if got != "30\n" {
		t.Fatalf("for-in over list[struct{Ref}] sum = %q, want %q", got, "30\n")
	}
}

// TestListInMembershipGate: `v in xs` over a list is not implemented (list membership).
// Without a sema gate it types as bool and the backend emits `(v ? zg_xs)`, which cc
// rejects — a deferred construct escaping to cc. It must be a clean diagnostic.
func TestListInMembershipGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\txs := [1, 2, 3]\n\tif 2 in xs { print 1 }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "list membership") {
		t.Fatalf("expected a list-membership deferral gate, got %v", diags)
	}
}

// TestListEqualityGate: `xs == ys` on two lists is not implemented (list equality).
// Without a sema gate the operands are identical types and pass `comparable`, so the
// backend emits `(zg_xs == zg_ys)` on the runtime list struct, which cc rejects. It
// must be a clean diagnostic.
func TestListEqualityGate(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\txs := [1, 2, 3]\n\tys := [1, 2, 3]\n\tif xs == ys { print 1 }\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "list equality") {
		t.Fatalf("expected a list-equality deferral gate, got %v", diags)
	}
}

// TestListGetIndexTypeChecked (FIX 1): `.get(i)` must TYPE-CHECK its index argument as an
// int. A wrong index type is a clean "list index" diagnostic — never a silent bool -> int
// coercion, and never a `zrt_list_get(_, "x")` that escapes to cc. Regression guard for the
// bare `check()` that context-typed a literal but reported no mismatch.
func TestListGetIndexTypeChecked(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\txs := [10, 20]\n\tprint xs.get(true) ?? -1\n}\n",
		"fn main() {\n\txs := [10, 20]\n\tprint xs.get(\"x\") ?? -1\n}\n",
	} {
		_, _, diags := Compile(src)
		if len(diags) == 0 || !strings.Contains(diags[0].Msg, "list index") {
			t.Fatalf("expected a \"list index\" diagnostic for %q, got %v", src, diags)
		}
	}
}

// TestListIndexTypeChecked (FIX 1): `xs[i]` must TYPE-CHECK its index the same way `.get`
// and `m[k]` do — an unchecked list index reached the backend as `zrt_list_at(_, true)` /
// `zrt_list_at(_, "x")` (silent coercion / bad C). It must be a clean "list index" gate.
func TestListIndexTypeChecked(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\txs := [10, 20]\n\tprint xs[true]\n}\n",
		"fn main() {\n\txs := [10, 20]\n\tprint xs[\"x\"]\n}\n",
	} {
		_, _, diags := Compile(src)
		if len(diags) == 0 || !strings.Contains(diags[0].Msg, "list index") {
			t.Fatalf("expected a \"list index\" diagnostic for %q, got %v", src, diags)
		}
	}
}

// TestListIndexOK (FIX 1): a valid int index still type-checks and runs unchanged — the
// added assignability check rejects only a mismatch.
func TestListIndexOK(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\txs := [5, 6, 7]\n\ti := 2\n\tprint xs[i]\n\tprint xs[0]\n}\n")
	if got != "7\n5\n" {
		t.Fatalf("list index (valid) = %q, want %q", got, "7\n5\n")
	}
}

// TestListFreshIndexRefTransient (FIX 2): indexing a FRESH (rvalue) list whose element is a
// Ref and reading it transiently (`deref([…][i])`). The materialized-base index returns an
// OWNED copy of the box; deref releases that transient copy after reading, so the box is
// freed exactly once (balance 0). Regression guard for the leak (balance 1).
func TestListFreshIndexRefTransient(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tprint deref([Ref(42), Ref(43)][0])\n}\n")
	if got != "42\n" {
		t.Fatalf("fresh list[Ref] index (transient) = %q, want %q", got, "42\n")
	}
}

// TestListFreshIndexRefBinding (FIX 2): binding a fresh-list Ref index (`r := […][i]`). The
// owned copy is retained exactly once (namesStorage no longer double-counts a fresh-base
// index), so `r`'s single scope-exit drop balances it.
func TestListFreshIndexRefBinding(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\tr := [Ref(1), Ref(2)][1]\n\tprint deref(r)\n}\n")
	if got != "2\n" {
		t.Fatalf("fresh list[Ref] index (binding) = %q, want %q", got, "2\n")
	}
}

// TestListFreshIndexRefArg (FIX 2): a fresh-list Ref index passed to a by-value Ref
// parameter — the owned copy is moved to the callee, which drops it (balance 0).
func TestListFreshIndexRefArg(t *testing.T) {
	src := "fn take(r: Ref[int]) -> int {\n\treturn deref(r)\n}\n" +
		"fn main() {\n\tprint take([Ref(7), Ref(8)][1])\n}\n"
	got := runProgramRTBalanced(t, src)
	if got != "8\n" {
		t.Fatalf("fresh list[Ref] index (fn arg) = %q, want %q", got, "8\n")
	}
}

// TestListNestedIndexRefBinding (review follow-up): a NESTED index `xss[0][1]` — the outer
// base `xss[0]` is already a materialized owned copy, so binding the inner Ref must move
// it, not retain a second time. Previously the recursive namesStorage walked to the named
// root and double-retained → a leak; the balance must now be 0.
func TestListNestedIndexRefBinding(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n\txss := [[Ref(1), Ref(2)]]\n\tr := xss[0][1]\n\tprint deref(r)\n}\n")
	if got != "2\n" {
		t.Fatalf("nested list index (binding) = %q, want %q", got, "2\n")
	}
}

// TestListNestedIndexRefArg (review follow-up): the same nested index passed as an argument.
func TestListNestedIndexRefArg(t *testing.T) {
	src := "fn take(r: Ref[int]) -> int {\n\treturn deref(r)\n}\n" +
		"fn main() {\n\txss := [[Ref(1), Ref(2)]]\n\tprint take(xss[0][1])\n}\n"
	got := runProgramRTBalanced(t, src)
	if got != "2\n" {
		t.Fatalf("nested list index (fn arg) = %q, want %q", got, "2\n")
	}
}
