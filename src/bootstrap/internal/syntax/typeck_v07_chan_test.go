package syntax

import (
	"testing"
)

// v0.7 Unit 2 — typeck for chan[T] constructors, send / receive, close(),
// and `for v in ch` desugaring. Coverage:
//
//   - TypeChan registers; chan[T] in type position resolves; canonical pointer.
//   - chan[T]() / chan[T](N) constructor typecheck.
//   - ch <- v send statement typecheck (with value-type matching, T → T? lift).
//   - <- ch receive expression yields Option[T].
//   - close(ch) built-in: arity, chan-type argument, void result.
//   - for v in ch: marks ForChan, binds v with type T.
//   - print of channel rejects.
//   - chan / close reservation diagnostics at every binding site.

// --- TypeChan registration -----------------------------------------------

func TestV07ChanTypePositionResolves(t *testing.T) {
	prog := checkSrc(t, "fn run(ch: chan[int]) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	got := fn.Params[0].Type.Resolved
	if got == nil || got.Kind != TypeChan {
		t.Fatalf("kind = %v, want TypeChan", got)
	}
	if got.Element != tInt {
		t.Errorf("element = %v, want int", got.Element)
	}
	if got.String() != "chan[int]" {
		t.Errorf("String = %q, want chan[int]", got.String())
	}
}

func TestV07ChanCanonicalPointerEquality(t *testing.T) {
	src := "fn a(ch: chan[int]) {}\nfn b(ch: chan[int]) {}\n"
	prog := checkSrc(t, src)
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements", len(prog.Statements))
	}
	a := prog.Statements[0].(*FnDecl).Params[0].Type.Resolved
	b := prog.Statements[1].(*FnDecl).Params[0].Type.Resolved
	if a != b {
		t.Errorf("two `chan[int]` resolutions yielded distinct pointers: a=%p b=%p", a, b)
	}
}

func TestV07ChanZeroTypeArgsRejects(t *testing.T) {
	checkErr(t, "fn run(ch: chan) {}\n",
		"chan takes exactly one type argument, got 0")
}

func TestV07ChanTwoTypeArgsRejects(t *testing.T) {
	checkErr(t, "fn run(ch: chan[int, str]) {}\n",
		"chan takes exactly one type argument, got 2")
}

// --- ChanConstructorExpr -------------------------------------------------

func TestV07ChanConstructorUnbuffered(t *testing.T) {
	prog := checkSrc(t, "ch := chan[int]()\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got == nil || got.Kind != TypeChan || got.Element != tInt {
		t.Errorf("type = %v, want chan[int]", got)
	}
	cc := s.Value.(*ChanConstructorExpr)
	if cc.Capacity != nil {
		t.Errorf("capacity = %v, want nil (unbuffered)", cc.Capacity)
	}
}

func TestV07ChanConstructorBuffered(t *testing.T) {
	prog := checkSrc(t, "ch := chan[str](10)\n")
	s := expectOne[*LetStmt](t, prog)
	got := s.Value.Type()
	if got == nil || got.Kind != TypeChan || got.Element != tStr {
		t.Errorf("type = %v, want chan[str]", got)
	}
}

func TestV07ChanConstructorWithExprCapacity(t *testing.T) {
	// Capacity may be any int expression; runtime panics on negative values.
	checkSrc(t, "fn run(n: int) { ch := chan[int](n + 1) }\n")
}

func TestV07ChanConstructorBoolCapacityRejects(t *testing.T) {
	checkErr(t, "ch := chan[int](true)\n",
		"chan capacity must be int")
}

func TestV07ChanConstructorStringCapacityRejects(t *testing.T) {
	checkErr(t, `ch := chan[int]("hi")`+"\n",
		"chan capacity must be int")
}

// --- annotated binding with chan ---------------------------------------

func TestV07ChanLetAnnotated(t *testing.T) {
	prog := checkSrc(t, "ch: chan[int] = chan[int]()\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Type == nil || s.Type.Resolved == nil {
		t.Fatalf("type ref unresolved")
	}
	if s.Type.Resolved.Kind != TypeChan {
		t.Errorf("kind = %v, want TypeChan", s.Type.Resolved.Kind)
	}
}

// --- SendStmt -------------------------------------------------------------

func TestV07SendOK(t *testing.T) {
	checkSrc(t, "fn run(ch: chan[int]) { ch <- 5 }\n")
}

func TestV07SendStringChannel(t *testing.T) {
	checkSrc(t, `fn run(ch: chan[str]) { ch <- "hi" }`+"\n")
}

func TestV07SendTypeMismatch(t *testing.T) {
	checkErr(t, `fn run(ch: chan[int]) { ch <- "x" }`+"\n",
		"cannot send value of type str on chan[int]")
}

func TestV07SendIntoNonChannel(t *testing.T) {
	checkErr(t, "fn run(x: int) { x <- 5 }\n",
		"send requires a channel on the left")
}

func TestV07SendIntoOptionLifts(t *testing.T) {
	// T → T? lift on send: a chan[int?] admits an int value, wrapped as
	// Some(...) at the boundary.
	checkSrc(t, "fn run(ch: chan[int?]) { ch <- 5 }\n")
}

func TestV07SendNilToOptionChannel(t *testing.T) {
	// `nil` flows into a chan[Option[int]] at typeck — same v0.6 None path.
	checkSrc(t, "fn run(ch: chan[int?]) { ch <- nil }\n")
}

// --- RecvExpr -------------------------------------------------------------

func TestV07RecvYieldsOption(t *testing.T) {
	prog := checkSrc(t, "fn run(ch: chan[int]) { v := <- ch }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[0].(*LetStmt)
	got := let.Value.Type()
	if got == nil || got.Kind != TypeEnum {
		t.Fatalf("kind = %v, want TypeEnum (Option[int])", got)
	}
	if got.Name != "Option[int]" {
		t.Errorf("name = %q, want Option[int]", got.Name)
	}
}

func TestV07RecvFromNonChannel(t *testing.T) {
	checkErr(t, "fn run() { v := <- 42 }\n",
		"receive requires a channel operand")
}

func TestV07RecvDiscardStmt(t *testing.T) {
	// Bare `<- ch` at statement position is admitted (typeck checks the recv;
	// the value is discarded by the ExprStmt wrapper).
	checkSrc(t, "fn run(ch: chan[int]) { <- ch }\n")
}

// --- close() built-in -----------------------------------------------------

func TestV07CloseOK(t *testing.T) {
	checkSrc(t, "fn run(ch: chan[int]) { close(ch) }\n")
}

func TestV07CloseNoArgsRejects(t *testing.T) {
	checkErr(t, "fn run() { close() }\n",
		`function "close" expects 1 argument`)
}

func TestV07CloseExtraArgsRejects(t *testing.T) {
	checkErr(t, "fn run(a: chan[int], b: chan[int]) { close(a, b) }\n",
		`function "close" expects 1 argument`)
}

func TestV07CloseNonChannelRejects(t *testing.T) {
	checkErr(t, "fn run() { close(42) }\n",
		"close: argument must be a channel")
}

func TestV07CloseListRejects(t *testing.T) {
	checkErr(t, "fn run(xs: list[int]) { close(xs) }\n",
		"close: argument must be a channel")
}

// --- for v in ch ----------------------------------------------------------

func TestV07ForVInChanBindsElement(t *testing.T) {
	prog := checkSrc(t, "fn run(ch: chan[int]) { for v in ch { print v } }\n")
	fn := expectOne[*FnDecl](t, prog)
	for_ := fn.Body.Statements[0].(*ForStmt)
	if for_.Kind != ForChan {
		t.Errorf("kind = %v, want ForChan (re-tagged from ForIter)", for_.Kind)
	}
	// The body's `print v` succeeded — that asserts `v` was in scope with a
	// printable element type. Spot-check the element-type is int by checking
	// the iter expr's resolved type.
	chT := for_.Iter.Type()
	if chT == nil || chT.Kind != TypeChan || chT.Element != tInt {
		t.Errorf("iter type = %v, want chan[int]", chT)
	}
}

func TestV07ForVInChanScopeOnlyInBody(t *testing.T) {
	// `v` is in scope only inside the body — referencing it after the loop
	// must fail with the standard undefined-name diagnostic.
	checkErr(t,
		"fn run(ch: chan[int]) {\nfor v in ch { nop }\nprint v\n}\n",
		`undefined name "v"`)
}

func TestV07ForVInListStillWorks(t *testing.T) {
	// `for v in xs` (list) keeps the v0.2 ForIter shape; the new ForChan
	// branch must NOT poach list iteration.
	prog := checkSrc(t, "fn run(xs: list[int]) { for v in xs { print v } }\n")
	fn := expectOne[*FnDecl](t, prog)
	for_ := fn.Body.Statements[0].(*ForStmt)
	if for_.Kind != ForIter {
		t.Errorf("kind = %v, want ForIter (unchanged for list)", for_.Kind)
	}
}

func TestV07ForVInIntRejects(t *testing.T) {
	checkErr(t, "fn run() { for v in 5 { } }\n",
		"'for ... in' iterable must be a list or channel")
}

// --- print of channel ----------------------------------------------------

func TestV07PrintChannelRejects(t *testing.T) {
	checkErr(t, "fn run(ch: chan[int]) { print ch }\n",
		"cannot print channel value (channels are not Printable)")
}

// --- reservation: chan ---------------------------------------------------

func TestV07ChanReservedAsLetName(t *testing.T) {
	checkErr(t, "chan := 1\n",
		`name "chan" is reserved (built-in)`)
}

func TestV07ChanReservedAsMutName(t *testing.T) {
	checkErr(t, "mut chan := 1\n",
		`name "chan" is reserved (built-in)`)
}

func TestV07ChanReservedAsConstName(t *testing.T) {
	checkErr(t, "const chan := 1\n",
		`name "chan" is reserved (built-in)`)
}

func TestV07ChanReservedAsFnName(t *testing.T) {
	checkErr(t, "fn chan() { }\n",
		`name "chan" is reserved (built-in)`)
}

func TestV07ChanReservedAsStructName(t *testing.T) {
	checkErr(t, "struct chan { x: int }\n",
		`name "chan" is reserved (built-in)`)
}

func TestV07ChanReservedAsEnumName(t *testing.T) {
	checkErr(t, "enum chan { A, B }\n",
		`name "chan" is reserved (built-in)`)
}

// --- reservation: close --------------------------------------------------

func TestV07CloseReservedAsFnName(t *testing.T) {
	checkErr(t, "fn close(x: int) { }\n",
		`name "close" is reserved (built-in)`)
}

func TestV07CloseReservedAsLetName(t *testing.T) {
	checkErr(t, "close := 1\n",
		`name "close" is reserved (built-in)`)
}

// --- nested chan + annotated recv ----------------------------------------

func TestV07ChanOfChan(t *testing.T) {
	// chan[chan[int]] is a channel whose element type is itself a channel.
	// The element resolves to canonical chan[int]; the outer channel uses
	// that as its element type.
	prog := checkSrc(t, "fn run(meta: chan[chan[int]]) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	got := fn.Params[0].Type.Resolved
	if got == nil || got.Kind != TypeChan {
		t.Fatalf("kind = %v, want TypeChan", got)
	}
	if got.Element == nil || got.Element.Kind != TypeChan {
		t.Fatalf("element kind = %v, want TypeChan", got.Element)
	}
	if got.Element.Element != tInt {
		t.Errorf("inner element = %v, want int", got.Element.Element)
	}
}

func TestV07RecvIntoOptionAnnotated(t *testing.T) {
	// `v: int? = <- ch` — the annotation hint is Option[int]; the recv
	// expression already produces Option[int], so the assignment is direct.
	checkSrc(t, "fn run(ch: chan[int]) { v: int? = <- ch }\n")
}

// --- send via chained-target rejection scaffold --------------------------

func TestV07SendOptionLiftFromValue(t *testing.T) {
	// chan[Option[int]] admits a bare int via the v0.6 T → T? lift. After
	// typeck rewrites the value with a synthetic Some(...) wrapper, the send
	// is well-typed.
	prog := checkSrc(t, "fn run(ch: chan[int?]) { ch <- 5 }\n")
	fn := expectOne[*FnDecl](t, prog)
	send := fn.Body.Statements[0].(*SendStmt)
	if _, ok := send.Value.(*EnumLit); !ok {
		t.Errorf("after lift, value = %T, want *EnumLit (Some-wrapper)", send.Value)
	}
}
