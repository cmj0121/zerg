package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.7 Unit 1a — parser tests for anonymous functions, `spawn`, and `defer`.
//
// Unit 1a is parser-only: typeck / interpreter / codegen do not yet consume
// the new AnonFnExpr / SpawnStmt / DeferStmt nodes (Units 3 / 6 / 7 do).
// These tests pin the lexer / AST plumbing and the rejection diagnostics for
// malformed shapes. Existing v0.0–v0.6 corpora continue to parse with no
// new node types appearing in their trees — the regression tests in the
// prior parser suites already lock that in.
// ---------------------------------------------------------------------------

// --- anon-fn expression ---------------------------------------------------

func TestParseAnonFnInLet(t *testing.T) {
	prog := parseProgramSrc(t, "f := fn() { print 1 }\n")
	s := expectOne[*LetStmt](t, prog)
	fn, ok := s.Value.(*AnonFnExpr)
	if !ok {
		t.Fatalf("value = %T, want *AnonFnExpr", s.Value)
	}
	if len(fn.Params) != 0 {
		t.Errorf("params = %v, want empty", fn.Params)
	}
	if fn.Return != nil {
		t.Errorf("return = %v, want nil", fn.Return)
	}
	if fn.Body == nil || len(fn.Body.Statements) != 1 {
		t.Fatalf("body = %v, want 1 stmt", fn.Body)
	}
	if _, ok := fn.Body.Statements[0].(*PrintStmt); !ok {
		t.Errorf("body[0] = %T, want *PrintStmt", fn.Body.Statements[0])
	}
}

func TestParseAnonFnWithParamsAndReturn(t *testing.T) {
	prog := parseProgramSrc(t, "g := fn(x: int) -> int { return x * 2 }\n")
	s := expectOne[*LetStmt](t, prog)
	fn, ok := s.Value.(*AnonFnExpr)
	if !ok {
		t.Fatalf("value = %T, want *AnonFnExpr", s.Value)
	}
	if len(fn.Params) != 1 || fn.Params[0].Name != "x" || fn.Params[0].Type.Name != "int" {
		t.Errorf("params = %+v, want [x:int]", fn.Params)
	}
	if fn.Return == nil || fn.Return.Name != "int" {
		t.Errorf("return = %v, want int", fn.Return)
	}
}

func TestParseAnonFnMultiParam(t *testing.T) {
	prog := parseProgramSrc(t, "h := fn(a: int, b: int) -> int { return a + b }\n")
	s := expectOne[*LetStmt](t, prog)
	fn := s.Value.(*AnonFnExpr)
	if len(fn.Params) != 2 {
		t.Fatalf("got %d params, want 2", len(fn.Params))
	}
	if fn.Params[0].Name != "a" || fn.Params[1].Name != "b" {
		t.Errorf("param names = %v, want [a, b]", fn.Params)
	}
}

func TestParseAnonFnNoCollideWithFnDecl(t *testing.T) {
	// `fn name() { ... }` must still parse as a fn-decl. The disambiguation
	// pin: `fn` followed by IDENT ⇒ fn-decl; `fn` followed by `(` ⇒ anon-fn.
	prog := parseProgramSrc(t, "fn greet() { print 1 }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Name != "greet" {
		t.Errorf("name = %q, want greet", fn.Name)
	}
}

func TestParseAnonFnAsArgument(t *testing.T) {
	// Anon-fn in argument position — `apply(fn(x: int) -> int { return x })`.
	// Validates the parseAtom dispatch beyond the let-RHS path.
	prog := parseProgramSrc(t, "fn use_cb() { apply(fn(x: int) -> int { return x }) }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(fn.Body.Statements))
	}
	es, ok := fn.Body.Statements[0].(*ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ExprStmt", fn.Body.Statements[0])
	}
	call, ok := es.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("expr = %T, want *CallExpr", es.Expr)
	}
	if len(call.Args) != 1 {
		t.Fatalf("args = %d, want 1", len(call.Args))
	}
	if _, ok := call.Args[0].(*AnonFnExpr); !ok {
		t.Errorf("arg[0] = %T, want *AnonFnExpr", call.Args[0])
	}
}

func TestParseAnonFnRejectPubInParamType(t *testing.T) {
	// Mirrors the existing fn-decl rule: `pub` in type-position rejects with
	// "expected type name, got 'pub'" via parseTypeRef. The anon-fn path
	// inherits the same diagnostic — no special-case needed.
	expectParseErr(t,
		"f := fn(x: pub int) { print 1 }\n",
		"expected type name, got 'pub'",
	)
}

func TestParseAnonFnRejectPubBeforeParamName(t *testing.T) {
	// Mirrors fn-decl: `pub` before the param name rejects via the same
	// expect(KindIdent) path that fn-decl uses.
	expectParseErr(t,
		"f := fn(pub x: int) { print 1 }\n",
		"expected identifier",
	)
}

func TestParseFnInExprNeedsParen(t *testing.T) {
	// `fn` in expression position not followed by `(` is meaningless: it is
	// neither a fn-decl (no IDENT) nor a valid anon-fn (no params block).
	expectParseErr(t,
		"f := fn { print 1 }\n",
		"'fn' in expression position must be followed by '('",
	)
}

// --- spawn ----------------------------------------------------------------

func TestParseSpawnIIFE(t *testing.T) {
	// `spawn fn() { print 1 }()` — IIFE shape.
	prog := parseProgramSrc(t, "fn run() { spawn fn() { print 1 }() }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(fn.Body.Statements))
	}
	sp, ok := fn.Body.Statements[0].(*SpawnStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *SpawnStmt", fn.Body.Statements[0])
	}
	call, ok := sp.Call.(*CallExpr)
	if !ok {
		t.Fatalf("Call = %T, want *CallExpr", sp.Call)
	}
	if _, ok := call.Callee.(*AnonFnExpr); !ok {
		t.Errorf("callee = %T, want *AnonFnExpr (IIFE)", call.Callee)
	}
}

func TestParseSpawnNamedCall(t *testing.T) {
	prog := parseProgramSrc(t, "fn run() { spawn do_work() }\n")
	fn := expectOne[*FnDecl](t, prog)
	sp, ok := fn.Body.Statements[0].(*SpawnStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *SpawnStmt", fn.Body.Statements[0])
	}
	call, ok := sp.Call.(*CallExpr)
	if !ok {
		t.Fatalf("Call = %T, want *CallExpr", sp.Call)
	}
	id, ok := call.Callee.(*IdentExpr)
	if !ok || id.Name != "do_work" {
		t.Errorf("callee = %v, want IdentExpr{do_work}", call.Callee)
	}
}

func TestParseSpawnQualifiedCall(t *testing.T) {
	// `spawn mod.do_work()` — qualified callee. parsePostfix's `expr DOT
	// IDENT (` rule produces a MethodCallExpr; spawn admits both *CallExpr
	// and *MethodCallExpr shapes so the cross-module fn-call path lands here
	// untouched. Typeck (Unit 3) routes MethodCallExpr through the v0.5
	// cross-module fn-call machinery.
	prog := parseProgramSrc(t, "fn run() { spawn mod.do_work() }\n")
	fn := expectOne[*FnDecl](t, prog)
	sp, ok := fn.Body.Statements[0].(*SpawnStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *SpawnStmt", fn.Body.Statements[0])
	}
	mc, ok := sp.Call.(*MethodCallExpr)
	if !ok {
		t.Fatalf("Call = %T, want *MethodCallExpr", sp.Call)
	}
	if mc.Method != "do_work" {
		t.Errorf("method = %q, want do_work", mc.Method)
	}
	id, ok := mc.Receiver.(*IdentExpr)
	if !ok || id.Name != "mod" {
		t.Errorf("receiver = %v, want IdentExpr{mod}", mc.Receiver)
	}
}

func TestParseSpawnRejectNonCall(t *testing.T) {
	expectParseErr(t,
		"fn run() { spawn x }\n",
		"spawn requires a function call expression",
	)
}

func TestParseSpawnRejectArith(t *testing.T) {
	expectParseErr(t,
		"fn run() { spawn 1 + 2 }\n",
		"spawn requires a function call expression",
	)
}

// --- defer ----------------------------------------------------------------

func TestParseDeferCall(t *testing.T) {
	prog := parseProgramSrc(t, "fn run() { defer cleanup() }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(fn.Body.Statements))
	}
	d, ok := fn.Body.Statements[0].(*DeferStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *DeferStmt", fn.Body.Statements[0])
	}
	if d.Body == nil || len(d.Body.Statements) != 1 {
		t.Fatalf("body = %v, want 1-stmt block", d.Body)
	}
	es, ok := d.Body.Statements[0].(*ExprStmt)
	if !ok {
		t.Fatalf("inner = %T, want *ExprStmt", d.Body.Statements[0])
	}
	if _, ok := es.Expr.(*CallExpr); !ok {
		t.Errorf("inner expr = %T, want *CallExpr", es.Expr)
	}
}

func TestParseDeferBlock(t *testing.T) {
	prog := parseProgramSrc(t, "fn run() { defer { close(ch) } }\n")
	fn := expectOne[*FnDecl](t, prog)
	d, ok := fn.Body.Statements[0].(*DeferStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *DeferStmt", fn.Body.Statements[0])
	}
	if d.Body == nil || len(d.Body.Statements) != 1 {
		t.Fatalf("body = %v, want 1-stmt block", d.Body)
	}
}

func TestParseDeferRejectAtTopLevel(t *testing.T) {
	expectParseErr(t,
		"defer cleanup()\n",
		"defer only allowed inside a function body",
	)
}

func TestParseDeferRejectInIf(t *testing.T) {
	expectParseErr(t,
		"fn run() { if true { defer cleanup() } }\n",
		"defer only allowed at fn-body scope",
	)
}

func TestParseDeferRejectInFor(t *testing.T) {
	expectParseErr(t,
		"fn run() { for i in 0..3 { defer cleanup() } }\n",
		"defer only allowed at fn-body scope",
	)
}

func TestParseDeferRejectInMatch(t *testing.T) {
	expectParseErr(t,
		"fn run(x: int) { match x { _ => { defer cleanup() } } }\n",
		"defer only allowed at fn-body scope",
	)
}

func TestParseDeferAtREPL(t *testing.T) {
	// REPL feeds tokens through ParseStatement; no enclosing fn body means
	// fnBodyDepths is empty and defer rejects with the precise diagnostic.
	tokens, err := Lex([]byte("defer cleanup()\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, err := ParseStatement(tokens); err == nil {
		t.Fatal("ParseStatement succeeded, expected defer-at-REPL rejection")
	} else if pe, ok := err.(*ParseError); !ok {
		t.Errorf("error = %T, want *ParseError", err)
	} else if !strings.Contains(pe.Message, "defer only allowed inside a function body") {
		t.Errorf("error = %q, want 'defer only allowed inside a function body'", pe.Message)
	}
}

func TestParseDeferInsideAnonFnBody(t *testing.T) {
	// Anon-fn introduces a fresh fn-body scope; defer at the immediate body
	// level of an anon-fn is admitted.
	prog := parseProgramSrc(t, "fn outer() { f := fn() { defer cleanup() } }\n")
	fn := expectOne[*FnDecl](t, prog)
	let, ok := fn.Body.Statements[0].(*LetStmt)
	if !ok {
		t.Fatalf("outer[0] = %T, want *LetStmt", fn.Body.Statements[0])
	}
	anon, ok := let.Value.(*AnonFnExpr)
	if !ok {
		t.Fatalf("let.Value = %T, want *AnonFnExpr", let.Value)
	}
	if len(anon.Body.Statements) != 1 {
		t.Fatalf("anon body has %d stmts, want 1", len(anon.Body.Statements))
	}
	if _, ok := anon.Body.Statements[0].(*DeferStmt); !ok {
		t.Errorf("anon body[0] = %T, want *DeferStmt", anon.Body.Statements[0])
	}
}

func TestParseDeferInsideImplMethod(t *testing.T) {
	// Impl-method bodies route through parseFnDecl (which now uses
	// parseFnBody); defer at the immediate method body level should be admitted.
	prog := parseProgramSrc(t, "struct S {}\nimpl S { fn close() { defer cleanup() } }\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d stmts, want 2", len(prog.Statements))
	}
	im, ok := prog.Statements[1].(*ImplDecl)
	if !ok {
		t.Fatalf("[1] = %T, want *ImplDecl", prog.Statements[1])
	}
	if len(im.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(im.Methods))
	}
	body := im.Methods[0].Body
	if body == nil || len(body.Statements) != 1 {
		t.Fatalf("method body = %v, want 1 stmt", body)
	}
	if _, ok := body.Statements[0].(*DeferStmt); !ok {
		t.Errorf("body[0] = %T, want *DeferStmt", body.Statements[0])
	}
}

// --- lexer keyword coverage ----------------------------------------------

func TestLexV07Keywords(t *testing.T) {
	// `spawn` and `defer` are keywords starting v0.7 Unit 1a — verify the
	// lexer table promotes them and never leaves them as KindIdent.
	cases := []struct {
		src  string
		want Kind
	}{
		{"spawn", KindSpawn},
		{"defer", KindDefer},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			tokens, err := Lex([]byte(c.src))
			if err != nil {
				t.Fatalf("Lex: %v", err)
			}
			if len(tokens) != 2 {
				t.Fatalf("got %d tokens, want 2 (keyword + EOF)", len(tokens))
			}
			if tokens[0].Kind != c.want {
				t.Errorf("kind = %v, want %v", tokens[0].Kind, c.want)
			}
			if tokens[0].Value != c.src {
				t.Errorf("value = %q, want %q", tokens[0].Value, c.src)
			}
		})
	}
}

func TestLexV07KeywordPrefixIsIdent(t *testing.T) {
	// Names with a keyword prefix must still be identifiers (e.g. `spawned`
	// is an IDENT, not `spawn` followed by `ed`). Inherits the v0.0+
	// "longest-match identifier" rule.
	tokens, err := Lex([]byte("spawned deferred"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	for i, want := range []string{"spawned", "deferred"} {
		if tokens[i].Kind != KindIdent {
			t.Errorf("token %d kind = %v, want KindIdent", i, tokens[i].Kind)
		}
		if tokens[i].Value != want {
			t.Errorf("token %d value = %q, want %q", i, tokens[i].Value, want)
		}
	}
}
