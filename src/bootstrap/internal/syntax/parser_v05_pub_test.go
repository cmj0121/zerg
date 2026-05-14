package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.5 Unit 1a — parser tests for the `pub` visibility modifier.
//
// Unit 1a is parser-only: typeck / interpreter / codegen do not consume the
// Pub bit yet. These tests exercise the lexer keyword + AST field plumbing
// and the focused diagnostics for misplaced `pub`. Existing v0.0–v0.4
// programs (no `pub` anywhere) keep parsing with Pub=false on every decl —
// the regression tests below pin that behaviour.
// ---------------------------------------------------------------------------

func TestParsePubFn(t *testing.T) {
	prog := parseProgramSrc(t, "pub fn foo() {}\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Name != "foo" {
		t.Errorf("name = %q, want foo", fn.Name)
	}
	if !fn.Pub {
		t.Errorf("Pub = false, want true on `pub fn`")
	}
}

func TestParseBareFnIsPrivate(t *testing.T) {
	prog := parseProgramSrc(t, "fn foo() {}\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Pub {
		t.Errorf("Pub = true, want false on bare `fn` (regression)")
	}
}

func TestParsePubStruct(t *testing.T) {
	prog := parseProgramSrc(t, "pub struct S { x: int }\n")
	st := expectOne[*StructDecl](t, prog)
	if st.Name != "S" {
		t.Errorf("name = %q, want S", st.Name)
	}
	if !st.Pub {
		t.Errorf("Pub = false, want true on `pub struct`")
	}
}

func TestParseBareStructIsPrivate(t *testing.T) {
	prog := parseProgramSrc(t, "struct S { x: int }\n")
	st := expectOne[*StructDecl](t, prog)
	if st.Pub {
		t.Errorf("Pub = true, want false on bare `struct`")
	}
}

func TestParsePubEnum(t *testing.T) {
	prog := parseProgramSrc(t, "pub enum E { A, B }\n")
	en := expectOne[*EnumDecl](t, prog)
	if en.Name != "E" {
		t.Errorf("name = %q, want E", en.Name)
	}
	if !en.Pub {
		t.Errorf("Pub = false, want true on `pub enum`")
	}
}

func TestParsePubSpecAndDefaultMethodPrivate(t *testing.T) {
	// `pub spec X { fn ... }`: SpecDecl.Pub should be true; the inner
	// SpecMethod has no `pub`, so SpecMethod.Pub must remain false.
	prog := parseProgramSrc(t, "pub spec X { fn m() -> int }\n")
	sp := expectOne[*SpecDecl](t, prog)
	if sp.Name != "X" {
		t.Errorf("name = %q, want X", sp.Name)
	}
	if !sp.Pub {
		t.Errorf("SpecDecl.Pub = false, want true on `pub spec`")
	}
	if len(sp.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(sp.Methods))
	}
	if sp.Methods[0].Pub {
		t.Errorf("inner SpecMethod.Pub = true, want false (no `pub` written)")
	}
}

func TestParsePubSpecMethodInsideSpec(t *testing.T) {
	// `pub fn` inside a `spec X { ... }` body: SpecMethod.Pub must be true.
	prog := parseProgramSrc(t, "spec X { pub fn m() -> int }\n")
	sp := expectOne[*SpecDecl](t, prog)
	if sp.Pub {
		t.Errorf("outer SpecDecl.Pub = true, want false")
	}
	if len(sp.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(sp.Methods))
	}
	if !sp.Methods[0].Pub {
		t.Errorf("inner SpecMethod.Pub = false, want true on `pub fn` in spec")
	}
}

func TestParsePubFnInsideImpl(t *testing.T) {
	// `pub fn` inside an `impl T { ... }` body: the inner FnDecl carries
	// Pub=true. The impl block itself has no Pub field.
	src := "impl Counter { pub fn double() -> int { return 0 } fn private_helper() {} }\n"
	prog := parseProgramSrc(t, src)
	im := expectOne[*ImplDecl](t, prog)
	if len(im.Methods) != 2 {
		t.Fatalf("got %d methods, want 2", len(im.Methods))
	}
	if !im.Methods[0].Pub {
		t.Errorf("impl method 0 Pub = false, want true on `pub fn`")
	}
	if im.Methods[1].Pub {
		t.Errorf("impl method 1 Pub = true, want false on bare `fn` (regression)")
	}
}

func TestParsePubLetRejected(t *testing.T) {
	expectParseErr(t, "pub let x := 1\n", "pub may only modify fn / struct / enum / spec")
}

func TestParsePubMutRejected(t *testing.T) {
	expectParseErr(t, "pub mut x := 1\n", "pub may only modify fn / struct / enum / spec")
}

func TestParsePubConstRejected(t *testing.T) {
	expectParseErr(t, "pub const x := 1\n", "pub may only modify fn / struct / enum / spec")
}

func TestParsePubImplRejected(t *testing.T) {
	// `impl` carries no `pub`; the visibility lives on each inner method's
	// `fn`. `pub impl T { ... }` is rejected with the focused diagnostic.
	// v0.18 added `import` to the admitted set.
	expectParseErr(t, "pub impl Counter { fn m() {} }\n", "pub may only modify fn / struct / enum / spec / import")
}

func TestParsePubAtEOFRejected(t *testing.T) {
	expectParseErr(t, "pub", "expected fn / struct / enum / spec / import after 'pub'")
}

func TestParsePubBeforeIdentRejected(t *testing.T) {
	// A non-decl token after `pub` (here, an identifier) is rejected with
	// the same focused diagnostic. This protects against typos like
	// `pub foo()` where the user forgot the `fn` keyword.
	msg := expectParseErr(t, "pub foo\n", "expected fn / struct / enum / spec / import after 'pub'")
	if !strings.Contains(msg, "after 'pub'") {
		t.Errorf("error %q does not mention `after 'pub'`", msg)
	}
}
