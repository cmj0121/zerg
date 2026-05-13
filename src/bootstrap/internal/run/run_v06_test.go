package run

import (
	"testing"
)

// v0.6 Unit 6 — interpreter for generics + null-safety.
//
// Each test exercises one execution path. Mirrors the v0.4 / v0.5 conventions
// (expectOK / expectErr from run_test.go). The corpus in test/v0_6/ (Unit 8)
// covers larger end-to-end programs; here we keep one rule per test.

// ---------------------------------------------------------------------------
// Generic fns — both T = int and T = str share one decl, body executes per
// value type.
// ---------------------------------------------------------------------------

func TestRunV06GenericIdentityIntStr(t *testing.T) {
	src := `fn id[T](x: T) -> T { return x }
a := id(7)
b := id("hello")
print a
print b
`
	expectOK(t, src, "7\nhello\n")
}

func TestRunV06GenericIdentityCalledTwiceSameT(t *testing.T) {
	src := `fn id[T](x: T) -> T { return x }
print id(1)
print id(2)
print id(3)
`
	expectOK(t, src, "1\n2\n3\n")
}

func TestRunV06GenericFnReturnsList(t *testing.T) {
	src := `fn wrap[T](x: T) -> list[T] { return [x] }
xs := wrap(7)
print xs
`
	expectOK(t, src, "[ 7 ]\n")
}

// ---------------------------------------------------------------------------
// Generic structs — Box[int] constructed and printed; bracketed type-arg is
// suppressed in stdout per PLAN.md §Print parity.
// ---------------------------------------------------------------------------

func TestRunV06GenericStructConstruct(t *testing.T) {
	src := `struct Box[T] { value: T }
b: Box[int] = Box { value: 7 }
print b
`
	expectOK(t, src, "Box { value: 7 }\n")
}

func TestRunV06GenericStructFieldAccess(t *testing.T) {
	src := `struct Box[T] { value: T }
b: Box[int] = Box { value: 42 }
print b.value
`
	expectOK(t, src, "42\n")
}

func TestRunV06GenericStructPolyValueTypes(t *testing.T) {
	src := `struct Box[T] { value: T }
bi: Box[int] = Box { value: 1 }
bs: Box[str] = Box { value: "hi" }
print bi
print bs
`
	expectOK(t, src, "Box { value: 1 }\nBox { value: hi }\n")
}

// ---------------------------------------------------------------------------
// Built-in Option construction.
// ---------------------------------------------------------------------------

func TestRunV06OptionSomeInt(t *testing.T) {
	src := `x: int? = 7
print x
`
	expectOK(t, src, "7\n")
}

func TestRunV06OptionSomeStr(t *testing.T) {
	src := `x: str? = "ok"
print x
`
	expectOK(t, src, "ok\n")
}

func TestRunV06OptionNone(t *testing.T) {
	src := `x: int? = nil
print x
`
	expectOK(t, src, "nil\n")
}

// ---------------------------------------------------------------------------
// Built-in Result construction.
// ---------------------------------------------------------------------------

func TestRunV06ResultOk(t *testing.T) {
	src := `r: Result[int, str] = Result.Ok(42)
print r
`
	expectOK(t, src, "Result.Ok(42)\n")
}

func TestRunV06ResultErr(t *testing.T) {
	src := `r: Result[int, str] = Result.Err("oops")
print r
`
	expectOK(t, src, "Result.Err(oops)\n")
}

// ---------------------------------------------------------------------------
// nil literal.
// ---------------------------------------------------------------------------

func TestRunV06NilLetAnnotated(t *testing.T) {
	src := `x: int? = nil
print x
`
	expectOK(t, src, "nil\n")
}

func TestRunV06IntLiftToOptional(t *testing.T) {
	src := `x: int? = 42
print x
`
	expectOK(t, src, "42\n")
}

func TestRunV06ListOfOptional(t *testing.T) {
	src := `xs: list[int?] = [1, nil, 2]
print xs
`
	expectOK(t, src, "[ 1, nil, 2 ]\n")
}

func TestRunV06NilThenCoalesce(t *testing.T) {
	// At top level, nil binds to a binding then ?? draws the fallback.
	src := `a: int? = nil
b: int? = 7
print a ?? -1
print b ?? -1
`
	expectOK(t, src, "-1\n7\n")
}

// ---------------------------------------------------------------------------
// `?` propagation.
// ---------------------------------------------------------------------------

func TestRunV06PropagateOptionSomePassesThrough(t *testing.T) {
	src := `fn maybe() -> int? { return 7 }
fn outer() -> int? {
  v := maybe()?
  return v + 1
}
print outer()
`
	expectOK(t, src, "8\n")
}

func TestRunV06PropagateOptionNoneEarlyReturns(t *testing.T) {
	src := `fn maybe() -> int? { return nil }
fn outer() -> int? {
  v := maybe()?
  return v + 1
}
print outer()
`
	expectOK(t, src, "nil\n")
}

func TestRunV06PropagateResultOkPassesThrough(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Ok(7) }
fn outer() -> Result[int, str] {
  v := fetch()?
  return Result.Ok(v + 1)
}
print outer()
`
	expectOK(t, src, "Result.Ok(8)\n")
}

func TestRunV06PropagateResultErrEarlyReturns(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Err("boom") }
fn outer() -> Result[int, str] {
  v := fetch()?
  return Result.Ok(v + 1)
}
print outer()
`
	expectOK(t, src, "Result.Err(boom)\n")
}

func TestRunV06PropagateChain(t *testing.T) {
	// `?` short-circuits at the first None in a chain of fn calls.
	src := `fn step_a() -> int? { return 1 }
fn step_b() -> int? { return nil }
fn outer() -> int? {
  a := step_a()?
  b := step_b()?
  return a + b
}
print outer()
`
	expectOK(t, src, "nil\n")
}

func TestRunV06PropagateResultChangingInnerT(t *testing.T) {
	// inner Result[int, str], outer Result[bool, str]: matching E.
	src := `fn fetch() -> Result[int, str] { return Result.Err("nope") }
fn outer() -> Result[bool, str] {
  n := fetch()?
  return Result.Ok(n > 0)
}
print outer()
`
	expectOK(t, src, "Result.Err(nope)\n")
}

// ---------------------------------------------------------------------------
// `??` nil-coalesce.
// ---------------------------------------------------------------------------

func TestRunV06CoalesceNoneTakesRhs(t *testing.T) {
	src := `x: int? = nil
print x ?? 99
`
	expectOK(t, src, "99\n")
}

func TestRunV06CoalesceSomeTakesInner(t *testing.T) {
	src := `x: int? = 7
print x ?? 99
`
	expectOK(t, src, "7\n")
}

func TestRunV06CoalesceErrTakesRhs(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Err("oops") }
print fetch() ?? -1
`
	expectOK(t, src, "-1\n")
}

func TestRunV06CoalesceOkTakesInner(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Ok(7) }
print fetch() ?? -1
`
	expectOK(t, src, "7\n")
}

func TestRunV06CoalesceRhsNotEvaluatedOnSome(t *testing.T) {
	// Side-effect proof: rhs is a fn that prints. On Some, rhs must not run.
	src := `fn rhs() -> int {
  print "rhs!"
  return 0
}
x: int? = 5
print x ?? rhs()
`
	expectOK(t, src, "5\n")
}

func TestRunV06CoalesceRhsEvaluatedOnNone(t *testing.T) {
	// On None, rhs runs once.
	src := `fn rhs() -> int {
  print "rhs!"
  return 99
}
x: int? = nil
print x ?? rhs()
`
	expectOK(t, src, "rhs!\n99\n")
}

func TestRunV06CoalesceRightAssocChain(t *testing.T) {
	// a ?? b ?? c is a ?? (b ?? c). All None ⇒ c.
	src := `a: int? = nil
b: int? = nil
print a ?? b ?? 7
`
	expectOK(t, src, "7\n")
}

// ---------------------------------------------------------------------------
// `?.` safe-navigation.
// ---------------------------------------------------------------------------

func TestRunV06SafeFieldAccessSome(t *testing.T) {
	src := `struct Box { v: int }
b: Box? = Box { v: 7 }
print b?.v
`
	expectOK(t, src, "7\n")
}

func TestRunV06SafeFieldAccessNone(t *testing.T) {
	src := `struct Box { v: int }
b: Box? = nil
print b?.v
`
	expectOK(t, src, "nil\n")
}

func TestRunV06SafeFieldAccessChain(t *testing.T) {
	// Both layers Some ⇒ inner field wrapped in Option.
	src := `struct Inner { v: int }
struct Outer { inner: Inner }
o: Outer? = Outer { inner: Inner { v: 9 } }
print o?.inner?.v
`
	expectOK(t, src, "9\n")
}

func TestRunV06SafeFieldAccessChainNoneAtRoot(t *testing.T) {
	src := `struct Inner { v: int }
struct Outer { inner: Inner }
o: Outer? = nil
print o?.inner?.v
`
	expectOK(t, src, "nil\n")
}

func TestRunV06SafeFieldAccessThenCoalesce(t *testing.T) {
	src := `struct Box { v: int }
b: Box? = nil
print b?.v ?? -1
`
	expectOK(t, src, "-1\n")
}

// ---------------------------------------------------------------------------
// Generic spec impl + method dispatch on a monomorphized generic struct.
// ---------------------------------------------------------------------------

func TestRunV06GenericImplBoxForPrintable(t *testing.T) {
	src := `spec Tagged { fn tag() -> str }
struct Box[T] { value: T }
impl[T] Box[T] for Tagged {
  fn tag() -> str { return "box" }
}
b: Box[int] = Box { value: 7 }
print b.tag()
`
	expectOK(t, src, "box\n")
}

func TestRunV06GenericImplDispatchPerInstance(t *testing.T) {
	// Two specialisations of Box, each calling its own inherent method.
	src := `struct Box[T] { value: T }
impl[T] Box[T] {
  fn echo() -> str { return "boxed" }
}
bi: Box[int] = Box { value: 1 }
bs: Box[str] = Box { value: "hi" }
print bi.echo()
print bs.echo()
`
	expectOK(t, src, "boxed\nboxed\n")
}

// ---------------------------------------------------------------------------
// Equality across Option / Result (regression — payload eq already exists).
// ---------------------------------------------------------------------------

func TestRunV06EqOptionSome(t *testing.T) {
	src := `a: int? = 7
b: int? = 7
print a == b
`
	expectOK(t, src, "true\n")
}

func TestRunV06EqOptionNone(t *testing.T) {
	src := `a: int? = nil
b: int? = nil
print a == b
`
	expectOK(t, src, "true\n")
}

func TestRunV06NeqOptionSomeVsNone(t *testing.T) {
	src := `a: int? = 7
b: int? = nil
print a == b
`
	expectOK(t, src, "false\n")
}

// (Match on Option / Result requires typeck pattern handling beyond Unit 6's
// scope. The interpreter's existing EnumPat path already keys patterns by
// VariantName so when typeck wires bare-name patterns through to the
// monomorphized canonical *Type, runtime dispatch will Just Work.)

// ---------------------------------------------------------------------------
// Generic enum (user-defined).
// ---------------------------------------------------------------------------

func TestRunV06UserGenericEnum(t *testing.T) {
	src := `enum Pair[T, U] { Both(T, U), Left(T) }
p: Pair[int, str] = Pair.Both(7, "hi")
print p
`
	expectOK(t, src, "Pair.Both(7, hi)\n")
}

// ---------------------------------------------------------------------------
// Nested Option (Option[Option[T]]).
// ---------------------------------------------------------------------------

// Nested nullables (`int??` / `Option[int?]`) are no longer expressible —
// `T?` is the only spellable nullable form and `??` in type position rejects.
// The two tests previously covering nested Option construction
// (TestRunV06NestedOption, TestRunV06NestedOptionInnerNone) retired here;
// the Result-nested counterparts still pin the algebraic case (Result keeps
// the qualified `Result[T, E]` spelling).

// ---------------------------------------------------------------------------
// Generic fn returning Option/Result.
// ---------------------------------------------------------------------------

func TestRunV06GenericFnReturnsOption(t *testing.T) {
	src := `fn lift[T](x: T) -> T? { return x }
print lift(7)
print lift("hi")
`
	expectOK(t, src, "7\nhi\n")
}

// ---------------------------------------------------------------------------
// Propagate with explicit Option.None construction.
// ---------------------------------------------------------------------------

func TestRunV06PropagateExplicitNoneConstruction(t *testing.T) {
	src := `fn outer() -> int? {
  x: int? = nil
  v := x?
  return v
}
print outer()
`
	expectOK(t, src, "nil\n")
}
