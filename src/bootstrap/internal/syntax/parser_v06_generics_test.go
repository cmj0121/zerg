package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.6 Unit 1a — parser tests for generic type-parameter lists and use-site
// generic type-arguments.
//
// Unit 1a is parser-only: typeck / interpreter / codegen do not consume the
// new TypeParams / TypeArgs fields yet. These tests pin the lexer / AST
// plumbing and the rejection diagnostics for malformed shapes. Existing
// v0.0–v0.5 corpora continue to parse with both fields at their zero values
// — the regression tests in the v0.5 suites already lock that in.
// ---------------------------------------------------------------------------

// --- generic fn declarations ----------------------------------------------

func TestParseGenericFnUnconstrained(t *testing.T) {
	prog := parseProgramSrc(t, "fn id[T](x: T) -> T { return x }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.TypeParams) != 1 {
		t.Fatalf("got %d type params, want 1", len(fn.TypeParams))
	}
	tp := fn.TypeParams[0]
	if tp.Name != "T" {
		t.Errorf("type param name = %q, want T", tp.Name)
	}
	if len(tp.Bounds) != 0 {
		t.Errorf("unconstrained T should have no bounds, got %d", len(tp.Bounds))
	}
}

func TestParseGenericFnSingleBound(t *testing.T) {
	prog := parseProgramSrc(t, "fn show[T: Printable](x: T) { print x }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.TypeParams) != 1 {
		t.Fatalf("got %d type params, want 1", len(fn.TypeParams))
	}
	tp := fn.TypeParams[0]
	if tp.Name != "T" {
		t.Errorf("name = %q, want T", tp.Name)
	}
	if len(tp.Bounds) != 1 || tp.Bounds[0].Name != "Printable" {
		t.Errorf("bounds = %v, want [Printable]", tp.Bounds)
	}
}

func TestParseGenericFnMultiBound(t *testing.T) {
	prog := parseProgramSrc(t, "fn show[T: Printable + Hashable + Comparable](x: T) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	tp := fn.TypeParams[0]
	if len(tp.Bounds) != 3 {
		t.Fatalf("got %d bounds, want 3", len(tp.Bounds))
	}
	wantNames := []string{"Printable", "Hashable", "Comparable"}
	for i, want := range wantNames {
		if tp.Bounds[i].Name != want {
			t.Errorf("bound %d = %q, want %q", i, tp.Bounds[i].Name, want)
		}
	}
}

func TestParseGenericFnMixedArity(t *testing.T) {
	// `[K: Hashable, V]` — first param constrained, second unconstrained.
	prog := parseProgramSrc(t, "fn put[K: Hashable, V](k: K, v: V) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.TypeParams) != 2 {
		t.Fatalf("got %d type params, want 2", len(fn.TypeParams))
	}
	if fn.TypeParams[0].Name != "K" || len(fn.TypeParams[0].Bounds) != 1 {
		t.Errorf("K mis-shape: %+v", fn.TypeParams[0])
	}
	if fn.TypeParams[1].Name != "V" || len(fn.TypeParams[1].Bounds) != 0 {
		t.Errorf("V mis-shape: %+v", fn.TypeParams[1])
	}
}

func TestParseGenericStruct(t *testing.T) {
	prog := parseProgramSrc(t, "struct Box[T] { value: T }\n")
	st := expectOne[*StructDecl](t, prog)
	if len(st.TypeParams) != 1 || st.TypeParams[0].Name != "T" {
		t.Errorf("type params = %+v, want [{T}]", st.TypeParams)
	}
}

func TestParseGenericEnum(t *testing.T) {
	prog := parseProgramSrc(t, "enum Pair[T, U] { Both(T, U), Neither }\n")
	en := expectOne[*EnumDecl](t, prog)
	if len(en.TypeParams) != 2 {
		t.Fatalf("got %d type params, want 2", len(en.TypeParams))
	}
	if en.TypeParams[0].Name != "T" || en.TypeParams[1].Name != "U" {
		t.Errorf("type params = %+v, want [T, U]", en.TypeParams)
	}
	// Variant payload should reference the type parameters by name.
	if len(en.Variants) != 2 {
		t.Fatalf("got %d variants, want 2", len(en.Variants))
	}
	if v := en.Variants[0]; v.Name != "Both" || len(v.Payload) != 2 {
		t.Errorf("variant 0 = %+v, want Both(T, U)", v)
	}
}

func TestParseGenericSpec(t *testing.T) {
	prog := parseProgramSrc(t, "spec Iterator[T] { fn next() -> T? }\n")
	sp := expectOne[*SpecDecl](t, prog)
	if len(sp.TypeParams) != 1 || sp.TypeParams[0].Name != "T" {
		t.Errorf("spec type params = %+v, want [{T}]", sp.TypeParams)
	}
	if len(sp.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(sp.Methods))
	}
	if ret := sp.Methods[0].Return; ret == nil || ret.Name != "T" || !ret.Nullable {
		t.Errorf("method return = %v, want T?", ret)
	}
}

func TestParseImplGenericReceiverConcrete(t *testing.T) {
	// `impl Box[int] for Printable { ... }` — concrete type-arg in receiver
	// type. The impl-level TypeParams list stays empty; receiver TypeArgs
	// holds [int].
	prog := parseProgramSrc(t, "impl Box[int] for Printable { fn to_string() -> str { return \"\" } }\n")
	im := expectOne[*ImplDecl](t, prog)
	if len(im.TypeParams) != 0 {
		t.Errorf("impl-level type params should be empty, got %+v", im.TypeParams)
	}
	if im.Type != "Box" {
		t.Errorf("type name = %q, want Box", im.Type)
	}
	if len(im.TypeArgs) != 1 || im.TypeArgs[0].Name != "int" {
		t.Errorf("type args = %v, want [int]", im.TypeArgs)
	}
	if im.Spec != "Printable" {
		t.Errorf("spec = %q, want Printable", im.Spec)
	}
}

func TestParseImplGenericReceiverParameterised(t *testing.T) {
	// `impl[T: Bound] LocalType[T] for SomeSpec { ... }` — generic impl.
	prog := parseProgramSrc(t, "impl[T: Bound] Box[T] for Printable { fn to_string() -> str { return \"\" } }\n")
	im := expectOne[*ImplDecl](t, prog)
	if len(im.TypeParams) != 1 {
		t.Fatalf("got %d impl type params, want 1", len(im.TypeParams))
	}
	if im.TypeParams[0].Name != "T" || len(im.TypeParams[0].Bounds) != 1 {
		t.Errorf("impl type param = %+v, want T:Bound", im.TypeParams[0])
	}
	if im.TypeParams[0].Bounds[0].Name != "Bound" {
		t.Errorf("bound = %q, want Bound", im.TypeParams[0].Bounds[0].Name)
	}
	if len(im.TypeArgs) != 1 || im.TypeArgs[0].Name != "T" {
		t.Errorf("receiver type args = %v, want [T]", im.TypeArgs)
	}
}

// --- type-args at use sites ----------------------------------------------

func TestParseGenericTypeRefSingle(t *testing.T) {
	// `let x: Box[int] = ...` — single type-arg, parsed by parseTypeRef.
	prog := parseProgramSrc(t, "fn make() -> Box[int] { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	ret := fn.Return
	if ret == nil {
		t.Fatal("missing return type")
	}
	if ret.Name != "Box" {
		t.Errorf("type name = %q, want Box", ret.Name)
	}
	if len(ret.TypeArgs) != 1 || ret.TypeArgs[0].Name != "int" {
		t.Errorf("type args = %v, want [int]", ret.TypeArgs)
	}
}

func TestParseGenericTypeRefMulti(t *testing.T) {
	prog := parseProgramSrc(t, "fn make() -> Result[int, str] { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	ret := fn.Return
	if len(ret.TypeArgs) != 2 {
		t.Fatalf("got %d type args, want 2", len(ret.TypeArgs))
	}
	if ret.TypeArgs[0].Name != "int" || ret.TypeArgs[1].Name != "str" {
		t.Errorf("type args = %v, want [int, str]", ret.TypeArgs)
	}
}

func TestParseGenericTypeRefNested(t *testing.T) {
	// `list[Option[int]]` — nested generic. parseListTypeRef hands the
	// inner element to parseTypeRef which itself reads `Option[int]`.
	prog := parseProgramSrc(t, "fn make() -> list[Option[int]] { return [] }\n")
	fn := expectOne[*FnDecl](t, prog)
	ret := fn.Return
	if ret.Kind != TypeRefList {
		t.Fatalf("kind = %v, want TypeRefList", ret.Kind)
	}
	inner := ret.Element
	if inner == nil || inner.Name != "Option" {
		t.Fatalf("inner = %v, want Option", inner)
	}
	if len(inner.TypeArgs) != 1 || inner.TypeArgs[0].Name != "int" {
		t.Errorf("inner type args = %v, want [int]", inner.TypeArgs)
	}
}

func TestParseGenericTypeRefDeeplyNested(t *testing.T) {
	// `Pair[Box[int], Result[str, int]]` — multiple levels deep, each
	// resolved by recursive parseTypeRef calls.
	prog := parseProgramSrc(t, "fn make() -> Pair[Box[int], Result[str, int]] { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	ret := fn.Return
	if ret.Name != "Pair" || len(ret.TypeArgs) != 2 {
		t.Fatalf("outer mis-shape: %v", ret)
	}
	if ret.TypeArgs[0].Name != "Box" || ret.TypeArgs[0].TypeArgs[0].Name != "int" {
		t.Errorf("first arg = %v, want Box[int]", ret.TypeArgs[0])
	}
	if ret.TypeArgs[1].Name != "Result" {
		t.Errorf("second arg = %v, want Result[str, int]", ret.TypeArgs[1])
	}
}

// --- reject cases --------------------------------------------------------

func TestParseRejectEmptyTypeParamList(t *testing.T) {
	expectParseErr(t, "fn id[](x: int) -> int { return x }\n", "type parameter list cannot be empty")
}

func TestParseRejectEmptyTypeArgList(t *testing.T) {
	expectParseErr(t, "fn make() -> Box[] { return 0 }\n", "type argument list cannot be empty")
}

func TestParseRejectTrailingCommaInTypeParams(t *testing.T) {
	expectParseErr(t, "fn id[T,](x: T) -> T { return x }\n", "trailing comma not allowed in type parameter list")
}

func TestParseRejectTrailingCommaInTypeArgs(t *testing.T) {
	expectParseErr(t, "fn make() -> Result[int, str,] { return 0 }\n", "trailing comma not allowed in type argument list")
}

func TestParseRejectPubInBound(t *testing.T) {
	// `pub` (or any reserved keyword) inside a bound is rejected — the
	// diagnostic mentions the "reserved keyword" phrasing so users see why.
	expectParseErr(t, "fn show[T: pub](x: T) {}\n", "reserved keyword")
}

func TestParseRejectKeywordAsTypeParamName(t *testing.T) {
	expectParseErr(t, "fn id[fn](x: int) -> int { return x }\n", "reserved keyword")
}

func TestParseRejectDuplicateTypeParam(t *testing.T) {
	expectParseErr(t, "fn id[T, T](x: T) -> T { return x }\n", "declared twice")
}

// --- regressions: pre-v0.6 programs continue to parse --------------------

func TestParseV06FnDeclWithoutTypeParamsRegression(t *testing.T) {
	prog := parseProgramSrc(t, "fn add(a: int, b: int) -> int { return a + b }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.TypeParams != nil {
		t.Errorf("non-generic fn carries type params: %+v", fn.TypeParams)
	}
}

func TestParseV06StructWithoutTypeParamsRegression(t *testing.T) {
	prog := parseProgramSrc(t, "struct Point { x: int, y: int }\n")
	st := expectOne[*StructDecl](t, prog)
	if st.TypeParams != nil {
		t.Errorf("non-generic struct carries type params: %+v", st.TypeParams)
	}
}

func TestParseV06ImplWithoutTypeParamsRegression(t *testing.T) {
	prog := parseProgramSrc(t, "impl Counter for Printable { fn to_string() -> str { return \"\" } }\n")
	im := expectOne[*ImplDecl](t, prog)
	if im.TypeParams != nil || im.TypeArgs != nil {
		t.Errorf("non-generic impl carries type params/args: %+v / %+v", im.TypeParams, im.TypeArgs)
	}
}

// TypeRef.String() must render type-args and the nullable bit.
func TestParseV06TypeRefString(t *testing.T) {
	prog := parseProgramSrc(t, "fn make() -> Result[int, str]? { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	got := fn.Return.String()
	want := "Result[int, str]?"
	if got != want {
		t.Errorf("TypeRef.String() = %q, want %q", got, want)
	}
}
