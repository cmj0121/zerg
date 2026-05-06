package syntax_test

// v0.5 Unit 4 — borrow check across module boundaries.
//
// PLAN.md §Borrow check pins: "No new rules. Cross-module is purely name
// resolution; once a name resolves, the borrow walker treats it as if it
// were a local symbol." This file verifies that the v0.3 + v0.4 borrow
// rules continue to hold when the move/borrow target lives in another
// module — and that no false positives or false negatives arise from a
// cross-module reference passing through name resolution.
//
// Test fixtures use the same writeFixture / loadAndCheck helpers Unit 3
// established in typeck_v05_test.go. We exercise the public CheckBundle
// entry through the loader because that's how the real CLI invokes the
// multi-module check, and CheckBundle re-runs borrowCheck per-module
// after typeck has annotated every Expr.
//
// Scenarios covered:
//
//   Positive (must NOT reject):
//     1. cross-module fn call with primitive args (no move semantics)
//     2. cross-module fn call passing a list (implicit shared borrow;
//        caller retains ownership per v0.3)
//     3. cross-module method call on a struct (receiver shared-borrow
//        per v0.4; binding remains usable after)
//     4. cross-module clone — the foreign call returns a fresh value;
//        the argument is shared-borrowed, the result is owned
//     5. cross-module mutation of a locally-owned mut binding via the
//        list-builtin lowering (push lowers to a synthetic CallExpr;
//        ownership state on the binding is local)
//     6. `this` in a cross-module impl method body — receiver type is
//        the local struct, BorrowedShared during the body
//     7. cross-module enum match destructure — pattern names match the
//        foreign type's variants by name
//
//   Negative (must reject):
//     1. move-then-use across module boundary — let-rebind off a
//        cross-module call result, then use the original
//     2. `return this` from a cross-module spec impl method — the
//        spec lives in another module but the receiver is local; the
//        v0.4 move-of-borrowed rule still fires
//     3. push of a locally-bound mut list whose contents originate from
//        a cross-module call — the binding is owned, push works (this
//        verifies the borrow walker doesn't get confused by the cross-
//        module origin and produce a false rejection)
//     4. clone of a borrowed iterable across modules — inside a for-iter
//        whose iterable is `util.xs()`, calling clone on the same call
//        result returns a fresh owned list; verify the for-iter borrow
//        does not bleed into the loop body's foreign expressions

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Positive cases — must pass borrow check across modules.
// ---------------------------------------------------------------------------

// V05-1: Cross-module fn call with primitive arguments. No move semantics on
// primitives — the call type-checks and the borrow walker has nothing to
// reject because primitive bindings are not tracked.
func TestBorrowV05PositiveCrossModuleFnCallPrimitives(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub fn add(a: int, b: int) -> int { return a + b }\n",
		"main": "import \"util\"\nn := util.add(1, 2)\nprint n\n",
	}, "main.zg")
}

// V05-2: Cross-module fn call passing a list. v0.3 makes fn-call composite
// args implicit shared borrows — caller retains ownership. After the call,
// xs must remain readable.
func TestBorrowV05PositiveCrossModuleFnCallListBorrow(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub fn observe(ys: list[int]) {\nprint ys[0]\n}\n",
		"main": "import \"util\"\nxs := [1, 2, 3]\nutil.observe(xs)\nprint xs[0]\n",
	}, "main.zg")
}

// V05-3: Cross-module method call on a local struct. The struct lives in
// main; its inherent impl method is invoked through `c.show()`. Receiver
// is shared-borrowed per v0.4; c remains usable afterwards.
func TestBorrowV05PositiveCrossModuleMethodCallReceiverBorrow(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Printable {\n" +
		"pub fn to_string() -> int { return this.count }\n" +
		"}\n" +
		"c := Counter { count: 7 }\n" +
		"print c.to_string()\n" +
		"print c.count\n"
	loadOk(t, fixture{
		"util": "pub spec Printable { fn to_string() -> int }\n",
		"main": src,
	}, "main.zg")
}

// V05-4: Cross-module call returns a fresh list. The argument is shared-
// borrowed (so xs stays usable), and the result is owned (so ys can be
// rebound, indexed, etc.).
func TestBorrowV05PositiveCrossModuleCallReturnsOwnedList(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub fn dup(xs: list[int]) -> list[int] { return [0] }\n",
		"main": "import \"util\"\nxs := [1, 2, 3]\nys := util.dup(xs)\nprint xs[0]\nprint ys[0]\n",
	}, "main.zg")
}

// V05-5: Cross-module call result rebound to a local mut binding, then
// mutated via the list-builtin lowering. The push lowers to a synthetic
// CallExpr; ownership state lives on the local binding regardless of
// whether the value originated in another module.
func TestBorrowV05PositiveCrossModuleResultPushable(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"mut xs := util.make()\n" +
		"xs.push(99)\n" +
		"print xs[0]\n"
	loadOk(t, fixture{
		"util": "pub fn make() -> list[int] { return [1, 2, 3] }\n",
		"main": src,
	}, "main.zg")
}

// V05-6: `this` in a cross-module impl method body. The impl is `impl
// Counter for util.Printable`; the receiver type is the local Counter.
// Inside the body, `this` is BorrowedShared with the local struct type;
// reads (this.count) are admitted.
func TestBorrowV05PositiveCrossModuleImplMethodThisRead(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Printable {\n" +
		"pub fn show() -> int { return this.count }\n" +
		"}\n" +
		"c := Counter { count: 4 }\n" +
		"print c.show()\n"
	loadOk(t, fixture{
		"util": "pub spec Printable { fn show() -> int }\n",
		"main": src,
	}, "main.zg")
}

// V05-7: Cross-module enum match destructure. The subject's type comes
// from a cross-module call; pattern matches are by enum-name + variant-
// name. The borrow walker treats the bound payload names as Owned
// primitives (the variant payload is an int).
func TestBorrowV05PositiveCrossModuleMatchDestructure(t *testing.T) {
	utilSrc := "" +
		"pub enum Outcome { Ok(int), Err(int) }\n" +
		"pub fn parse() -> Outcome { return Outcome.Ok(7) }\n"
	mainSrc := "" +
		"import \"util\"\n" +
		"r := util.parse()\n" +
		"match r {\n" +
		"Outcome.Ok(v) => print v\n" +
		"Outcome.Err(e) => print e\n" +
		"}\n"
	loadOk(t, fixture{
		"util": utilSrc,
		"main": mainSrc,
	}, "main.zg")
}

// ---------------------------------------------------------------------------
// Negative cases — must reject across module boundaries with the same
// diagnostic shape used in v0.3 / v0.4 single-module tests.
// ---------------------------------------------------------------------------

// V05-N1: Move-then-use across a module boundary. `let ys := xs` moves xs
// (the v0.3 whole-binding-rebind rule); using xs afterwards rejects with
// "use of moved value". The list originated in a cross-module call; the
// move site is local but the test pins that cross-module call results
// are tracked as Owned (and therefore movable) just like any local list.
func TestBorrowV05NegativeMoveThenUseCrossModuleSource(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"xs := util.make()\n" +
		"ys := xs\n" +
		"print xs[0]\n" +
		"print ys[0]\n"
	loadErr(t, fixture{
		"util": "pub fn make() -> list[int] { return [1, 2, 3] }\n",
		"main": src,
	}, "main.zg", "use of moved value")
}

// V05-N2 baseline: a cross-module-spec impl method body that only reads
// `this.count` is admitted. Pins that the borrow walker doesn't false-
// positive on a cross-module-spec impl when the body is purely
// observational. Companion to V05-N2 below.
func TestBorrowV05PositiveCrossModuleSpecImplFieldReadOK(t *testing.T) {
	utilSrc := "" +
		"pub spec Showable { fn show() -> int }\n"
	mainSrc := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Showable {\n" +
		"pub fn show() -> int { return this.count }\n" +
		"}\n" +
		"c := Counter { count: 0 }\n" +
		"print c.show()\n"
	loadOk(t, fixture{
		"util": utilSrc,
		"main": mainSrc,
	}, "main.zg")
}

// V05-N2: Move-of-borrowed `this` inside a cross-module spec impl method
// body. The spec's method signature lives in util; the impl is in main;
// the receiver type (Counter) is local. Inside the impl method body,
// `this` is BorrowedShared per v0.4 — and the aggregation-consume rule
// rejects moving `this` into a list literal element. This pins that the
// v0.4 borrow rule fires identically when the spec slot is resolved
// through the cross-module import binding.
//
// Mirrors v0.4's TestBorrowListLitFromThisRejected, but with a foreign
// spec.
func TestBorrowV05NegativeMoveBorrowedThisInCrossModuleSpecImpl(t *testing.T) {
	utilSrc := "" +
		"pub spec Wrappable { fn wrap() -> int }\n"
	mainSrc := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Wrappable {\n" +
		"pub fn wrap() -> int { return [this][0].count }\n" +
		"}\n" +
		"c := Counter { count: 0 }\n" +
		"print c.wrap()\n"
	loadErr(t, fixture{
		"util": utilSrc,
		"main": mainSrc,
	}, "main.zg", `cannot move borrowed value: "this"`)
}

// V05-N3: A locally-bound mut list whose contents originate from a cross-
// module call. push must NOT reject — the binding is local-owned, the
// borrow walker should not get confused by the cross-module origin and
// produce a false rejection. (This is a positive verification check;
// "Negative" here means it pins the absence of a false-positive.)
//
// Distinct from V05-5 only in that we also read xs after the push to
// confirm the binding survives the lowering with state intact.
func TestBorrowV05PositiveCrossModuleResultPushSurvivesRead(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"mut ys := util.xs()\n" +
		"ys.push(99)\n" +
		"print ys[0]\n" +
		"print ys[1]\n"
	loadOk(t, fixture{
		"util": "pub fn xs() -> list[int] { return [1, 2, 3] }\n",
		"main": src,
	}, "main.zg")
}

// V05-N4: clone of a fresh foreign-call result inside the body of a for-iter
// whose iterable is also a fresh foreign-call result. Both call results are
// fresh values (not bound names) — neither participates in the for-iter
// shared-borrow because for-iter only borrows a NAMED iterable. The clone
// inside the body is therefore admitted: it observes a fresh list and
// returns a new owned list. This pins the absence of a false-positive
// "cannot mutate while borrowed" diagnostic on the inner clone.
func TestBorrowV05PositiveCrossModuleCloneInsideForIter(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"for x in util.xs() {\n" +
		"copy := util.xs().clone()\n" +
		"print copy[0]\n" +
		"print x\n" +
		"}\n"
	loadOk(t, fixture{
		"util": "pub fn xs() -> list[int] { return [1, 2, 3] }\n",
		"main": src,
	}, "main.zg")
}

// V05-N4b: The companion negative — moving the for-iter ITERABLE binding
// inside the body rejects with the borrow-of-borrowed diagnostic. The
// iterable is `xs` (local mut list seeded from a cross-module call), which
// the for-iter shared-borrows for the body's duration. Calling push on it
// inside the body must reject.
func TestBorrowV05NegativeMutateForIterIterableOfCrossModuleOrigin(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"mut xs := util.xs()\n" +
		"for x in xs {\n" +
		"xs.push(99)\n" +
		"print x\n" +
		"}\n"
	loadErr(t, fixture{
		"util": "pub fn xs() -> list[int] { return [1, 2, 3] }\n",
		"main": src,
	}, "main.zg", "borrowed")
}
