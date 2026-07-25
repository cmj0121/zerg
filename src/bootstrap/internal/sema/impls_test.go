package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// specRegistry parses and checks src, failing on any diagnostic, and returns the
// collected spec/impl registry.
func specRegistry(t *testing.T, src string) *SpecRegistry {
	t.Helper()
	info, msgs := checkInfo(t, src)
	if len(msgs) != 0 {
		t.Fatalf("unexpected sema errors for %q: %v", src, msgs)
	}
	return info.Specs
}

// findSpecMethod returns a spec's method by name, or nil.
func findSpecMethod(def *types.SpecDef, name string) *types.SpecMethod {
	for _, m := range def.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// TestSpecCollection collects a spec's required and provided methods, associated
// type and value, and its super-spec chain (DESIGN-1c §1, U1).
func TestSpecCollection(t *testing.T) {
	src := "spec Eq {\n  fn eq(o: This) -> bool\n}\n" +
		"spec Ord: Eq {\n  fn lt(o: This) -> bool\n  fn le(o: This) -> bool { return true }\n" +
		"  type Item\n  BITS: int\n}"
	reg := specRegistry(t, src)
	if reg == nil {
		t.Fatal("no spec registry")
	}

	eq := reg.Specs["Eq"]
	if eq == nil {
		t.Fatal("spec Eq not collected")
	}
	if m := findSpecMethod(eq, "eq"); m == nil || m.Provided {
		t.Fatalf("Eq.eq: got %+v, want a required method", m)
	}

	ord := reg.Specs["Ord"]
	if ord == nil {
		t.Fatal("spec Ord not collected")
	}
	if len(ord.Supers) != 1 || ord.Supers[0] != eq {
		t.Fatalf("Ord.Supers = %v, want [Eq]", ord.Supers)
	}
	if m := findSpecMethod(ord, "lt"); m == nil || m.Provided {
		t.Fatalf("Ord.lt: got %+v, want a required method", m)
	}
	if m := findSpecMethod(ord, "le"); m == nil || !m.Provided {
		t.Fatalf("Ord.le: got %+v, want a provided method", m)
	}
	if len(ord.AssocTypes) != 1 || ord.AssocTypes[0].Name != "Item" {
		t.Fatalf("Ord.AssocTypes = %+v, want [Item]", ord.AssocTypes)
	}
	if len(ord.AssocVals) != 1 || ord.AssocVals[0].Name != "BITS" || ord.AssocVals[0].Of != Int {
		t.Fatalf("Ord.AssocVals = %+v, want [BITS: int]", ord.AssocVals)
	}
}

// TestImplCollection collects both an inherent impl and a spec impl, and builds
// the target type's shared method namespace (DESIGN-1c §1.2/§1.3, U1).
func TestImplCollection(t *testing.T) {
	src := "struct Point {\n  x: int\n}\n" +
		"spec Eq {\n  fn eq(o: This) -> bool\n}\n" +
		"impl Point {\n  fn get() -> int { return this.x }\n}\n" +
		"impl Eq for Point {\n  fn eq(o: This) -> bool { return this.x == o.x }\n}"
	reg := specRegistry(t, src)

	if len(reg.Impls) != 2 {
		t.Fatalf("collected %d impls, want 2", len(reg.Impls))
	}
	var inherent, specImpl *types.ImplDef
	for _, im := range reg.Impls {
		if im.Spec == nil {
			inherent = im
		} else {
			specImpl = im
		}
	}
	if inherent == nil || specImpl == nil {
		t.Fatalf("want one inherent and one spec impl, got %+v", reg.Impls)
	}
	if specImpl.Spec.Name != "Eq" {
		t.Fatalf("spec impl targets spec %q, want Eq", specImpl.Spec.Name)
	}

	// the two impls share Point's one method namespace: get + eq.
	var ms *types.MethodSet
	for def, set := range reg.Methods {
		if def.Name == "Point" {
			ms = set
		}
	}
	if ms == nil {
		t.Fatal("no method set for Point")
	}
	if _, ok := ms.Methods["get"]; !ok {
		t.Fatalf("Point namespace missing 'get': %v", ms.Methods)
	}
	if _, ok := ms.Methods["eq"]; !ok {
		t.Fatalf("Point namespace missing 'eq': %v", ms.Methods)
	}
}

// TestImplDiagnostics covers the U1 error paths: a duplicate method across the
// shared namespace, a missing associated type or value, and a spec-impl body type
// error (the one body check 1c adds).
func TestImplDiagnostics(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		substr string
	}{
		{
			name: "duplicate method across spec and inherent impls",
			src: "struct Point {\n  x: int\n}\n" +
				"spec Eq {\n  fn eq(o: This) -> bool\n}\n" +
				"impl Point {\n  fn eq() -> bool { return true }\n}\n" +
				"impl Eq for Point {\n  fn eq(o: This) -> bool { return true }\n}",
			substr: "already declares method \"eq\"",
		},
		{
			name: "impl missing a required associated type",
			src: "spec Container {\n  type Item\n  fn get() -> This\n}\n" +
				"struct Box {\n  v: int\n}\n" +
				"impl Container for Box {\n  fn get() -> This { return this }\n}",
			substr: "missing associated type \"Item\"",
		},
		{
			name: "impl missing a required associated value",
			src: "spec Sized {\n  BITS: int\n}\n" +
				"struct Box {\n  v: int\n}\n" +
				"impl Sized for Box {\n}",
			substr: "missing associated value \"BITS\"",
		},
		{
			name: "spec-impl body type error",
			src: "struct Point {\n  x: int\n}\n" +
				"spec Show {\n  fn show() -> int\n}\n" +
				"impl Show for Point {\n  fn show() -> int { return \"hello\" }\n}",
			substr: "cannot return str from a function returning int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantErr(t, tc.src, tc.substr)
		})
	}
}

// TestSpecImplBodyOK confirms a well-typed spec-impl body raises no diagnostic:
// 1c checks spec-impl bodies, so a correct one must stay clean.
func TestSpecImplBodyOK(t *testing.T) {
	wantOK(t, "struct Point {\n  x: int\n}\n"+
		"spec Show {\n  fn show() -> int\n}\n"+
		"impl Show for Point {\n  fn show() -> int { return this.x }\n}")
}
