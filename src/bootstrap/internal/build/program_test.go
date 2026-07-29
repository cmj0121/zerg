package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTwoModuleProgramRuns is the whole-program guard: the committed two-module
// example (examples/modules/main.zg importing the sibling directory module
// util/text) must compile and run, exercising a cross-module function returning a
// real module type, that type named in another module's signature, and a
// cross-module generic monomorphized at two call sites.
func TestTwoModuleProgramRuns(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join(root, "examples", "modules", "main.zg")
	if _, err := os.Stat(entry); err != nil {
		t.Skipf("two-module example not present: %v", err)
	}
	code, _, diags := CompileProgram(entry)
	if len(diags) != 0 {
		t.Fatalf("two-module program should compile, got diagnostics: %v", diags)
	}
	// The entry module keeps its bare names; the imported module is canonical-path
	// mangled (util/text -> util_text), never leaking a bare imported name.
	if !strings.Contains(code, "zg_util_text__make") || !strings.Contains(code, "zg_util_text__Pair") {
		t.Fatalf("imported items must be canonical-path mangled in the C:\n%s", code)
	}
	if got := compileAndRun(t, cc, code); got != "7\n100\n7\n" {
		t.Fatalf("two-module run: got %q, want %q", got, "7\n100\n7\n")
	}
}

// TestCompileProgramMatchesCompile is the byte-identical guard for the driver's
// switch to CompileProgram: a single-file example with no import must emit exactly
// the same C through CompileProgram (the on-disk multi-module entry point) as
// through Compile (the string entry point) — the flatten appends nothing and
// mangles nothing when there is no cross-module import.
func TestCompileProgramMatchesCompile(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	sources, err := filepath.Glob(filepath.Join(root, "examples", "[0-9][0-9]_*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skip("no numbered examples found")
	}
	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			// An example past the seed's line has no C to compare, but the two entry
			// points must still agree — a refusal that only one of them reaches would
			// be the same divergence this test exists to catch.
			if beyondSeed(string(src)) {
				want := checkSeedRefuses(t, name, string(src))
				_, _, got := CompileProgram(zg)
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("%s: CompileProgram refused differently from Compile:\n got %v\nwant %v", name, got, want)
				}
				return
			}
			strCode, _, sd := Compile(string(src))
			if len(sd) != 0 {
				t.Fatalf("Compile diagnostics: %v", sd)
			}
			fileCode, _, fd := CompileProgram(zg)
			if len(fd) != 0 {
				t.Fatalf("CompileProgram diagnostics: %v", fd)
			}
			if strCode != fileCode {
				t.Fatalf("%s: CompileProgram C differs from Compile C (must be byte-identical)", name)
			}
		})
	}
}
