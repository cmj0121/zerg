// v0.6 Unit 7 codegen tests — generic monomorphisation + null-safety
// operators (?, ??, ?., nil) lowered to C.
//
// What's covered:
//
//   * Generic struct: `Box[int]` emits one C struct + helpers; two uses of
//     the same instance dedupe to one shape.
//   * Generic struct with two distinct args (`Box[int]`, `Box[str]`) emits
//     two distinct shapes with mangled names.
//   * Generic fn: `id[T](x: T)` called with int and str emits two C fns
//     with distinct mangled names.
//   * Built-in Option / Result Some / None / Ok / Err construction at
//     stdout matches PLAN.md print parity (no bracketed type-arg suffix).
//   * `?` propagation on Option in fn returning Option lowers to early-
//     return None when the inner is None.
//   * `?` propagation on Result with the same E lowers to early-return
//     Err when the inner is Err; carries the same E payload.
//   * `??` lowers to a conditional that picks the LHS Some payload or
//     evaluates the RHS only on None.
//   * `?.` chains: `obj?.field` returns Option of the field type; chained
//     `?.` carries None forward.
//   * `nil` in a `x: int? = nil` lowers to None of the contextual
//     Option type.
//   * Print suppresses the `[T]` suffix in the variant name.
//   * Codegen size guard: emitted C source ≤ 50× source bytes on a v0.6
//     program.

package build

import (
	"strings"
	"testing"
)

// --- generic struct emission ---------------------------------------------

func TestV06CgenGenericStructEmitsOneShape(t *testing.T) {
	src := `struct Box[T] { value: T }
b: Box[int] = Box { value: 7 }
print b.value
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	want := "zerg_struct_" + mm + "__Box__int"
	if !strings.Contains(out, want) {
		t.Errorf("expected mangled struct name %q in output; got:\n%s", want, out)
	}
	// Must not emit a "Box[int]" shape — bracket characters are invalid C.
	if strings.Contains(out, "Box[int]") {
		t.Errorf("emitted output should not contain raw `Box[int]` Name suffix; got:\n%s", out)
	}
}

func TestV06CgenGenericStructDeduplicates(t *testing.T) {
	// Two `Box[int]` uses must dedupe to a single C struct definition.
	src := `struct Box[T] { value: T }
a: Box[int] = Box { value: 1 }
b: Box[int] = Box { value: 2 }
print a.value
print b.value
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	want := "struct zerg_struct_" + mm + "__Box__int {"
	count := strings.Count(out, want)
	if count != 1 {
		t.Errorf("expected exactly 1 definition of %q, got %d in output:\n%s", want, count, out)
	}
}

func TestV06CgenGenericStructTwoArgsDistinct(t *testing.T) {
	// Box[int] and Box[str] produce two distinct mangled shapes.
	src := `struct Box[T] { value: T }
a: Box[int] = Box { value: 7 }
b: Box[str] = Box { value: "hi" }
print a.value
print b.value
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	want1 := "zerg_struct_" + mm + "__Box__int"
	want2 := "zerg_struct_" + mm + "__Box__str"
	if !strings.Contains(out, want1) {
		t.Errorf("missing Box__int mangle; got:\n%s", out)
	}
	if !strings.Contains(out, want2) {
		t.Errorf("missing Box__str mangle; got:\n%s", out)
	}
}

func TestV06CgenGenericStructRuns(t *testing.T) {
	src := `struct Box[T] { value: T }
b: Box[int] = Box { value: 42 }
print b.value
`
	expectStdout(t, src, "42\n")
}

// --- generic fn emission --------------------------------------------------

func TestV06CgenGenericFnTwoInstancesEmitDistinctSymbols(t *testing.T) {
	src := `fn id[T](x: T) -> T { return x }
a := id(7)
b := id("hi")
print a
print b
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	want1 := "z_" + mm + "__id__int"
	want2 := "z_" + mm + "__id__str"
	if !strings.Contains(out, want1) {
		t.Errorf("missing id__int specialisation; got:\n%s", out)
	}
	if !strings.Contains(out, want2) {
		t.Errorf("missing id__str specialisation; got:\n%s", out)
	}
}

func TestV06CgenGenericFnRuns(t *testing.T) {
	src := `fn id[T](x: T) -> T { return x }
a := id(7)
b := id("hi")
print a
print b
`
	expectStdout(t, src, "7\nhi\n")
}

func TestV06CgenGenericFnSameTypeDedupes(t *testing.T) {
	// Two id(int) calls must share the specialised symbol.
	src := `fn id[T](x: T) -> T { return x }
a := id(1)
b := id(2)
print a
print b
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	sig := "static int64_t z_" + mm + "__id__int("
	count := strings.Count(out, sig)
	// One forward decl + one body = 2.
	if count != 2 {
		t.Errorf("expected exactly 2 occurrences (decl + body) of %q, got %d:\n%s", sig, count, out)
	}
}

// --- Option / Result construction ----------------------------------------

func TestV06CgenOptionSomePrint(t *testing.T) {
	src := `x: int? = 7
print x
`
	expectStdout(t, src, "7\n")
}

func TestV06CgenOptionNonePrint(t *testing.T) {
	src := `x: int? = nil
print x
`
	expectStdout(t, src, "nil\n")
}

func TestV06CgenResultOkPrint(t *testing.T) {
	src := `x: Result[int, str] = Result.Ok(42)
print x
`
	expectStdout(t, src, "Result.Ok(42)\n")
}

func TestV06CgenResultErrPrint(t *testing.T) {
	src := `x: Result[int, str] = Result.Err("oops")
print x
`
	expectStdout(t, src, "Result.Err(oops)\n")
}

func TestV06CgenOptionMangleIsBuiltin(t *testing.T) {
	src := `x: int? = 7
print x
`
	out := mustEmit(t, src)
	want := "zerg_enum_zerg_builtin__Option__int"
	if !strings.Contains(out, want) {
		t.Errorf("expected built-in mangle %q in output; got:\n%s", want, out)
	}
}

func TestV06CgenResultMangleIsBuiltin(t *testing.T) {
	src := `x: Result[int, str] = Result.Ok(1)
print x
`
	out := mustEmit(t, src)
	want := "zerg_enum_zerg_builtin__Result__int_str"
	if !strings.Contains(out, want) {
		t.Errorf("expected built-in mangle %q in output; got:\n%s", want, out)
	}
}

// --- ? propagation --------------------------------------------------------

func TestV06CgenPropagateOption(t *testing.T) {
	src := `fn first(xs: list[int?]) -> int? {
    x := xs[0]
    return x? + 1
}
xs: list[int?] = [10]
r := first(xs)
print r
`
	expectStdout(t, src, "11\n")
}

func TestV06CgenPropagateOptionNoneEarlyReturns(t *testing.T) {
	src := `fn first(xs: list[int?]) -> int? {
    x := xs[0]
    return x? + 1
}
xs: list[int?] = [nil]
r := first(xs)
print r
`
	expectStdout(t, src, "nil\n")
}

func TestV06CgenPropagateResult(t *testing.T) {
	src := `fn unwrap_inc() -> Result[int, str] {
    r: Result[int, str] = Result.Ok(10)
    return Result.Ok(r? + 1)
}
fn unwrap_err() -> Result[int, str] {
    r: Result[int, str] = Result.Err("nope")
    return Result.Ok(r? + 1)
}
print unwrap_inc()
print unwrap_err()
`
	expectStdout(t, src, "Result.Ok(11)\nResult.Err(nope)\n")
}

// --- ?? coalesce ----------------------------------------------------------

func TestV06CgenCoalesceOptionSome(t *testing.T) {
	src := `x: int? = 7
v := x ?? 0
print v
`
	expectStdout(t, src, "7\n")
}

func TestV06CgenCoalesceOptionNone(t *testing.T) {
	src := `x: int? = nil
v := x ?? 99
print v
`
	expectStdout(t, src, "99\n")
}

func TestV06CgenCoalesceResultErr(t *testing.T) {
	src := `x: Result[int, str] = Result.Err("bad")
v := x ?? -1
print v
`
	expectStdout(t, src, "-1\n")
}

// --- ?. safe navigation ---------------------------------------------------

func TestV06CgenSafeFieldSome(t *testing.T) {
	src := `struct P { x: int }
p: P? = P { x: 7 }
v := p?.x
print v
`
	expectStdout(t, src, "7\n")
}

func TestV06CgenSafeFieldNone(t *testing.T) {
	src := `struct P { x: int }
p: P? = nil
v := p?.x
print v
`
	expectStdout(t, src, "nil\n")
}

func TestV06CgenSafeFieldChain(t *testing.T) {
	src := `struct A { b: B }
struct B { c: int }
a: A? = A { b: B { c: 42 } }
v := a?.b
print v
`
	// v: B? = Some(B{c:42})
	expectStdout(t, src, "B { c: 42 }\n")
}

// --- nil literal ----------------------------------------------------------

func TestV06CgenNilInLetWithAnnotation(t *testing.T) {
	src := `x: int? = nil
print x
`
	expectStdout(t, src, "nil\n")
}

func TestV06CgenNilInListLiteral(t *testing.T) {
	src := `xs: list[int?] = [1, nil, 3]
for x in xs {
    print x
}
`
	expectStdout(t, src, "1\nnil\n3\n")
}

// --- print parity: nullable suppresses constructor entirely -------------

func TestV06CgenPrintSuppressesBracketSuffix(t *testing.T) {
	// A nullable value prints its inner payload directly (or `nil` for the
	// absent case). The previous parity assertion (bracket suffix stripped
	// from `Option[int].Some(7)` → `Option.Some(7)`) is moot because
	// `Option` no longer appears in stdout at all.
	src := `x: int? = 7
print x
`
	out := mustEmit(t, src)
	// No bracketed instance name in any fputs literal.
	if strings.Contains(out, `"int?`) {
		t.Errorf("emitted print path should not include `int?` literal; got:\n%s", out)
	}
	// No `Option.Some(` in any fputs literal — the print path emits the
	// bare payload value, not the qualified variant constructor.
	if strings.Contains(out, `"Option.Some(`) {
		t.Errorf("emitted print path leaks `Option.Some(`; got:\n%s", out)
	}
	if strings.Contains(out, `"Option.None`) {
		t.Errorf("emitted print path leaks `Option.None`; got:\n%s", out)
	}
}

// --- T → T? lift symmetry -------------------------------------------------

func TestV06CgenSymmetricLiftAtLetInit(t *testing.T) {
	// `x: int? = 7` lifts 7 to Some(7) via typeck's synthetic EnumLit.
	src := `x: int? = 7
print x
`
	expectStdout(t, src, "7\n")
}

// --- nested generic instance ---------------------------------------------

func TestV06CgenNestedGenericInstance(t *testing.T) {
	// Option as a user-visible type name is rejected; a nested nullable
	// holds a Result[T, E] payload via the `T?` spelling. Mangle still goes
	// through the `Option[Result[int,str]]` canonical name internally —
	// that's the cache key, not user syntax.
	src := `r: Result[int, str] = Result.Ok(7)
x: Result[int, str]? = r
print x
`
	out := mustEmit(t, src)
	want := "zerg_enum_zerg_builtin__Option__Result__int_str"
	if !strings.Contains(out, want) {
		t.Errorf("expected nested mangle %q; got:\n%s", want, out)
	}
	expectStdout(t, src, "Result.Ok(7)\n")
}

// --- generic struct equality / clone -------------------------------------

func TestV06CgenGenericStructEqHelper(t *testing.T) {
	// `==` on two Box[int] values exercises the per-shape _eq helper.
	src := `struct Box[T] { value: T }
a: Box[int] = Box { value: 7 }
b: Box[int] = Box { value: 7 }
print a == b
`
	expectStdout(t, src, "true\n")
}

func TestV06CgenGenericStructCloneHelper(t *testing.T) {
	// clone() on a Box[int] exercises the per-shape _copy helper.
	src := `struct Box[T] { value: T }
a: Box[int] = Box { value: 9 }
b := clone(a)
print b.value
`
	expectStdout(t, src, "9\n")
}

// --- cross-module generic fn ---------------------------------------------

func TestV06CgenCrossModuleGenericFn(t *testing.T) {
	// A generic fn defined in one module and called from another must
	// produce one specialised symbol whose owner is the defining module.
	files := map[string]string{
		"util.zg": `pub fn id[T](x: T) -> T { return x }
`,
		"main.zg": `import "util"
a := util.id(7)
b := util.id("hi")
print a
print b
`,
	}
	got, err := buildBundleFromFiles(t, "main.zg", files)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := "7\nhi\n"
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// --- codegen size guard ---------------------------------------------------

// TestV06CgenGenericImplBlockEmits pins Bug 2: a generic impl block
// (`impl[T] Box[T] for Spec`) must surface its methods to cgen so each
// monomorphised receiver gets its own C function. Previously cgen only
// looked up methods by canonical *Type, but generic impls register no
// concrete *Type — dispatch produced "method %q on %s not resolvable".
func TestV06CgenGenericImplBlockEmits(t *testing.T) {
	src := `spec Tagged { fn tag() -> str }
struct Box[T] { value: T }
impl[T] Box[T] for Tagged { fn tag() -> str { return "Box" } }
bi: Box[int] = Box { value: 7 }
bs: Box[str] = Box { value: "hi" }
print bi.tag()
print bs.tag()
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	wantInt := "zerg_struct_" + mm + "__Box__int__" + mm + "__Tagged__tag"
	wantStr := "zerg_struct_" + mm + "__Box__str__" + mm + "__Tagged__tag"
	if !strings.Contains(out, wantInt) {
		t.Errorf("missing Box[int] tag method symbol %q in:\n%s", wantInt, out)
	}
	if !strings.Contains(out, wantStr) {
		t.Errorf("missing Box[str] tag method symbol %q in:\n%s", wantStr, out)
	}
}

// TestV06CgenGenericImplBlockBodyClonedPerInstance exercises the type-stomp
// regression that drove Bug 2's fix: when an impl method's signature
// references the impl-level type-param (e.g. `fn unwrap() -> T`), the C
// function for Box[int] must return int64_t and for Box[str] must return
// zerg_str. A shared TypeRef.Resolved would emit the same return type for
// both instances and the C compiler would reject.
func TestV06CgenGenericImplBlockBodyClonedPerInstance(t *testing.T) {
	src := `struct Box[T] { value: T }
impl[T] Box[T] { fn unwrap() -> T { return this.value } }
bi: Box[int] = Box { value: 5 }
bs: Box[str] = Box { value: "hi" }
print bi.unwrap()
print bs.unwrap()
`
	out := mustEmit(t, src)
	mm := mangleModule("main")
	wantInt := "static int64_t zerg_struct_" + mm + "__Box__int__unwrap"
	wantStr := "static zerg_str zerg_struct_" + mm + "__Box__str__unwrap"
	if !strings.Contains(out, wantInt) {
		t.Errorf("missing int64_t-returning unwrap %q in:\n%s", wantInt, out)
	}
	if !strings.Contains(out, wantStr) {
		t.Errorf("missing zerg_str-returning unwrap %q in:\n%s", wantStr, out)
	}
}

// TestV06CgenSizeGuard pins the tenth-man rule from PLAN.md §Codegen size
// guard: emitted C source must not exceed a fixed 50× ratio of the input
// source bytes for a representative v0.6 program. The ratio bounds catch
// future regressions where a corpus addition fans out per-shape helpers
// quadratically.
func TestV06CgenSizeGuard(t *testing.T) {
	src := `struct Box[T] { value: T }
fn id[T](x: T) -> T { return x }
a: Box[int] = Box { value: id(7) }
b: Box[str] = Box { value: id("hi") }
r: Result[int, str] = Result.Ok(id(42))
ninety_nine := id(99)
o: int? = ninety_nine
print a.value
print b.value
print r
print o
`
	out := mustEmit(t, src)
	srcLen := len(src)
	emitLen := len(out)
	// Ratio bumps when the always-emitted runtime gains a small helper.
	// v0.11 bumped from 50 → 55 when `let` was retired from binding
	// statements (representative sources shrank ~10%). v0.14 bumps
	// 55 → 60 with the addition of zerg_panic to the always-emitted
	// runtime block. The intent is sanity-check, not bloat.
	const ratio = 60
	if emitLen > srcLen*ratio {
		t.Errorf("emitted size %d exceeds %d× source size %d (%d > %d)",
			emitLen, ratio, srcLen, emitLen, srcLen*ratio)
	}
}
