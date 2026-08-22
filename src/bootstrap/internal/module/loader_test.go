package module

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// memProvider is an in-memory source root: path -> module files, for exercising
// the loader without touching the filesystem.
type memProvider struct {
	mods map[string]string // canonical import path -> single-file source
}

func (p memProvider) Resolve(importPath string) (string, string, []ModuleFile, bool) {
	src, ok := p.mods[importPath]
	if !ok {
		return "", "", nil, false
	}
	// The map is keyed by module path, so every module here is a directory module and its
	// directory is that path — which is what a `./` import inside it would be relative to.
	return importPath, importPath, []ModuleFile{{Name: "mod.zg", Src: src}}, true
}

// topNames returns every top-level declaration name of a flattened file, so a test
// can assert which names were mangled and which stayed byte-identical.
func topNames(f *ast.File) []string {
	var out []string
	for _, it := range f.Items {
		switch n := it.(type) {
		case *ast.FuncDecl:
			out = append(out, n.Name)
		case *ast.StructDecl:
			out = append(out, n.Name)
		case *ast.EnumDecl:
			out = append(out, n.Name)
		}
	}
	return out
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestNoImportIsUnchanged(t *testing.T) {
	l := NewLoader(memProvider{})
	file, diags := l.LoadSource("fn main() {\n  print 1\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	names := topNames(file)
	if len(names) != 1 || names[0] != "main" {
		t.Fatalf("a no-import program must keep its own names, got %v", names)
	}
}

func TestCrossModuleFlattenMangling(t *testing.T) {
	root := memProvider{mods: map[string]string{
		"util/text": "pub struct Pair {\n  pub lo: int\n}\npub fn make() -> Pair {\n  return Pair(lo: 1)\n}\n",
	}}
	l := NewLoader(root)
	file, diags := l.LoadSource("import \"util/text\"\nfn main() {\n  p := text.make()\n  print p.lo\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	names := topNames(file)
	// The entry module's names stay byte-identical; the imported module's items are
	// mangled by canonical path, so the return type inside make() also resolves.
	if !has(names, "main") {
		t.Fatalf("entry name 'main' must be unchanged, got %v", names)
	}
	if !has(names, "util_text__make") || !has(names, "util_text__Pair") {
		t.Fatalf("imported items must be canonical-path mangled, got %v", names)
	}
	if has(names, "make") || has(names, "Pair") {
		t.Fatalf("imported items must not keep their bare names, got %v", names)
	}
}

func TestImportSpecTagAssigned(t *testing.T) {
	root := memProvider{mods: map[string]string{"util/text": "pub fn f() {\n}\n"}}
	l := NewLoader(root)
	file, diags := l.LoadSource("import \"util/text\" as t\nfn main() {\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, it := range file.Items {
		if imp, ok := it.(*ast.ImportStmt); ok {
			if got := imp.Specs[0].Module; got != "util_text" {
				t.Fatalf("spec.Module = %q, want the canonical tag %q", got, "util_text")
			}
			return
		}
	}
	t.Fatal("entry import statement not found in the flattened file")
}

func TestUnresolvedImportDiagnoses(t *testing.T) {
	l := NewLoader(memProvider{})
	_, diags := l.LoadSource("import \"no/such\"\nfn main() {\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "cannot resolve import") {
		t.Fatalf("want an unresolved-import diagnostic, got %v", diags)
	}
}

func TestImportCycleDetected(t *testing.T) {
	root := memProvider{mods: map[string]string{
		"a": "import \"b\"\npub fn fa() {\n}\n",
		"b": "import \"a\"\npub fn fb() {\n}\n",
	}}
	l := NewLoader(root)
	_, diags := l.LoadSource("import \"a\"\nfn main() {\n}\n")
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "import cycle") {
		t.Fatalf("want an import-cycle diagnostic, got %v", diags)
	}
}

func TestTransitiveImportLoaded(t *testing.T) {
	root := memProvider{mods: map[string]string{
		"a": "import \"b\"\npub fn fa() {\n}\n",
		"b": "pub fn fb() {\n}\n",
	}}
	l := NewLoader(root)
	file, diags := l.LoadSource("import \"a\"\nfn main() {\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	names := topNames(file)
	if !has(names, "a__fa") || !has(names, "b__fb") {
		t.Fatalf("a transitive module must be flattened too, got %v", names)
	}
}
