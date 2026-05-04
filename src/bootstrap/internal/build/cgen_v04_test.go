package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 codegen tests — Unit 7 deliverables.
//
// What's covered:
//
//   * Inherent method emission (mangled name, receiver-by-value param).
//   * Spec impl method emission + per-(Type, Spec) vtable initialiser.
//   * Spec fat-pointer typedef + vtable struct typedef.
//   * Method call on a concrete receiver: direct C fn call.
//   * Method call on a spec-typed receiver: vtable indirection.
//   * Spec coercion at let / list-elem / fn-arg sites.
//   * Enum tag+union layout, EnumLit construction, match destructure with
//     bind / lit / wildcard payload.
//   * Composite == per-shape helpers: list, tuple, struct, enum bare,
//     enum-with-payload, nested.
//   * LoweredCall path: `xs.push(v)` reuses the v0.3 builtin emit.
//   * NotImplemented stub emits the parity-format error string.
//
// Where a piece of the lowering is best validated by running the binary
// (rather than substring-matching the C source), the test invokes the C
// compiler. Those tests skip cleanly when no C compiler is available.
// ---------------------------------------------------------------------------

// runZerg emits, compiles, and executes src. Returns the binary's stdout.
// Any phase failing returns an error rather than calling t.Fatalf so callers
// can inspect.
func runZerg(t *testing.T, src string) (string, error) {
	t.Helper()
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		return "", err
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	got, err := exec.Command(binPath).CombinedOutput()
	return string(got), err
}

// expectStdout runs src and asserts equality against want. err is the
// expected error condition — pass ok==true for a successful run, ok==false
// to assert a non-zero exit code (e.g. NotImplemented panic).
func expectStdout(t *testing.T, src, want string) {
	t.Helper()
	got, err := runZerg(t, src)
	if err != nil {
		t.Fatalf("run failed: %v\nstdout: %q", err, got)
	}
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Inherent method.
// ---------------------------------------------------------------------------

// TestCgenInherentMethodEmitsMangledFn — the inherent impl method emits as
// a static C fn whose name is the mangled-receiver-type plus method name.
func TestCgenInherentMethodEmitsMangledFn(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int {
return this.count * 2
}
}
let c := Counter { count: 5 }
print c.double()
`
	out := mustEmit(t, src)
	if !strings.Contains(out, "static int64_t zerg_struct_Counter__double(zerg_struct_Counter z_this)") {
		t.Errorf("inherent method missing mangled signature; got:\n%s", out)
	}
	// Method-call site emits a direct call — receiver as first arg.
	if !strings.Contains(out, "zerg_struct_Counter__double(z_c)") {
		t.Errorf("method-call should pass receiver as first arg; got:\n%s", out)
	}
}

// TestCgenInherentMethodRunsCorrectly — the smoke test from the unit
// description. Validates the full lowering executes and prints the right
// value.
func TestCgenInherentMethodRunsCorrectly(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int {
let y := this.count
return y * 2
}
}
let c := Counter { count: 5 }
print c.double()
`
	expectStdout(t, src, "10\n")
}

// ---------------------------------------------------------------------------
// Spec impl + vtable.
// ---------------------------------------------------------------------------

// TestCgenSpecImplEmitsVtableInit — the static const vtable holds an
// adapter pointer for each impl method.
func TestCgenSpecImplEmitsVtableInit(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "wrapped"
}
}
let c := Counter { count: 1 }
print c.to_string()
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"struct zerg_vtable_Printable {",
		"typedef struct { void* data; const struct zerg_vtable_Printable* vt; } zerg_dyn_Printable;",
		"static zerg_str zerg_struct_Counter__Printable__to_string(zerg_struct_Counter z_this)",
		"static const struct zerg_vtable_Printable zerg_vt_zerg_struct_Counter_Printable",
		".to_string = zerg_adapter_zerg_struct_Counter__Printable__to_string",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing vtable piece %q; got:\n%s", want, out)
		}
	}
}

// TestCgenSpecImplRuns — same program runs and prints the impl's return
// value via the concrete-dispatch path.
func TestCgenSpecImplRuns(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "wrapped"
}
}
let c := Counter { count: 1 }
print c.to_string()
`
	expectStdout(t, src, "wrapped\n")
}

// TestCgenSpecDefaultInheritedRuns — empty impl body falls through to the
// type-specialised default adapter and returns the spec's default value.
func TestCgenSpecDefaultInheritedRuns(t *testing.T) {
	src := `spec Hashable {
fn hash() -> int {
return 99
}
}
struct Counter { count: int }
impl Counter for Hashable {}
let c := Counter { count: 1 }
print c.hash()
`
	expectStdout(t, src, "99\n")
}

// TestCgenSpecDefaultOverriddenRuns — impl override beats spec default.
func TestCgenSpecDefaultOverriddenRuns(t *testing.T) {
	src := `spec Hashable {
fn hash() -> int {
return 0
}
}
struct Counter { count: int }
impl Counter for Hashable {
fn hash() -> int {
return this.count + 100
}
}
let c := Counter { count: 5 }
print c.hash()
`
	expectStdout(t, src, "105\n")
}

// TestCgenNotImplementedStubMessage — a spec method with no override and
// no default emits the NotImplemented stub; the runtime panic prints the
// PLAN-pinned diagnostic line.
func TestCgenNotImplementedStubMessage(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {}
let c := Counter { count: 1 }
print c.to_string()
`
	out := mustEmit(t, src)
	// The stub forward-declared and emitted with the parity-format error.
	if !strings.Contains(out, "zerg_not_impl_zerg_struct_Counter__Printable__to_string") {
		t.Errorf("NotImplemented stub missing; got:\n%s", out)
	}
	if !strings.Contains(out, `zerg_not_implemented("Counter", "to_string", "Printable"`) {
		t.Errorf("NotImplemented call should pass type/method/spec strings; got:\n%s", out)
	}
	// Run-time check: the binary exits non-zero with the parity message.
	stdout, err := runZerg(t, src)
	if err == nil {
		t.Fatalf("expected NotImplemented exit, got stdout %q", stdout)
	}
	if !strings.Contains(stdout, "not implemented: Counter.to_string (declared in spec Printable") {
		t.Errorf("stub diagnostic does not match parity format; got %q", stdout)
	}
}

// ---------------------------------------------------------------------------
// Spec-typed receiver dispatch + spec coercion.
// ---------------------------------------------------------------------------

// TestCgenSpecTypedReceiverDispatchEmits — `let p: Printable = c` lowers
// to a fat-pointer literal; `p.to_string()` lowers to a vtable indirection.
func TestCgenSpecTypedReceiverDispatchEmits(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "wrapped"
}
}
let c := Counter { count: 1 }
let p: Printable = c
print p.to_string()
`
	out := mustEmit(t, src)
	// Coercion site: heap-box and wrap.
	if !strings.Contains(out, "(zerg_dyn_Printable){.data = __p, .vt = &zerg_vt_zerg_struct_Counter_Printable}") {
		t.Errorf("spec coercion did not emit fat-pointer literal; got:\n%s", out)
	}
	// Vtable dispatch: `__r.vt->method(__r.data, ...)`.
	if !strings.Contains(out, "__r.vt->to_string(__r.data") {
		t.Errorf("spec method-call did not emit vtable indirection; got:\n%s", out)
	}
}

// TestCgenSpecTypedReceiverDispatchRuns — same program runs and prints the
// method's return value through the vtable indirection.
func TestCgenSpecTypedReceiverDispatchRuns(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "wrapped"
}
}
let c := Counter { count: 1 }
let p: Printable = c
print p.to_string()
`
	expectStdout(t, src, "wrapped\n")
}

// TestCgenListOfSpecRuns — list[Printable] with mixed concrete element
// types (struct + enum) iterates and dispatches each through its own
// vtable.
func TestCgenListOfSpecRuns(t *testing.T) {
	src := `spec Tagged { fn tag() -> str }
struct Counter { count: int }
enum Color { Red }
impl Counter for Tagged {
fn tag() -> str { return "counter" }
}
impl Color for Tagged {
fn tag() -> str { return "color" }
}
let c := Counter { count: 1 }
let r := Color.Red
let xs: list[Tagged] = [c, r]
for x in xs {
print x.tag()
}
`
	expectStdout(t, src, "counter\ncolor\n")
}

// ---------------------------------------------------------------------------
// Enum payload literal + match.
// ---------------------------------------------------------------------------

// TestCgenEnumPayloadLayout — the per-enum struct contains an int32_t tag
// and a union with one sub-struct per variant. Variants without payload use
// an `_empty` placeholder so the union stays well-defined.
func TestCgenEnumPayloadLayout(t *testing.T) {
	src := `enum Token { Eof, Ident(str), Number(int, int) }
let t := Token.Eof
print t
`
	out := mustEmit(t, src)
	for _, want := range []string{
		"struct zerg_enum_Token {",
		"int32_t tag;",
		"union {",
		"struct { char _empty; } p0; /* Eof */",
		"struct { zerg_str a0; } p1; /* Ident */",
		"struct { int64_t a0; int64_t a1; } p2; /* Number */",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing enum layout piece %q; got:\n%s", want, out)
		}
	}
}

// TestCgenEnumWithListPayloadTypedefOrder — when an enum variant embeds a
// `list[T]` payload by value, the per-shape `struct zerg_list_<T>` body
// MUST be emitted before the enum's body or the C compiler reports an
// `incomplete type` error on the union field. Regression for v0.4 corpus
// program 25.
func TestCgenEnumWithListPayloadTypedefOrder(t *testing.T) {
	src := `enum Frame { Args(list[int]), Empty }
let f := Frame.Args([1, 2, 3])
print f
`
	out := mustEmit(t, src)
	listDef := "struct zerg_list_int64_t { int64_t* data;"
	enumDef := "struct zerg_enum_Frame {"
	li := strings.Index(out, listDef)
	ei := strings.Index(out, enumDef)
	if li < 0 {
		t.Fatalf("list shape definition not found in output:\n%s", out)
	}
	if ei < 0 {
		t.Fatalf("enum shape definition not found in output:\n%s", out)
	}
	if li > ei {
		t.Errorf("list[int] body must precede enum Frame body (list@%d, enum@%d)", li, ei)
	}
}

// TestCgenEnumPayloadLiteralRuns — `Token.Ident("foo")` constructs the
// tag+payload value and prints it in the documented format.
func TestCgenEnumPayloadLiteralRuns(t *testing.T) {
	src := `enum Token { Eof, Ident(str), Number(int, int) }
let t := Token.Ident("hello")
print t
let n := Token.Number(10, 16)
print n
let e := Token.Eof
print e
`
	expectStdout(t, src, "Token.Ident(hello)\nToken.Number(10, 16)\nToken.Eof\n")
}

// TestCgenMatchPayloadBindRuns — match arm with a BindPat at a payload
// position binds the value into arm scope.
func TestCgenMatchPayloadBindRuns(t *testing.T) {
	src := `enum Token { Ident(str), Number(int) }
let t := Token.Ident("zerg")
match t {
Token.Ident(name) => { print name }
Token.Number(n) => { print n }
}
`
	expectStdout(t, src, "zerg\n")
}

// TestCgenMatchPayloadLiteralRuns — literal payload pattern participates
// in arm selection. `Op.Add(0)` does NOT match because the payload is 5.
func TestCgenMatchPayloadLiteralRuns(t *testing.T) {
	src := `enum Op { Add(int), Sub(int) }
let o := Op.Add(5)
match o {
Op.Add(0) => { print "zero" }
Op.Add(n) => { print n }
Op.Sub(n) => { print n }
}
`
	expectStdout(t, src, "5\n")
}

// TestCgenMatchPayloadWildcardRuns — wildcard at a payload position is a
// no-op; the bound positions still bind.
func TestCgenMatchPayloadWildcardRuns(t *testing.T) {
	src := `enum Pair { Both(int, int) }
let p := Pair.Both(7, 8)
match p {
Pair.Both(_, second) => { print second }
}
`
	expectStdout(t, src, "8\n")
}

// ---------------------------------------------------------------------------
// Composite == — per-shape helpers.
// ---------------------------------------------------------------------------

// TestCgenListEqRuns — list_eq returns true for identical content, false
// otherwise.
func TestCgenListEqRuns(t *testing.T) {
	src := `let a := [1, 2, 3]
let b := [1, 2, 3]
let c := [1, 2]
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// TestCgenTupleEqRuns — per-position tuple equality.
func TestCgenTupleEqRuns(t *testing.T) {
	src := `let a := (1, "x")
let b := (1, "x")
let c := (1, "y")
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// TestCgenStructEqRuns — declaration-order field equality.
func TestCgenStructEqRuns(t *testing.T) {
	src := `struct Point { x: int, y: int }
let a := Point { x: 1, y: 2 }
let b := Point { x: 1, y: 2 }
let c := Point { x: 1, y: 3 }
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// TestCgenEnumBareEqRuns — same tag → true; different tag → false.
func TestCgenEnumBareEqRuns(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let a := Color.Red
let b := Color.Red
let c := Color.Green
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// TestCgenEnumPayloadEqRuns — same tag + payload equality recurses through
// each payload position.
func TestCgenEnumPayloadEqRuns(t *testing.T) {
	src := `enum Token { Ident(str), Number(int) }
let a := Token.Ident("foo")
let b := Token.Ident("foo")
let c := Token.Ident("bar")
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// TestCgenNestedListOfTupleEqRuns — nested composite == recurses correctly
// (list of tuple → tuple_eq → primitive equality).
func TestCgenNestedListOfTupleEqRuns(t *testing.T) {
	src := `let a := [(1, "x"), (2, "y")]
let b := [(1, "x"), (2, "y")]
let c := [(1, "x"), (3, "y")]
print a == b
print a == c
`
	expectStdout(t, src, "true\nfalse\n")
}

// ---------------------------------------------------------------------------
// Method form on lists — LoweredCall path.
// ---------------------------------------------------------------------------

// TestCgenLoweredCallMethodPushRuns — `xs.push(v)` lowers to the v0.3
// `push(xs, v)` builtin emit and produces the same output.
func TestCgenLoweredCallMethodPushRuns(t *testing.T) {
	src := `mut xs := [1, 2]
xs.push(3)
print xs
`
	expectStdout(t, src, "[ 1, 2, 3 ]\n")
}

// ---------------------------------------------------------------------------
// `this` resolution.
// ---------------------------------------------------------------------------

// TestCgenThisFieldAccessRuns — `this.x + this.y` reads two fields of the
// receiver.
func TestCgenThisFieldAccessRuns(t *testing.T) {
	src := `struct Box { x: int, y: int }
impl Box {
fn sum() -> int {
return this.x + this.y
}
}
let b := Box { x: 4, y: 5 }
print b.sum()
`
	expectStdout(t, src, "9\n")
}

// TestCgenThisMethodCallRuns — `this.helper()` chains method calls inside
// the same impl block.
func TestCgenThisMethodCallRuns(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int {
return this.count * 2
}
fn quad() -> int {
return this.double() * 2
}
}
let c := Counter { count: 3 }
print c.quad()
`
	expectStdout(t, src, "12\n")
}
