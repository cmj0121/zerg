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

// mangledNames returns the mangled name of every function instance, for asserting
// which specializations were collected.
func mangledNames(prog *Program) map[string]bool {
	got := map[string]bool{}
	for _, in := range prog.Funcs {
		got[in.Mangled] = true
	}
	return got
}

func assertFuncs(t *testing.T, prog *Program, want ...string) {
	t.Helper()
	got := mangledNames(prog)
	if len(got) != len(prog.Funcs) {
		t.Fatalf("duplicate mangled names among %d instances: %v", len(prog.Funcs), got)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing instance %q; got %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("instance set = %v, want exactly %v", got, want)
	}
}

// TestInstantiateTwoTypes checks that a generic identity function used at two types
// yields one specialized instance per type, with distinct mangled names, and that
// the generic function itself is not emitted un-instantiated.
func TestInstantiateTwoTypes(t *testing.T) {
	prog := build(t, "fn id[T](x: T) -> T { return x }\nfn main() { print id(5)\n print id(true) }")
	assertFuncs(t, prog, "zg_main", "zgg_2_idi", "zgg_2_idb")
}

// TestDedup checks that the same instantiation reached twice collapses to a single
// instance (DESIGN-1c §4.3).
func TestDedup(t *testing.T) {
	prog := build(t, "fn id[T](x: T) -> T { return x }\nfn main() { print id(5)\n print id(6) }")
	assertFuncs(t, prog, "zg_main", "zgg_2_idi")
}

// TestTransitive checks that a generic function calling another generic function
// instantiates the callee transitively at the caller's concrete type.
func TestTransitive(t *testing.T) {
	src := "fn id[T](x: T) -> T { return x }\n" +
		"fn wrap[T](x: T) -> T { return id(x) }\n" +
		"fn main() { print wrap(7)\n print wrap(false) }"
	prog := build(t, src)
	assertFuncs(t, prog, "zg_main", "zgg_4_wrapi", "zgg_4_wrapb", "zgg_2_idi", "zgg_2_idb")
}

// TestValueGeneric checks that a value-generic parameter instantiates once per
// distinct concrete value: '[int; 3]' and '[int; 2]' are two instances.
func TestValueGeneric(t *testing.T) {
	src := "fn head[N: int](xs: [int; N]) -> int { return xs[0] }\n" +
		"fn main() {\n a: [int; 3] = [10, 20, 30]\n b: [int; 2] = [40, 50]\n" +
		" print head(a)\n print head(b) }"
	prog := build(t, src)
	assertFuncs(t, prog, "zg_main", "zgg_4_headVi3_", "zgg_4_headVi2_")
}

// TestGenericStruct checks that a generic struct used at two types yields one
// specialized C type per type argument, each with its field type concretized.
func TestGenericStruct(t *testing.T) {
	src := "struct Box[T] { pub value: T }\n" +
		"fn main() { bi := Box(5)\n bb := Box(true)\n print bi.value\n print bb.value }"
	prog := build(t, src)
	if len(prog.Types) != 2 {
		t.Fatalf("got %d type instances, want 2: %+v", len(prog.Types), prog.Types)
	}
	names := map[string]*TypeInstance{}
	for _, ti := range prog.Types {
		names[ti.Mangled] = ti
	}
	for _, want := range []string{"zgt_N3_Box1_i", "zgt_N3_Box1_b"} {
		if names[want] == nil {
			t.Fatalf("missing type instance %q; got %v", want, names)
		}
	}
	if f := names["zgt_N3_Box1_i"].Fields; len(f) != 1 || f[0].Name != "value" || f[0].Type != sema.Int {
		t.Fatalf("Box[int] fields = %+v, want value:int", f)
	}
}
