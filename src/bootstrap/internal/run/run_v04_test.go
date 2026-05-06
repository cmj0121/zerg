package run

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 — interpreter for specs, impls, vtable dispatch, enum payloads,
// composite ==, and method-form list builtins.
//
// Each test exercises one of the dispatch paths or one of the new value
// shapes. The tests aim for the minimal program the rule needs; the v0.4
// corpus (Unit 8) covers larger end-to-end programs.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Method dispatch — inherent / spec impl / default impl.
// ---------------------------------------------------------------------------

// TestRunInherentMethodOnStruct — `c.double()` routes to the inherent impl.
func TestRunInherentMethodOnStruct(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int {
return this.count * 2
}
}
c := Counter { count: 7 }
print c.double()
`
	expectOK(t, src, "14\n")
}

// TestRunInherentMethodOnEnum — same path on an enum-typed receiver.
func TestRunInherentMethodOnEnum(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
impl Color {
fn label() -> str {
return "color"
}
}
c := Color.Red
print c.label()
`
	expectOK(t, src, "color\n")
}

// TestRunSpecImplStruct — spec-defined method routed via impl-for-spec.
func TestRunSpecImplStruct(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "counter"
}
}
c := Counter { count: 1 }
print c.to_string()
`
	expectOK(t, src, "counter\n")
}

// TestRunSpecImplEnum — same path on an enum.
func TestRunSpecImplEnum(t *testing.T) {
	src := `spec Tagged { fn tag() -> str }
enum Light { On, Off }
impl Light for Tagged {
fn tag() -> str {
return "light"
}
}
l := Light.On
print l.tag()
`
	expectOK(t, src, "light\n")
}

// TestRunSpecDefaultInherited — empty impl body falls through to spec
// default.
func TestRunSpecDefaultInherited(t *testing.T) {
	src := `spec Hashable {
fn hash() -> int {
return 99
}
}
struct Counter { count: int }
impl Counter for Hashable {}
c := Counter { count: 1 }
print c.hash()
`
	expectOK(t, src, "99\n")
}

// TestRunSpecDefaultOverridden — impl override beats spec default.
func TestRunSpecDefaultOverridden(t *testing.T) {
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
c := Counter { count: 5 }
print c.hash()
`
	expectOK(t, src, "105\n")
}

// TestRunNotImplementedPanic — signature-only spec method, empty impl,
// invocation panics with the documented diagnostic.
func TestRunNotImplementedPanic(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {}
c := Counter { count: 1 }
print c.to_string()
`
	got, err := runSrc(t, src)
	if err == nil {
		t.Fatalf("expected NotImplemented panic, got stdout %q", got)
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error %q does not contain 'not implemented'", err.Error())
	}
	if !strings.Contains(err.Error(), "Counter") || !strings.Contains(err.Error(), "to_string") {
		t.Errorf("error %q does not name type/method", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Spec-as-type / vtable dispatch.
// ---------------------------------------------------------------------------

// TestRunSpecTypedReceiverDispatch — let-bind to a spec type wraps the
// concrete value; the next method call routes through the vtable to the
// (Counter, Printable) impl.
func TestRunSpecTypedReceiverDispatch(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
struct Counter { count: int }
impl Counter for Printable {
fn to_string() -> str {
return "wrapped"
}
}
c := Counter { count: 1 }
p: Printable = c
print p.to_string()
`
	expectOK(t, src, "wrapped\n")
}

// TestRunListOfSpecHeterogeneous — list[Printable] holds both a struct and
// an enum; for-iter dispatches each through its own vtable.
func TestRunListOfSpecHeterogeneous(t *testing.T) {
	src := `spec Tagged { fn tag() -> str }
struct Counter { count: int }
enum Color { Red }
impl Counter for Tagged {
fn tag() -> str {
return "counter"
}
}
impl Color for Tagged {
fn tag() -> str {
return "color"
}
}
c := Counter { count: 1 }
r := Color.Red
xs: list[Tagged] = [c, r]
for x in xs {
print x.tag()
}
`
	expectOK(t, src, "counter\ncolor\n")
}

// ---------------------------------------------------------------------------
// Enum payload literals + match destructure.
// ---------------------------------------------------------------------------

// TestRunEnumPayloadLitPrint — `Token.Ident("hello")` constructs and prints
// in the documented "Name.Variant(arg)" format.
func TestRunEnumPayloadLitPrint(t *testing.T) {
	src := `enum Token { Eof, Ident(str), Number(int, int) }
t := Token.Ident("hello")
print t
`
	expectOK(t, src, "Token.Ident(hello)\n")
}

// TestRunEnumPayloadMultiPrint — multi-position payload prints comma-space
// separated.
func TestRunEnumPayloadMultiPrint(t *testing.T) {
	src := `enum Token { Eof, Ident(str), Number(int, int) }
t := Token.Number(10, 16)
print t
`
	expectOK(t, src, "Token.Number(10, 16)\n")
}

// TestRunEnumBareNoParens — bare variant prints without parens (compatible
// with v0.2 behaviour).
func TestRunEnumBarePrint(t *testing.T) {
	src := `enum Token { Eof, Ident(str) }
t := Token.Eof
print t
`
	expectOK(t, src, "Token.Eof\n")
}

// TestRunEnumMatchPayloadBind — binds the payload value into the arm scope.
func TestRunEnumMatchPayloadBind(t *testing.T) {
	src := `enum Token { Ident(str), Number(int) }
t := Token.Ident("zerg")
match t {
Token.Ident(name) => { print name }
Token.Number(n) => { print n }
}
`
	expectOK(t, src, "zerg\n")
}

// TestRunEnumMatchPayloadLiteral — literal payload patterns participate in
// arm selection.
func TestRunEnumMatchPayloadLiteral(t *testing.T) {
	src := `enum Op { Add(int), Sub(int) }
o := Op.Add(5)
match o {
Op.Add(0) => { print "zero" }
Op.Add(n) => { print n }
Op.Sub(n) => { print n }
}
`
	expectOK(t, src, "5\n")
}

// TestRunEnumMatchPayloadWildcard — wildcard in payload position.
func TestRunEnumMatchPayloadWildcard(t *testing.T) {
	src := `enum Pair { Both(int, int) }
p := Pair.Both(7, 8)
match p {
Pair.Both(_, second) => { print second }
}
`
	expectOK(t, src, "8\n")
}

// ---------------------------------------------------------------------------
// Composite == evaluation.
// ---------------------------------------------------------------------------

// TestRunListEq — same length, same elements, same order.
func TestRunListEq(t *testing.T) {
	src := `a := [1, 2, 3]
b := [1, 2, 3]
print a == b
`
	expectOK(t, src, "true\n")
}

// TestRunListNeqLength — different lengths compare as false.
func TestRunListNeqLength(t *testing.T) {
	src := `a := [1, 2, 3]
b := [1, 2]
print a == b
`
	expectOK(t, src, "false\n")
}

// TestRunTupleEq — per-position == on tuples.
func TestRunTupleEq(t *testing.T) {
	src := `a := (1, "x")
b := (1, "x")
print a == b
`
	expectOK(t, src, "true\n")
}

// TestRunStructEq — declaration-order field equality.
func TestRunStructEq(t *testing.T) {
	src := `struct Point { x: int, y: int }
a := Point { x: 1, y: 2 }
b := Point { x: 1, y: 2 }
c := Point { x: 1, y: 3 }
print a == b
print a == c
`
	expectOK(t, src, "true\nfalse\n")
}

// TestRunEnumBareEq — same tag → true; different tag → false.
func TestRunEnumBareEq(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
a := Color.Red
b := Color.Red
c := Color.Green
print a == b
print a == c
`
	expectOK(t, src, "true\nfalse\n")
}

// TestRunEnumPayloadEq — same tag + same payloads → true; payload differs
// → false.
func TestRunEnumPayloadEq(t *testing.T) {
	src := `enum Token { Ident(str), Number(int) }
a := Token.Ident("foo")
b := Token.Ident("foo")
c := Token.Ident("bar")
print a == b
print a == c
`
	expectOK(t, src, "true\nfalse\n")
}

// TestRunNestedListOfTupleEq — recursive composite ==.
func TestRunNestedListOfTupleEq(t *testing.T) {
	src := `a := [(1, "x"), (2, "y")]
b := [(1, "x"), (2, "y")]
c := [(1, "x"), (3, "y")]
print a == b
print a == c
`
	expectOK(t, src, "true\nfalse\n")
}

// ---------------------------------------------------------------------------
// Method form on lists (v0.3 builtins via lowering).
// ---------------------------------------------------------------------------

// TestRunListMethodPush — `xs.push(v)` lowers to `push(xs, v)`.
func TestRunListMethodPush(t *testing.T) {
	src := `mut xs := [1, 2]
xs.push(3)
print xs
`
	expectOK(t, src, "[ 1, 2, 3 ]\n")
}

// TestRunListMethodClone — `xs.clone()` returns an independent deep copy.
func TestRunListMethodClone(t *testing.T) {
	src := `xs := [1, 2, 3]
ys := xs.clone()
print ys
`
	expectOK(t, src, "[ 1, 2, 3 ]\n")
}

// TestRunListMethodLen — `xs.len()` lowers to `len(xs)`.
func TestRunListMethodLen(t *testing.T) {
	src := `xs := [10, 20, 30, 40]
print xs.len()
`
	expectOK(t, src, "4\n")
}

// ---------------------------------------------------------------------------
// `this` access patterns.
// ---------------------------------------------------------------------------

// TestRunThisFieldRead — `this.x` reads in a method body.
func TestRunThisFieldRead(t *testing.T) {
	src := `struct Box { x: int, y: int }
impl Box {
fn sum() -> int {
return this.x + this.y
}
}
b := Box { x: 4, y: 5 }
print b.sum()
`
	expectOK(t, src, "9\n")
}

// TestRunThisMethodCall — `this.helper()` chains method calls within an
// impl body.
func TestRunThisMethodCall(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int {
return this.count * 2
}
fn quad() -> int {
return this.double() * 2
}
}
c := Counter { count: 3 }
print c.quad()
`
	expectOK(t, src, "12\n")
}
