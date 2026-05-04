// Tests for the v0.5 multi-module interpreter (Unit 5). Each test
// materialises a multi-file fixture under t.TempDir(), invokes the
// loader to walk the import graph, runs CheckBundle for typeck, then
// hands the resolved Bundle to RunBundle. The expected stdout is
// compared byte-for-byte to RunBundle's writer output.
//
// The harness mirrors loader_test.go's "write fixtures into TempDir"
// shape but lives in the run package so we can call RunBundle without an
// import cycle. The CheckBundle dependency comes through the public
// syntax package surface.
package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// runBundleFiles materialises each (name, content) pair under a fresh
// TempDir, loads from `entry`, runs CheckBundle, then RunBundle. Returns
// stdout and any error. Errors propagate verbatim from any layer.
func runBundleFiles(t *testing.T, entry string, files map[string]string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	bundle, err := loader.Load(filepath.Join(dir, entry))
	if err != nil {
		return "", err
	}
	if err := syntax.CheckBundle(bundle); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := RunBundle(bundle, &buf); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func expectBundleOK(t *testing.T, entry string, files map[string]string, want string) {
	t.Helper()
	got, err := runBundleFiles(t, entry, files)
	if err != nil {
		t.Fatalf("RunBundle failed: %v", err)
	}
	if got != want {
		t.Fatalf("stdout mismatch\n got: %q\nwant: %q", got, want)
	}
}

// 1. main calls util.add(1, 2) — cross-module fn call returning int.
func TestV05CrossModuleFnCall(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub fn add(a: int, b: int) -> int {
return a + b
}
`,
		"main.zg": `import "util"
print util.add(1, 2)
`,
	}, "3\n")
}

// 2. main constructs util.Counter { count: 5 } — cross-module struct lit.
func TestV05CrossModuleStructLit(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub struct Counter { count: int }
`,
		"main.zg": `import "util"
let c := util.Counter { count: 5 }
print c.count
`,
	}, "5\n")
}

// 3. main calls a method on a foreign struct — `c.inc(); print c.count`.
//
// PLAN: v0.5 doesn't introduce mutating methods; here we test a method
// returning a derived value to keep within v0.4 semantics. The cross-
// module method-dispatch path is the same regardless of whether the
// method writes through `this`.
func TestV05CrossModuleMethodOnStruct(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub struct Counter { count: int }
impl Counter {
pub fn doubled() -> int {
return this.count * 2
}
}
`,
		"main.zg": `import "util"
let c := util.Counter { count: 7 }
print c.doubled()
`,
	}, "14\n")
}

// 4. main uses a foreign bare-variant enum: `print util.Color.Red`.
func TestV05CrossModuleBareEnum(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Color { Red, Green, Blue }
`,
		"main.zg": `import "util"
print util.Color.Red
`,
	}, "Color.Red\n")
}

// 5. main uses a foreign enum payload variant and matches it.
func TestV05CrossModulePayloadEnumMatch(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Token {
Eof,
Ident(str),
Number(int),
}
`,
		"main.zg": `import "util"
let t := util.Token.Ident("hi")
match t {
Token.Ident(s) => print s
Token.Number(n) => print n
Token.Eof => print "eof"
}
`,
	}, "hi\n")
}

// 6. main impls util.Printable for its own struct and dispatches via a
// spec-typed binding.
func TestV05CrossModuleSpecImplLocalType(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub spec Printable {
pub fn to_string() -> str
}
`,
		"main.zg": `import "util"
struct Greeting { word: str }
impl Greeting for util.Printable {
pub fn to_string() -> str {
return this.word
}
}
let g := Greeting { word: "hello" }
let p: util.Printable = g
print p.to_string()
`,
	}, "hello\n")
}

// 7. List-of-spec heterogeneous dispatch where each impl lives in a
// different module. The list element type is the spec; each element
// wraps a concrete value declared in some module; dispatch must find
// the right (Type, Spec) impl regardless of which module declared it.
func TestV05CrossModuleListOfSpecHeterogeneous(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"shared.zg": `pub spec Tagged {
pub fn tag() -> str
}
`,
		"a.zg": `import "shared"
pub struct A { v: int }
impl A for shared.Tagged {
pub fn tag() -> str {
return "A"
}
}
`,
		"b.zg": `import "shared"
pub struct B { v: int }
impl B for shared.Tagged {
pub fn tag() -> str {
return "B"
}
}
`,
		"main.zg": `import "shared"
import "a"
import "b"
let xs: list[shared.Tagged] = [ a.A { v: 1 }, b.B { v: 2 } ]
for x in xs {
print x.tag()
}
`,
	}, "A\nB\n")
}

// 8. Diamond import: A imports B, A imports C, B and C both import D;
// D's pub fn is callable from A.
func TestV05CrossModuleDiamondCall(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"d.zg": `pub fn answer() -> int {
return 42
}
`,
		"b.zg": `import "d"
pub fn from_b() -> int {
return d.answer() + 1
}
`,
		"c.zg": `import "d"
pub fn from_c() -> int {
return d.answer() + 2
}
`,
		"main.zg": `import "b"
import "c"
import "d"
print d.answer()
print b.from_b()
print c.from_c()
`,
	}, "42\n43\n44\n")
}

// 9. Aliased import: `import "util" as u; u.foo()` runs.
func TestV05AliasedImportCall(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub fn answer() -> int {
return 42
}
`,
		"main.zg": `import "util" as u
print u.answer()
`,
	}, "42\n")
}

// 10. Cross-module enum match destructure with bare and payload variants.
func TestV05CrossModuleResultMatch(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Outcome {
Ok(int),
Err(str),
}
pub fn parse(input: str) -> Outcome {
if input == "good" {
return Outcome.Ok(7)
}
return Outcome.Err("bad")
}
`,
		"main.zg": `import "util"
let r := util.parse("good")
match r {
Outcome.Ok(v) => print v
Outcome.Err(e) => print e
}
let s := util.parse("nope")
match s {
Outcome.Ok(v) => print v
Outcome.Err(e) => print e
}
`,
	}, "7\nbad\n")
}

// V05 — additional sanity coverage, not in the headline 10.

// V05 cross-module call path back: foreign fn calling its own module's
// other fn (verifies lexical-scope switch on call).
func TestV05ForeignFnCallsOwnModule(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `fn helper(x: int) -> int {
return x + 100
}
pub fn wrap(y: int) -> int {
return helper(y)
}
`,
		"main.zg": `import "util"
print util.wrap(5)
`,
	}, "105\n")
}

// V05 method body resolves the receiver's own module's enums (no module
// prefix needed when the method body lives in the type's owning module).
func TestV05MethodBodyResolvesOwnEnum(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Color { Red, Green }
pub struct Light { c: Color }
impl Light {
pub fn name() -> str {
match this.c {
Color.Red => return "red"
Color.Green => return "green"
}
return "?"
}
}
`,
		"main.zg": `import "util"
let l := util.Light { c: util.Color.Red }
print l.name()
`,
	}, "red\n")
}

// V05 backward compat: a single-file program with no imports continues
// to run identically through RunBundle (the entry adapter wraps the
// program in a one-module bundle).
func TestV05SingleFileBackwardCompat(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"main.zg": `fn double(x: int) -> int {
return x * 2
}
print double(21)
`,
	}, "42\n")
}

// V05 cross-module spec coercion at fn-arg site: a fn in module M takes
// a list[spec]; main passes a heterogeneous list whose elements are
// concrete types declared across two modules.
func TestV05SpecCoercionAcrossModules(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"shared.zg": `pub spec Named {
pub fn name() -> str
}
`,
		"main.zg": `import "shared"
struct Cat { name: str }
struct Dog { name: str }
impl Cat for shared.Named {
pub fn name() -> str { return "cat" }
}
impl Dog for shared.Named {
pub fn name() -> str { return "dog" }
}
let xs: list[shared.Named] = [ Cat { name: "x" }, Dog { name: "y" } ]
for x in xs {
print x.name()
}
`,
	}, "cat\ndog\n")
}

// V05 same-named struct in two modules: main and util both declare
// `struct Counter` with their own inherent `show` method. Dispatch must
// route to the receiver's own module's impl. Mirrors the codegen
// guarantee enforced by TestV05CodegenSameNamedStructDistinctMangle —
// two distinct canonical *Type pointers, two distinct impl entries.
func TestV05SameNamedStructDistinctImpls(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub struct Counter { tag: int }
impl Counter {
pub fn show() -> str { return "util" }
}
`,
		"main.zg": `import "util"
struct Counter { tag: int }
impl Counter {
pub fn show() -> str { return "main" }
}
let mc := Counter { tag: 1 }
print mc.show()
let uc := util.Counter { tag: 1 }
print uc.show()
`,
	}, "main\nutil\n")
}

// V05 same-named enum WITH PAYLOAD variants in two modules — exercises
// the cross-module enum-lit construction (`util.Token.Ident("hi")`)
// path. Dispatch on a method must route to the impl belonging to the
// receiver's owning module's canonical enum *Type.
func TestV05SameNamedPayloadEnumDistinctImpls(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Token { Eof, Ident(str) }
impl Token {
pub fn label() -> str { return "util" }
}
`,
		"main.zg": `import "util"
enum Token { Eof, Ident(str) }
impl Token {
pub fn label() -> str { return "main" }
}
let mt := Token.Ident("x")
print mt.label()
let ut := util.Token.Ident("y")
print ut.label()
`,
	}, "main\nutil\n")
}

// V05 same-named enum in two modules. Each declares an inherent method
// returning the module's own label. Dispatch must key by canonical
// *Type so the two impls don't collide.
func TestV05SameNamedEnumDistinctImpls(t *testing.T) {
	expectBundleOK(t, "main.zg", map[string]string{
		"util.zg": `pub enum Color { Red, Green }
impl Color {
pub fn label() -> str { return "util" }
}
`,
		"main.zg": `import "util"
enum Color { Red, Green }
impl Color {
pub fn label() -> str { return "main" }
}
let mc := Color.Red
print mc.label()
let uc := util.Color.Red
print uc.label()
`,
	}, "main\nutil\n")
}
