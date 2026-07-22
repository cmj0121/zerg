package module

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// TestInitPlanTopologicalOrder checks the init plan lists only modules that own an
// init() block or a module constant, in dependency (topological) order — a
// dependency before the module that imports it, and the entry module last.
func TestInitPlanTopologicalOrder(t *testing.T) {
	root := memProvider{mods: map[string]string{
		// base owns a module constant; mid imports base and owns an init(); leaf owns
		// nothing and must not appear in the plan.
		"base": "seed := 1\n",
		"mid":  "import \"base\"\ninit() {\n  print 1\n}\n",
		"leaf": "pub fn noop() {\n  nop\n}\n",
	}}
	l := NewLoader(root)
	_, plan, diags := l.LoadProgram("import \"mid\"\nimport \"leaf\"\nseed2 := 2\nfn main() {\n  nop\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var tags []string
	for _, m := range plan.Modules {
		tags = append(tags, m.Tag)
	}
	// base (constant) before mid (init, imports base); entry ("", constant seed2) last;
	// leaf (no init/constant) absent.
	want := []string{"base", "mid", ""}
	if len(tags) != len(want) {
		t.Fatalf("init plan tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("init plan order = %v, want %v", tags, want)
		}
	}
}

// TestReexportWiring checks a `import pub` module's re-export tags are wired onto
// the import spec of a module that imports it (one level), so sema can resolve a
// re-exported member through the importing namespace.
func TestReexportWiring(t *testing.T) {
	root := memProvider{mods: map[string]string{
		"deep": "pub fn hello() -> int {\n  return 7\n}\n",
		"mid":  "import pub \"deep\"\n",
	}}
	l := NewLoader(root)
	file, _, diags := l.LoadProgram("import \"mid\"\nfn main() {\n  print mid.hello()\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	// The entry's `import "mid"` spec must carry deep's tag as a re-export.
	specs := importSpecs([]*ast.File{file})
	found := false
	for _, s := range specs {
		if s.Module == "mid" {
			for _, r := range s.Reexports {
				if r == "deep" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected the mid import to re-export deep, specs: %+v", specs)
	}
}
