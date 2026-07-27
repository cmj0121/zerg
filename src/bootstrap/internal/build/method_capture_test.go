package build

import (
	"strings"
	"testing"
)

// Loud-gap fix (Fix 1): `defer recv.method()` / `spawn recv.method(args)` — a method
// (Field) callee, which previously hit a loud emit stub. These RUN tests link against
// the runtime under ASan+UBSan; the spawn case additionally proves a zero alloc/free
// balance (runProgramRTBalanced) since it captures a non-POD channel argument.

// TestDeferMethodRuns proves a deferred method call runs at scope exit, in LIFO order
// against a second defer, dispatched on its captured (POD) receiver. It runs under
// ASan+UBSan (runProgramRT); the receiver is a value struct that allocates nothing.
func TestDeferMethodRuns(t *testing.T) {
	got := runProgramRT(t,
		"struct Lock {\n\tpub n: int\n}\n"+
			"impl Lock {\n\tpub fn teardown() {\n\t\tprint this.n\n\t}\n}\n"+
			"fn main() {\n"+
			"\ta := Lock(n: 1)\n"+
			"\tb := Lock(n: 2)\n"+
			"\tdefer a.teardown()\n"+
			"\tdefer b.teardown()\n"+
			"\tprint 0\n"+
			"}\n")
	// print 0 first, then LIFO teardown: b (2) before a (1).
	if want := "0\n2\n1\n"; got != want {
		t.Fatalf("defer-method ordering: got %q, want %q", got, want)
	}
}

// TestDeferMethodNonPodTemporaryReceiver is the ownership regression for the defer
// receiver: a `defer` over a NON-POD TEMPORARY receiver (`defer make_res().use()`) has no
// surviving owner for the temporary, so a bare borrow would leak (or dangle) it. The defer
// now OWNS its receiver (the fresh Res is moved into the env and released by the thunk
// after the deferred call), so it is alloc/free balanced under ASan+UBSan.
func TestDeferMethodNonPodTemporaryReceiver(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Res {\n\tr: Ref[int]\n}\n"+
			"impl Res {\n\tpub fn use() {\n\t\tprint deref(this.r)\n\t}\n}\n"+
			"fn make_res() -> Res {\n\treturn Res(r: Ref(7))\n}\n"+
			"fn main() {\n"+
			"\tdefer make_res().use()\n"+ // no owner for the temporary but the defer's copy
			"\tprint 0\n"+
			"}\n")
	if want := "0\n7\n"; got != want {
		t.Fatalf("defer non-POD temporary receiver: got %q, want %q", got, want)
	}
}

// TestDeferMethodNonPodLocalLifoBalanced covers the common case: a `defer` over a NAMED
// non-POD local stays balanced (the defer's owned copy is retained then released; the
// local's own drop runs separately — two independent Refs, each freed once, no double
// free) and LIFO ordering against a second defer is preserved.
func TestDeferMethodNonPodLocalLifoBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Res {\n\tr: Ref[int]\n}\n"+
			"impl Res {\n\tpub fn use() {\n\t\tprint deref(this.r)\n\t}\n}\n"+
			"fn main() {\n"+
			"\ta := Res(r: Ref(1))\n"+
			"\tb := Res(r: Ref(2))\n"+
			"\tdefer a.use()\n"+
			"\tdefer b.use()\n"+
			"\tprint 0\n"+
			"}\n")
	// print 0, then LIFO teardown: b (2) before a (1); each Res's Ref freed exactly once.
	if want := "0\n2\n1\n"; got != want {
		t.Fatalf("defer non-POD local LIFO/balance: got %q, want %q", got, want)
	}
}

// TestDeferMethodRunsOnAbortPath confirms a deferred non-POD method runs on the ABORT
// unwind path (the same cleanup stack), releasing its owned receiver there too. A
// `Result[nil]` main arms the root abort handler; an out-of-range index aborts after the
// defer is registered, and the deferred method's effect (9) is observed during the abort
// unwind — with no ASan/UBSan error (balanced release on the abort path).
func TestDeferMethodRunsOnAbortPath(t *testing.T) {
	out := runProgramRTAbort(t,
		"struct Res {\n\tr: Ref[int]\n}\n"+
			"impl Res {\n\tpub fn use() {\n\t\tprint deref(this.r)\n\t}\n}\n"+
			"fn main() -> Result[nil] {\n"+
			"\tx := Res(r: Ref(9))\n"+
			"\tdefer x.use()\n"+
			"\txs := [1, 2]\n"+
			"\tprint xs[5]\n"+ // IndexError -> abort -> root handler unwinds, running `defer x.use()`
			"\treturn nil\n"+
			"}\n")
	if !strings.Contains(out, "9") {
		t.Fatalf("deferred method should run on the abort-unwind path; output:\n%s", out)
	}
	if strings.Contains(out, "AddressSanitizer") || strings.Contains(out, "runtime error") {
		t.Fatalf("abort-unwind release must be sanitizer-clean; output:\n%s", out)
	}
}
