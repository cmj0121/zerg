package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 parser tests — Unit 1: spec / impl declarations, method-call syntax,
// and the `this` keyword.
//
// Unit 1 is parser-only: typeck / interpreter / codegen support arrives in
// later units. The tests below exercise the parser surface (shape of the AST
// produced from a source string, plus negative diagnostics for malformed
// input). Existing v0.2/v0.3 field-access and call-expression tests still
// pass — the new dispatch in parsePostfix is purely additive (DOT IDENT '(' ⇒
// MethodCallExpr; otherwise FieldAccessExpr).
//
// Typeck rejects every v0.4 shape with a "v0.4 work in progress" diagnostic,
// so source that parses cleanly still won't run end-to-end yet. That is by
// design — Unit 3 lights up typeck.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// spec declarations.
// ---------------------------------------------------------------------------

func TestParseSpecSignatureOnly(t *testing.T) {
	prog := parseProgramSrc(t, "spec Printable { fn to_string() -> str }\n")
	s := expectOne[*SpecDecl](t, prog)
	if s.Name != "Printable" {
		t.Errorf("name = %q, want Printable", s.Name)
	}
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
	m := s.Methods[0]
	if m.Name != "to_string" {
		t.Errorf("method name = %q, want to_string", m.Name)
	}
	if m.Body != nil {
		t.Errorf("expected signature-only method, got default body")
	}
	if m.Return == nil || m.Return.Name != "str" {
		t.Errorf("return type = %v, want str", m.Return)
	}
	if len(m.Params) != 0 {
		t.Errorf("got %d params, want 0", len(m.Params))
	}
}

func TestParseSpecDefaultImpl(t *testing.T) {
	prog := parseProgramSrc(t, "spec Hashable { fn hash() -> int { return 0 } }\n")
	s := expectOne[*SpecDecl](t, prog)
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
	m := s.Methods[0]
	if m.Body == nil {
		t.Fatalf("expected default body, got nil")
	}
	if len(m.Body.Statements) != 1 {
		t.Errorf("body has %d statements, want 1", len(m.Body.Statements))
	}
	if _, ok := m.Body.Statements[0].(*ReturnStmt); !ok {
		t.Errorf("body[0] is %T, want *ReturnStmt", m.Body.Statements[0])
	}
}

func TestParseSpecEmpty(t *testing.T) {
	prog := parseProgramSrc(t, "spec Empty {}\n")
	s := expectOne[*SpecDecl](t, prog)
	if s.Name != "Empty" {
		t.Errorf("name = %q, want Empty", s.Name)
	}
	if len(s.Methods) != 0 {
		t.Errorf("got %d methods, want 0", len(s.Methods))
	}
}

func TestParseSpecMixedSignatureAndDefault(t *testing.T) {
	src := `spec Both {
fn must() -> int
fn maybe() -> int { return 0 }
}
`
	prog := parseProgramSrc(t, src)
	s := expectOne[*SpecDecl](t, prog)
	if len(s.Methods) != 2 {
		t.Fatalf("got %d methods, want 2", len(s.Methods))
	}
	if s.Methods[0].Body != nil {
		t.Errorf("methods[0] should be sig-only")
	}
	if s.Methods[1].Body == nil {
		t.Errorf("methods[1] should be default")
	}
}

func TestParseSpecMethodWithParams(t *testing.T) {
	prog := parseProgramSrc(t, "spec Comparable { fn cmp(other: int) -> int }\n")
	s := expectOne[*SpecDecl](t, prog)
	m := s.Methods[0]
	if len(m.Params) != 1 {
		t.Fatalf("got %d params, want 1", len(m.Params))
	}
	if m.Params[0].Name != "other" || m.Params[0].Type.Name != "int" {
		t.Errorf("param = %v:%v, want other:int", m.Params[0].Name, m.Params[0].Type.Name)
	}
}

// ---------------------------------------------------------------------------
// impl declarations (inherent + for-spec).
// ---------------------------------------------------------------------------

func TestParseImplInherent(t *testing.T) {
	prog := parseProgramSrc(t, "impl Counter { fn double() -> int { return 0 } }\n")
	s := expectOne[*ImplDecl](t, prog)
	if s.Type != "Counter" {
		t.Errorf("type = %q, want Counter", s.Type)
	}
	if s.Spec != "" {
		t.Errorf("spec = %q, want \"\" (inherent)", s.Spec)
	}
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
	if s.Methods[0].Name != "double" {
		t.Errorf("method name = %q, want double", s.Methods[0].Name)
	}
}

func TestParseImplForSpec(t *testing.T) {
	prog := parseProgramSrc(t, `impl Counter for Printable { fn to_string() -> str { return "x" } }`+"\n")
	s := expectOne[*ImplDecl](t, prog)
	if s.Type != "Counter" {
		t.Errorf("type = %q, want Counter", s.Type)
	}
	if s.Spec != "Printable" {
		t.Errorf("spec = %q, want Printable", s.Spec)
	}
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
}

func TestParseImplForSpecEmptyBody(t *testing.T) {
	prog := parseProgramSrc(t, "impl Default for Hashable {}\n")
	s := expectOne[*ImplDecl](t, prog)
	if s.Spec != "Hashable" {
		t.Errorf("spec = %q, want Hashable", s.Spec)
	}
	if len(s.Methods) != 0 {
		t.Errorf("got %d methods, want 0", len(s.Methods))
	}
}

func TestParseImplMultipleMethods(t *testing.T) {
	src := `impl Counter {
fn inc() -> int { return 0 }
fn dec() -> int { return 0 }
}
`
	prog := parseProgramSrc(t, src)
	s := expectOne[*ImplDecl](t, prog)
	if len(s.Methods) != 2 {
		t.Fatalf("got %d methods, want 2", len(s.Methods))
	}
	if s.Methods[0].Name != "inc" || s.Methods[1].Name != "dec" {
		t.Errorf("method names = %q,%q, want inc,dec", s.Methods[0].Name, s.Methods[1].Name)
	}
}

// TestParseImplMultiStatementBody — guards against the regression where
// parseImplDecl bumped parenDepth around the body, causing peek() to consume
// the NEWLINE between statements inside a method body and then reject the
// next statement with "expected newline or end of statement, got 'return'".
func TestParseImplMultiStatementBody(t *testing.T) {
	src := `impl Counter {
fn double() -> int {
let y := this.count
return y * 2
}
}
`
	prog := parseProgramSrc(t, src)
	s := expectOne[*ImplDecl](t, prog)
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
	body := s.Methods[0].Body
	if body == nil {
		t.Fatalf("method body is nil")
	}
	if len(body.Statements) != 2 {
		t.Fatalf("got %d body statements, want 2", len(body.Statements))
	}
}

// TestParseSpecDefaultMultiStatementBody — same regression guard for spec
// default bodies.
func TestParseSpecDefaultMultiStatementBody(t *testing.T) {
	src := `spec Greeter {
fn greet() -> str {
let s := "hi"
return s
}
}
`
	prog := parseProgramSrc(t, src)
	s := expectOne[*SpecDecl](t, prog)
	if len(s.Methods) != 1 {
		t.Fatalf("got %d methods, want 1", len(s.Methods))
	}
	body := s.Methods[0].Body
	if body == nil {
		t.Fatalf("default body is nil")
	}
	if len(body.Statements) != 2 {
		t.Fatalf("got %d body statements, want 2", len(body.Statements))
	}
}

// ---------------------------------------------------------------------------
// Method-call syntax: postfix DOT IDENT '(' ⇒ MethodCallExpr.
// ---------------------------------------------------------------------------

func TestParseMethodCallZeroArg(t *testing.T) {
	prog := parseProgramSrc(t, "c.method()\n")
	es := expectOne[*ExprStmt](t, prog)
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *MethodCallExpr", es.Expr)
	}
	if mc.Method != "method" {
		t.Errorf("method = %q, want method", mc.Method)
	}
	if len(mc.Args) != 0 {
		t.Errorf("got %d args, want 0", len(mc.Args))
	}
	if id, ok := mc.Receiver.(*IdentExpr); !ok || id.Name != "c" {
		t.Errorf("receiver = %T %v, want IdentExpr c", mc.Receiver, mc.Receiver)
	}
}

func TestParseMethodCallOneArg(t *testing.T) {
	prog := parseProgramSrc(t, "c.method(arg)\n")
	es := expectOne[*ExprStmt](t, prog)
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *MethodCallExpr", es.Expr)
	}
	if len(mc.Args) != 1 {
		t.Fatalf("got %d args, want 1", len(mc.Args))
	}
	if id, ok := mc.Args[0].(*IdentExpr); !ok || id.Name != "arg" {
		t.Errorf("arg[0] = %T %v, want IdentExpr arg", mc.Args[0], mc.Args[0])
	}
}

func TestParseMethodCallMultiArg(t *testing.T) {
	prog := parseProgramSrc(t, "c.method(a, b)\n")
	es := expectOne[*ExprStmt](t, prog)
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *MethodCallExpr", es.Expr)
	}
	if len(mc.Args) != 2 {
		t.Fatalf("got %d args, want 2", len(mc.Args))
	}
}

// TestParseMethodCallOnFieldAccess covers `c.x.method()` — chain of field
// access then method call. The postfix loop walks left to right so
// MethodCallExpr's Receiver is FieldAccessExpr.
func TestParseMethodCallOnFieldAccess(t *testing.T) {
	prog := parseProgramSrc(t, "c.x.method()\n")
	es := expectOne[*ExprStmt](t, prog)
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *MethodCallExpr", es.Expr)
	}
	fa, ok := mc.Receiver.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("receiver is %T, want *FieldAccessExpr", mc.Receiver)
	}
	if fa.FieldName != "x" {
		t.Errorf("field name = %q, want x", fa.FieldName)
	}
	if id, ok := fa.Receiver.(*IdentExpr); !ok || id.Name != "c" {
		t.Errorf("base = %T %v, want IdentExpr c", fa.Receiver, fa.Receiver)
	}
}

// TestParseFieldAccessAfterMethodCall covers `c.method().field` — method
// call result then field access. The postfix loop produces FieldAccessExpr
// wrapping MethodCallExpr.
func TestParseFieldAccessAfterMethodCall(t *testing.T) {
	prog := parseProgramSrc(t, "let v := c.method().field\n")
	let := expectOne[*LetStmt](t, prog)
	fa, ok := let.Value.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr", let.Value)
	}
	if fa.FieldName != "field" {
		t.Errorf("field name = %q, want field", fa.FieldName)
	}
	if _, ok := fa.Receiver.(*MethodCallExpr); !ok {
		t.Errorf("receiver is %T, want *MethodCallExpr", fa.Receiver)
	}
}

// TestParseFieldAccessUnchanged confirms the v0.2/v0.3 shape is unchanged
// when no `(` follows DOT IDENT — this test guards the additive nature of
// the new method-call rule.
func TestParseFieldAccessUnchanged(t *testing.T) {
	prog := parseProgramSrc(t, "let v := c.x\n")
	let := expectOne[*LetStmt](t, prog)
	fa, ok := let.Value.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr (no method call without parens)", let.Value)
	}
	if fa.FieldName != "x" {
		t.Errorf("field name = %q, want x", fa.FieldName)
	}
}

// ---------------------------------------------------------------------------
// `this` keyword.
// ---------------------------------------------------------------------------

func TestParseThisInExpression(t *testing.T) {
	prog := parseProgramSrc(t, "let x := this\n")
	let := expectOne[*LetStmt](t, prog)
	if _, ok := let.Value.(*ThisExpr); !ok {
		t.Errorf("value is %T, want *ThisExpr", let.Value)
	}
}

// TestParseThisFieldAccess verifies `this.x` parses as FieldAccessExpr
// over a ThisExpr — the receiver shape used inside method bodies.
func TestParseThisFieldAccess(t *testing.T) {
	prog := parseProgramSrc(t, "let x := this.count\n")
	let := expectOne[*LetStmt](t, prog)
	fa, ok := let.Value.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr", let.Value)
	}
	if fa.FieldName != "count" {
		t.Errorf("field = %q, want count", fa.FieldName)
	}
	if _, ok := fa.Receiver.(*ThisExpr); !ok {
		t.Errorf("receiver is %T, want *ThisExpr", fa.Receiver)
	}
}

// TestParseThisMethodCall verifies `this.method()` parses as a method call
// on `this` — the form a default impl uses to call another method on the
// same receiver.
func TestParseThisMethodCall(t *testing.T) {
	prog := parseProgramSrc(t, "this.helper()\n")
	es := expectOne[*ExprStmt](t, prog)
	mc, ok := es.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *MethodCallExpr", es.Expr)
	}
	if mc.Method != "helper" {
		t.Errorf("method = %q, want helper", mc.Method)
	}
	if _, ok := mc.Receiver.(*ThisExpr); !ok {
		t.Errorf("receiver is %T, want *ThisExpr", mc.Receiver)
	}
}

// ---------------------------------------------------------------------------
// Negative cases.
// ---------------------------------------------------------------------------

// TestParseSpecMissingParens — `fn to_string` without `()` after the name.
func TestParseSpecMissingParens(t *testing.T) {
	expectParseErr(t, "spec Printable { fn to_string }\n", "expected '('")
}

// TestParseImplForMissingSpec — `impl Counter for { ... }` lacks the spec
// name. We surface a focused "expected spec name after 'for'" diagnostic.
func TestParseImplForMissingSpec(t *testing.T) {
	expectParseErr(t, "impl Counter for { }\n", "expected spec name")
}

// TestParseEmptyMethodName — `c.()` has no identifier after the dot.
func TestParseEmptyMethodName(t *testing.T) {
	expectParseErr(t, "c.()\n", "expected identifier")
}

// TestParseDoubleDot — `c..method()` lexes the `..` as a single range token,
// so the parser rejects with the dedicated range-position diagnostic. Either
// way, the form does NOT parse as a method call.
func TestParseDoubleDot(t *testing.T) {
	expectParseErr(t, "c..method()\n", "range")
}
