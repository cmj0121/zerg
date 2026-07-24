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

// TestSpawnMethodRuns proves a spawned method runs on a coroutine: the worker sends its
// id on a captured channel argument, and main observes it — leak-balanced (the channel
// arg is retained into the heap env and released by the method's own param drop).
func TestSpawnMethodRuns(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Worker {\n\tpub id: int\n}\n"+
			"impl Worker {\n\tpub fn run(ch: chan[int]) {\n\t\tch <- this.id\n\t}\n}\n"+
			"fn main() {\n"+
			"\tch := chan[int](1)\n"+
			"\tw := Worker(id: 42)\n"+
			"\tspawn w.run(ch)\n"+
			"\tprint (<-ch)!\n"+
			"}\n")
	if want := "42\n"; got != want {
		t.Fatalf("spawn-method channel: got %q, want %q", got, want)
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

// TestSpawnMethodNonPodReceiverOutlivesScope is the ownership regression for the spawn
// receiver: a fire-and-forget coroutine can run AFTER the spawner's scope has dropped
// the receiver (sched.c never joins it), so a NON-POD receiver must be OWNED by the
// coroutine, not borrowed, or its inner cells dangle (use-after-free / double-free).
// Here `start` spawns `b.emit(ch)` and RETURNS immediately — dropping its local Box (and
// releasing the ORIGINAL Ref) before the coroutine runs; main only reads the channel
// afterward, from a longer-lived scope. It must be alloc/free balanced with no UAF under
// ASan+UBSan, which holds only because the coroutine deep-copied the receiver and the
// trampoline releases that copy.
func TestSpawnMethodNonPodReceiverOutlivesScope(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct Box {\n\tr: Ref[int]\n}\n"+
			"impl Box {\n\tpub fn emit(ch: chan[int]) {\n\t\tch <- deref(this.r)\n\t}\n}\n"+
			"fn start(ch: chan[int]) {\n"+
			"\tb := Box(r: Ref(99))\n"+
			"\tspawn b.emit(ch)\n"+ // start() returns here -> b (and the ORIGINAL Ref) is dropped
			"}\n"+
			"fn main() {\n"+
			"\tch := chan[int](1)\n"+
			"\tstart(ch)\n"+ // spawner scope exits before the coroutine runs
			"\tprint (<-ch)!\n"+ // now the coroutine runs and reads its OWN receiver copy
			"}\n")
	if want := "99\n"; got != want {
		t.Fatalf("spawn non-POD receiver UAF regression: got %q, want %q", got, want)
	}
}
