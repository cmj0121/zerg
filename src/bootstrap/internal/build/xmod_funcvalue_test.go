package build

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCrossModuleFuncValueRuns is the loud-gap fix (Fix 2) corpus: a two-module program
// (testdata/xmod_funcvalue) that binds a function from another module as a first-class
// VALUE (`f := other.helper`) and calls it, and also passes such a value straight as an
// argument (`apply(other.helper, 20)`). Previously sema rejected a cross-module function
// value; it now emits the same env-taking thunk / closure value as a same-module one.
func TestCrossModuleFuncValueRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join("testdata", "xmod_funcvalue", "main.zg")
	code, manifest, diags := CompileProgram(entry)
	if len(diags) != 0 {
		t.Fatalf("cross-module func-value corpus should compile, got diagnostics: %v", diags)
	}
	// A value-only program needs no runtime; the closure value is plain data (env=NULL).
	if manifest.NeedsRuntime {
		t.Fatalf("value-only corpus should need no runtime, got %+v", manifest)
	}
	// The cross-module function is reached under its canonical merged name via a value
	// thunk (the same mechanism a same-module bare function value uses).
	if !strings.Contains(code, "zg_other__helper") {
		t.Fatalf("cross-module function must be canonical-path mangled:\n%s", code)
	}
	const want = "11\n21\n"
	if got := compileAndRun(t, cc, code); got != want {
		t.Fatalf("cross-module func-value run: got %q, want %q", got, want)
	}
}
