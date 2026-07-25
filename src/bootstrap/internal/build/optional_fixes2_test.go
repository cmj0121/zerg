package build

import "testing"

// Regression tests for the SECOND adversarial-review defects in the Optional-carrier work.
// Each program is compiled to C, linked under ASan+UBSan, and asserted on output (a silent
// miscompile — a value in the wrong carrier slot, no heap so no balance check) or on a zero
// alloc/free balance (the if-EXPRESSION early-exit leak). Every test FAILS before its fix
// and passes after.
//
// The class fixed here: a value materialized into an OPTIONAL CARRIER slot must be wrapped
// into the `{tag, ok}` carrier. The prior fix covered the struct-field EXPLICIT arg; these
// cover the remaining sites — a field DEFAULT, an enum variant optional payload, a
// reassignment into a POD optional, and a defaulted/explicit optional call argument — plus
// the if-EXPRESSION if-let early-exit teardown leak.

// --- G1: a POD optional struct-field DEFAULT is placed in the wrong carrier slot ----------

// TestOptFix2PODFieldDefault: `struct Cfg { port: int? = 8080 }` — the backfilled default
// must wrap into the carrier, not drop 8080 raw into the `tag` slot. Before the fix
// `Cfg().port ?? -1` read as -1 (absent). This is docs/grammar.md's headline example.
func TestOptFix2PODFieldDefault(t *testing.T) {
	src := "struct Cfg {\n\tport: int? = 8080\n}\n" +
		"fn main() {\n\tprint (Cfg().port ?? -1)\n}\n"
	if got := runProgramRT(t, src); got != "8080\n" {
		t.Fatalf("POD optional field default = %q, want %q", got, "8080\n")
	}
}

// TestOptFix2NilFieldDefault: a `= nil` optional field default reads back absent.
func TestOptFix2NilFieldDefault(t *testing.T) {
	src := "struct Cfg {\n\tport: int? = nil\n}\n" +
		"fn main() {\n\tprint (Cfg().port ?? -1)\n}\n"
	if got := runProgramRT(t, src); got != "-1\n" {
		t.Fatalf("nil optional field default = %q, want %q", got, "-1\n")
	}
}

// TestOptFix2PlainFieldDefaultUnchanged: a NON-optional field default (`port: int = 8080`)
// is unaffected — it still reads its value, confirming the carrier wrap does not touch a
// plain POD default.
func TestOptFix2PlainFieldDefaultUnchanged(t *testing.T) {
	src := "struct Cfg {\n\tport: int = 8080\n}\n" +
		"fn main() {\n\tprint Cfg().port\n}\n"
	if got := runProgramRT(t, src); got != "8080\n" {
		t.Fatalf("plain field default = %q, want %q", got, "8080\n")
	}
}

// --- G2: an enum variant OPTIONAL payload is placed in the wrong carrier slot -------------

// TestOptFix2EnumPODOptionalPayload: `enum E { Some(int?) }; Some(5)` must wrap the payload
// into the carrier. Before the fix `match Some(5) { Some(v) => v ?? -9 }` matched as absent
// (-9). Present and absent are both asserted.
func TestOptFix2EnumPODOptionalPayload(t *testing.T) {
	src := "enum E {\n\tSome(int?)\n\tNone\n}\n" +
		"fn probe(e: E) -> int {\n\treturn match e {\n\t\tSome(v) => v ?? -9\n\t\tNone => 0\n\t}\n}\n" +
		"fn main() {\n\tprint probe(Some(5))\n\tprint probe(Some(nil))\n\tprint probe(None)\n}\n"
	if got := runProgramRT(t, src); got != "5\n-9\n0\n" {
		t.Fatalf("enum POD optional payload = %q, want %q", got, "5\n-9\n0\n")
	}
}

// TestOptFix2EnumNominalOptionalPayload: a NOMINAL optional payload `Wrap(Inner?)` with a
// non-nil value is a LOUD cc type error before the fix; after, it compiles and round-trips
// (the wrapped Some carries the Inner, whose own `str?` field is read back). ASan-balanced
// because Inner's `label` is heap str.
func TestOptFix2EnumNominalOptionalPayload(t *testing.T) {
	src := "struct Inner {\n\tlabel: str?\n}\n" +
		"enum E {\n\tWrap(Inner?)\n\tNone\n}\n" +
		"fn show(e: E) {\n\tmatch e {\n\t\tWrap(v) => {\n\t\t\tif inner := v {\n\t\t\t\tif l := inner.label {\n\t\t\t\t\tprint l\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t\tNone => {\n\t\t\tprint (\"no\" + \"ne\")\n\t\t}\n\t}\n}\n" +
		"fn main() {\n\tshow(Wrap(Inner(label: \"x\" + \"y\")))\n\tshow(Wrap(nil))\n\tshow(None)\n}\n"
	if got := runProgramRTBalanced(t, src); got != "xy\nnone\n" {
		t.Fatalf("enum nominal optional payload = %q, want %q", got, "xy\nnone\n")
	}
}

// --- G3: a reassignment into a POD optional variable is placed in the wrong carrier slot ---

// TestOptFix2PODOptionalReassign: `mut x: int?; x = 42` / `x = nil` must wrap into the
// carrier. Before the fix the reassignment took the POD fast path and stored the value raw
// (a loud cc type error / silent miscompile). The bind initializer already wrapped, so only
// the reassignment path was broken.
func TestOptFix2PODOptionalReassign(t *testing.T) {
	src := "fn main() {\n" +
		"\tmut x: int? = 9\n\tprint (x ?? -1)\n" +
		"\tx = nil\n\tprint (x ?? -1)\n" +
		"\tx = 42\n\tprint (x ?? -1)\n" +
		"}\n"
	if got := runProgramRT(t, src); got != "9\n-1\n42\n" {
		t.Fatalf("POD optional reassign = %q, want %q", got, "9\n-1\n42\n")
	}
}

// --- G4: a value passed to an OPTIONAL-carrier parameter (default + explicit) --------------

// TestOptFix2OptionalParamDefault: `fn f(p: int? = 8080)` — the backfilled default and an
// explicit bare argument must both wrap into the carrier parameter. Before the fix both
// reached cc as a raw int in a `zg_opt_0` parameter slot (a loud cc type error).
func TestOptFix2OptionalParamDefault(t *testing.T) {
	src := "fn f(p: int? = 8080) -> int {\n\treturn p ?? -1\n}\n" +
		"fn main() {\n\tprint f()\n\tprint f(5)\n\tprint f(nil)\n}\n"
	if got := runProgramRT(t, src); got != "8080\n5\n-1\n" {
		t.Fatalf("optional param default/explicit = %q, want %q", got, "8080\n5\n-1\n")
	}
}

// --- G5: an if-EXPRESSION if-let early exit leaks the optional payload --------------------
//
// An if-EXPRESSION binding head (`x := if s := f() { …; <early exit>; … } else { … }`) is a
// teardown SOURCE like the statement form, but subtreeTeardown did not recognize it (an
// IfExpr is an expression), so the enclosing fn/loop recorded no mark and an early
// return/break/continue skipped the deferred payload drop (ZRT_ALLOC_BALANCE=1). Each test
// exercises one early-exit kind, for a `str?` and a `Ref[int]?` producer, ASan-balanced.

const ifExprStrDecl = "fn f(k: int) -> str? {\n\treturn (\"a\" + \"b\") if k == 1\n\treturn nil\n}\n"
const ifExprRefDecl = "fn f(k: int) -> Ref[int]? {\n\treturn Ref(5) if k == 1\n\treturn nil\n}\n"

// TestOptFix2IfExprEarlyReturnStr: an early `return` out of a `str?` if-expr then-block, on
// the early path (g(1)) and the normal path (g(0)) — the normal path must not double-drop.
func TestOptFix2IfExprEarlyReturnStr(t *testing.T) {
	src := ifExprStrDecl +
		"fn g(early: int) -> int {\n" +
		"\tx := if s := f(1) {\n\t\tprint s\n\t\treturn -1 if early == 1\n\t\t5\n\t} else {\n\t\t0\n\t}\n\treturn x\n}\n" +
		"fn main() {\n\tprint g(1)\n\tprint g(0)\n}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\n-1\nab\n5\n" {
		t.Fatalf("if-expr early return (str?) = %q, want %q", got, "ab\n-1\nab\n5\n")
	}
}

// TestOptFix2IfExprEarlyReturnRef: an early `return` out of a `Ref[int]?` if-expr then-block.
func TestOptFix2IfExprEarlyReturnRef(t *testing.T) {
	src := ifExprRefDecl +
		"fn g(early: int) -> int {\n" +
		"\tx := if r := f(1) {\n\t\tprint deref(r)\n\t\treturn -1 if early == 1\n\t\t9\n\t} else {\n\t\t0\n\t}\n\treturn x\n}\n" +
		"fn main() {\n\tprint g(1)\n\tprint g(0)\n}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n-1\n5\n9\n" {
		t.Fatalf("if-expr early return (Ref?) = %q, want %q", got, "5\n-1\n5\n9\n")
	}
}

// TestOptFix2IfExprEarlyBreakStr: an early `break` out of a `str?` if-expr then-block; the
// break unwinds to the loop mark, running the payload drop once.
func TestOptFix2IfExprEarlyBreakStr(t *testing.T) {
	src := ifExprStrDecl +
		"fn main() {\n\tfor i in 0..3 {\n" +
		"\t\tx := if s := f(1) {\n\t\t\tprint s\n\t\t\tbreak if i == 0\n\t\t\t5\n\t\t} else {\n\t\t\t0\n\t\t}\n\t\tprint x\n\t}\n}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\n" {
		t.Fatalf("if-expr early break (str?) = %q, want %q", got, "ab\n")
	}
}

// TestOptFix2IfExprEarlyBreakRef: an early `break` out of a `Ref[int]?` if-expr then-block.
func TestOptFix2IfExprEarlyBreakRef(t *testing.T) {
	src := ifExprRefDecl +
		"fn main() {\n\tfor i in 0..3 {\n" +
		"\t\tx := if r := f(1) {\n\t\t\tprint deref(r)\n\t\t\tbreak if i == 0\n\t\t\t9\n\t\t} else {\n\t\t\t0\n\t\t}\n\t\tprint x\n\t}\n}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n" {
		t.Fatalf("if-expr early break (Ref?) = %q, want %q", got, "5\n")
	}
}

// TestOptFix2IfExprEarlyContinueStr: an early `continue` out of a `str?` if-expr then-block,
// three iterations — each allocation must be dropped on the continue (balance stays 0).
func TestOptFix2IfExprEarlyContinueStr(t *testing.T) {
	src := ifExprStrDecl +
		"fn main() {\n\tfor i in 0..3 {\n" +
		"\t\tx := if s := f(1) {\n\t\t\tprint s\n\t\t\tcontinue if i < 2\n\t\t\t5\n\t\t} else {\n\t\t\t0\n\t\t}\n\t\tprint x\n\t}\n}\n"
	if got := runProgramRTBalanced(t, src); got != "ab\nab\nab\n5\n" {
		t.Fatalf("if-expr early continue (str?) = %q, want %q", got, "ab\nab\nab\n5\n")
	}
}

// TestOptFix2IfExprEarlyContinueRef: an early `continue` out of a `Ref[int]?` if-expr
// then-block, three iterations — each box released on the continue (balance stays 0).
func TestOptFix2IfExprEarlyContinueRef(t *testing.T) {
	src := ifExprRefDecl +
		"fn main() {\n\tfor i in 0..3 {\n" +
		"\t\tx := if r := f(1) {\n\t\t\tprint deref(r)\n\t\t\tcontinue if i < 2\n\t\t\t9\n\t\t} else {\n\t\t\t0\n\t\t}\n\t\tprint x\n\t}\n}\n"
	if got := runProgramRTBalanced(t, src); got != "5\n5\n5\n9\n" {
		t.Fatalf("if-expr early continue (Ref?) = %q, want %q", got, "5\n5\n5\n9\n")
	}
}
