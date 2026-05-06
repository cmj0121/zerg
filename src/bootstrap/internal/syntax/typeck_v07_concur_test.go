package syntax

import (
	"testing"
)

// v0.7 Unit 3 — typeck for anonymous functions (with closure capture
// analysis), the `spawn` statement, the `defer` statement, and the
// `wait_group` / WaitGroup built-in. Coverage:
//
//   - AnonFnExpr: type is TypeFn with resolved param/return; void return
//     printed as `fn(...)`; non-void as `fn(...) -> R`.
//   - Calling: IIFE `fn() { ... }()` typechecks; `f := fn(...) ...; f()`
//     typechecks; arity / arg-type mismatches reject.
//   - Closure capture: outer immutable / const captured; capture recorded;
//     mut capture rejected.
//   - SpawnStmt: named-fn call, IIFE, qualified call type-check; rejects
//     non-call-expr (parser already handles, but typeck runs the call).
//   - DeferStmt: walks body; HasDefers recorded on enclosing FnDecl /
//     AnonFnExpr.
//   - wait_group(): result type is WaitGroup; methods add/done/wait
//     dispatch.
//   - Reservations: wait_group / WaitGroup / spawn / defer / select reject
//     at every binding site.

// --- AnonFnExpr type ----------------------------------------------------

func TestV07AnonFnVoidType(t *testing.T) {
	prog := checkSrc(t, "f := fn() { print 1 }\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got == nil || got.Kind != TypeFn {
		t.Fatalf("kind = %v, want TypeFn", got)
	}
	if len(got.FnParams) != 0 {
		t.Errorf("params = %v, want empty", got.FnParams)
	}
	if got.FnReturn != tVoid {
		t.Errorf("return = %v, want void", got.FnReturn)
	}
	if got.String() != "fn()" {
		t.Errorf("String = %q, want fn()", got.String())
	}
}

func TestV07AnonFnTypedSignature(t *testing.T) {
	prog := checkSrc(t, "g := fn(x: int) -> int { return x * 2 }\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got == nil || got.Kind != TypeFn {
		t.Fatalf("kind = %v, want TypeFn", got)
	}
	if len(got.FnParams) != 1 || got.FnParams[0] != tInt {
		t.Errorf("params = %v, want [int]", got.FnParams)
	}
	if got.FnReturn != tInt {
		t.Errorf("return = %v, want int", got.FnReturn)
	}
	if got.String() != "fn(int) -> int" {
		t.Errorf("String = %q, want fn(int) -> int", got.String())
	}
}

func TestV07AnonFnMultiParamString(t *testing.T) {
	prog := checkSrc(t, "h := fn(a: int, b: str) -> bool { return true }\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got.String() != "fn(int, str) -> bool" {
		t.Errorf("String = %q, want fn(int, str) -> bool", got.String())
	}
}

// --- Calling fn-typed bindings -----------------------------------------

func TestV07AnonFnCallVoid(t *testing.T) {
	checkSrc(t, "f := fn() { print 1 }\nfn run() { f() }\n")
}

func TestV07AnonFnCallReturnsInt(t *testing.T) {
	prog := checkSrc(t, "g := fn(x: int) -> int { return x * 2 }\nfn run() { y := g(5) }\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements", len(prog.Statements))
	}
	fn := prog.Statements[1].(*FnDecl)
	let := fn.Body.Statements[0].(*LetStmt)
	if let.Value.Type() != tInt {
		t.Errorf("g(5) type = %v, want int", let.Value.Type())
	}
}

func TestV07AnonFnIIFEVoid(t *testing.T) {
	// Statement-position IIFE: `fn() { ... }()` discards the result.
	checkSrc(t, "fn run() { fn() { print 1 }() }\n")
}

func TestV07AnonFnIIFEReturning(t *testing.T) {
	// Expression-position IIFE: `x := fn() -> int { return 1 }()`.
	prog := checkSrc(t, "x := fn() -> int { return 7 }()\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Value.Type() != tInt {
		t.Errorf("type = %v, want int", s.Value.Type())
	}
}

func TestV07AnonFnArityMismatch(t *testing.T) {
	checkErr(t,
		"g := fn(x: int) -> int { return x }\nfn run() { y := g(1, 2) }\n",
		`function "g" expects 1 argument(s), got 2`)
}

func TestV07AnonFnArgTypeMismatch(t *testing.T) {
	checkErr(t,
		"g := fn(x: int) -> int { return x }\nfn run() { y := g(\"hi\") }\n",
		`argument 1 to "g" has type str, expected int`)
}

// --- Closure capture ---------------------------------------------------

func TestV07ClosureCaptureLet(t *testing.T) {
	prog := checkSrc(t, "fn run() { x := 5\nf := fn() { print x } }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[1].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 1 {
		t.Fatalf("captures = %d, want 1", len(anon.Captures))
	}
	if anon.Captures[0].Name != "x" {
		t.Errorf("capture[0].Name = %q, want x", anon.Captures[0].Name)
	}
	if anon.Captures[0].Type != tInt {
		t.Errorf("capture[0].Type = %v, want int", anon.Captures[0].Type)
	}
}

func TestV07ClosureCaptureConst(t *testing.T) {
	prog := checkSrc(t, "const N := 10\nf := fn() -> int { return N }\n")
	let := prog.Statements[1].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 1 || anon.Captures[0].Name != "N" {
		t.Fatalf("captures = %v, want [N]", anon.Captures)
	}
}

func TestV07ClosureCaptureMutRejects(t *testing.T) {
	checkErr(t,
		"fn run() { mut x := 5\nf := fn() { print x } }\n",
		`cannot capture mut binding "x" in closure`)
}

func TestV07ClosureCaptureMultipleNames(t *testing.T) {
	prog := checkSrc(t, "fn run() { a := 1\nb := 2\nf := fn() -> int { return a + b } }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[2].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 2 {
		t.Fatalf("captures = %d, want 2", len(anon.Captures))
	}
	names := map[string]bool{}
	for _, ca := range anon.Captures {
		names[ca.Name] = true
	}
	if !names["a"] || !names["b"] {
		t.Errorf("captures = %v, want {a, b}", names)
	}
}

func TestV07ClosureCaptureSameNameOnce(t *testing.T) {
	// Multiple references to the same outer binding record one capture.
	prog := checkSrc(t, "fn run() { x := 1\nf := fn() -> int { return x + x + x } }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[1].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 1 {
		t.Errorf("captures = %d, want 1", len(anon.Captures))
	}
}

func TestV07ClosureNoCaptureForLocalParam(t *testing.T) {
	// `x` is the anon-fn's own param; not a capture.
	prog := checkSrc(t, "f := fn(x: int) -> int { return x + 1 }\n")
	let := expectOne[*LetStmt](t, prog)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 0 {
		t.Errorf("captures = %v, want empty", anon.Captures)
	}
}

func TestV07ClosureNoCaptureForLocalLet(t *testing.T) {
	// `y` is declared inside the body; not a capture.
	prog := checkSrc(t, "f := fn() -> int { y := 7\nreturn y }\n")
	let := expectOne[*LetStmt](t, prog)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 0 {
		t.Errorf("captures = %v, want empty", anon.Captures)
	}
}

func TestV07ClosureCaptureGlobalLet(t *testing.T) {
	// Top-level immutable bindings can be captured under the same rule.
	prog := checkSrc(t, "g := 100\nf := fn() -> int { return g }\n")
	let := prog.Statements[1].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 1 || anon.Captures[0].Name != "g" {
		t.Errorf("captures = %v, want [g]", anon.Captures)
	}
}

func TestV07ClosureCaptureGlobalMutRejects(t *testing.T) {
	checkErr(t,
		"mut g := 100\nf := fn() -> int { return g }\n",
		`cannot capture mut binding "g" in closure`)
}

func TestV07ClosureCallingOuterFnIsAdmitted(t *testing.T) {
	// Calling an outer fn name from inside an anon-fn is fine — fn names
	// don't live in the value scope, so they're not captures.
	prog := checkSrc(t,
		"fn helper() -> int { return 42 }\nf := fn() -> int { return helper() }\n")
	let := prog.Statements[1].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if len(anon.Captures) != 0 {
		t.Errorf("captures = %v, want empty (fn names aren't captures)", anon.Captures)
	}
}

func TestV07ClosureNestedAnonFnCaptureChain(t *testing.T) {
	// inner anon-fn captures `x`; outer anon-fn also captures because the
	// inner-fn body's reference to `x` flows up the frame stack — every
	// active frame whose parentScope contains the binding records it.
	prog := checkSrc(t,
		"fn run() { x := 5\nouter := fn() { inner := fn() { print x } } }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[1].(*LetStmt)
	outerAnon := let.Value.(*AnonFnExpr)
	innerLet := outerAnon.Body.Statements[0].(*LetStmt)
	innerAnon := innerLet.Value.(*AnonFnExpr)
	if len(innerAnon.Captures) != 1 || innerAnon.Captures[0].Name != "x" {
		t.Errorf("inner captures = %v, want [x]", innerAnon.Captures)
	}
	// Outer anon-fn also captures `x` per the nested-frame propagation
	// rule: the inner body's free-variable `x` is outside both the inner
	// and outer anon-fn scopes, so both frames record it.
	if len(outerAnon.Captures) != 1 || outerAnon.Captures[0].Name != "x" {
		t.Errorf("outer captures = %v, want [x]", outerAnon.Captures)
	}
}

// --- spawn -------------------------------------------------------------

func TestV07SpawnNamedCall(t *testing.T) {
	checkSrc(t, "fn do_work() { print 1 }\nfn run() { spawn do_work() }\n")
}

func TestV07SpawnIIFE(t *testing.T) {
	checkSrc(t, "fn run() { spawn fn() { print 1 }() }\n")
}

func TestV07SpawnAnonFnWithArgs(t *testing.T) {
	checkSrc(t, "fn run() { spawn fn(x: int) { print x }(3) }\n")
}

func TestV07SpawnUnknownFnRejects(t *testing.T) {
	checkErr(t, "fn run() { spawn unknown() }\n", `undefined function "unknown"`)
}

func TestV07SpawnCallArityMismatchRejects(t *testing.T) {
	checkErr(t,
		"fn do_work(x: int) { }\nfn run() { spawn do_work() }\n",
		`function "do_work" expects 1 argument`)
}

// --- defer -------------------------------------------------------------

func TestV07DeferCall(t *testing.T) {
	prog := checkSrc(t, "fn cleanup() { print 1 }\nfn run() { defer cleanup() }\n")
	fn := prog.Statements[1].(*FnDecl)
	if !fn.HasDefers {
		t.Error("HasDefers = false, want true")
	}
}

func TestV07DeferBlock(t *testing.T) {
	prog := checkSrc(t, "fn run() { defer { print 1\nprint 2 } }\n")
	fn := expectOne[*FnDecl](t, prog)
	if !fn.HasDefers {
		t.Error("HasDefers = false, want true")
	}
}

func TestV07DeferDoesNotMarkUnrelatedFn(t *testing.T) {
	prog := checkSrc(t,
		"fn other() { print 1 }\nfn run() { defer other() }\n")
	other := prog.Statements[0].(*FnDecl)
	if other.HasDefers {
		t.Error("other.HasDefers = true, want false")
	}
}

func TestV07DeferInsideAnonFnRecordsOnAnon(t *testing.T) {
	prog := checkSrc(t,
		"fn cleanup() { print 1 }\nfn outer() { f := fn() { defer cleanup() } }\n")
	outer := prog.Statements[1].(*FnDecl)
	if outer.HasDefers {
		t.Error("outer.HasDefers = true, want false (defer is inside the anon-fn)")
	}
	let := outer.Body.Statements[0].(*LetStmt)
	anon := let.Value.(*AnonFnExpr)
	if !anon.HasDefers {
		t.Error("anon.HasDefers = false, want true")
	}
}

// --- wait_group --------------------------------------------------------

func TestV07WaitGroupConstructorType(t *testing.T) {
	prog := checkSrc(t, "wg := wait_group()\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got == nil || got.Kind != TypeStruct || got.Name != "WaitGroup" {
		t.Errorf("type = %v, want WaitGroup", got)
	}
}

func TestV07WaitGroupAdd(t *testing.T) {
	checkSrc(t, "fn run() { wg := wait_group()\nwg.add(5) }\n")
}

func TestV07WaitGroupDone(t *testing.T) {
	checkSrc(t, "fn run() { wg := wait_group()\nwg.done() }\n")
}

func TestV07WaitGroupWait(t *testing.T) {
	checkSrc(t, "fn run() { wg := wait_group()\nwg.wait() }\n")
}

func TestV07WaitGroupAddWrongArgType(t *testing.T) {
	checkErr(t,
		`fn run() { wg := wait_group()
wg.add("hi") }
`,
		`argument 1 to "add" has type str, expected int`)
}

func TestV07WaitGroupUnknownMethodRejects(t *testing.T) {
	checkErr(t,
		"fn run() { wg := wait_group()\nwg.foo() }\n",
		`method "foo" does not exist`)
}

func TestV07WaitGroupExtraArgsRejects(t *testing.T) {
	checkErr(t,
		"fn run() { wg := wait_group(1) }\n",
		`function "wait_group" expects 0 arguments`)
}

// --- reservation: wait_group / WaitGroup -------------------------------

func TestV07WaitGroupFnNameReserved(t *testing.T) {
	checkErr(t, "wait_group := 1\n",
		`name "wait_group" is reserved (built-in)`)
}

func TestV07WaitGroupFnAsFnNameReserved(t *testing.T) {
	checkErr(t, "fn wait_group() { }\n",
		`name "wait_group" is reserved (built-in)`)
}

func TestV07WaitGroupTypeAsStructNameReserved(t *testing.T) {
	checkErr(t, "struct WaitGroup { x: int }\n",
		`name "WaitGroup" is reserved (built-in)`)
}

func TestV07WaitGroupTypeAsEnumNameReserved(t *testing.T) {
	checkErr(t, "enum WaitGroup { A, B }\n",
		`name "WaitGroup" is reserved (built-in)`)
}

// --- reservation: spawn / defer / select are KEYWORDS -----------------
//
// `spawn`, `defer`, and `select` are reserved at the lexer level (see
// keywords.go), so any attempt to use them as identifiers rejects at parse
// time before typeck runs. The typeck-level reservation in
// isReservedV07ConcurName is a defense-in-depth gate for the unlikely
// path where a future parser tweak admits one of those names as an IDENT.
// We assert the parse-time rejection here since that's the user-visible
// diagnostic.

func TestV07SpawnIsKeywordAtLetName(t *testing.T) {
	tokens, err := Lex([]byte("spawn := 1\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, err := Parse(tokens); err == nil {
		t.Fatal("Parse succeeded, want keyword rejection on `spawn`")
	}
}

func TestV07DeferIsKeywordAtLetName(t *testing.T) {
	tokens, err := Lex([]byte("defer := 1\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, err := Parse(tokens); err == nil {
		t.Fatal("Parse succeeded, want keyword rejection on `defer`")
	}
}

func TestV07SelectIsKeywordAtLetName(t *testing.T) {
	tokens, err := Lex([]byte("select := 1\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, err := Parse(tokens); err == nil {
		t.Fatal("Parse succeeded, want keyword rejection on `select`")
	}
}

// --- typed arg propagation through anon-fn calls -----------------------

func TestV07AnonFnCallsCapturedFnValue(t *testing.T) {
	// Captured fn-typed binding: the inner anon-fn calls `g`, which is a
	// fn-typed outer binding. Capture analysis records `g`.
	prog := checkSrc(t,
		"fn run() { g := fn(x: int) -> int { return x + 1 }\nh := fn() -> int { return g(5) } }\n")
	fn := expectOne[*FnDecl](t, prog)
	hLet := fn.Body.Statements[1].(*LetStmt)
	hAnon := hLet.Value.(*AnonFnExpr)
	if len(hAnon.Captures) != 1 || hAnon.Captures[0].Name != "g" {
		t.Errorf("captures = %v, want [g]", hAnon.Captures)
	}
}

func TestV07AnonFnReturnTypeMismatch(t *testing.T) {
	checkErr(t,
		"f := fn() -> int { return \"hi\" }\n",
		"return type mismatch")
}

// --- spawn of stmt-not-call rejects (parser already does it for arith;
// here we exercise typeck on a parser-admitted shape that's still an
// invalid call target — `spawn print x` is parser-rejected, but a
// well-formed CallExpr to an unknown fn is rejected at typeck) ----------

func TestV07SpawnDispatchesThroughCheckCall(t *testing.T) {
	// Confirms checkSpawnStmt routes through the full call typeck path —
	// type errors inside the spawned call surface here.
	checkErr(t,
		`fn do_work(x: int) { print x }
fn run() { spawn do_work("hi") }
`,
		`argument 1 to "do_work" has type str, expected int`)
}
