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
let a := id(7)
let b := id("hello")
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
let xs := wrap(7)
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
let b: Box[int] = Box { value: 7 }
print b
`
	expectOK(t, src, "Box { value: 7 }\n")
}

func TestRunV06GenericStructFieldAccess(t *testing.T) {
	src := `struct Box[T] { value: T }
let b: Box[int] = Box { value: 42 }
print b.value
`
	expectOK(t, src, "42\n")
}

func TestRunV06GenericStructPolyValueTypes(t *testing.T) {
	src := `struct Box[T] { value: T }
let bi: Box[int] = Box { value: 1 }
let bs: Box[str] = Box { value: "hi" }
print bi
print bs
`
	expectOK(t, src, "Box { value: 1 }\nBox { value: hi }\n")
}

// ---------------------------------------------------------------------------
// Built-in Option construction.
// ---------------------------------------------------------------------------

func TestRunV06OptionSomeInt(t *testing.T) {
	src := `let x: Option[int] = Option.Some(7)
print x
`
	expectOK(t, src, "Option.Some(7)\n")
}

func TestRunV06OptionSomeStr(t *testing.T) {
	src := `let x: Option[str] = Option.Some("ok")
print x
`
	expectOK(t, src, "Option.Some(ok)\n")
}

func TestRunV06OptionNone(t *testing.T) {
	src := `let x: Option[int] = Option.None
print x
`
	expectOK(t, src, "Option.None\n")
}

// ---------------------------------------------------------------------------
// Built-in Result construction.
// ---------------------------------------------------------------------------

func TestRunV06ResultOk(t *testing.T) {
	src := `let r: Result[int, str] = Result.Ok(42)
print r
`
	expectOK(t, src, "Result.Ok(42)\n")
}

func TestRunV06ResultErr(t *testing.T) {
	src := `let r: Result[int, str] = Result.Err("oops")
print r
`
	expectOK(t, src, "Result.Err(oops)\n")
}

// ---------------------------------------------------------------------------
// nil literal.
// ---------------------------------------------------------------------------

func TestRunV06NilLetAnnotated(t *testing.T) {
	src := `let x: int? = nil
print x
`
	expectOK(t, src, "Option.None\n")
}

func TestRunV06IntLiftToOptional(t *testing.T) {
	src := `let x: int? = 42
print x
`
	expectOK(t, src, "Option.Some(42)\n")
}

func TestRunV06ListOfOptional(t *testing.T) {
	src := `let xs: list[int?] = [1, nil, 2]
print xs
`
	expectOK(t, src, "[ Option.Some(1), Option.None, Option.Some(2) ]\n")
}

func TestRunV06NilThenCoalesce(t *testing.T) {
	// At top level, nil binds to a let then ?? draws the fallback.
	src := `let a: int? = nil
let b: int? = 7
print a ?? -1
print b ?? -1
`
	expectOK(t, src, "-1\n7\n")
}

// ---------------------------------------------------------------------------
// `?` propagation.
// ---------------------------------------------------------------------------

func TestRunV06PropagateOptionSomePassesThrough(t *testing.T) {
	src := `fn maybe() -> int? { return Option.Some(7) }
fn outer() -> int? {
  let v := maybe()?
  return Option.Some(v + 1)
}
print outer()
`
	expectOK(t, src, "Option.Some(8)\n")
}

func TestRunV06PropagateOptionNoneEarlyReturns(t *testing.T) {
	src := `fn maybe() -> int? { return Option.None }
fn outer() -> int? {
  let v := maybe()?
  return Option.Some(v + 1)
}
print outer()
`
	expectOK(t, src, "Option.None\n")
}

func TestRunV06PropagateResultOkPassesThrough(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Ok(7) }
fn outer() -> Result[int, str] {
  let v := fetch()?
  return Result.Ok(v + 1)
}
print outer()
`
	expectOK(t, src, "Result.Ok(8)\n")
}

func TestRunV06PropagateResultErrEarlyReturns(t *testing.T) {
	src := `fn fetch() -> Result[int, str] { return Result.Err("boom") }
fn outer() -> Result[int, str] {
  let v := fetch()?
  return Result.Ok(v + 1)
}
print outer()
`
	expectOK(t, src, "Result.Err(boom)\n")
}

func TestRunV06PropagateChain(t *testing.T) {
	// `?` short-circuits at the first None in a chain of fn calls.
	src := `fn step_a() -> int? { return Option.Some(1) }
fn step_b() -> int? { return Option.None }
fn outer() -> int? {
  let a := step_a()?
  let b := step_b()?
  return Option.Some(a + b)
}
print outer()
`
	expectOK(t, src, "Option.None\n")
}

func TestRunV06PropagateResultChangingInnerT(t *testing.T) {
	// inner Result[int, str], outer Result[bool, str]: matching E.
	src := `fn fetch() -> Result[int, str] { return Result.Err("nope") }
fn outer() -> Result[bool, str] {
  let n := fetch()?
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
	src := `let x: int? = nil
print x ?? 99
`
	expectOK(t, src, "99\n")
}

func TestRunV06CoalesceSomeTakesInner(t *testing.T) {
	src := `let x: int? = Option.Some(7)
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
let x: int? = Option.Some(5)
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
let x: int? = nil
print x ?? rhs()
`
	expectOK(t, src, "rhs!\n99\n")
}

func TestRunV06CoalesceRightAssocChain(t *testing.T) {
	// a ?? b ?? c is a ?? (b ?? c). All None ⇒ c.
	src := `let a: int? = nil
let b: int? = nil
print a ?? b ?? 7
`
	expectOK(t, src, "7\n")
}

// ---------------------------------------------------------------------------
// `?.` safe-navigation.
// ---------------------------------------------------------------------------

func TestRunV06SafeFieldAccessSome(t *testing.T) {
	src := `struct Box { v: int }
let b: Box? = Box { v: 7 }
print b?.v
`
	expectOK(t, src, "Option.Some(7)\n")
}

func TestRunV06SafeFieldAccessNone(t *testing.T) {
	src := `struct Box { v: int }
let b: Box? = nil
print b?.v
`
	expectOK(t, src, "Option.None\n")
}

func TestRunV06SafeFieldAccessChain(t *testing.T) {
	// Both layers Some ⇒ inner field wrapped in Option.
	src := `struct Inner { v: int }
struct Outer { inner: Inner }
let o: Outer? = Outer { inner: Inner { v: 9 } }
print o?.inner?.v
`
	expectOK(t, src, "Option.Some(9)\n")
}

func TestRunV06SafeFieldAccessChainNoneAtRoot(t *testing.T) {
	src := `struct Inner { v: int }
struct Outer { inner: Inner }
let o: Outer? = nil
print o?.inner?.v
`
	expectOK(t, src, "Option.None\n")
}

func TestRunV06SafeFieldAccessThenCoalesce(t *testing.T) {
	src := `struct Box { v: int }
let b: Box? = nil
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
let b: Box[int] = Box { value: 7 }
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
let bi: Box[int] = Box { value: 1 }
let bs: Box[str] = Box { value: "hi" }
print bi.echo()
print bs.echo()
`
	expectOK(t, src, "boxed\nboxed\n")
}

// ---------------------------------------------------------------------------
// Equality across Option / Result (regression — payload eq already exists).
// ---------------------------------------------------------------------------

func TestRunV06EqOptionSome(t *testing.T) {
	src := `let a: Option[int] = Option.Some(7)
let b: Option[int] = Option.Some(7)
print a == b
`
	expectOK(t, src, "true\n")
}

func TestRunV06EqOptionNone(t *testing.T) {
	src := `let a: Option[int] = Option.None
let b: Option[int] = Option.None
print a == b
`
	expectOK(t, src, "true\n")
}

func TestRunV06NeqOptionSomeVsNone(t *testing.T) {
	src := `let a: Option[int] = Option.Some(7)
let b: Option[int] = Option.None
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
let p: Pair[int, str] = Pair.Both(7, "hi")
print p
`
	expectOK(t, src, "Pair.Both(7, hi)\n")
}

// ---------------------------------------------------------------------------
// Nested Option (Option[Option[T]]).
// ---------------------------------------------------------------------------

func TestRunV06NestedOption(t *testing.T) {
	src := `let x: Option[Option[int]] = Option.Some(Option.Some(7))
print x
`
	expectOK(t, src, "Option.Some(Option.Some(7))\n")
}

func TestRunV06NestedOptionInnerNone(t *testing.T) {
	// `nil` works as the inner None because it inherits its Option[int] type
	// from the surrounding hint (Option.Some expects an Option[int]).
	src := `let inner: Option[int] = Option.None
let x: Option[Option[int]] = Option.Some(inner)
print x
`
	expectOK(t, src, "Option.Some(Option.None)\n")
}

// ---------------------------------------------------------------------------
// Generic fn returning Option/Result.
// ---------------------------------------------------------------------------

func TestRunV06GenericFnReturnsOption(t *testing.T) {
	src := `fn lift[T](x: T) -> T? { return Option.Some(x) }
print lift(7)
print lift("hi")
`
	expectOK(t, src, "Option.Some(7)\nOption.Some(hi)\n")
}

// ---------------------------------------------------------------------------
// Propagate with explicit Option.None construction.
// ---------------------------------------------------------------------------

func TestRunV06PropagateExplicitNoneConstruction(t *testing.T) {
	src := `fn outer() -> int? {
  let x: int? = Option.None
  let v := x?
  return Option.Some(v)
}
print outer()
`
	expectOK(t, src, "Option.None\n")
}
