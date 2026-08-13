package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// findImpl returns the collected impl of the named spec for the named nominal
// target, or nil.
func findImpl(info *Info, spec, target string) *types.ImplDef {
	if info.Specs == nil {
		return nil
	}
	for _, im := range info.Specs.Impls {
		if im.Spec != nil && im.Spec.Name == spec && im.Target != nil && im.Target.String() == target {
			return im
		}
	}
	return nil
}

// TestDeriveSynthesisStruct checks that '#[derive(Eq, Ord)]' on a struct
// synthesizes the canonical blessed impls, marked derived, with the expected
// methods registered in the type's one namespace.
func TestDeriveSynthesisStruct(t *testing.T) {
	info, msgs := checkInfo(t, "#[derive(Eq, Ord)]\nstruct Point {\n  pub x: int\n  pub y: int\n}")
	if len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
	var eq, ord bool
	for _, im := range info.Specs.Impls {
		if im.Spec == nil || im.Target.String() != "Point" {
			continue
		}
		if !im.Derived {
			t.Fatalf("impl of %s for Point should be marked derived", im.Spec.Name)
		}
		switch im.Spec.Name {
		case "Eq":
			eq = true
			if im.Methods["eq"] == nil || im.Methods["ne"] == nil {
				t.Fatalf("derived Eq is missing eq/ne: %v", im.Methods)
			}
		case "Ord":
			ord = true
			for _, m := range []string{"lt", "le", "gt", "ge"} {
				if im.Methods[m] == nil {
					t.Fatalf("derived Ord is missing %q", m)
				}
			}
		}
	}
	if !eq || !ord {
		t.Fatalf("derive did not synthesize both Eq and Ord (eq=%v ord=%v)", eq, ord)
	}
}

// TestDeriveSynthesisEnum checks that derive also reads an enum's structure,
// synthesizing the blessed impls for it.
func TestDeriveSynthesisEnum(t *testing.T) {
	info, msgs := checkInfo(t, "#[derive(Eq)]\nenum Color {\n  Red\n  Green\n  Blue\n}")
	if len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}
	if findImpl(info, "Eq", "Color") == nil {
		t.Fatalf("derive(Eq) did not synthesize impl Eq for Color")
	}
}

// TestDeriveNonBlessed rejects a derive of a spec outside the blessed set.
func TestDeriveNonBlessed(t *testing.T) {
	wantErr(t, "#[derive(Frobnicate)]\nstruct P {\n  pub x: int\n}", "cannot derive")
}

// TestDeriveBoundSatisfied accepts a generic call whose Ord bound is met by a
// derived impl — the derived impl is an ordinary impl that satisfies the bound.
func TestDeriveBoundSatisfied(t *testing.T) {
	wantOK(t, "#[derive(Eq, Ord)]\nstruct P {\n  pub x: int\n}\n"+
		"fn pick[T: Ord](a: T, b: T) -> T {\n  return a\n}\n"+
		"fn main() {\n  p := pick(P(1), P(2))\n  print p.x\n}")
}

// TestBoundUnsatisfiedWithoutDerive rejects the same call when the type has no
// impl of the bound spec — the use-site half of bound resolution 1c adds.
func TestBoundUnsatisfiedWithoutDerive(t *testing.T) {
	wantErr(t, "struct P {\n  pub x: int\n}\n"+
		"fn pick[T: Ord](a: T, b: T) -> T {\n  return a\n}\n"+
		"fn main() {\n  p := pick(P(1), P(2))\n  print p.x\n}", "does not satisfy bound Ord")
}
