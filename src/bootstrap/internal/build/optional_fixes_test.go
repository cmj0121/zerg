package build

import "testing"

// Regression tests for the four adversarial-review defects in the non-POD Optional carrier
// work. Each program is compiled to C, linked under ASan+UBSan with the counting allocator,
// run, and asserted to exit cleanly with a ZERO alloc/free balance (no leak, no double-free).
// Every test FAILS before its fix — F1 miscompiles (value in the wrong carrier slot), F2/F4
// fail to compile (undeclared `zg_ref_drop` / unknown carrier typedef), F3 leaks the payload
// on an early exit — and passes after.

// --- F1: a POD optional struct field is placed in the wrong carrier slot -------------------

// TestOptFixPODOptionalStructField: constructing a struct with a POD optional field
// (`n: int?`) must wrap the argument into the carrier (`{tag, ok}`), not drop it raw into the
// `tag` slot. Before the fix `Box(n: 5).n` read as ABSENT and `Box(n: nil).n` as present-0.
// This is a silent MISCOMPILE, not a leak (a POD carrier owns no heap), so it is asserted on
// output under ASan+UBSan rather than on alloc balance.
func TestOptFixPODOptionalStructField(t *testing.T) {
	src := "struct Box {\n\tn: int?\n}\n" +
		"fn main() {\n" +
		"\tb := Box(n: 5)\n" +
		"\tprint (b.n ?? -1)\n" +
		"\te := Box(n: nil)\n" +
		"\tprint (e.n ?? -1)\n" +
		"}\n"
	if got := runProgramRT(t, src); got != "5\n-1\n" {
		t.Fatalf("POD optional struct field = %q, want %q", got, "5\n-1\n")
	}
}

// --- F2: if-let over a boxed (recursive) optional with no other Ref local -------------------

// TestOptFixBoxedOptIfLetNoRefLocal: an if-let over a boxed `Node?` (`if n := a.next`)
// registers the zg_ref_drop slot guard for the evaluated-optional temp, so that thunk must be
// emitted even when the program binds no other Ref local. Before the fix the C failed to
// compile with "use of undeclared identifier 'zg_ref_drop'".
func TestOptFixBoxedOptIfLetNoRefLocal(t *testing.T) {
	src := "struct Node {\n\tval: int\n\tnext: Node?\n}\n" +
		"fn main() {\n" +
		"\ta := Node(val: 1, next: Node(val: 2, next: nil))\n" +
		"\tif n := a.next {\n" +
		"\t\tprint n.val\n" +
		"\t\tif m := n.next {\n\t\t\tprint m.val\n\t\t} else {\n\t\t\tprint -1\n\t\t}\n" +
		"\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "2\n-1\n" {
		t.Fatalf("boxed-opt if-let (no other Ref local) = %q, want %q", got, "2\n-1\n")
	}
}

// --- F3: early exit out of a non-POD if-let then-block leaks the payload --------------------
//
// When a non-POD if-let is a function's/loop's ONLY teardown source, an early
// return/break/continue inside the then-block must still run the if-let's deferred drop. The
// enclosing scope now marks itself for unwind so the early exit unwinds to it (drop runs once).

// strOpt is the str?-producing helper prefix; getRef is the Ref[int]?-producing one. Each
// forces a heap allocation on the present path so a skipped drop shows up as a leak.
const strOptDecl = "fn f(k: int) -> str? {\n\treturn (\"a\" + \"b\") if k == 1\n\treturn nil\n}\n"
const refOptDecl = "fn f(k: int) -> Ref[int]? {\n\treturn Ref(5) if k == 1\n\treturn nil\n}\n"

// TestOptFixEarlyReturnStr: an early `return` from inside a `str?` if-let then-block.
func TestOptFixEarlyReturnStr(t *testing.T) {
	src := strOptDecl +
		"fn g() {\n" +
		"\tif s := f(1) {\n\t\tprint s\n\t\treturn\n\t}\n" +
		"\tprint (\"x\" + \"y\")\n" +
		"}\n" +
		"fn main() {\n\tg()\n}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\n" {
		t.Fatalf("early return from str? if-let = %q, want %q", got, "ab\n")
	}
}

// TestOptFixEarlyBreakStr: an early `break` from inside a `str?` if-let then-block.
func TestOptFixEarlyBreakStr(t *testing.T) {
	src := strOptDecl +
		"fn main() {\n" +
		"\tfor i in 0..3 {\n" +
		"\t\tif s := f(1) {\n\t\t\tprint s\n\t\t\tbreak\n\t\t}\n" +
		"\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\n" {
		t.Fatalf("early break from str? if-let = %q, want %q", got, "ab\n")
	}
}

// TestOptFixEarlyContinueStr: an early `continue` from inside a `str?` if-let then-block,
// three iterations — each allocation must be dropped on the continue (balance stays 0).
func TestOptFixEarlyContinueStr(t *testing.T) {
	src := strOptDecl +
		"fn main() {\n" +
		"\tfor i in 0..3 {\n" +
		"\t\tif s := f(1) {\n\t\t\tprint s\n\t\t\tcontinue\n\t\t}\n" +
		"\t\tprint -1\n" +
		"\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\nab\nab\n" {
		t.Fatalf("early continue from str? if-let = %q, want %q", got, "ab\nab\nab\n")
	}
}

// TestOptFixEarlyReturnRef: an early `return` from inside a `Ref[int]?` if-let then-block.
func TestOptFixEarlyReturnRef(t *testing.T) {
	src := refOptDecl +
		"fn g() {\n" +
		"\tif r := f(1) {\n\t\tprint deref(r)\n\t\treturn\n\t}\n" +
		"\tprint -1\n" +
		"}\n" +
		"fn main() {\n\tg()\n}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n" {
		t.Fatalf("early return from Ref? if-let = %q, want %q", got, "5\n")
	}
}

// TestOptFixEarlyBreakRef: an early `break` from inside a `Ref[int]?` if-let then-block.
func TestOptFixEarlyBreakRef(t *testing.T) {
	src := refOptDecl +
		"fn main() {\n" +
		"\tfor i in 0..3 {\n" +
		"\t\tif r := f(1) {\n\t\t\tprint deref(r)\n\t\t\tbreak\n\t\t}\n" +
		"\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n" {
		t.Fatalf("early break from Ref? if-let = %q, want %q", got, "5\n")
	}
}

// TestOptFixEarlyContinueRef: an early `continue` from inside a `Ref[int]?` if-let then-block,
// three iterations — each box must be released on the continue (balance stays 0).
func TestOptFixEarlyContinueRef(t *testing.T) {
	src := refOptDecl +
		"fn main() {\n" +
		"\tfor i in 0..3 {\n" +
		"\t\tif r := f(1) {\n\t\t\tprint deref(r)\n\t\t\tcontinue\n\t\t}\n" +
		"\t\tprint -1\n" +
		"\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n5\n5\n" {
		t.Fatalf("early continue from Ref? if-let = %q, want %q", got, "5\n5\n5\n")
	}
}

// --- F4: a struct whose optional field wraps another nominal -------------------------------

// TestOptFixNestedOptionalStruct: `Outer { kid: Inner? }` where `Inner` itself has an optional
// field forces the ordering Outer -> Inner? -> Inner -> str?, which the historic plain/nominal
// two-pass could not satisfy (the `Inner?` carrier typedef followed `zg_Outer` that embeds it
// by value). The topological typedef order emits each type after everything it embeds by value.
func TestOptFixNestedOptionalStruct(t *testing.T) {
	src := "struct Inner {\n\tlabel: str?\n}\n" +
		"struct Outer {\n\tkid: Inner?\n}\n" +
		"fn main() {\n" +
		"\to := Outer(kid: Inner(label: \"x\" + \"y\"))\n" +
		"\tif k := o.kid {\n\t\tif l := k.label {\n\t\t\tprint l\n\t\t}\n\t}\n" +
		"\te := Outer(kid: nil)\n" +
		"\tif k := e.kid {\n\t\tprint (\"so\" + \"me\")\n\t} else {\n\t\tprint (\"no\" + \"ne\")\n\t}\n" +
		"}\n"
	if got := runProgramRTBalanced(t, src); got != "xy\nnone\n" {
		t.Fatalf("nested optional struct = %q, want %q", got, "xy\nnone\n")
	}
}
