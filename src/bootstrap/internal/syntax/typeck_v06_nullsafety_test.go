package syntax

import (
	"strings"
	"testing"
)

// v0.6 Unit 4 — typeck for `?`, `??`, `?.`, plus deeper nil inference and
// composite eq across Option / Result.

// ---------------------------------------------------------------------------
// `?` propagation
// ---------------------------------------------------------------------------

func TestV06PropagateOptionInOptionFn(t *testing.T) {
	src := "fn maybe() -> int? {\n" +
		"  return Option.Some(7)\n" +
		"}\n" +
		"fn outer() -> int? {\n" +
		"  v := maybe()?\n" +
		"  return Option.Some(v)\n" +
		"}\n"
	prog := checkSrc(t, src)
	// The PropagateExpr inside outer's body should typecheck to int.
	var pe *PropagateExpr
	for _, st := range prog.Statements {
		fn, ok := st.(*FnDecl)
		if !ok || fn.Name != "outer" {
			continue
		}
		for _, stmt := range fn.Body.Statements {
			ls, ok := stmt.(*LetStmt)
			if !ok {
				continue
			}
			pe, _ = ls.Value.(*PropagateExpr)
		}
	}
	if pe == nil {
		t.Fatalf("did not find PropagateExpr")
	}
	if pe.Type() != tInt {
		t.Errorf("propagate type = %v, want int", pe.Type())
	}
}

func TestV06PropagateResultInResultFn(t *testing.T) {
	src := "fn fetch() -> Result[int, str] {\n" +
		"  return Result.Ok(7)\n" +
		"}\n" +
		"fn outer() -> Result[int, str] {\n" +
		"  v := fetch()?\n" +
		"  return Result.Ok(v)\n" +
		"}\n"
	checkSrc(t, src)
}

func TestV06PropagateOptionAtTopLevelRejects(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"v := maybe()?\n"
	checkErr(t, src,
		"? propagation only legal inside fn returning Option[...] or Result[..., E]")
}

func TestV06PropagateInIntFnRejects(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"fn outer() -> int {\n" +
		"  v := maybe()?\n" +
		"  return v\n" +
		"}\n"
	checkErr(t, src,
		"? propagation only legal inside fn returning")
}

func TestV06PropagateInVoidFnRejects(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"fn outer() {\n" +
		"  v := maybe()?\n" +
		"}\n"
	checkErr(t, src,
		"? propagation only legal inside fn returning")
}

func TestV06PropagateMismatchedErrType(t *testing.T) {
	src := "fn fetch() -> Result[int, str] { return Result.Ok(7) }\n" +
		"fn outer() -> Result[int, bool] {\n" +
		"  v := fetch()?\n" +
		"  return Result.Ok(v)\n" +
		"}\n"
	checkErr(t, src, "? error type mismatch")
}

func TestV06PropagateOptionInResultFnRejects(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"fn outer() -> Result[int, str] {\n" +
		"  v := maybe()?\n" +
		"  return Result.Ok(v)\n" +
		"}\n"
	checkErr(t, src,
		"? propagation only legal inside fn returning")
}

func TestV06PropagateResultInOptionFnRejects(t *testing.T) {
	src := "fn fetch() -> Result[int, str] { return Result.Ok(7) }\n" +
		"fn outer() -> int? {\n" +
		"  v := fetch()?\n" +
		"  return Option.Some(v)\n" +
		"}\n"
	checkErr(t, src,
		"? propagation only legal inside fn returning")
}

func TestV06PropagateOnNonOptionRejects(t *testing.T) {
	src := "fn outer() -> int? {\n" +
		"  v := 7?\n" +
		"  return Option.Some(v)\n" +
		"}\n"
	checkErr(t, src,
		"? requires Option[...] or Result[..., ...] receiver")
}

func TestV06PropagateInferenceCarriesT(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"fn outer() -> str? {\n" +
		"  v := maybe()?\n" +
		"  return Option.Some(\"ok\")\n" +
		"}\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// `??` nil-coalesce
// ---------------------------------------------------------------------------

func TestV06CoalesceOption(t *testing.T) {
	src := "x: int? = nil\n" +
		"y: int = x ?? 0\n"
	prog := checkSrc(t, src)
	var ce *CoalesceExpr
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok || ls.Name != "y" {
			continue
		}
		ce, _ = ls.Value.(*CoalesceExpr)
	}
	if ce == nil {
		t.Fatalf("did not find CoalesceExpr")
	}
	if ce.Type() != tInt {
		t.Errorf("coalesce type = %v, want int", ce.Type())
	}
}

func TestV06CoalesceResult(t *testing.T) {
	src := "fn fetch() -> Result[int, str] { return Result.Ok(3) }\n" +
		"y: int = fetch() ?? 0\n"
	checkSrc(t, src)
}

func TestV06CoalesceOnIntRejects(t *testing.T) {
	src := "y: int = 3 ?? 0\n"
	checkErr(t, src, "?? requires Option[...] or Result[..., ...] on the left")
}

func TestV06CoalesceOnStrRejects(t *testing.T) {
	src := "y: str = \"hi\" ?? \"bye\"\n"
	checkErr(t, src, "?? requires Option[...] or Result[..., ...] on the left")
}

func TestV06CoalesceRhsMismatch(t *testing.T) {
	src := "x: int? = nil\n" +
		"y := x ?? \"oops\"\n"
	checkErr(t, src, "?? right-hand side has type str")
}

func TestV06CoalesceChainRightAssoc(t *testing.T) {
	src := "a: int? = nil\n" +
		"b: int? = nil\n" +
		"y: int = a ?? b ?? 0\n"
	prog := checkSrc(t, src)
	var ce *CoalesceExpr
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok || ls.Name != "y" {
			continue
		}
		ce, _ = ls.Value.(*CoalesceExpr)
	}
	if ce == nil {
		t.Fatalf("did not find outer CoalesceExpr")
	}
	if ce.Type() != tInt {
		t.Errorf("outer coalesce type = %v, want int", ce.Type())
	}
	if _, ok := ce.Right.(*CoalesceExpr); !ok {
		t.Fatalf("right-assoc broken: outer.Right = %T, want *CoalesceExpr", ce.Right)
	}
}

func TestV06CoalesceOptionResultMix(t *testing.T) {
	// Different Option/Result on each side is rejected because the inner T's
	// must agree.
	src := "fn fetch() -> Result[int, str] { return Result.Ok(3) }\n" +
		"opt: int? = nil\n" +
		"y: int = opt ?? fetch() ?? 0\n"
	// fetch() returns Result[int, str]; its ?? RHS must be assignable to
	// int — 0 is assignable to int. opt ?? <int> requires the RHS to be int;
	// but `fetch() ?? 0` produces int, so `opt ?? int` ⇒ int.
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// `?.` safe-navigation
// ---------------------------------------------------------------------------

func TestV06SafeFieldAccess(t *testing.T) {
	src := "struct Box { v: int }\n" +
		"b: Box? = nil\n" +
		"x := b?.v\n"
	prog := checkSrc(t, src)
	var fa *FieldAccessExpr
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok || ls.Name != "x" {
			continue
		}
		fa, _ = ls.Value.(*FieldAccessExpr)
	}
	if fa == nil {
		t.Fatalf("did not find FieldAccessExpr")
	}
	if !fa.Safe {
		t.Errorf("Safe bit not set")
	}
	got := fa.Type()
	if got == nil || got.Kind != TypeEnum || !isOptionInstance(got) {
		t.Fatalf("type = %v, want Option[int]", got)
	}
	if got.Name != "Option[int]" {
		t.Errorf("name = %q, want Option[int]", got.Name)
	}
}

func TestV06SafeFieldAccessChain(t *testing.T) {
	// `o?.inner?.v` — both layers non-Option struct fields except outer
	// nullable wrapper. Each ?. yields Option[T], the next ?. consumes it.
	src := "struct Inner { v: int }\n" +
		"struct Outer { inner: Inner }\n" +
		"o: Outer? = nil\n" +
		"v := o?.inner?.v\n"
	prog := checkSrc(t, src)
	var ls *LetStmt
	for _, st := range prog.Statements {
		l, ok := st.(*LetStmt)
		if !ok || l.Name != "v" {
			continue
		}
		ls = l
	}
	if ls == nil {
		t.Fatalf("did not find v")
	}
	got := ls.Value.Type()
	if got == nil || !isOptionInstance(got) || got.Name != "Option[int]" {
		t.Errorf("type = %v, want Option[int]", got)
	}
}

func TestV06SafeFieldAccessOnNonNullable(t *testing.T) {
	src := "struct Box { v: int }\n" +
		"b := Box { v: 7 }\n" +
		"x := b?.v\n"
	checkErr(t, src, "?. requires nullable receiver")
}

func TestV06SafeFieldAccessOnInt(t *testing.T) {
	src := "n := 7\nx := n?.v\n"
	checkErr(t, src, "?. requires nullable receiver")
}

func TestV06SafeFieldAccessUnknownField(t *testing.T) {
	src := "struct Box { v: int }\n" +
		"b: Box? = nil\n" +
		"x := b?.missing\n"
	checkErr(t, src, `field "missing" not found on Box`)
}

func TestV06SafeFieldAccessOnOptionOfList(t *testing.T) {
	// Option of a non-struct inner — `?.` only makes sense on struct fields.
	src := "xs: list[int]? = nil\n" +
		"n := xs?.len\n"
	checkErr(t, src, "?. requires struct inside Option")
}

// ---------------------------------------------------------------------------
// Deeper nil inference (Unit 4 verification)
// ---------------------------------------------------------------------------

func TestV06NilInReturnPosition(t *testing.T) {
	src := "fn opt() -> int? {\n" +
		"  return nil\n" +
		"}\n"
	prog := checkSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	rs, ok := fn.Body.Statements[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("expected ReturnStmt")
	}
	got := rs.Value.Type()
	if got == nil || !isOptionInstance(got) || got.Name != "Option[int]" {
		t.Errorf("nil type = %v, want Option[int]", got)
	}
}

func TestV06NilAsFnArg(t *testing.T) {
	src := "fn take(x: int?) -> int { return 0 }\n" +
		"fn use() -> int { return take(nil) }\n"
	checkSrc(t, src)
}

func TestV06NilInListElement(t *testing.T) {
	src := "xs: list[int?] = [nil, Option.Some(1), nil]\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Type.Resolved
	if got == nil || got.Kind != TypeList {
		t.Fatalf("type = %v, want list[int?]", got)
	}
	if !isOptionInstance(got.Element) {
		t.Errorf("element type = %v, want Option[int]", got.Element)
	}
}

func TestV06IntInListUnderOptionHintLifts(t *testing.T) {
	src := "xs: list[int?] = [1, nil, 2]\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Type.Resolved
	if got == nil || got.Kind != TypeList {
		t.Fatalf("type = %v, want list[int?]", got)
	}
	if !isOptionInstance(got.Element) {
		t.Errorf("element type = %v, want Option[int]", got.Element)
	}
}

func TestV06NilInStructFieldInit(t *testing.T) {
	src := "struct Box { v: int? }\n" +
		"b := Box { v: nil }\n"
	checkSrc(t, src)
}

func TestV06ResultOkInferredFromLetHint(t *testing.T) {
	src := "r: Result[int, str] = Result.Ok(7)\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Type.Resolved
	if got == nil || !isResultInstance(got) {
		t.Fatalf("type = %v, want Result[int, str]", got)
	}
	if got.Name != "Result[int,str]" {
		t.Errorf("name = %q, want Result[int,str]", got.Name)
	}
}

func TestV06ResultErrInferredFromLetHint(t *testing.T) {
	src := "r: Result[int, str] = Result.Err(\"oops\")\n"
	checkSrc(t, src)
}

func TestV06BareNilLetStillRejects(t *testing.T) {
	checkErr(t, "x := nil\n", "cannot infer type of nil")
}

// ---------------------------------------------------------------------------
// Composite eq across Option / Result (regression)
// ---------------------------------------------------------------------------

func TestV06EqOptionInt(t *testing.T) {
	src := "a: Option[int] = Option.Some(7)\n" +
		"b: Option[int] = Option.Some(7)\n" +
		"c: bool = a == b\n"
	prog := checkSrc(t, src)
	var be *BinaryExpr
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok || ls.Name != "c" {
			continue
		}
		be, _ = ls.Value.(*BinaryExpr)
	}
	if be == nil {
		t.Fatalf("did not find BinaryExpr")
	}
	if be.Type() != tBool {
		t.Errorf("== type = %v, want bool", be.Type())
	}
}

func TestV06EqResultErr(t *testing.T) {
	src := "a: Result[int, str] = Result.Err(\"a\")\n" +
		"b: Result[int, str] = Result.Err(\"a\")\n" +
		"c: bool = a == b\n"
	checkSrc(t, src)
}

func TestV06NeOptionInt(t *testing.T) {
	src := "a: Option[int] = Option.Some(1)\n" +
		"b: Option[int] = Option.Some(2)\n" +
		"c: bool = a != b\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// Helpers / regression
// ---------------------------------------------------------------------------

func TestV06CoalesceLiftsRhs(t *testing.T) {
	// `?? Some(7)` on Option[Option[int]] would not lift; but `?? 7` on
	// Option[int] takes the RHS hint and expects int (no lift needed).
	src := "x: int? = Option.Some(3)\ny := x ?? 0\n"
	prog := checkSrc(t, src)
	for _, st := range prog.Statements {
		ls, ok := st.(*LetStmt)
		if !ok || ls.Name != "y" {
			continue
		}
		if ls.Value.Type() != tInt {
			t.Errorf("y type = %v, want int", ls.Value.Type())
		}
	}
}

func TestV06PropagateInOptionFnReturnsInt(t *testing.T) {
	// Verify that `?` *expression* type is the inner T, usable in arithmetic.
	src := "fn maybe() -> int? { return Option.Some(2) }\n" +
		"fn outer() -> int? {\n" +
		"  n := maybe()? + 1\n" +
		"  return Option.Some(n)\n" +
		"}\n"
	checkSrc(t, src)
}

// Quick sanity: TypeError messages aren't accidentally swallowed.
func TestV06DiagnosticAnchors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"propagate-out-of-context", "fn m() -> int? { return Option.Some(1) }\nv := m()?\n", "?"},
		{"coalesce-bad-lhs", "y := 3 ?? 0\n", "??"},
		{"safe-on-non-nullable", "struct B { v: int }\nb := B { v: 1 }\nx := b?.v\n", "?."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkErr(t, tc.src, tc.want)
			if !strings.Contains(err, tc.want) {
				t.Errorf("err %q missing %q", err, tc.want)
			}
		})
	}
}
