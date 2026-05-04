package syntax_test

// v0.5 Unit 3 typeck tests live in `package syntax_test` (not `package
// syntax`) so they exercise the public CheckBundle entry through the
// loader — that's how the real pipeline calls the multi-module check.
// Single-module fixtures use checkSrc/checkErr from the in-package test
// helpers via the syntax package's exported Check function instead.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// ---------------------------------------------------------------------------
// Multi-file fixture helpers.
//
// Each fixture writes a flat directory of .zg files via t.TempDir() and runs
// the loader → CheckBundle pipeline against the named entry file. Tests
// invoke loadOk for the success path and loadErr for the rejection path.
// The harness uses the same code path the real CLI does, so any cross-
// module behaviour is exercised through the actual entry point.
// ---------------------------------------------------------------------------

// fixture is a name → source map for a single test. The keys are filenames
// (without the .zg suffix); the values are the .zg source.
type fixture map[string]string

// writeFixture materialises f to a fresh temp directory and returns the
// directory path. The entry file is named "main.zg" — every multi-module
// fixture in v0.5 conventionally enters through main.
func writeFixture(t *testing.T, f fixture) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range f {
		full := filepath.Join(dir, name+".zg")
		if err := writeFileBytes(full, src); err != nil {
			t.Fatalf("writeFile(%s): %v", full, err)
		}
	}
	return dir
}

// writeFileBytes is a thin os.WriteFile wrapper kept local so the helper
// vocabulary mirrors loader_test.go.
func writeFileBytes(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// loadAndCheck loads dir/entry and runs CheckBundle on the bundle. The
// returned error is verbatim from the pipeline so tests can match its
// text.
func loadAndCheck(t *testing.T, dir, entry string) error {
	t.Helper()
	bundle, err := loader.Load(filepath.Join(dir, entry))
	if err != nil {
		return err
	}
	return syntax.CheckBundle(bundle)
}

// loadOk asserts that the fixture loads and type-checks cleanly.
func loadOk(t *testing.T, f fixture, entry string) {
	t.Helper()
	dir := writeFixture(t, f)
	if err := loadAndCheck(t, dir, entry); err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
}

// loadErr asserts that the fixture loads (or parses) but CheckBundle
// rejects with an error containing want. Loader-level errors are also
// admissible — the bundle never reaches typeck for a bad import path —
// but the test name should reflect that case.
func loadErr(t *testing.T, f fixture, entry, want string) string {
	t.Helper()
	dir := writeFixture(t, f)
	err := loadAndCheck(t, dir, entry)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Positive cases.
// ---------------------------------------------------------------------------

func TestCheckBundleCrossModuleFnCall(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub fn double(x: int) -> int { return x * 2 }\n",
		"main": "import \"util\"\nlet n := util.double(21)\nprint n\n",
	}, "main.zg")
}

func TestCheckBundleCrossModuleStructLit(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub struct Point { x: int, y: int }\n",
		"main": "import \"util\"\nlet p := util.Point { x: 1, y: 2 }\nprint p.x\n",
	}, "main.zg")
}

func TestCheckBundleCrossModuleEnumTypeRef(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub enum Color { Red, Blue }\n",
		"main": "import \"util\"\nlet c: util.Color = util.Color.Red\nprint c\n",
	}, "main.zg")
}

func TestCheckBundleCrossModuleEnumBareVariant(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub enum Color { Red, Blue }\n",
		"main": "import \"util\"\nlet c := util.Color.Red\nprint c\n",
	}, "main.zg")
}

func TestCheckBundleCrossModuleEnumPayloadVariant(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub enum Token { Eof, Ident(str) }\n",
		"main": "import \"util\"\nlet t := util.Token.Ident(\"hi\")\nprint t\n",
	}, "main.zg")
}

func TestCheckBundleCrossModuleSpecAsType(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Printable {\n" +
		"pub fn to_string() -> str { return \"counter\" }\n" +
		"}\n" +
		"let c := Counter { count: 1 }\n" +
		"let p: util.Printable = c\n" +
		"print p.to_string()\n"
	loadOk(t, fixture{
		"util": "pub spec Printable { fn to_string() -> str }\n",
		"main": src,
	}, "main.zg")
}

func TestCheckBundleCrossModuleMethodDispatch(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Printable {\n" +
		"pub fn to_string() -> str { return \"hi\" }\n" +
		"}\n" +
		"let c := Counter { count: 7 }\n" +
		"print c.to_string()\n"
	loadOk(t, fixture{
		"util": "pub spec Printable { fn to_string() -> str }\n",
		"main": src,
	}, "main.zg")
}

func TestCheckBundleAliasedImport(t *testing.T) {
	loadOk(t, fixture{
		"util": "pub fn foo() -> int { return 7 }\n",
		"main": "import \"util\" as u\nlet n := u.foo()\nprint n\n",
	}, "main.zg")
}

func TestCheckBundleNoImportsBackwardCompat(t *testing.T) {
	loadOk(t, fixture{
		"main": "let x := 42\nprint x\n",
	}, "main.zg")
}

func TestCheckBundleOrphanRuleAdmittedLocalType(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct Counter { count: int }\n" +
		"impl Counter for util.Printable {\n" +
		"pub fn to_string() -> str { return \"c\" }\n" +
		"}\n" +
		"let c := Counter { count: 1 }\n" +
		"print c.to_string()\n"
	loadOk(t, fixture{
		"util": "pub spec Printable { fn to_string() -> str }\n",
		"main": src,
	}, "main.zg")
}

func TestCheckBundleOrphanRuleAdmittedLocalSpec(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"spec Stringy { fn show() -> str }\n" +
		"impl util.Counter for Stringy {\n" +
		"pub fn show() -> str { return \"ok\" }\n" +
		"}\n"
	loadOk(t, fixture{
		"util": "pub struct Counter { count: int }\n",
		"main": src,
	}, "main.zg")
}

func TestCheckBundleInherentImplOnLocalType(t *testing.T) {
	src := "" +
		"struct Local { v: int }\n" +
		"impl Local {\n" +
		"fn show() -> int { return this.v }\n" +
		"}\n" +
		"let x := Local { v: 9 }\n" +
		"print x.show()\n"
	loadOk(t, fixture{
		"main": src,
	}, "main.zg")
}

// ---------------------------------------------------------------------------
// Negative cases.
// ---------------------------------------------------------------------------

func TestCheckBundleRejectNonPubFnCall(t *testing.T) {
	loadErr(t, fixture{
		"util": "fn private_fn() -> int { return 1 }\n",
		"main": "import \"util\"\nlet n := util.private_fn()\nprint n\n",
	}, "main.zg", "is not pub")
}

func TestCheckBundleRejectNonPubStructConstruct(t *testing.T) {
	loadErr(t, fixture{
		"util": "struct Hidden { v: int }\n",
		"main": "import \"util\"\nlet h := util.Hidden { v: 1 }\nprint h.v\n",
	}, "main.zg", "is not pub")
}

func TestCheckBundleRejectUnknownMember(t *testing.T) {
	loadErr(t, fixture{
		"util": "pub fn foo() -> int { return 1 }\n",
		"main": "import \"util\"\nlet n := util.bar()\nprint n\n",
	}, "main.zg", "no function")
}

func TestCheckBundleOrphanRejectsBothForeign(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"import \"other\"\n" +
		"impl util.T for other.S {\n" +
		"pub fn ping() -> int { return 0 }\n" +
		"}\n"
	loadErr(t, fixture{
		"util":  "pub struct T { v: int }\n",
		"other": "pub spec S { fn ping() -> int }\n",
		"main":  src,
	}, "main.zg", "cross-module orphan impl")
}

func TestCheckBundleOrphanRejectsForeignInherent(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"impl util.T {\n" +
		"fn rogue() -> int { return 1 }\n" +
		"}\n"
	loadErr(t, fixture{
		"util": "pub struct T { v: int }\n",
		"main": src,
	}, "main.zg", "cross-module orphan impl")
}

func TestCheckBundleCrossModuleImplCollisionRejectedByOrphan(t *testing.T) {
	// Composability check: when two modules try to implement the same
	// (foreign Type, foreign Spec) pair, the orphan rule fires before
	// the cross-module collision detector ever sees the pair. This is
	// expected — the orphan rule guarantees that natural duplicates
	// can't reach the collision pass. The collision detector is kept
	// in CheckBundle as defence-in-depth for future surfaces (re-
	// exports, etc.) where orphan won't reject. We pin the orphan
	// rejection here so any future loosening of orphan is force-checked
	// against this test.
	utilSrc := "" +
		"pub struct Counter { count: int }\n" +
		"pub spec Printable { fn to_string() -> str }\n"
	mainSrc := "" +
		"import \"util\"\n" +
		"impl util.Counter for util.Printable {\n" +
		"pub fn to_string() -> str { return \"a\" }\n" +
		"}\n"
	loadErr(t, fixture{
		"util": utilSrc,
		"main": mainSrc,
	}, "main.zg", "cross-module orphan impl")
}

func TestCheckBundleLetShadowsImport(t *testing.T) {
	loadErr(t, fixture{
		"u":    "pub fn foo() -> int { return 1 }\n",
		"main": "import \"u\"\nlet u := 1\nprint u\n",
	}, "main.zg", "shadows imported module")
}

func TestCheckBundleStructDeclShadowsImport(t *testing.T) {
	loadErr(t, fixture{
		"u":    "pub fn foo() -> int { return 1 }\n",
		"main": "import \"u\"\nstruct u { v: int }\n",
	}, "main.zg", "collides with top-level declaration")
}

func TestCheckBundleEnumDeclShadowsImport(t *testing.T) {
	loadErr(t, fixture{
		"color": "pub fn nothing() -> int { return 0 }\n",
		"main":  "import \"color\"\nenum color { Red, Blue }\n",
	}, "main.zg", "collides with top-level declaration")
}

func TestCheckBundleAliasCollisionTwoImports(t *testing.T) {
	src := "" +
		"import \"a\"\n" +
		"import \"b\" as a\n"
	loadErr(t, fixture{
		"a":    "pub fn foo() -> int { return 1 }\n",
		"b":    "pub fn bar() -> int { return 2 }\n",
		"main": src,
	}, "main.zg", "already declared")
}

func TestCheckBundleDuplicateImport(t *testing.T) {
	src := "" +
		"import \"a\"\n" +
		"import \"a\"\n"
	loadErr(t, fixture{
		"a":    "pub fn foo() -> int { return 1 }\n",
		"main": src,
	}, "main.zg", "already declared")
}

func TestCheckBundleSingleProgramRejectsUnknownModule(t *testing.T) {
	// A single-file program references mod.foo() with no `import "mod"`
	// — typeck must still reject cleanly. The failure is at the
	// MethodCall layer with `mod` resolving as an undefined name.
	loadErr(t, fixture{
		"main": "let x := mod.foo()\nprint x\n",
	}, "main.zg", "undefined name")
}

func TestCheckBundleRejectNonPubMethod(t *testing.T) {
	// Method dispatch on a foreign-typed receiver via inherent impl —
	// the inherent impl's method must be pub for the importing module to
	// reach it. Without pub, the call rejects with "method ... does not
	// exist" because the foreign visibility entry is filtered out.
	utilSrc := "" +
		"pub struct Counter { count: int }\n" +
		"impl Counter {\n" +
		"fn private_show() -> int { return this.count }\n" +
		"}\n"
	mainSrc := "" +
		"import \"util\"\n" +
		"let c := util.Counter { count: 1 }\n" +
		"print c.private_show()\n"
	loadErr(t, fixture{
		"util": utilSrc,
		"main": mainSrc,
	}, "main.zg", "does not exist")
}

func TestCheckBundleRejectNonPubSpecAsType(t *testing.T) {
	src := "" +
		"import \"util\"\n" +
		"struct C { v: int }\n" +
		"impl C for util.Hidden {\n" +
		"pub fn show() -> str { return \"x\" }\n" +
		"}\n"
	loadErr(t, fixture{
		"util": "spec Hidden { fn show() -> str }\n",
		"main": src,
	}, "main.zg", "is not pub")
}
