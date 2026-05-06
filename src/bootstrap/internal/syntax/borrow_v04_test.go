package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 Unit 5 — borrow-check coverage for method-call receivers and `this`.
//
// Two related concerns at the typeck/borrow boundary:
//
//   (A) push/clone/len method-form desugar at typeck. A list-receiver
//       MethodCallExpr with method push / clone / len is rewritten to a
//       synthetic CallExpr stashed on MethodCallExpr.LoweredCall. The
//       borrow checker walks LoweredCall when present so the v0.3 push
//       rule (receiver must be Owned) fires unchanged on the method form.
//
//   (B) `this` inside method bodies. The borrow checker registers `this`
//       as a BorrowedShared local with the impl receiver type. Reads are
//       fine; `y := this` and `return this` reject as move-of-borrowed.
//
// Helpers (checkSrc, checkErr, borrowErrSrc) are shared with the v0.3
// borrow_test.go and typeck_test.go files.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// (A) Method-form desugar: positive cases.
// ---------------------------------------------------------------------------

// Method-form push on a mut list works just like the fn form.
func TestBorrowMethodPushOnMutList(t *testing.T) {
	src := `mut xs := [1, 2]
xs.push(3)
print xs[0]
`
	checkSrc(t, src)
}

// Method-form clone observes its receiver — caller retains ownership.
func TestBorrowMethodCloneDoesNotMove(t *testing.T) {
	src := `xs := [1, 2]
ys := xs.clone()
print xs[0]
print ys[0]
`
	checkSrc(t, src)
}

// Method-form len returns the length without moving the receiver.
func TestBorrowMethodLenDoesNotMove(t *testing.T) {
	src := `xs := [1, 2]
print xs.len()
print xs[0]
`
	checkSrc(t, src)
}

// Fn form still works — regression for v0.3 path.
func TestBorrowFnPushStillWorks(t *testing.T) {
	src := `mut xs := [1, 2]
push(xs, 3)
print xs[0]
`
	checkSrc(t, src)
}

// LoweredCall lookup verifies the synthetic CallExpr is wired up by typeck.
func TestCheckMethodPushSetsLoweredCall(t *testing.T) {
	prog := checkSrc(t, "mut xs := [1, 2]\nxs.push(3)\n")
	// Statement 1 is `xs.push(3)`, an ExprStmt wrapping a MethodCallExpr.
	es, ok := prog.Statements[1].(*ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", prog.Statements[1])
	}
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expected MethodCallExpr, got %T", es.Expr)
	}
	if mc.LoweredCall == nil {
		t.Fatalf("MethodCallExpr.LoweredCall not set on method-form push")
	}
	id, ok := mc.LoweredCall.Callee.(*IdentExpr)
	if !ok || id.Name != "push" {
		t.Fatalf("LoweredCall.Callee = %#v, want IdentExpr 'push'", mc.LoweredCall.Callee)
	}
	if len(mc.LoweredCall.Args) != 2 {
		t.Fatalf("LoweredCall.Args has %d, want 2", len(mc.LoweredCall.Args))
	}
}

// ---------------------------------------------------------------------------
// (A) Method-form desugar: negative cases.
// ---------------------------------------------------------------------------

// Method form on a let-bound list rejects with the same "must be mut" rule
// the fn form enforces.
func TestCheckMethodPushOnLetRejected(t *testing.T) {
	src := `xs := [1, 2]
xs.push(3)
`
	checkErr(t, src, "must be mut")
}

// Method form push on the iterable of a for-iter rejects: the iterable is
// BorrowedShared for the body's duration.
func TestBorrowMethodPushOnForIterableRejected(t *testing.T) {
	src := `mut xs := [1, 2]
for x in xs {
xs.push(3)
}
`
	borrowErrSrc(t, src, "borrowed")
}

// Method form clone with a value type that's not a composite is still
// rejected by the underlying checkCloneCall — primitive receivers reach the
// "method does not exist on int" path because list[T] dispatch is the only
// path that lowers to clone.
func TestCheckMethodCloneOnPrimitiveNotLowered(t *testing.T) {
	checkErr(t, "x := 5\nprint x.clone()\n", "method \"clone\" does not exist on int")
}

// ---------------------------------------------------------------------------
// (B) `this` inside method bodies: positive cases.
// ---------------------------------------------------------------------------

// Reading `this.count` inside a method body is a field read; receiver stays
// BorrowedShared throughout. Single-statement method bodies are used in this
// file because the parser does not yet admit multi-statement impl method
// bodies — that's an orthogonal issue tracked separately from Unit 5.
func TestBorrowThisFieldReadOK(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn show() -> int { return this.count }
}
c := Counter { count: 7 }
print c.show()
`
	checkSrc(t, src)
}

// A method that doesn't touch `this` is fine — verifies that registering
// `this` in the body scope doesn't introduce false positives for unused
// receivers.
func TestBorrowThisUnusedOK(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn answer() -> int { return 42 }
}
c := Counter { count: 0 }
print c.answer()
`
	checkSrc(t, src)
}

// Spec impl: reading `this.count` through a spec-typed receiver works.
func TestBorrowSpecImplThisFieldReadOK(t *testing.T) {
	src := `spec Doublable { fn doubled() -> int }
struct Counter { count: int }
impl Counter for Doublable {
fn doubled() -> int { return this.count * 2 }
}
c := Counter { count: 4 }
print c.doubled()
`
	checkSrc(t, src)
}

// Spec default body: `this` available, type is the spec type. The default
// body never references `this`, so the borrow checker should not produce
// false positives.
func TestBorrowSpecDefaultBodyThisOK(t *testing.T) {
	src := `spec Hashable {
fn hash() -> int { return 0 }
}
struct K { tag: int }
impl K for Hashable {}
k := K { tag: 1 }
print k.hash()
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// (B) `this` inside method bodies: negative cases.
// ---------------------------------------------------------------------------

// `return this` inside a method body rejects with the move-of-borrowed
// diagnostic shape used in v0.3. Method bodies in tests use single
// statements because the parser does not yet admit multi-statement impl
// method bodies — that's an orthogonal issue tracked separately.
func TestBorrowReturnThisRejected(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn give() -> Counter { return this }
}
c := Counter { count: 0 }
print c.give().count
`
	borrowErrSrc(t, src, `cannot move borrowed value: "this"`)
}

// Storing `this` inside a returned list literal is also a move site.
func TestBorrowListLitFromThisRejected(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn pack() -> list[Counter] { return [this] }
}
c := Counter { count: 0 }
print c.pack()
`
	borrowErrSrc(t, src, `cannot move borrowed value: "this"`)
}

// A let-binding from `this` inside a method body rejects. The let-rebind
// rule is the same as for any other BorrowedShared name: the consume site
// fires on ThisExpr.
func TestBorrowLetFromThisRejected(t *testing.T) {
	// We use a let-stmt at file scope with `this` as the RHS — this hits the
	// "this outside method body" typeck check first, NOT borrow check. So
	// instead, exercise let-from-this via an ExprStmt path: a struct literal
	// whose field initializer reads `this` consumes the borrow.
	src := `struct Counter { count: int }
struct Pair { c: Counter }
impl Counter {
fn pair() -> Pair { return Pair { c: this } }
}
c := Counter { count: 0 }
print c.pair().c.count
`
	borrowErrSrc(t, src, `cannot move borrowed value: "this"`)
}

// Spec-typed binding constructed from a concrete-typed binding moves the
// source. After `p: Printable = c`, c is Moved; reading c.count rejects.
func TestBorrowSpecBindMovesSource(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str { return "x" }
}
c := Counter { count: 1 }
p: Printable = c
print c.count
`
	borrowErrSrc(t, src, "use of moved value")
}
