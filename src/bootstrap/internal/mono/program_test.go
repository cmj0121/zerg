package mono

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// build parses, checks, and monomorphizes src, failing on any diagnostic.
func build(t *testing.T, src string) *Program {
	t.Helper()
	file, pdiags := parser.Parse(src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags)
	}
	info, sdiags := sema.Check(file)
	if len(sdiags) != 0 {
		t.Fatalf("sema errors: %v", sdiags)
	}
	return Build(file, info)
}

// TestIdentityRail checks the iteration-1 pass-through: one instance per
// top-level function in source order, each mangled 'zg_<name>', with Main set and
// CallTarget resolving to the mangled name.
func TestIdentityRail(t *testing.T) {
	prog := build(t, "fn helper() -> int { return 1 }\nfn main() { print helper() }")

	if len(prog.Funcs) != 2 {
		t.Fatalf("got %d instances, want 2", len(prog.Funcs))
	}
	if prog.Funcs[0].Origin.Name != "helper" || prog.Funcs[1].Origin.Name != "main" {
		t.Fatalf("instances out of source order: %s, %s", prog.Funcs[0].Origin.Name, prog.Funcs[1].Origin.Name)
	}
	if prog.Funcs[0].Mangled != "zg_helper" || prog.Funcs[1].Mangled != "zg_main" {
		t.Fatalf("unexpected mangled names: %s, %s", prog.Funcs[0].Mangled, prog.Funcs[1].Mangled)
	}
	if prog.Main == nil || prog.Main.Origin.Name != "main" {
		t.Fatalf("Main = %+v, want the main instance", prog.Main)
	}
	if got := prog.CallTarget("helper"); got != "zg_helper" {
		t.Fatalf("CallTarget(helper) = %q, want zg_helper", got)
	}
	if got := prog.CallTarget("unknown"); got != "zg_unknown" {
		t.Fatalf("CallTarget(unknown) = %q, want zg_unknown fallback", got)
	}
}
