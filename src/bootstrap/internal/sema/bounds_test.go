package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// TestBoundOperatorSatisfied accepts a generic body that uses '<' on a parameter
// bounded by Ord: the operator resolves through the parameter's bound spec
// (DESIGN-1c §3.3, U3).
func TestBoundOperatorSatisfied(t *testing.T) {
	wantOK(t, "fn less[T: Ord](a: T, b: T) -> bool {\n  return a < b\n}")
}

// TestBoundOperatorUnsatisfied reports '<' on an unbounded parameter: without an
// Ord bound the operator has no spec to resolve to (DESIGN-1c §3.3, U3).
func TestBoundOperatorUnsatisfied(t *testing.T) {
	wantErr(t, "fn less[T](a: T, b: T) -> bool {\n  return a < b\n}", "requires bound Ord")
}

// TestBoundEqualityThroughSuperSpec accepts '==' on a parameter bounded by Ord: Ord
// closes over its super-spec Eq, so the equality operator's spec is available
// through the bound (DESIGN-1c §3.2/§3.4, U3).
func TestBoundEqualityThroughSuperSpec(t *testing.T) {
	wantOK(t, "fn same[T: Ord](a: T, b: T) -> bool {\n  return a == b\n}")
}

// TestParamBoundsClosure resolves '[T: Ord]' to Ord closed over its super-spec Eq,
// so the parameter carries both specs (DESIGN-1c §3, U3).
func TestParamBoundsClosure(t *testing.T) {
	info, msgs := checkInfo(t, "fn less[T: Ord](a: T, b: T) -> bool {\n  return a < b\n}")
	if len(msgs) != 0 {
		t.Fatalf("unexpected sema errors: %v", msgs)
	}
	sig := info.Funcs["less"]
	if sig == nil || sig.Generic == nil {
		t.Fatal("no generic signature for 'less'")
	}
	p, ok := sig.Generic.Params["T"].(*types.Param)
	if !ok {
		t.Fatalf("T is %T, want *types.Param", sig.Generic.Params["T"])
	}
	names := map[string]bool{}
	for _, sp := range p.Bounds {
		names[sp.Name] = true
	}
	if !names["Ord"] || !names["Eq"] {
		t.Fatalf("T.Bounds = %v, want Ord + Eq (super-spec closed)", names)
	}
}

// TestAssocTypeInSpecSignature resolves an associated type named in a spec's own
// signature to the abstract projection This.Item (DESIGN-1c §3 associated-type
// resolution, U3).
func TestAssocTypeInSpecSignature(t *testing.T) {
	reg := specRegistry(t, "spec Get {\n  type Item\n  fn get() -> Item\n}")
	m := findSpecMethod(reg.Specs["Get"], "get")
	if m == nil || m.Sig == nil {
		t.Fatal("Get.get not collected")
	}
	proj, ok := m.Sig.Ret.(*types.Proj)
	if !ok {
		t.Fatalf("Get.get returns %T, want *types.Proj (abstract Item)", m.Sig.Ret)
	}
	if len(proj.Path) != 1 || proj.Path[0] != "Item" {
		t.Fatalf("projection path = %v, want [Item]", proj.Path)
	}
}

// TestAssocTypeInImplSignature resolves an associated type named in an impl's
// signature to the impl's concrete binding — 'fn get() -> Item' with 'type Item =
// int' returns int (DESIGN-1c §3 associated-type resolution, U3).
func TestAssocTypeInImplSignature(t *testing.T) {
	src := "spec Get {\n  type Item\n  fn get() -> Item\n}\n" +
		"struct Box {\n  v: int\n}\n" +
		"impl Get for Box {\n  type Item = int\n  fn get() -> Item { return this.v }\n}"
	reg := specRegistry(t, src)
	var im *types.ImplDef
	for _, i := range reg.Impls {
		if i.Spec != nil && i.Spec.Name == "Get" {
			im = i
		}
	}
	if im == nil {
		t.Fatal("no Get impl collected")
	}
	if got := im.Methods["get"].Sig.Ret; got != Int {
		t.Fatalf("Box.get returns %s, want int (the impl's Item binding)", got)
	}
}

// TestAssocProjectionInTypePosition resolves a projection 'Box.Item' in type
// position to the concrete impl binding: a str bound to a 'Box.Item' (int) binding
// is a type error (DESIGN-1c §3 associated-type resolution, U3).
func TestAssocProjectionInTypePosition(t *testing.T) {
	src := "spec Get {\n  type Item\n  fn get() -> Item\n}\n" +
		"struct Box {\n  v: int\n}\n" +
		"impl Get for Box {\n  type Item = int\n  fn get() -> Item { return this.v }\n}\n" +
		"fn f() {\n  x: Box.Item = \"hi\"\n  print x\n}"
	wantErr(t, src, "cannot bind str to a int binding")
}
