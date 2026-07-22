package mono

import "testing"

// hasType reports whether the program collected a specialized type of the mangled
// name.
func hasType(prog *Program, mangled string) *TypeInstance {
	for _, ti := range prog.Types {
		if ti.Mangled == mangled {
			return ti
		}
	}
	return nil
}

// TestGenericEnumSpecialized checks that a generic enum used at a concrete type is
// emitted as a specialized tagged union, with each variant's payload specialized.
func TestGenericEnumSpecialized(t *testing.T) {
	prog := build(t, "enum Box[T] {\n  Full(T)\n  Empty\n}\n"+
		"fn unwrap[T](b: Box[T], d: T) -> T {\n  return match b {\n    Full(v) => v\n    Empty => d\n  }\n}\n"+
		"fn main() {\n  print unwrap(Full(7), 0)\n}")

	ti := hasType(prog, "zg_Box__i")
	if ti == nil {
		t.Fatalf("no specialized enum zg_Box__i; got %v", typeNames(prog))
	}
	if !ti.IsEnum || len(ti.Variants) != 2 {
		t.Fatalf("Box__i should be an enum with 2 variants, got IsEnum=%v variants=%d", ti.IsEnum, len(ti.Variants))
	}
	full, ok := ti.Variant("Full")
	if !ok || full.Tag != 0 || len(full.Payload) != 1 {
		t.Fatalf("Full should be tag 0 with one payload, got %+v (ok=%v)", full, ok)
	}
}

// typeNames lists the mangled names of the collected specialized types.
func typeNames(prog *Program) []string {
	out := make([]string, len(prog.Types))
	for i, ti := range prog.Types {
		out[i] = ti.Mangled
	}
	return out
}

// TestDynWitnessBuilt checks that a '#[dyn]' call builds one shared erased body and
// one witness table per concrete type, rather than per-type specializations.
func TestDynWitnessBuilt(t *testing.T) {
	prog := build(t, "spec Show {\n  fn value() -> int\n}\n"+
		"struct Wrap {\n  n: int\n}\n"+
		"impl Show for Wrap {\n  fn value() -> int {\n    return this.n\n  }\n}\n"+
		"#[dyn]\nfn total[T: Show](x: T) -> int {\n  return x.value()\n}\n"+
		"fn main() {\n  print total(Wrap(5))\n}")

	var dyn *Instance
	for _, in := range prog.Funcs {
		if in.Dyn {
			dyn = in
		}
	}
	if dyn == nil || dyn.Mangled != "zg_total__dyn" {
		t.Fatalf("expected one erased dyn body zg_total__dyn; funcs=%v", mangledNames(prog))
	}
	if len(dyn.Erased) != 1 || !dyn.Erased[0] {
		t.Fatalf("dyn body's single type parameter should be erased, got %v", dyn.Erased)
	}
	if len(prog.Witnesses) != 1 {
		t.Fatalf("expected one witness table, got %d", len(prog.Witnesses))
	}
	w := prog.Witnesses[0]
	if w.Global != "zg_witness_Show__Wrap" || len(w.Slots) != 1 || w.Slots[0].Fn != "zg_Wrap__value" {
		t.Fatalf("witness table wrong: %+v", w)
	}
}
