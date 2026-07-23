package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtime "github.com/cmj0121/zerg/src/runtime"
)

// RUN-based tests for the built-in list[T] container (docs/collections.md), one per
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
	code, manifest, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	if manifest.Concurrency {
		cfiles = append(cfiles, runtime.ConcurrencyCUnits(dir, runtime.HostArch())...)
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
	got := runProgramRT(t, "struct Point {\n\tx: int\n\ty: int\n}\n"+
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
