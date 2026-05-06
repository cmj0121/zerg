package syntax

import (
	"strings"
	"testing"
)

// v0.6 Unit 2 — typeck for built-in Option / Result enums.
//
// Coverage:
//   - Option / Result are visible as type names without an import.
//   - `x: Option[int] = ...` resolves; arity / name validation fires on
//     malformed type-arg shapes.
//   - `x: int? = ...` resolves to the same canonical *Type as
//     `x: Option[int] = ...` (T? ≡ Option[T] at the canonical level).
//   - User `enum Option { ... }`, `struct Option { ... }`, `spec Option`,
//     and likewise for Result, reject with the reservation diagnostic.
//   - `x: int? = nil` succeeds (binding annotation supplies the
//     expected type); `x := nil` and `print nil` reject with the
//     inference-failure diagnostic.

// --- visibility tests -----------------------------------------------------

func TestV06BuiltinOptionVisibleByName(t *testing.T) {
	prog := checkSrc(t, "x: Option[int] = nil\n")
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Type.Resolved
	if got == nil || got.Kind != TypeEnum {
		t.Fatalf("kind = %v, want TypeEnum", got)
	}
	if got.Name != "Option[int]" {
		t.Errorf("name = %q, want Option[int]", got.Name)
	}
	wantVariants := []string{"Some", "None"}
	if len(got.Variants) != len(wantVariants) {
		t.Fatalf("variants = %v, want %v", got.Variants, wantVariants)
	}
	for i, v := range wantVariants {
		if got.Variants[i] != v {
			t.Errorf("variant[%d] = %q, want %q", i, got.Variants[i], v)
		}
	}
	// Some payload is the substituted T (= int); None has no payload.
	if len(got.VariantPayloads) != 2 {
		t.Fatalf("payloads = %v, want 2 entries", got.VariantPayloads)
	}
	if len(got.VariantPayloads[0]) != 1 || got.VariantPayloads[0][0] != tInt {
		t.Errorf("Some payload = %v, want [int]", got.VariantPayloads[0])
	}
	if len(got.VariantPayloads[1]) != 0 {
		t.Errorf("None payload = %v, want empty", got.VariantPayloads[1])
	}
}

func TestV06BuiltinResultVisibleByName(t *testing.T) {
	prog := checkSrc(t, "fn use(r: Result[int, str]) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	got := fn.Params[0].Type.Resolved
	if got == nil || got.Kind != TypeEnum {
		t.Fatalf("got %v, want TypeEnum", got)
	}
	if got.Name != "Result[int,str]" {
		t.Errorf("name = %q, want Result[int,str]", got.Name)
	}
	if len(got.Variants) != 2 || got.Variants[0] != "Ok" || got.Variants[1] != "Err" {
		t.Errorf("variants = %v, want [Ok, Err]", got.Variants)
	}
	if len(got.VariantPayloads) != 2 ||
		len(got.VariantPayloads[0]) != 1 || got.VariantPayloads[0][0] != tInt {
		t.Errorf("Ok payload = %v, want [int]", got.VariantPayloads[0])
	}
	if len(got.VariantPayloads[1]) != 1 || got.VariantPayloads[1][0] != tStr {
		t.Errorf("Err payload = %v, want [str]", got.VariantPayloads[1])
	}
}

func TestV06BuiltinOptionWrongArity(t *testing.T) {
	checkErr(t, "fn use(x: Option[int, str]) {}\n",
		`generic type "Option" expects 1 type argument(s), got 2`)
}

func TestV06BuiltinResultWrongArity(t *testing.T) {
	checkErr(t, "fn use(x: Result[int]) {}\n",
		`generic type "Result" expects 2 type argument(s), got 1`)
}

func TestV06BuiltinOptionBareNameRejects(t *testing.T) {
	checkErr(t, "fn use(x: Option) {}\n",
		`generic type "Option" requires type arguments`)
}

// --- T? ≡ Option[T] canonicalisation -------------------------------------

func TestV06NullableEqualsOption(t *testing.T) {
	src := "a: Option[int] = nil\nb: int? = nil\n"
	prog := checkSrc(t, src)
	var aT, bT *Type
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok {
			continue
		}
		switch ls.Name {
		case "a":
			aT = ls.Type.Resolved
		case "b":
			bT = ls.Type.Resolved
		}
	}
	if aT == nil || bT == nil {
		t.Fatalf("missing resolved types: a=%v, b=%v", aT, bT)
	}
	if aT != bT {
		t.Errorf("Option[int] (%p, %s) and int? (%p, %s) must canonicalise to the same *Type",
			aT, aT, bT, bT)
	}
}

func TestV06NullableNestedList(t *testing.T) {
	prog := checkSrc(t, "fn use(xs: list[int]?) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	got := fn.Params[0].Type.Resolved
	if got == nil || got.Kind != TypeEnum || !strings.HasPrefix(got.Name, "Option[list[int]]") {
		t.Errorf("type = %v, want Option[list[int]]", got)
	}
}

// --- reservation rule -----------------------------------------------------

func TestV06ReservedEnumOption(t *testing.T) {
	checkErr(t, "enum Option { Foo }\n",
		`name "Option" is reserved (built-in)`)
}

func TestV06ReservedEnumResult(t *testing.T) {
	checkErr(t, "enum Result { Foo }\n",
		`name "Result" is reserved (built-in)`)
}

func TestV06ReservedStructOption(t *testing.T) {
	checkErr(t, "struct Option { x: int }\n",
		`name "Option" is reserved (built-in)`)
}

func TestV06ReservedStructResult(t *testing.T) {
	checkErr(t, "struct Result { x: int }\n",
		`name "Result" is reserved (built-in)`)
}

func TestV06ReservedSpecOption(t *testing.T) {
	checkErr(t, "spec Option { fn foo() -> int }\n",
		`name "Option" is reserved (built-in)`)
}

func TestV06ReservedSpecResult(t *testing.T) {
	checkErr(t, "spec Result { fn foo() -> int }\n",
		`name "Result" is reserved (built-in)`)
}

// --- nil binding ----------------------------------------------------------

func TestV06NilBareLetRejects(t *testing.T) {
	checkErr(t, "x := nil\n",
		"cannot infer type of nil")
}

func TestV06NilPrintRejects(t *testing.T) {
	checkErr(t, "print nil\n",
		"cannot infer type of nil")
}

func TestV06NilAnnotatedLetSucceeds(t *testing.T) {
	prog := checkSrc(t, "x: int? = nil\n")
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Value.Type()
	if got == nil || got.Kind != TypeEnum || got.Name != "Option[int]" {
		t.Errorf("nil literal type = %v, want Option[int]", got)
	}
	if ls.Type.Resolved != got {
		t.Errorf("nil literal type %p does not equal annotation %p", got, ls.Type.Resolved)
	}
}

func TestV06NilAnnotatedRejectsNonOption(t *testing.T) {
	// nil resolves only to Option[T]; a Result[T, E] annotation does not
	// pull nil to Result.Err / Result.Ok at Unit 2.
	checkErr(t, "x: Result[int, str] = nil\n",
		"cannot infer type of nil")
}
