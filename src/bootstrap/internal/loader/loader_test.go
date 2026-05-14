// Tests for the v0.5 module loader. We exercise both the happy path
// (single file, two-file, diamond, transitive, grouped, aliased) and the
// reject-path (cycles of length 1/2/3, missing siblings, malformed paths,
// future-version requires).
//
// Fixtures are written into t.TempDir() so the tests are hermetic and run
// in parallel without contention.
package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// writeFile materialises a fixture file under dir. Errors are fatal.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

// findResolvedImport returns the first ResolvedImport in mod whose
// LocalName matches; t.Fatal if not found.
func findResolvedImport(t *testing.T, mod *Module, localName string) *ResolvedImport {
	t.Helper()
	for _, ri := range mod.Imports {
		if ri.LocalName == localName {
			return ri
		}
	}
	t.Fatalf("module %q has no resolved import with LocalName %q (have: %v)",
		mod.Name, localName, importNames(mod))
	return nil
}

func importNames(mod *Module) []string {
	out := make([]string, 0, len(mod.Imports))
	for _, ri := range mod.Imports {
		out = append(out, ri.LocalName)
	}
	return out
}

// ---------------------------------------------------------------------------
// Positive cases.
// ---------------------------------------------------------------------------

// Single-file: no imports → bundle has 1 module, Entry.Name == "main".
func TestLoadSingleFile(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg", "print 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 1 {
		t.Errorf("len(Modules) = %d, want 1", got)
	}
	if bundle.Entry.Name != "main" {
		t.Errorf("Entry.Name = %q, want %q", bundle.Entry.Name, "main")
	}
	if got := len(bundle.Entry.Imports); got != 0 {
		t.Errorf("Entry.Imports = %d entries, want 0", got)
	}
}

// Two-file: main → util. Bundle has 2 modules; LocalName == "util".
func TestLoadTwoFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "util.zg", "fn double(x: int) -> int { return x * 2 }\n")
	entry := writeFile(t, dir, "main.zg", "import \"util\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 2 {
		t.Fatalf("len(Modules) = %d, want 2", got)
	}
	if got := len(bundle.Entry.Imports); got != 1 {
		t.Fatalf("Entry.Imports = %d, want 1", got)
	}
	ri := bundle.Entry.Imports[0]
	if ri.LocalName != "util" {
		t.Errorf("LocalName = %q, want %q", ri.LocalName, "util")
	}
	if ri.Target == nil {
		t.Fatalf("ResolvedImport.Target is nil")
	}
	if ri.Target.ShortName != "util" {
		t.Errorf("Target.ShortName = %q, want %q", ri.Target.ShortName, "util")
	}
	if ri.Decl == nil || ri.Decl.Path != "util" {
		t.Errorf("Decl.Path = %v, want \"util\"", ri.Decl)
	}
}

// Aliased: `import "util" as u` → LocalName == "u".
func TestLoadAliasedImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "util.zg", "fn x() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg", "import \"util\" as u\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := bundle.Entry.Imports[0]
	if ri.LocalName != "u" {
		t.Errorf("LocalName = %q, want %q", ri.LocalName, "u")
	}
	if ri.Target.ShortName != "util" {
		t.Errorf("Target.ShortName = %q, want %q", ri.Target.ShortName, "util")
	}
}

// `import "name"` and `import "name" as name` admit; both have LocalName ==
// "name". We verify that the bare form behaves the same as the explicit
// alias-equals-path form.
func TestLoadBareEqualsAliasMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "util.zg", "fn x() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg", "import \"util\" as util\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ri := bundle.Entry.Imports[0]
	if ri.LocalName != "util" {
		t.Errorf("LocalName = %q, want %q", ri.LocalName, "util")
	}
}

// Diamond: A imports B and C; both B and C import D. D is parsed once;
// B.Imports[0].Target == C.Imports[0].Target.
func TestLoadDiamond(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "d.zg", "fn d() -> int { return 4 }\n")
	writeFile(t, dir, "b.zg", "import \"d\"\nfn b() -> int { return 2 }\n")
	writeFile(t, dir, "c.zg", "import \"d\"\nfn c() -> int { return 3 }\n")
	entry := writeFile(t, dir, "a.zg",
		"import \"b\"\nimport \"c\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 4 {
		t.Fatalf("len(Modules) = %d, want 4", got)
	}
	b := findResolvedImport(t, bundle.Entry, "b").Target
	c := findResolvedImport(t, bundle.Entry, "c").Target
	if len(b.Imports) != 1 || len(c.Imports) != 1 {
		t.Fatalf("expected one import each from b and c; got b=%d c=%d",
			len(b.Imports), len(c.Imports))
	}
	if b.Imports[0].Target != c.Imports[0].Target {
		t.Errorf("d should be parsed once; got distinct *Module pointers: %p vs %p",
			b.Imports[0].Target, c.Imports[0].Target)
	}
	if b.Imports[0].Target.ShortName != "d" {
		t.Errorf("Target.ShortName = %q, want %q",
			b.Imports[0].Target.ShortName, "d")
	}
}

// Transitive A → B → C: bundle has 3 modules.
func TestLoadTransitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "c.zg", "fn c() -> int { return 3 }\n")
	writeFile(t, dir, "b.zg", "import \"c\"\nfn b() -> int { return 2 }\n")
	entry := writeFile(t, dir, "a.zg",
		"import \"b\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 3 {
		t.Fatalf("len(Modules) = %d, want 3", got)
	}
	short := map[string]bool{}
	for _, m := range bundle.Modules {
		short[m.ShortName] = true
	}
	for _, want := range []string{"main", "b", "c"} {
		if !short[want] {
			t.Errorf("missing module ShortName %q (have: %v)", want, short)
		}
	}
}

// Grouped: import (...) produces one ResolvedImport per entry.
func TestLoadGroupedImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.zg", "fn a() -> int { return 1 }\n")
	writeFile(t, dir, "b.zg", "fn b() -> int { return 2 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import (\n    \"a\"\n    \"b\"\n)\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Entry.Imports); got != 2 {
		t.Fatalf("len(Entry.Imports) = %d, want 2", got)
	}
	if bundle.Entry.Imports[0].LocalName != "a" ||
		bundle.Entry.Imports[1].LocalName != "b" {
		t.Errorf("LocalNames = %q,%q; want a,b",
			bundle.Entry.Imports[0].LocalName,
			bundle.Entry.Imports[1].LocalName)
	}
}

// Empty group: `import ()` desugars to zero ImportDecls; loader returns a
// 1-module bundle.
func TestLoadEmptyGroup(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg", "import ()\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 1 {
		t.Errorf("len(Modules) = %d, want 1", got)
	}
}

// Entry has Source and Program populated.
func TestLoadEntryFieldsPopulated(t *testing.T) {
	dir := t.TempDir()
	src := "print 42\n"
	entry := writeFile(t, dir, "main.zg", src)

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(bundle.Entry.Source) != src {
		t.Errorf("Entry.Source = %q, want %q", bundle.Entry.Source, src)
	}
	if bundle.Entry.Program == nil {
		t.Errorf("Entry.Program is nil")
	}
	// First statement is a PrintStmt — sanity-check we parsed.
	if _, ok := bundle.Entry.Program.Statements[0].(*syntax.PrintStmt); !ok {
		t.Errorf("Statements[0] = %T, want *syntax.PrintStmt",
			bundle.Entry.Program.Statements[0])
	}
}

// ---------------------------------------------------------------------------
// Negative cases — cycles.
// ---------------------------------------------------------------------------

// A imports A: minimal self-cycle.
func TestLoadSelfCycle(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"main\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "import cycle detected") {
		t.Errorf("error does not mention 'import cycle detected': %s", msg)
	}
	if !strings.Contains(msg, "main.zg imports main.zg") {
		t.Errorf("error does not list the self-edge: %s", msg)
	}
}

// A → B → A.
func TestLoadTwoCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.zg",
		"import \"a\"\nfn b() -> int { return 2 }\n")
	entry := writeFile(t, dir, "a.zg",
		"import \"b\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"import cycle detected", "main.zg imports b.zg", "b.zg imports"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q\n%s", want, msg)
		}
	}
}

// A → B → C → A: 3-node cycle.
func TestLoadThreeCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "c.zg",
		"import \"a\"\nfn c() -> int { return 3 }\n")
	writeFile(t, dir, "b.zg",
		"import \"c\"\nfn b() -> int { return 2 }\n")
	entry := writeFile(t, dir, "a.zg",
		"import \"b\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"import cycle detected",
		"main.zg imports b.zg",
		"b.zg imports c.zg",
		"c.zg imports",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q\n%s", want, msg)
		}
	}
}

// ---------------------------------------------------------------------------
// Negative cases — resolution.
// ---------------------------------------------------------------------------

// Sibling not on disk: clear "not found" diagnostic.
func TestLoadMissingSibling(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"missing\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected missing-sibling error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "module \"missing\" not found") {
		t.Errorf("error does not mention missing module: %s", msg)
	}
}

// Path with slash: v0.17 admits one segment of stdlib nesting
// (`"math/big"`) as a strict superset of the v0.5 flat-only rule.
// A single-slash path now routes through the std/* fall-through;
// unknown nested paths surface as the standard not-found wording.
// Deeper nesting (two or more slashes) still rejects with the
// invalid-path diagnostic — loader_v0_17_test.go pins that branch.
func TestLoadSlashPathRoutesToStdlibFallthrough(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"a/b\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stdlib module not found") {
		t.Errorf("error does not surface stdlib-fall-through wording: %s", msg)
	}
	if !strings.Contains(msg, "std/a/b") {
		t.Errorf("error does not name the synthesised stdlib path: %s", msg)
	}
}

// Path with dot: `import "a.b"`.
func TestLoadDotPathRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"a.b\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected flat-only rejection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "v0.5 supports flat sibling imports only") {
		t.Errorf("error does not mention the v0.5 flat-only rule: %s", msg)
	}
}

// Non-identifier path: `import "123abc"` — leading digit.
func TestLoadLeadingDigitPathRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"123abc\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected flat-only rejection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "v0.5 supports flat sibling imports only") {
		t.Errorf("error does not mention the v0.5 flat-only rule: %s", msg)
	}
}

// `# requires: v0.99` in an imported module: rejected with the standard
// version-gate diagnostic, anchored on the importing module's ImportDecl.
func TestLoadImportRequiresFutureVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "future.zg",
		"# requires: v0.99\nfn x() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"future\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected requires-future rejection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "requires v0.99") {
		t.Errorf("error does not mention v0.99: %s", msg)
	}
}

// Entry file `# requires: v0.99` rejected at the loader level too.
func TestLoadEntryRequiresFutureVersion(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"# requires: v0.99\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected requires-future rejection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "requires v0.99") {
		t.Errorf("error does not mention v0.99: %s", msg)
	}
}

// A v0.5 entry that imports a v0.4 module is admitted (the imported
// module's surface is a strict subset of v0.5).
func TestLoadImportRequiresPastVersionAdmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "older.zg",
		"# requires: v0.4\nfn x() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"older\"\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 2 {
		t.Errorf("len(Modules) = %d, want 2", got)
	}
}

// Empty path is rejected (`import ""` is a degenerate v0.5-flat-only
// failure — empty isn't a valid identifier).
func TestLoadEmptyPathRejected(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "main.zg",
		"import \"\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected rejection of empty path, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "v0.5 supports flat sibling imports only") {
		t.Errorf("error does not mention the v0.5 flat-only rule: %s", msg)
	}
}

// Module dedup: two ImportDecls in the same file resolving to the same
// sibling. Bundle has 2 modules; both ResolvedImport.Target pointers
// reference the same *Module.
func TestLoadDedupSameImportTwice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "util.zg", "fn x() -> int { return 1 }\n")
	entry := writeFile(t, dir, "main.zg",
		"import \"util\"\nimport \"util\" as u\nprint 1\n")

	bundle, err := Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(bundle.Modules); got != 2 {
		t.Fatalf("len(Modules) = %d, want 2", got)
	}
	if got := len(bundle.Entry.Imports); got != 2 {
		t.Fatalf("len(Entry.Imports) = %d, want 2", got)
	}
	if bundle.Entry.Imports[0].Target != bundle.Entry.Imports[1].Target {
		t.Errorf("util parsed twice; expected pointer dedup")
	}
}

// Diagnostic anchors on the importing module's ImportDecl: error message
// includes the `line:col` of the ImportDecl when the failure is import-
// scoped (missing sibling, flat-only, requires-future).
func TestLoadDiagnosticAnchorsOnImport(t *testing.T) {
	dir := t.TempDir()
	// Pad the entry with a leading blank line so the import is on line 2.
	entry := writeFile(t, dir, "main.zg",
		"\nimport \"missing\"\nprint 1\n")

	_, err := Load(entry)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("error does not anchor on line 2: %s", err.Error())
	}
}
