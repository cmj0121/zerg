package build

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/module"
)

// compileProgramFromString compiles src as an entry module rooted in entryDir (with
// the embedded stdlib as the fallback root), so a test can drive a multi-module
// program without committing a second on-disk entry file.
func compileProgramFromString(t *testing.T, entryDir, src string) string {
	t.Helper()
	loader := module.NewLoader(module.OSProvider{Root: entryDir}, stdlibProvider{})
	code, _, diags := compileWith(loader, src)
	if len(diags) != 0 {
		t.Fatalf("program should compile, got diagnostics: %v", diags)
	}
	return code
}

// TestVisibilityRejectsPrivateMember is the S2 negative guard: referencing a
// module-private member across the module boundary (`lib.helper()` where lib's
// `helper` is not `pub`) is a compile error with a clean, span-anchored
// visibility diagnostic — the whole-program flatten moved the private item into
// the unit, so visibility is enforced by sema's `pub` check, not by presence.
func TestVisibilityRejectsPrivateMember(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	entry := filepath.Join(root, "examples", "1g", "private", "main.zg")
	_, _, diags := CompileProgram(entry)
	if len(diags) == 0 {
		t.Fatalf("calling a module-private member across modules must be rejected")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "not a public member") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a visibility diagnostic, got: %v", diags)
	}
}

// TestVisibilityAllowsPublicMember is the S2 positive guard: the public member of
// the same module compiles and runs, so the `pub` check rejects only the private
// one, not the public surface.
func TestVisibilityAllowsPublicMember(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	// Build a tiny entry (in the same source root) that calls the public member.
	src := "import \"./lib\"\nfn main() {\n\tprint lib.public_fn()\n}\n"
	entryDir := filepath.Join(root, "examples", "1g", "private")
	code := compileProgramFromString(t, entryDir, src)
	if got := compileAndRun(t, cc, code); got != "1\n" {
		t.Fatalf("public member run: got %q, want %q", got, "1\n")
	}
}

// TestInitRunsOnceAndSetsConstant is the S3 guard: the imported module's init()
// runs exactly once before main's body (a single "1"), and its module constant is
// evaluated at init and observed from main through a pub accessor (40, twice).
func TestInitRunsOnceAndSetsConstant(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join(root, "examples", "1g", "init", "main.zg")
	code, _, diags := CompileProgram(entry)
	if len(diags) != 0 {
		t.Fatalf("init program should compile, got diagnostics: %v", diags)
	}
	// The module gets a once-guarded init function and a module-level constant global.
	if !strings.Contains(code, "__zerg_init_m(void)") {
		t.Fatalf("expected a per-module init function in the C:\n%s", code)
	}
	if !strings.Contains(code, "static _Bool __done") {
		t.Fatalf("expected a static once-guard in the init function:\n%s", code)
	}
	if got := compileAndRun(t, cc, code); got != "1\n40\n40\n" {
		t.Fatalf("init run: got %q, want %q", got, "1\n40\n40\n")
	}
}

// TestSingleFileInitAndConstant is the S3 single-file guard: a program with no
// import that owns an init() block and a module constant lowers to the entry
// module's init function and its constant global — the machinery is not limited to
// imported modules, and it runs before main's body.
func TestSingleFileInitAndConstant(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "answer := 40\n\ninit() {\n\tprint 1\n}\n\nfn main() {\n\tprint answer\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("single-file init program should compile, got: %v", diags)
	}
	if !strings.Contains(code, "__zerg_init_main(void)") || !strings.Contains(code, "int64_t zg_answer;") {
		t.Fatalf("expected an entry init function and a constant global:\n%s", code)
	}
	if got := compileAndRun(t, cc, code); got != "1\n40\n" {
		t.Fatalf("single-file init run: got %q, want %q", got, "1\n40\n")
	}
}

// TestImportPubReExport is the S2 re-export guard: main imports mid, which
// re-exports deep with `import pub`, so deep's public `hello` is reachable through
// the mid namespace as `mid.hello()` and dispatches to deep's definition.
func TestImportPubReExport(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join(root, "examples", "1g", "reexport", "main.zg")
	code, _, diags := CompileProgram(entry)
	if len(diags) != 0 {
		t.Fatalf("re-export program should compile, got diagnostics: %v", diags)
	}
	// `mid.hello()` must dispatch to deep's definition, not a (non-existent) mid one.
	if !strings.Contains(code, "zg_deep__hello") {
		t.Fatalf("re-export must resolve to deep's function in the C:\n%s", code)
	}
	if got := compileAndRun(t, cc, code); got != "7\n" {
		t.Fatalf("re-export run: got %q, want %q", got, "7\n")
	}
}
