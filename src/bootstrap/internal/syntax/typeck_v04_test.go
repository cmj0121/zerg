package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 Unit 3 typeck: spec/impl tables, method dispatch, recursive enum cycle,
// spec-as-type.
//
// Helpers (checkSrc / checkErr / firstStmt) are shared with typeck_test.go.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 1. Spec decl + impl block.
// ---------------------------------------------------------------------------

func TestCheckSpecDeclSignatureOnly(t *testing.T) {
	src := `spec Printable {
fn to_string() -> str
}
`
	checkSrc(t, src)
}

func TestCheckSpecDeclWithDefault(t *testing.T) {
	src := `spec Hashable {
fn hash() -> int { return 0 }
}
`
	checkSrc(t, src)
}

func TestCheckSpecDeclEmptyBody(t *testing.T) {
	checkSrc(t, "spec Marker {}\n")
}

func TestCheckSpecDeclDuplicateName(t *testing.T) {
	src := `spec Printable { fn to_string() -> str }
spec Printable { fn show() -> str }
`
	checkErr(t, src, `spec "Printable" already declared`)
}

func TestCheckSpecDeclDuplicateMethodName(t *testing.T) {
	src := `spec Bad {
fn x() -> int
fn x() -> int
}
`
	checkErr(t, src, `duplicate method "x"`)
}

func TestCheckSpecDeclExplicitThisRejected(t *testing.T) {
	// `this` is a keyword and the parser already rejects it as a parameter
	// name. Probe at the parse level — we just want to confirm the surface
	// stays closed; the diagnostic location (parse vs typeck) is incidental.
	src := `spec Bad {
fn show(this: int) -> int
}
`
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if _, err := Parse(tokens); err == nil {
		t.Fatalf("expected parse error for 'this' as parameter name")
	}
}

func TestCheckSpecDeclUnknownReturnType(t *testing.T) {
	src := `spec Bad {
fn x() -> Bogus
}
`
	checkErr(t, src, `unknown type "Bogus"`)
}

// ---------------------------------------------------------------------------
// 2. Inherent impl + concrete-typed receiver dispatch.
// ---------------------------------------------------------------------------

func TestCheckInherentImplAndCall(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn double() -> int { return this.count * 2 }
}
let c := Counter { count: 7 }
print c.double()
`
	checkSrc(t, src)
}

func TestCheckInherentImplOnEnumOK(t *testing.T) {
	src := `enum Color { Red, Blue }
impl Color {
fn describe() -> int { return 1 }
}
let c := Color.Red
print c.describe()
`
	checkSrc(t, src)
}

func TestCheckInherentImplUnknownTypeRejected(t *testing.T) {
	src := `impl Bogus {
fn x() -> int { return 0 }
}
`
	checkErr(t, src, `unknown type "Bogus"`)
}

func TestCheckInherentImplDuplicateMethodInBlock(t *testing.T) {
	src := `struct C { x: int }
impl C {
fn foo() -> int { return 0 }
fn foo() -> int { return 1 }
}
`
	checkErr(t, src, `duplicate method "foo"`)
}

// ---------------------------------------------------------------------------
// 3. Spec impl + dispatch.
// ---------------------------------------------------------------------------

func TestCheckSpecImplBasic(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable {
fn show() -> int { return this.count }
}
let c := Counter { count: 5 }
print c.show()
`
	checkSrc(t, src)
}

func TestCheckSpecImplEmptyBodyWithDefault(t *testing.T) {
	src := `spec Hashable { fn hash() -> int { return 0 } }
struct Counter { count: int }
impl Counter for Hashable {}
let c := Counter { count: 5 }
print c.hash()
`
	checkSrc(t, src)
}

func TestCheckSpecImplOverrideDefault(t *testing.T) {
	src := `spec Hashable { fn hash() -> int { return 0 } }
struct Counter { count: int }
impl Counter for Hashable {
fn hash() -> int { return this.count }
}
let c := Counter { count: 5 }
print c.hash()
`
	checkSrc(t, src)
}

func TestCheckSpecImplOnPrimitiveRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
impl int for Printable {
fn show() -> int { return 0 }
}
`
	checkErr(t, src, "cannot impl spec at v0.4")
}

func TestCheckSpecImplOnUnknownTypeRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
impl Bogus for Printable {
fn show() -> int { return 0 }
}
`
	checkErr(t, src, `unknown type "Bogus"`)
}

func TestCheckSpecImplOnUnknownSpecRejected(t *testing.T) {
	src := `struct C { x: int }
impl C for NoSuchSpec {
fn show() -> int { return 0 }
}
`
	checkErr(t, src, `unknown spec "NoSuchSpec"`)
}

func TestCheckSpecImplDuplicateRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable {
fn show() -> int { return 1 }
}
impl Counter for Printable {
fn show() -> int { return 2 }
}
`
	checkErr(t, src, "duplicate impl")
}

func TestCheckSpecImplMethodNotInSpecRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable {
fn other() -> int { return 0 }
}
`
	checkErr(t, src, `method "other" is not declared in spec "Printable"`)
}

func TestCheckSpecImplMethodWrongReturnRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable {
fn show() -> str { return "x" }
}
`
	checkErr(t, src, "return type")
}

// ---------------------------------------------------------------------------
// 4. Method-name collision rules (TENTH-MAN).
// ---------------------------------------------------------------------------

func TestCheckMethodCollisionInherentInherent(t *testing.T) {
	src := `struct C { x: int }
impl C {
fn foo() -> int { return 1 }
}
impl C {
fn foo() -> int { return 2 }
}
`
	checkErr(t, src, "defined multiple times in inherent impl blocks")
}

func TestCheckMethodCollisionInherentVsSpec(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct C { x: int }
impl C {
fn show() -> int { return 1 }
}
impl C for Printable {
fn show() -> int { return 2 }
}
`
	checkErr(t, src, "defined twice")
}

func TestCheckMethodAmbiguousMultipleSpecs(t *testing.T) {
	src := `spec A { fn name() -> int }
spec B { fn name() -> int }
struct C { x: int }
impl C for A { fn name() -> int { return 1 } }
impl C for B { fn name() -> int { return 2 } }
let c := C { x: 0 }
print c.name()
`
	checkErr(t, src, "matches multiple specs")
}

func TestCheckMethodAmbiguousResolvedBySpecBinding(t *testing.T) {
	src := `spec A { fn name() -> int }
spec B { fn name() -> int }
struct C { x: int }
impl C for A { fn name() -> int { return 1 } }
impl C for B { fn name() -> int { return 2 } }
let c := C { x: 0 }
let p: A = c
print p.name()
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// 5. Spec-as-type bindings + spec-typed dispatch.
// ---------------------------------------------------------------------------

func TestCheckSpecAsTypeBind(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable { fn show() -> int { return this.count } }
let c := Counter { count: 5 }
let p: Printable = c
print p.show()
`
	checkSrc(t, src)
}

func TestCheckSpecAsTypeBindFromNonImplRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
let p: Printable = 5
`
	checkErr(t, src, "cannot assign int to Printable")
}

func TestCheckSpecAsTypeBindFromStructWithoutImplRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct C { x: int }
let c := C { x: 0 }
let p: Printable = c
`
	checkErr(t, src, "cannot assign C to Printable")
}

func TestCheckListOfSpecType(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct C { x: int }
impl C for Printable { fn show() -> int { return this.x } }
struct D { y: int }
impl D for Printable { fn show() -> int { return this.y } }
let c := C { x: 1 }
let d := D { y: 2 }
let xs: list[Printable] = [c, d]
`
	checkSrc(t, src)
}

func TestCheckSpecMethodNotInSpecCalledOnSpecBindRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct C { x: int }
impl C for Printable { fn show() -> int { return this.x } }
let c := C { x: 0 }
let p: Printable = c
print p.bogus()
`
	checkErr(t, src, `method "bogus" does not exist on spec`)
}

// ---------------------------------------------------------------------------
// 6. `this` inside method bodies.
// ---------------------------------------------------------------------------

func TestCheckThisFieldRead(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn show() -> int { return this.count }
}
let c := Counter { count: 5 }
print c.show()
`
	checkSrc(t, src)
}

func TestCheckThisMethodCallInsideBody(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn helper() -> int { return this.count }
fn show() -> int { return this.helper() }
}
let c := Counter { count: 5 }
print c.show()
`
	checkSrc(t, src)
}

func TestCheckThisOutsideMethodRejected(t *testing.T) {
	checkErr(t, "let x := this\n", "'this' is only valid inside an impl method body")
}

// `this = other` is rejected at parse, before typeck. We probe the parse
// error here rather than checkErr to confirm the surface stays closed.
func TestCheckThisAssignmentRejectedAtParse(t *testing.T) {
	src := "this = 5\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for 'this = 5'")
	}
}

// ---------------------------------------------------------------------------
// 7. Method dispatch on missing method.
// ---------------------------------------------------------------------------

func TestCheckMethodMissingOnConcreteRejected(t *testing.T) {
	src := `struct C { x: int }
let c := C { x: 0 }
print c.bogus()
`
	checkErr(t, src, `method "bogus" does not exist on C`)
}

func TestCheckMethodOnPrimitiveRejected(t *testing.T) {
	checkErr(t, "let x := 5\nprint x.foo()\n", `method "foo" does not exist on int`)
}

// ---------------------------------------------------------------------------
// 8. Enum payloads.
// ---------------------------------------------------------------------------

func TestCheckEnumPayloadConstruct(t *testing.T) {
	src := `enum Token { Eof, Ident(str), Number(int, int) }
let t := Token.Ident("hello")
print t
`
	checkSrc(t, src)
}

func TestCheckEnumPayloadConstructMultiArg(t *testing.T) {
	src := `enum Token { Number(int, int) }
let t := Token.Number(10, 16)
print t
`
	checkSrc(t, src)
}

func TestCheckEnumBareVariantStillOK(t *testing.T) {
	src := `enum Token { Eof, Ident(str) }
let t := Token.Eof
print t
`
	checkSrc(t, src)
}

func TestCheckEnumPayloadArityMismatchTooFew(t *testing.T) {
	src := `enum Token { Number(int, int) }
let t := Token.Number(10)
print t
`
	checkErr(t, src, "expects 2 payload value(s)")
}

func TestCheckEnumPayloadArityMismatchTooMany(t *testing.T) {
	src := `enum Token { Ident(str) }
let t := Token.Ident("a", "b")
print t
`
	checkErr(t, src, "expects 1 payload value(s)")
}

func TestCheckEnumPayloadTypeMismatch(t *testing.T) {
	src := `enum Token { Ident(str) }
let t := Token.Ident(42)
print t
`
	checkErr(t, src, "payload position 1")
}

func TestCheckEnumBareVariantWithParensRejected(t *testing.T) {
	src := `enum Token { Eof }
let t := Token.Eof()
print t
`
	checkErr(t, src, "no payload")
}

func TestCheckEnumPayloadVariantBareAccessRejected(t *testing.T) {
	src := `enum Token { Ident(str) }
let t := Token.Ident
print t
`
	checkErr(t, src, "use Token.Ident(...) to construct")
}

// ---------------------------------------------------------------------------
// 9. Match enum patterns with payloads.
// ---------------------------------------------------------------------------

func TestCheckMatchPayloadBind(t *testing.T) {
	src := `enum Token { Eof, Ident(str) }
let t := Token.Ident("hi")
match t {
Token.Eof => { print "eof" }
Token.Ident(name) => { print name }
}
`
	checkSrc(t, src)
}

func TestCheckMatchPayloadArityMismatch(t *testing.T) {
	src := `enum Token { Number(int, int) }
let t := Token.Number(10, 16)
match t {
Token.Number(a) => { print a }
}
`
	checkErr(t, src, "pattern supplies 1")
}

func TestCheckMatchPayloadBareForPayloadVariant(t *testing.T) {
	src := `enum Token { Ident(str) }
let t := Token.Ident("hi")
match t {
Token.Ident => { print "x" }
}
`
	checkErr(t, src, "must destructure with parens")
}

// ---------------------------------------------------------------------------
// 10. Recursive enum cycle detection (TENTH-MAN).
// ---------------------------------------------------------------------------

func TestCheckRecursiveEnumDirect(t *testing.T) {
	src := `enum Tree { Node(Tree, Tree) }
`
	checkErr(t, src, "recursive enum")
}

func TestCheckRecursiveEnumViaList(t *testing.T) {
	src := `enum E { V(list[E]) }
`
	checkErr(t, src, "recursive enum")
}

func TestCheckRecursiveEnumViaTuple(t *testing.T) {
	src := `enum E { V(tuple[int, E]) }
`
	checkErr(t, src, "recursive enum")
}

func TestCheckRecursiveEnumViaStruct(t *testing.T) {
	src := `enum E { V(S) }
struct S { e: E }
`
	// Cycle is detected on either side; either error message is fine.
	checkErr(t, src, "recursive")
}

func TestCheckRecursiveEnumMutual(t *testing.T) {
	src := `enum A { X(B) }
enum B { Y(A) }
`
	checkErr(t, src, "recursive enum")
}

func TestCheckRecursiveStructStillRejected(t *testing.T) {
	src := `struct A { b: A }
`
	checkErr(t, src, "recursive struct")
}

// ---------------------------------------------------------------------------
// 11. Field access of spec-typed struct field.
// ---------------------------------------------------------------------------

func TestCheckStructFieldOfSpecType(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct C { x: int }
impl C for Printable { fn show() -> int { return this.x } }
struct Holder { p: Printable }
let c := C { x: 7 }
let h := Holder { p: c }
print h.p.show()
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// 12. Spec default body inherits to non-overriding impl.
// ---------------------------------------------------------------------------

func TestCheckSpecDefaultInheritedNotOverridden(t *testing.T) {
	src := `spec Hashable { fn hash() -> int { return 0 } }
struct C { x: int }
impl C for Hashable {}
let c := C { x: 0 }
let h := c.hash()
print h
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// 13. Method args type-check.
// ---------------------------------------------------------------------------

func TestCheckMethodArgsTypeCheck(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn add(n: int) -> int { return this.count + n }
}
let c := Counter { count: 5 }
print c.add(10)
`
	checkSrc(t, src)
}

func TestCheckMethodArgArityMismatch(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn add(n: int) -> int { return this.count + n }
}
let c := Counter { count: 5 }
print c.add(10, 20)
`
	checkErr(t, src, "expects 1 argument")
}

func TestCheckMethodArgTypeMismatch(t *testing.T) {
	src := `struct Counter { count: int }
impl Counter {
fn add(n: int) -> int { return this.count + n }
}
let c := Counter { count: 5 }
print c.add("oops")
`
	checkErr(t, src, "argument 1")
}
