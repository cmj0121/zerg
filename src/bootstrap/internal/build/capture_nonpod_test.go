package build

import (
	"strings"
	"testing"
)

// RUN-based, leak-balanced tests for NON-POD closure captures (docs/functions.md): a
// closure may now capture an IMMUTABLE non-POD value (a Ref/list/map/str/boxed value).
// The capture is retained into the closure's refcounted environment box and released
// when the closure value is dropped, so the captured value's lifetime is extended past
// the defining scope. Each program is compiled+linked under ASan+UBSan with the counting
// allocator (runProgramRTBalanced), run, and asserted to exit cleanly with a ZERO
// alloc/free balance — so a leak, double-free, or use-after-free fails the test.

// TestCaptureRefBalanced: a closure captures a Ref[int], reads it through the
// environment, and both the closure and the original release it correctly (rc balanced).
func TestCaptureRefBalanced(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\tr := Ref(7)\n"+
		"\tf := fn() -> int { return deref(r) }\n"+
		"\tprint f()\n}\n")
	if want := "7\n"; got != want {
		t.Fatalf("Ref capture: got %q, want %q", got, want)
	}
}

// TestCaptureListAndStrBalanced: one closure captures BOTH a list[int] (deep-copied into
// the env by value) and a heap str (retained into the env), reads them, and both the env
// copies and the originals are freed exactly once.
func TestCaptureListAndStrBalanced(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\txs := [1, 2, 3]\n"+
		"\ts := \"x\" + \"y\"\n"+
		"\tg := fn() -> int {\n\t\tprint s\n\t\treturn xs.len()\n\t}\n"+
		"\tprint g()\n}\n")
	if want := "xy\n3\n"; got != want {
		t.Fatalf("list+str capture: got %q, want %q", got, want)
	}
}

// TestCaptureEscapesBalanced is the definitive lifetime test: a closure that captures a
// Ref ESCAPES its defining scope (returned from a factory). The env outlives the factory
// call — the captured Ref is retained past the factory's scope-exit release, held alive
// solely by the returned closure, and freed exactly once when the closure is dropped.
func TestCaptureEscapesBalanced(t *testing.T) {
	got := runProgramRTBalanced(t, "fn make_holder(n: int) -> fn() -> int {\n"+
		"\tr := Ref(n)\n"+
		"\treturn fn() -> int { return deref(r) }\n}\n"+
		"fn main() {\n"+
		"\th := make_holder(42)\n"+
		"\tprint h()\n}\n")
	if want := "42\n"; got != want {
		t.Fatalf("escaping Ref capture: got %q, want %q", got, want)
	}
}

// TestCaptureSharedRefBalanced: two DISTINCT closures capture the SAME Ref (each env
// retains it, so rc > 1 while both are live). Both read it validly, and the shared cell
// is freed exactly once after the last holder — the original and both envs — releases.
func TestCaptureSharedRefBalanced(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\tr := Ref(5)\n"+
		"\tf := fn() -> int { return deref(r) }\n"+
		"\tg := fn() -> int { return deref(r) + 1 }\n"+
		"\tprint f()\n\tprint g()\n}\n")
	if want := "5\n6\n"; got != want {
		t.Fatalf("shared Ref capture: got %q, want %q", got, want)
	}
}

// TestCaptureNonPODLowering pins the emitted C of a managed closure environment: the box
// is refcounted (zrt_ref_alloc with a per-lambda drop thunk), a non-POD capture is
// retained in, the body reads a capture through the box's payload, the fn-value local
// releases its env at scope exit, and the drop thunk releases the captured cell.
func TestCaptureNonPODLowering(t *testing.T) {
	code, _, diags := Compile("fn main() {\n" +
		"\tr := Ref(7)\n" +
		"\tf := fn() -> int { return deref(r) }\n" +
		"\tprint f()\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"static void zg_fnval_drop(void *slot)",                        // the fn-value env-slot guard
		"static void zg_envdrop_lambda_0(void *p)",                     // the per-lambda env drop thunk
		"zrt_ref_alloc(sizeof(zg_env_lambda_0), &zg_envdrop_lambda_0)", // refcounted env box
		"_p->zg_r = zrt_ref_copy(zg_r)",                                // a non-POD capture retained in
		"((zg_env_lambda_0 *)zrt_ref_payload(zg_env))->zg_r",           // a body read through the box payload
		"zrt_defer(zg_fnval_drop, &zg_f)",                              // the fn local releases its env at exit
		"zrt_release(e->zg_r);",                                        // the drop thunk releases the captured cell
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("managed-env C missing %q:\n%s", want, code)
		}
	}
}
