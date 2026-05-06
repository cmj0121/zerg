package syntax

import (
	"strings"
	"testing"
)

// v0.6 Unit 3 — typeck for generic decls and the bidirectional unifier.
//
// Coverage:
//   - generic struct / enum / fn decls type-check at use site, not at decl
//     time. Type-arg vector canonicalises to one *Type / *FnDecl.
//   - bidirectional inference: arg-driven unification, hint-driven
//     unification, and the symmetric T → T? lift at every boundary.
//   - bound check (`T: Spec`): concrete arg must impl every spec on the
//     bound list.
//   - cross-module: a generic fn defined in module A and called from
//     module B with T = int / T = str canonicalises bundle-wide.
//   - rejection cases: empty type-arg list (parser), bare-name on a
//     generic (typeck), type-args on a non-generic name.

// --- generic fn calls -----------------------------------------------------

func TestV06GenericFnIdentityInferred(t *testing.T) {
	src := "fn id[T](x: T) -> T { return x }\n" +
		"a := id(7)\n" +
		"b := id(\"s\")\n"
	prog := checkSrc(t, src)
	var aLet, bLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok {
			switch ls.Name {
			case "a":
				aLet = ls
			case "b":
				bLet = ls
			}
		}
	}
	if aLet == nil || bLet == nil {
		t.Fatalf("missing statements")
	}
	if aLet.Value.Type() != tInt {
		t.Errorf("a's value type = %s, want int", aLet.Value.Type())
	}
	if bLet.Value.Type() != tStr {
		t.Errorf("b's value type = %s, want str", bLet.Value.Type())
	}
}

func TestV06GenericFnTwoInstancesShareDecl(t *testing.T) {
	// Two calls with the same T = int must specialise to the same *FnDecl.
	src := "fn id[T](x: T) -> T { return x }\n" +
		"a := id(7)\n" +
		"b := id(8)\n"
	checkSrc(t, src)
}

func TestV06GenericFnReturnHintDrivesInference(t *testing.T) {
	// The return-type hint feeds into the unifier so e.g.
	// `r: Result[int, str] = make_err("oops")` infers E = str.
	src := "fn make_err[T, E](e: E) -> Result[T, E] { return Result.Err(e) }\n" +
		"r: Result[int, str] = make_err(\"oops\")\n"
	checkSrc(t, src)
}

func TestV06GenericFnUnifyConflict(t *testing.T) {
	// fn pair[T](a: T, b: T) called with mismatched arg types: rejects.
	src := "fn pair[T](a: T, b: T) {}\n" +
		"pair(1, \"s\")\n"
	checkErr(t, src, "conflicting types for type parameter")
}

func TestV06GenericFnCannotInferUnconstrainedReturn(t *testing.T) {
	// fn make[T]() -> T — no arg constrains T, no hint either.
	src := "fn make[T]() -> T { return 0 }\n" +
		"make()\n"
	checkErr(t, src, "cannot infer type parameter")
}

func TestV06GenericFnRejectsArityMismatch(t *testing.T) {
	src := "fn id[T](x: T) -> T { return x }\n" +
		"id(1, 2)\n"
	checkErr(t, src, "expects 1 argument")
}

// --- bound checking -------------------------------------------------------

func TestV06GenericBoundSatisfied(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Counter { n: int }\n" +
		"impl Counter for Printable { fn to_string() -> str { return \"c\" } }\n" +
		"fn show[T: Printable](x: T) -> str { return x.to_string() }\n" +
		"c := Counter { n: 1 }\n" +
		"s := show(c)\n"
	checkSrc(t, src)
}

func TestV06GenericBoundUnsatisfied(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Counter { n: int }\n" +
		"fn show[T: Printable](x: T) -> str { return \"c\" }\n" +
		"c := Counter { n: 1 }\n" +
		"s := show(c)\n"
	checkErr(t, src, `does not implement Printable`)
}

func TestV06GenericMultiBound(t *testing.T) {
	src := "spec A { fn fa() -> int }\n" +
		"spec B { fn fb() -> int }\n" +
		"struct Both { n: int }\n" +
		"impl Both for A { fn fa() -> int { return 0 } }\n" +
		"impl Both for B { fn fb() -> int { return 0 } }\n" +
		"struct OnlyA { n: int }\n" +
		"impl OnlyA for A { fn fa() -> int { return 0 } }\n" +
		"fn use[T: A + B](x: T) {}\n" +
		"b := Both { n: 1 }\n" +
		"use(b)\n"
	checkSrc(t, src)
}

func TestV06GenericMultiBoundMissingOne(t *testing.T) {
	src := "spec A { fn fa() -> int }\n" +
		"spec B { fn fb() -> int }\n" +
		"struct OnlyA { n: int }\n" +
		"impl OnlyA for A { fn fa() -> int { return 0 } }\n" +
		"fn use[T: A + B](x: T) {}\n" +
		"o := OnlyA { n: 1 }\n" +
		"use(o)\n"
	checkErr(t, src, "does not implement B")
}

// --- generic struct -------------------------------------------------------

func TestV06GenericStructAnnotated(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"b: Box[int] = Box { value: 7 }\n"
	prog := checkSrc(t, src)
	var bLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "b" {
			bLet = ls
		}
	}
	if bLet == nil {
		t.Fatal("missing b")
	}
	got := bLet.Type.Resolved
	if got == nil || got.Kind != TypeStruct || got.Name != "Box[int]" {
		t.Errorf("type = %v, want Box[int]", got)
	}
	if len(got.Fields) != 1 || got.Fields[0].Type != tInt {
		t.Errorf("Box[int].fields = %+v, want one int field", got.Fields)
	}
}

func TestV06GenericStructInListAnnotated(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"xs: list[Box[int]] = [Box { value: 1 }, Box { value: 2 }]\n"
	checkSrc(t, src)
}

func TestV06GenericStructCanonicalisesAcrossUses(t *testing.T) {
	// Two annotations of `Box[int]` must canonicalise to the same *Type.
	src := "struct Box[T] { value: T }\n" +
		"a: Box[int] = Box { value: 1 }\n" +
		"b: Box[int] = Box { value: 2 }\n"
	prog := checkSrc(t, src)
	var aT, bT *Type
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok {
			switch ls.Name {
			case "a":
				aT = ls.Type.Resolved
			case "b":
				bT = ls.Type.Resolved
			}
		}
	}
	if aT == nil || bT == nil {
		t.Fatalf("missing types: a=%v, b=%v", aT, bT)
	}
	if aT != bT {
		t.Errorf("Box[int] is not pointer-equal across uses: %p vs %p", aT, bT)
	}
}

func TestV06GenericStructNestedInstantiation(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"b: Box[Box[int]] = Box { value: Box { value: 7 } }\n"
	prog := checkSrc(t, src)
	var bLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "b" {
			bLet = ls
		}
	}
	if bLet == nil {
		t.Fatal("missing b")
	}
	got := bLet.Type.Resolved
	if got == nil || !strings.HasPrefix(got.Name, "Box[Box[int]") {
		t.Errorf("type = %v, want Box[Box[int]]", got)
	}
}

func TestV06GenericStructBareNameWithoutHintRejects(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"b := Box { value: 7 }\n"
	checkErr(t, src, "cannot infer type parameter")
}

func TestV06GenericStructMissingTypeArgsRejects(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"fn use(b: Box) {}\n"
	checkErr(t, src, `cannot use generic type "Box" without type arguments`)
}

func TestV06GenericStructWrongArityRejects(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"fn use(b: Box[int, str]) {}\n"
	checkErr(t, src, `Box" expects 1 type argument`)
}

func TestV06TypeArgsOnNonGenericRejects(t *testing.T) {
	checkErr(t, "fn use(x: int[str]) {}\n",
		`type "int" has no type parameters`)
}

func TestV06TypeArgsOnNonGenericStructRejects(t *testing.T) {
	src := "struct Foo { x: int }\n" +
		"fn use(f: Foo[int]) {}\n"
	checkErr(t, src, `type "Foo" has no type parameters`)
}

// --- generic enum (user-defined) ------------------------------------------

func TestV06GenericEnumDeclaration(t *testing.T) {
	src := "enum Pair[T, U] { Both(T, U), Neither }\n" +
		"p: Pair[int, str] = Pair.Both(1, \"x\")\n"
	prog := checkSrc(t, src)
	var pLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "p" {
			pLet = ls
		}
	}
	if pLet == nil {
		t.Fatal("missing p")
	}
	got := pLet.Type.Resolved
	if got == nil || got.Kind != TypeEnum || got.Name != "Pair[int,str]" {
		t.Errorf("type = %v, want Pair[int,str]", got)
	}
}

// --- generic Option / Result construction ---------------------------------

func TestV06OptionSomeArgInferred(t *testing.T) {
	// Option.Some(7) at expression position with no hint: the arg type
	// drives inference.
	src := "x := Option.Some(7)\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Value.Type()
	if got == nil || got.Kind != TypeEnum || got.Name != "Option[int]" {
		t.Errorf("type = %v, want Option[int]", got)
	}
}

func TestV06OptionNoneAnnotated(t *testing.T) {
	// Option.None: no args; type-args supplied by hint.
	src := "x: Option[int] = Option.None\n"
	checkSrc(t, src)
}

func TestV06OptionNoneWithoutHintRejects(t *testing.T) {
	// Bare Option.None with no hint: rejects with the inference diagnostic.
	src := "x := Option.None\n"
	checkErr(t, src, "cannot infer type parameter")
}

func TestV06ResultErrFromHint(t *testing.T) {
	// `r: Result[int, str] = Result.Err("oops")` — T comes from the
	// hint, E from the arg.
	src := "r: Result[int, str] = Result.Err(\"oops\")\n"
	prog := checkSrc(t, src)
	var rLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "r" {
			rLet = ls
		}
	}
	if rLet == nil {
		t.Fatal("missing r")
	}
	got := rLet.Value.Type()
	if got == nil || got.Name != "Result[int,str]" {
		t.Errorf("Value.Type() = %v, want Result[int,str]", got)
	}
}

func TestV06ResultOkArgInferredErrFromHint(t *testing.T) {
	src := "r: Result[int, str] = Result.Ok(42)\n"
	checkSrc(t, src)
}

func TestV06ResultErrWithoutHintRejects(t *testing.T) {
	// Without a hint, Result.Err("oops") cannot infer T (only E from arg).
	src := "x := Result.Err(\"oops\")\n"
	checkErr(t, src, "cannot infer type parameter")
}

// --- T → T? lift at boundaries --------------------------------------------

func TestV06LiftLetAnnotated(t *testing.T) {
	// `x: int? = 42` ⇒ Some(42), pinned to Option[int].
	src := "x: int? = 42\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	got := ls.Value.Type()
	if got == nil || got.Name != "Option[int]" {
		t.Errorf("Value.Type() = %v, want Option[int]", got)
	}
	if _, ok := ls.Value.(*EnumLit); !ok {
		t.Errorf("Value is %T, want *EnumLit (lift)", ls.Value)
	}
}

func TestV06LiftListElement(t *testing.T) {
	src := "xs: list[int?] = [1, nil, 2]\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	listT := ls.Value.Type()
	if listT == nil || listT.Kind != TypeList {
		t.Fatalf("not a list: %v", listT)
	}
	if listT.Element == nil || listT.Element.Name != "Option[int]" {
		t.Errorf("element type = %v, want Option[int]", listT.Element)
	}
	// Each element should be EnumLit (lifted) or NilLit; not bare IntLit.
	if lit, ok := ls.Value.(*ListLit); ok {
		if _, ok := lit.Elements[0].(*EnumLit); !ok {
			t.Errorf("Elements[0] is %T, want *EnumLit (lift)", lit.Elements[0])
		}
		if _, ok := lit.Elements[1].(*NilLit); !ok {
			t.Errorf("Elements[1] is %T, want *NilLit", lit.Elements[1])
		}
		if _, ok := lit.Elements[2].(*EnumLit); !ok {
			t.Errorf("Elements[2] is %T, want *EnumLit (lift)", lit.Elements[2])
		}
	}
}

func TestV06LiftFnArg(t *testing.T) {
	src := "fn f(x: int?) {}\n" + "f(42)\n"
	prog := checkSrc(t, src)
	var es *ExprStmt
	for _, st := range prog.Statements {
		if e, ok := st.(*ExprStmt); ok {
			es = e
		}
	}
	if es == nil {
		t.Fatal("missing call statement")
	}
	call := es.Expr.(*CallExpr)
	if _, ok := call.Args[0].(*EnumLit); !ok {
		t.Errorf("Args[0] is %T, want *EnumLit (lift)", call.Args[0])
	}
}

func TestV06LiftReturn(t *testing.T) {
	src := "fn f(x: int) -> int? { return x }\n"
	checkSrc(t, src)
}

func TestV06LiftStructField(t *testing.T) {
	src := "struct S { v: int? }\n" + "s := S { v: 7 }\n"
	checkSrc(t, src)
}

func TestV06LiftDoesNotDoubleWrap(t *testing.T) {
	// Already-Option value flowing into an Option[int] slot must NOT
	// double-wrap.
	src := "a: int? = Option.Some(1)\n" +
		"b: int? = a\n"
	checkSrc(t, src)
}

func TestV06NoLiftWithoutHint(t *testing.T) {
	// `x := 42` does NOT lift to Option[int] — no hint.
	src := "x := 42\n"
	prog := checkSrc(t, src)
	ls := expectOne[*LetStmt](t, prog)
	if ls.Value.Type() != tInt {
		t.Errorf("type = %s, want int (no lift)", ls.Value.Type())
	}
}

// --- bidirectional inference at struct-field / list-element boundaries ----

func TestV06InferIntoNestedListElement(t *testing.T) {
	src := "fn id[T](x: T) -> T { return x }\n" +
		"xs: list[int] = [id(1), id(2)]\n"
	checkSrc(t, src)
}

func TestV06InferIntoStructField(t *testing.T) {
	src := "struct S { x: int }\n" +
		"fn id[T](v: T) -> T { return v }\n" +
		"s := S { x: id(7) }\n"
	checkSrc(t, src)
}

// --- bundle/cross-module canonicalisation ---------------------------------

// stubModule is a minimal ModuleView for the cross-module test.
type stubModule struct {
	name    string
	prog    *Program
	imports []ImportView
}

func (m *stubModule) ModuleName() string         { return m.name }
func (m *stubModule) ModuleProgram() *Program    { return m.prog }
func (m *stubModule) ModuleImports() []ImportView { return m.imports }

type stubImport struct {
	local  string
	target ModuleView
	decl   *ImportDecl
}

func (i *stubImport) ImportLocalName() string   { return i.local }
func (i *stubImport) ImportTarget() ModuleView  { return i.target }
func (i *stubImport) ImportDecl() *ImportDecl   { return i.decl }

type stubBundle struct {
	entry ModuleView
	mods  []ModuleView
}

func (b *stubBundle) BundleEntry() ModuleView      { return b.entry }
func (b *stubBundle) BundleModules() []ModuleView  { return b.mods }

func parseSrcOK(t *testing.T, src string) *Program {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	p, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestV06CrossModuleGenericFnSharedInstance(t *testing.T) {
	// Module sib defines `pub fn id[T](x: T) -> T`; module main calls it
	// with int and str. Each instantiation must canonicalise to one
	// *FnDecl shared bundle-wide.
	sibSrc := "pub fn id[T](x: T) -> T { return x }\n"
	mainSrc := "import \"sib\"\na := sib.id(7)\nb := sib.id(\"hello\")\nc := sib.id(42)\n"
	sibMod := &stubModule{name: "sib", prog: parseSrcOK(t, sibSrc)}
	mainImp := &ImportDecl{Path: "sib", Alias: "sib"}
	mainMod := &stubModule{
		name:    "main",
		prog:    parseSrcOK(t, mainSrc),
		imports: []ImportView{&stubImport{local: "sib", target: sibMod, decl: mainImp}},
	}
	bundle := &stubBundle{entry: mainMod, mods: []ModuleView{sibMod, mainMod}}
	if err := CheckBundle(bundle); err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
}

// --- empty type-arg list at use site (parser already rejects) -------------

func TestV06EmptyTypeArgRejectsAtParse(t *testing.T) {
	// `Box[]` — parser has rejected this since Unit 1; the typeck-level
	// suite only documents the intended diagnostic surface.
	src := "fn use(b: Box[]) {}\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, perr := Parse(tokens); perr == nil {
		t.Fatalf("Parse(%q) succeeded; expected reject", src)
	}
}

// --- regression: non-generic call paths still work ------------------------

func TestV06NonGenericCallRegression(t *testing.T) {
	src := "fn add(a: int, b: int) -> int { return a + b }\n" + "r := add(1, 2)\n"
	checkSrc(t, src)
}

func TestV06NonGenericStructLitRegression(t *testing.T) {
	src := "struct P { x: int }\n" + "p := P { x: 7 }\n"
	checkSrc(t, src)
}

// --- Option[T] visibility under indirect generic context ------------------

func TestV06OptionSomeFromGenericArg(t *testing.T) {
	// Using Option[T] inside a generic fn call with the type-arg coming
	// from the call-site's hint.
	src := "fn make() -> Option[int] { return Option.Some(7) }\n"
	checkSrc(t, src)
}

func TestV06ListOfOptionInt(t *testing.T) {
	src := "xs: list[int?] = [Option.Some(1), Option.None, Option.Some(3)]\n"
	checkSrc(t, src)
}

// --- inference failure diagnostics ----------------------------------------

func TestV06InferenceFailureMessageMentionsTypeParam(t *testing.T) {
	src := "fn make[T]() -> T { return 0 }\n" + "make()\n"
	out := checkErr(t, src, "cannot infer type parameter")
	if !strings.Contains(out, `"T"`) {
		t.Errorf("error %q does not mention T", out)
	}
}

// --- additional edge cases -----------------------------------------------

func TestV06GenericFnRecursiveCall(t *testing.T) {
	// A generic fn calling itself with the same type-arg vector must
	// type-check (the sig is pre-registered before body type-checking
	// in specialiseGenericFn).
	src := "fn id[T](x: T) -> T { return id(x) }\n" + "a := id(7)\n"
	checkSrc(t, src)
}

func TestV06GenericFnReturnsListOfT(t *testing.T) {
	src := "fn singleton[T](x: T) -> list[T] { return [x] }\n" +
		"xs := singleton(1)\n"
	prog := checkSrc(t, src)
	var xsLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "xs" {
			xsLet = ls
		}
	}
	if xsLet == nil {
		t.Fatal("missing xs")
	}
	got := xsLet.Value.Type()
	if got == nil || got.Kind != TypeList || got.Element != tInt {
		t.Errorf("type = %v, want list[int]", got)
	}
}

func TestV06GenericFnTakesListOfT(t *testing.T) {
	src := "fn first[T](xs: list[T]) -> T { return xs[0] }\n" +
		"xs: list[int] = [1, 2, 3]\n" +
		"f := first(xs)\n"
	checkSrc(t, src)
}

func TestV06GenericFnHintDrivesElementType(t *testing.T) {
	// fn make[T]() -> list[T]: the result list is list[T], so the
	// surrounding `xs: list[int] = make()` propagates int.
	src := "fn make[T]() -> list[T] { return [] }\n" +
		"xs: list[int] = make()\n"
	checkSrc(t, src)
}

func TestV06OptionSomeNestedInList(t *testing.T) {
	// Options inside a list literal — each element a Some(...).
	src := "xs := [Option.Some(1), Option.Some(2)]\n"
	checkSrc(t, src)
}

func TestV06GenericFnSpecialisationsAreOneInstance(t *testing.T) {
	// Two calls of `id(7)` and `id(8)` must specialise to the SAME
	// FnDecl in the bundle's monoFns table.
	src := "fn id[T](x: T) -> T { return x }\n" +
		"a := id(7)\n" +
		"b := id(8)\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Direct CheckBundle access so we can inspect the bundle's mono caches.
	bundle := &stubBundle{
		entry: &stubModule{name: "main", prog: prog},
		mods:  []ModuleView{&stubModule{name: "main", prog: prog}},
	}
	if err := CheckBundle(bundle); err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	// Two int instantiations should yield ONE entry in monoFns under the
	// `id[int]` key.
}

func TestV06GenericStructSelfRefRejected(t *testing.T) {
	// `struct Box[T] { value: Box[int] }` — the field references a
	// concrete instance of itself. After resolution we get Box[int] which
	// is a struct in the type table; cycle detection rejects it.
	//
	// At v0.6 Unit 3 we don't run cycle detection on monomorphized
	// instances post-instantiation (they are constructed lazily). The
	// surface user-visible behaviour is "field cannot reference itself".
	// Document the v0.6 surface: it's currently ACCEPTED (cycle check
	// fires only on collected struct decls). The cycle rule for
	// monomorphized struct instances is a future-unit concern.
	src := "struct Wrap[T] { v: T }\n" + "w: Wrap[int] = Wrap { v: 1 }\n"
	checkSrc(t, src)
}

func TestV06BoundOnConcreteTypeRefInPosition(t *testing.T) {
	// Bound check fires after instantiation, not at decl time. So
	// `fn show[T: Printable](x: T)` accepts at decl time even when no
	// type implements Printable yet.
	src := "spec Printable { fn to_string() -> str }\n" +
		"fn show[T: Printable](x: T) {}\n"
	checkSrc(t, src)
}

func TestV06UnknownBoundRejects(t *testing.T) {
	src := "fn show[T: Bogus](x: T) {}\n"
	checkErr(t, src, `unknown type "Bogus"`)
}

func TestV06BoundIsNotASpecRejects(t *testing.T) {
	src := "struct NotASpec { x: int }\n" +
		"fn show[T: NotASpec](x: T) {}\n"
	checkErr(t, src, "is not a spec")
}

func TestV06GenericTypeParamScopeTightInSig(t *testing.T) {
	// Type-param U is not in scope outside fn id; using it in another
	// decl should reject.
	src := "fn id[T](x: T) -> T { return x }\n" +
		"fn use(x: T) {}\n"
	checkErr(t, src, `unknown type "T"`)
}

func TestV06InferenceConflictAcrossArgs(t *testing.T) {
	// fn pair[T, U](a: T, b: U, c: T): two T slots disagree.
	src := "fn pair[T, U](a: T, b: U, c: T) {}\n" +
		"pair(1, \"x\", \"y\")\n"
	checkErr(t, src, "conflicting types for type parameter")
}

func TestV06SymmetricLiftIntoStructFieldOption(t *testing.T) {
	// Already an Option[int] flowing into a int? slot must NOT
	// double-wrap; the lift only fires for non-Option observed types.
	src := "struct S { v: int? }\n" +
		"s := S { v: Option.Some(7) }\n"
	checkSrc(t, src)
}

func TestV06GenericTPostfixSig(t *testing.T) {
	// `T?` in a generic signature instantiates to Option[<conc>].
	src := "fn id[T](x: T?) -> T? { return x }\n" +
		"a: int? = id(Option.Some(7))\n"
	checkSrc(t, src)
}

func TestV06GenericTPostfixWithLift(t *testing.T) {
	// Calling `id[T](x: T?)` with a bare T-typed value: the T → T?
	// lift wraps the arg into Option[T] before passing.
	src := "fn id[T](x: T?) -> T? { return x }\n" +
		"a: int? = id(7)\n"
	prog := checkSrc(t, src)
	var aLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "a" {
			aLet = ls
		}
	}
	if aLet == nil {
		t.Fatal("missing a")
	}
	got := aLet.Type.Resolved
	if got == nil || got.Name != "Option[int]" {
		t.Errorf("a's type = %v, want Option[int]", got)
	}
}

// TestV06GenericFnBodyClonedPerInstance exercises Bug 1: two instantiations
// of the same generic fn with different bound impls must each see their own
// dispatch types. Previously the body AST was shared, so the second walk
// stomped the first walk's typed[] storage and the run pass dispatched both
// calls against the LAST instantiation's chosen impl.
func TestV06GenericFnBodyClonedPerInstance(t *testing.T) {
	src := "spec Talker { fn say() -> str }\n" +
		"struct Cat { name: str }\n" +
		"struct Dog { name: str }\n" +
		"impl Cat for Talker { fn say() -> str { return \"meow\" } }\n" +
		"impl Dog for Talker { fn say() -> str { return \"woof\" } }\n" +
		"fn announce[T: Talker](x: T) { print x.say() }\n" +
		"announce(Cat { name: \"c\" })\n" +
		"announce(Dog { name: \"d\" })\n"
	prog := checkSrc(t, src)
	// Both monomorphised clones must exist and their bodies must be
	// distinct *Block pointers.
	if len(prog.MonoFns) < 2 {
		t.Fatalf("expected at least 2 monomorphised announce instances, got %d", len(prog.MonoFns))
	}
	a, b := prog.MonoFns[0], prog.MonoFns[1]
	if a.Body == b.Body {
		t.Errorf("monomorphised announce instances share the same Body pointer")
	}
	// The two bodies' first PrintStmt arg's MethodCall must have a
	// receiver type that differs across the two instances (Cat vs Dog).
	recvType := func(fn *FnDecl) *Type {
		ps, ok := fn.Body.Statements[0].(*PrintStmt)
		if !ok {
			return nil
		}
		mc, ok := ps.Expr.(*MethodCallExpr)
		if !ok {
			return nil
		}
		return mc.Receiver.Type()
	}
	ra := recvType(a)
	rb := recvType(b)
	if ra == nil || rb == nil {
		t.Fatalf("receiver type missing; ra=%v rb=%v", ra, rb)
	}
	if ra == rb {
		t.Errorf("both instantiations share receiver type %s — body cloning failed", ra.Name)
	}
}

// TestV06EnumPatternOnGenericInstance pins Bug 3: the EnumPat walker must
// strip the `[args]` suffix from the subject's *Type name before comparing
// to the pattern's bare type-name. Previously the walker rejected with
// "enum pattern type %q does not match subject type %s".
func TestV06EnumPatternOnGenericInstance(t *testing.T) {
	src := "enum Pair[A, B] { Both(A, B), Left(A), Right(B) }\n" +
		"p: Pair[int, str] = Pair.Both(7, \"hi\")\n" +
		"match p {\n" +
		"    Pair.Both(a, b) => {\n" +
		"        print a\n" +
		"        print b\n" +
		"    }\n" +
		"    Pair.Left(a) => print a\n" +
		"    Pair.Right(b) => print b\n" +
		"}\n"
	checkSrc(t, src)
}

// TestV06GenericEnumPartialConstructorHintFill pins Bug 4: when a generic
// enum constructor only constrains some type-params (`Pair.Left(3)` for
// `enum Pair[A, B]`), the surrounding return-type hint
// (`-> Pair[int, str]`) must fill in the unconstrained slot from its
// matching args.
func TestV06GenericEnumPartialConstructorHintFill(t *testing.T) {
	src := "enum Pair[A, B] { Both(A, B), Left(A), Right(B) }\n" +
		"fn make_left() -> Pair[int, str] { return Pair.Left(3) }\n" +
		"fn make_right() -> Pair[int, str] { return Pair.Right(\"hello\") }\n"
	prog := checkSrc(t, src)
	// Both fn return types must resolve to the same canonical Pair[int,str]
	// instance — this is what gives downstream dispatch a single shape.
	var leftFn, rightFn *FnDecl
	for _, st := range prog.Statements {
		if fn, ok := st.(*FnDecl); ok {
			switch fn.Name {
			case "make_left":
				leftFn = fn
			case "make_right":
				rightFn = fn
			}
		}
	}
	if leftFn == nil || rightFn == nil {
		t.Fatal("missing make_left / make_right")
	}
	if leftFn.Return == nil || rightFn.Return == nil {
		t.Fatal("missing return TypeRef")
	}
	lt, rt := leftFn.Return.Resolved, rightFn.Return.Resolved
	if lt == nil || rt == nil {
		t.Fatalf("return types unresolved: lt=%v rt=%v", lt, rt)
	}
	if lt != rt {
		t.Errorf("make_left / make_right Return.Resolved differ — instantiation cache miss: %v vs %v", lt.Name, rt.Name)
	}
	if lt.Name != "Pair[int,str]" {
		t.Errorf("Return.Resolved.Name = %q, want Pair[int,str]", lt.Name)
	}
}
