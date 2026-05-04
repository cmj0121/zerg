// v0.5 Unit 6 codegen tests — module-mangled symbol names + cross-module
// fn / struct / enum / method / spec dispatch in one merged TU.
//
// The mangler is exercised directly at the string level. Multi-module
// codegen tests build a small fixture tree under t.TempDir(), run the
// loader + typeck + codegen path through Build (or its EmitBundle entry),
// then compile the emitted .c with the same `cc` Build uses and compare
// stdout against `RunBundle`'s output for the same fixture. Both halves
// must produce byte-identical output — that's the parity contract.
package build

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/run"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// ---------------------------------------------------------------------------
// Mangler tests (string-level — no compilation, no parity check).
// ---------------------------------------------------------------------------

// TestV05ModuleMangleEntryName — `mangleModule("main")` is the literal
// "main" (no leading-digit prepend) plus the FNV-1a suffix. The leading
// "m_" prepend rule does NOT fire on "main" because the name starts with
// a non-digit byte.
func TestV05ModuleMangleEntryName(t *testing.T) {
	got := mangleModule("main")
	if !strings.HasPrefix(got, "main_h") {
		t.Errorf("entry mangle should start with `main_h`; got %q", got)
	}
	if strings.HasPrefix(got, "m_main") {
		t.Errorf("entry mangle should NOT have the m_ leading-digit prepend; got %q", got)
	}
	hex := got[len("main_h"):]
	if len(hex) != 8 {
		t.Errorf("entry mangle should end with 8 hex chars; got %q (suffix len %d)", got, len(hex))
	}
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hex8 has non-hex char %q in %q", c, got)
		}
	}
}

// TestV05ModuleMangleHyphenLeadingDigit — basename whose post-strip
// begins with a digit gets the "m_" prepend. PLAN.md examples line.
func TestV05ModuleMangleHyphenLeadingDigit(t *testing.T) {
	got := mangleModule("2d-math.zg")
	if !strings.HasPrefix(got, "m_2d_math_h") {
		t.Errorf("hyphen + leading-digit basename mangle should be m_2d_math_h<hex>; got %q", got)
	}
	if !strings.Contains(got, "2d_math") {
		t.Errorf("mangle should contain canonical replaced name `2d_math`; got %q", got)
	}
	if len(got) != len("m_2d_math_h")+8 {
		t.Errorf("trailing hash should be 8 hex chars; got %q", got)
	}
}

// TestV05ModuleMangleNonASCII — every non-[A-Za-z0-9] byte is replaced
// with `_`. A 3-byte UTF-8 character produces 3 underscores.
func TestV05ModuleMangleNonASCII(t *testing.T) {
	got := mangleModule("/中文.zg")
	// "/中文" → "/" + 3 bytes for 中 + 3 bytes for 文 = 7 bytes, all replaced
	// → "_______". Trailing `_h<hex8>`.
	if !strings.HasPrefix(got, "_______") {
		t.Errorf("non-ASCII mangle should be all underscores; got %q", got)
	}
	if !strings.Contains(got, "_h") {
		t.Errorf("mangle should contain hash separator `_h`; got %q", got)
	}
}

// TestV05ModuleMangleDeterministic — same input produces same output
// across calls. The implementation is purely a function of the bytes.
func TestV05ModuleMangleDeterministic(t *testing.T) {
	in := "/some/path/util.zg"
	a := mangleModule(in)
	b := mangleModule(in)
	if a != b {
		t.Errorf("mangleModule is not deterministic: %q vs %q", a, b)
	}
}

// TestV05ModuleMangleHashDistinguishesPaths — two paths with the same
// basename produce different mangles via the FNV-1a suffix.
func TestV05ModuleMangleHashDistinguishesPaths(t *testing.T) {
	a := mangleModule("/aaa/util.zg")
	b := mangleModule("/bbb/util.zg")
	if a == b {
		t.Errorf("mangle should differ for different absolute paths sharing basename: both %q", a)
	}
	// Both should still contain `util` in the body somewhere.
	if !strings.Contains(a, "util") || !strings.Contains(b, "util") {
		t.Errorf("expected both to contain 'util'; got %q and %q", a, b)
	}
}

// ---------------------------------------------------------------------------
// Multi-module codegen / build / parity tests.
// ---------------------------------------------------------------------------

// buildBundleFromFiles materialises a multi-file fixture under t.TempDir(),
// loads it via the loader, type-checks the bundle, emits the merged C TU
// to a temp .c, compiles it, runs the binary, and returns the binary's
// stdout. The fixture's entry file is `entry`. cc is resolved through
// DefaultCC (mirrors Build).
func buildBundleFromFiles(t *testing.T, entry string, files map[string]string) (stdout string, err error) {
	t.Helper()
	cc := DefaultCC()
	if _, lerr := exec.LookPath(cc); lerr != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if werr := os.WriteFile(p, []byte(content), 0o644); werr != nil {
			t.Fatalf("write %s: %v", p, werr)
		}
	}
	bundle, lerr := loader.Load(filepath.Join(dir, entry))
	if lerr != nil {
		return "", fmt.Errorf("loader.Load: %w", lerr)
	}
	if cerr := syntax.CheckBundle(bundle); cerr != nil {
		return "", fmt.Errorf("CheckBundle: %w", cerr)
	}
	cPath := filepath.Join(dir, "merged.c")
	cFile, ferr := os.Create(cPath)
	if ferr != nil {
		t.Fatalf("create %s: %v", cPath, ferr)
	}
	if eerr := EmitBundle(bundle, cFile); eerr != nil {
		cFile.Close()
		return "", fmt.Errorf("EmitBundle: %w", eerr)
	}
	cFile.Close()
	binPath := filepath.Join(dir, "merged")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if cerr := cmd.Run(); cerr != nil {
		// Surface the .c so an inspecting reader can debug.
		c, _ := os.ReadFile(cPath)
		return "", fmt.Errorf("cc failed: %w\n--- merged.c ---\n%s", cerr, c)
	}
	out, runErr := exec.Command(binPath).CombinedOutput()
	return string(out), runErr
}

// runBundleFromFiles materialises the same fixture and runs it through
// the v0.5 interpreter. Returns interpreter stdout (the parity reference).
func runBundleFromFiles(t *testing.T, entry string, files map[string]string) (stdout string, err error) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if werr := os.WriteFile(p, []byte(content), 0o644); werr != nil {
			t.Fatalf("write %s: %v", p, werr)
		}
	}
	bundle, lerr := loader.Load(filepath.Join(dir, entry))
	if lerr != nil {
		return "", lerr
	}
	if cerr := syntax.CheckBundle(bundle); cerr != nil {
		return "", cerr
	}
	var buf bytes.Buffer
	if rerr := run.RunBundle(bundle, &buf); rerr != nil {
		return buf.String(), rerr
	}
	return buf.String(), nil
}

// expectV05Parity asserts both `RunBundle` and the `EmitBundle`-compiled
// binary produce the same `want` stdout for the fixture. This is the v0.5
// parity contract: interp and build emit identical bytes for every
// supported program.
func expectV05Parity(t *testing.T, entry string, files map[string]string, want string) {
	t.Helper()
	gotRun, rerr := runBundleFromFiles(t, entry, files)
	if rerr != nil {
		t.Fatalf("RunBundle failed: %v", rerr)
	}
	if gotRun != want {
		t.Fatalf("RunBundle stdout = %q, want %q", gotRun, want)
	}
	gotBuild, berr := buildBundleFromFiles(t, entry, files)
	if berr != nil {
		t.Fatalf("Build failed: %v\nbuild output: %q", berr, gotBuild)
	}
	if gotBuild != want {
		t.Fatalf("Build stdout = %q, want %q", gotBuild, want)
	}
	if gotRun != gotBuild {
		t.Fatalf("parity mismatch: run=%q build=%q", gotRun, gotBuild)
	}
}

// 1. Cross-module fn call (the headline smoke test).
func TestV05CodegenCrossModuleFnCall(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub fn add(a: int, b: int) -> int { return a + b }
`,
		"main.zg": `import "util"
print util.add(1, 2)
`,
	}, "3\n")
}

// 2. Cross-module struct construction + field access.
func TestV05CodegenCrossModuleStructLit(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub struct Counter { count: int }
`,
		"main.zg": `import "util"
let c := util.Counter { count: 5 }
print c.count
`,
	}, "5\n")
}

// 3. Cross-module struct + inherent method dispatch.
func TestV05CodegenCrossModuleStructMethod(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub struct Counter { count: int }
impl Counter {
pub fn doubled() -> int { return this.count * 2 }
}
`,
		"main.zg": `import "util"
let c := util.Counter { count: 7 }
print c.doubled()
`,
	}, "14\n")
}

// 4. Cross-module bare-variant enum.
func TestV05CodegenCrossModuleBareEnum(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub enum Color { Red, Green, Blue }
`,
		"main.zg": `import "util"
print util.Color.Red
`,
	}, "Color.Red\n")
}

// 5. Cross-module enum payload + match destructure.
func TestV05CodegenCrossModulePayloadEnumMatch(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
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

// 6. Cross-module spec impl: main impls util.Printable for main.Greeting,
// dispatch via spec-typed binding.
func TestV05CodegenCrossModuleSpecImpl(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub spec Printable {
pub fn to_string() -> str
}
`,
		"main.zg": `import "util"
struct Greeting { word: str }
impl Greeting for util.Printable {
pub fn to_string() -> str { return this.word }
}
let g := Greeting { word: "hello" }
let p: util.Printable = g
print p.to_string()
`,
	}, "hello\n")
}

// 7. Aliased import.
func TestV05CodegenAliasedImport(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub fn answer() -> int { return 42 }
`,
		"main.zg": `import "util" as u
print u.answer()
`,
	}, "42\n")
}

// 8. Cross-module result-style enum match.
func TestV05CodegenCrossModuleResultMatch(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub enum Result {
Ok(int),
Err(str),
}
pub fn parse(input: str) -> Result {
if input == "good" { return Result.Ok(7) }
return Result.Err("bad")
}
`,
		"main.zg": `import "util"
let r := util.parse("good")
match r {
Result.Ok(v) => print v
Result.Err(e) => print e
}
let s := util.parse("nope")
match s {
Result.Ok(v) => print v
Result.Err(e) => print e
}
`,
	}, "7\nbad\n")
}

// 9. List-of-spec heterogeneous dispatch with two-module impl coverage.
func TestV05CodegenListOfSpecHeterogeneous(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"shared.zg": `pub spec Tagged {
pub fn tag() -> str
}
`,
		"a.zg": `import "shared"
pub struct A { v: int }
impl A for shared.Tagged {
pub fn tag() -> str { return "A" }
}
`,
		"b.zg": `import "shared"
pub struct B { v: int }
impl B for shared.Tagged {
pub fn tag() -> str { return "B" }
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

// 10. Backward-compat: a single-file program with no imports continues to
// emit identical output (just with the new module-mangle prefix on
// composite type names).
func TestV05CodegenSingleFileBackwardCompat(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"main.zg": `fn double(x: int) -> int { return x * 2 }
print double(21)
`,
	}, "42\n")
}

// 11. Cross-module fn call where the foreign fn calls back into its own
// module's helpers — verifies the codegen routes the helper call through
// the right module's fn table.
func TestV05CodegenForeignFnCallsOwnModule(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `fn helper(x: int) -> int { return x + 100 }
pub fn wrap(y: int) -> int { return helper(y) }
`,
		"main.zg": `import "util"
print util.wrap(5)
`,
	}, "105\n")
}

// 12. Cross-module method body resolves the receiver's own module's
// enums (no module prefix in the body — the lookup must route through
// the type's owning module).
func TestV05CodegenMethodBodyResolvesOwnEnum(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
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

// 13. Two modules, each with a same-named struct, both with their own
// inherent impl. Codegen must keep the two `Counter` types' methods
// distinct in the merged TU. This test inspects the emitted C source
// rather than running the program: the v0.5 interpreter has its own
// resolution gap with same-named structs that is owned by the
// interp/loader path (Unit 5/7), not codegen. The codegen contract is
// that the merged TU contains two *distinct* mangled symbols for the
// two `Counter::show` methods.
func TestV05CodegenSameNamedStructDistinctMangle(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.zg": `pub struct Counter { v: int }
impl Counter {
pub fn show() -> str { return "a" }
}
`,
		"b.zg": `pub struct Counter { v: int }
impl Counter {
pub fn show() -> str { return "b" }
}
`,
		"main.zg": `import "a"
import "b"
let ca := a.Counter { v: 1 }
let cb := b.Counter { v: 2 }
print ca.v
print cb.v
`,
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if werr := os.WriteFile(p, []byte(content), 0o644); werr != nil {
			t.Fatalf("write %s: %v", p, werr)
		}
	}
	bundle, lerr := loader.Load(filepath.Join(dir, "main.zg"))
	if lerr != nil {
		t.Fatalf("loader.Load: %v", lerr)
	}
	if cerr := syntax.CheckBundle(bundle); cerr != nil {
		t.Fatalf("CheckBundle: %v", cerr)
	}
	var buf bytes.Buffer
	if eerr := EmitBundle(bundle, &buf); eerr != nil {
		t.Fatalf("EmitBundle: %v", eerr)
	}
	out := buf.String()
	// Two distinct module mangles must both appear, each owning a
	// `__Counter__show` method symbol.
	aMangle := mangleModule(filepath.Join(dir, "a.zg"))
	bMangle := mangleModule(filepath.Join(dir, "b.zg"))
	if aMangle == bMangle {
		t.Fatalf("same-named modules from different files should mangle distinctly")
	}
	wantA := "zerg_struct_" + aMangle + "__Counter__show"
	wantB := "zerg_struct_" + bMangle + "__Counter__show"
	if !strings.Contains(out, wantA) {
		t.Errorf("emitted TU missing %q; got:\n%s", wantA, out)
	}
	if !strings.Contains(out, wantB) {
		t.Errorf("emitted TU missing %q; got:\n%s", wantB, out)
	}
}

// 13b. Two modules each declaring the same-named struct with their own
// inherent method. Method dispatch must route to the receiver's own
// module's impl on both halves of the toolchain — this is the
// end-to-end parity twin of TestV05CodegenSameNamedStructDistinctMangle
// (which checked symbol presence). RunBundle's bug surfaced here was
// keying impl tables by bare typename; the fix re-keys by canonical
// *Type pointer (typeck stamps id.Receiver) so both halves agree.
func TestV05ParitySameNamedStructDispatch(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
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

// TestV05ParitySameNamedEnumDispatch — same shape as the struct twin but
// for enums. Two modules each declare `enum Status` and impl an inherent
// method. Codegen must mangle each impl method against the receiver's
// defining-module mangle so the two C symbols stay distinct (the bug
// before this fix collapsed them onto the entry module's mangle, producing
// a `cc` redefinition error).
func TestV05ParitySameNamedEnumDispatch(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"util.zg": `pub enum Status { Active, Done }
impl Status {
pub fn label() -> str { return "util" }
}
`,
		"main.zg": `import "util"
enum Status { Active, Done }
impl Status {
pub fn label() -> str { return "main" }
}
let ms := Status.Active
print ms.label()
let us := util.Status.Done
print us.label()
`,
	}, "main\nutil\n")
}

// TestV05ParitySameNamedPayloadEnumDispatch — payload-carrying twin of
// TestV05ParitySameNamedEnumDispatch. Same root cause: cross-module enum
// receiver's mangle must be the defining module's, not the entry's.
func TestV05ParitySameNamedPayloadEnumDispatch(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
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

// 14. Diamond import — main imports b and c, both import d. d's pub fn
// must be reachable from main and produce the same value through both
// paths.
func TestV05CodegenDiamondImport(t *testing.T) {
	expectV05Parity(t, "main.zg", map[string]string{
		"d.zg": `pub fn answer() -> int { return 42 }
`,
		"b.zg": `import "d"
pub fn from_b() -> int { return d.answer() + 1 }
`,
		"c.zg": `import "d"
pub fn from_c() -> int { return d.answer() + 2 }
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
