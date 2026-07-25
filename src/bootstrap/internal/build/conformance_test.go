package build

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestConformanceMultiModule is the Phase 1g module conformance corpus (S4): a
// three-module program (testdata/g10_modules) that must compile and run, exercising
// every cross-module shape in one build — a cross-module type named in another
// module's signature, a cross-module function returning it, a cross-module generic
// at a concrete argument type, a module-private helper used only inside its module
// (pub/private visibility), an init() block with a module constant evaluated at
// init, and an `import pub` re-export reached through a second namespace.
func TestConformanceMultiModule(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join("testdata", "g10_modules", "main.zg")
	code, manifest, diags := CompileProgram(entry)
	if len(diags) != 0 {
		t.Fatalf("multi-module corpus should compile, got diagnostics: %v", diags)
	}
	// A value-only program needs no runtime; its C stays a single-unit cc link.
	if manifest.NeedsRuntime {
		t.Fatalf("value-only corpus should need no runtime, got %+v", manifest)
	}
	// The imported modules are canonical-path mangled; no bare imported name leaks.
	for _, want := range []string{"zg_geom__make", "zg_geom__Point", "zg_app__base"} {
		if !strings.Contains(code, want) {
			t.Fatalf("imported items must be canonical-path mangled in the C, missing %q:\n%s", want, code)
		}
	}
	// init() runs eagerly before main's body, so the module constant is set (5) by
	// the time app.base() reads it; the re-exported geom.hundred() reaches 100.
	const want = "7\n42\n100\n5\n100\n"
	if got := compileAndRun(t, cc, code); got != want {
		t.Fatalf("multi-module corpus run: got %q, want %q", got, want)
	}
}
