// Package sema is the Phase 0 semantic checker. It resolves names, infers and
// checks types over the small type universe (int, float, bool, str, nil), and
// enforces the rules the roadmap's Phase 0 subset needs: conditions are bool,
// reassignment targets an existing mutable binding, calls match their signature,
// and returns match the function's result type.
//
// It produces an Info the emitter consumes: the type of every expression, the
// resolved type of every binding, and the collected function signatures. Numeric
// literals are untyped and adopt a float context (e.g. 'x: float = 1'); no other
// implicit numeric conversion happens.
package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Type is a Phase 0 type.
type Type int

const (
	Invalid Type = iota
	Nil
	Int
	Float
	Bool
	Str
)

// String renders a type as it is spelled in source.
func (t Type) String() string {
	switch t {
	case Nil:
		return "nil"
	case Int:
		return "int"
	case Float:
		return "float"
	case Bool:
		return "bool"
	case Str:
		return "str"
	default:
		return "<invalid>"
	}
}

func (t Type) numeric() bool { return t == Int || t == Float }

// printable reports whether 'print' accepts a value of this type (Phase 0).
func (t Type) printable() bool { return t.numeric() || t == Bool || t == Str }

// FuncSig is a resolved function signature.
type FuncSig struct {
	Name       string
	ParamNames []string
	Params     []Type
	Ret        Type // Nil when the function has no '-> type'
	Decl       *ast.FuncDecl
}

// Info is the result of a successful check, consumed by the emitter.
type Info struct {
	Funcs     map[string]*FuncSig
	ExprTypes map[ast.Expr]Type
	BindTypes map[*ast.BindStmt]Type
}

// Check resolves and type-checks the file, returning the analysis info and any
// diagnostics. When diagnostics are non-empty the Info is still returned but must
// not be used for emission.
func Check(file *ast.File) (*Info, []diag.Diagnostic) {
	c := &checker{
		info: &Info{
			Funcs:     map[string]*FuncSig{},
			ExprTypes: map[ast.Expr]Type{},
			BindTypes: map[*ast.BindStmt]Type{},
		},
	}
	c.collectFuncs(file)
	c.checkFuncs(file)
	return c.info, c.diags.Items()
}

type symbol struct {
	typ     Type
	mutable bool
}

type checker struct {
	info      *Info
	diags     diag.List
	scopes    []map[string]*symbol
	curFn     *FuncSig
	loopDepth int
}

func (c *checker) errorf(span token.Span, format string, args ...any) {
	c.diags.Add(span, format, args...)
}

// --- pass 1: collect signatures ------------------------------------------------

func (c *checker) collectFuncs(file *ast.File) {
	for _, d := range file.Items {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, dup := c.info.Funcs[fn.Name]; dup {
			c.errorf(fn.Span(), "function %q is already declared", fn.Name)
			continue
		}
		sig := &FuncSig{Name: fn.Name, Ret: Nil, Decl: fn}
		for _, p := range fn.Params {
			sig.ParamNames = append(sig.ParamNames, p.Name)
			sig.Params = append(sig.Params, c.resolveType(p.Type))
		}
		if fn.Ret != nil {
			sig.Ret = c.resolveType(fn.Ret)
		}
		c.info.Funcs[fn.Name] = sig
	}
}

func (c *checker) resolveType(t ast.Type) Type {
	ref, ok := t.(*ast.TypeRef)
	if !ok || len(ref.Args) != 0 || len(ref.Proj) != 0 {
		// composite and generic types belong to later phases; this pass only
		// resolves the Phase 0 built-in scalar names.
		c.errorf(t.Span(), "unsupported type in this phase")
		return Invalid
	}
	switch ref.Name {
	case "int":
		return Int
	case "float":
		return Float
	case "bool":
		return Bool
	case "str":
		return Str
	case "nil":
		return Nil
	default:
		c.errorf(ref.Span(), "unknown type %q", ref.Name)
		return Invalid
	}
}

// --- pass 2: check bodies ------------------------------------------------------

func (c *checker) checkFuncs(file *ast.File) {
	for _, d := range file.Items {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		c.checkFunc(fn)
	}
}

func (c *checker) checkFunc(fn *ast.FuncDecl) {
	sig := c.info.Funcs[fn.Name]
	c.curFn = sig
	c.pushScope() // parameter scope; the body opens a nested scope so 'mut n := n' can shadow
	for i, p := range fn.Params {
		c.declare(p.Span(), p.Name, sig.Params[i], false)
	}
	c.checkBlock(fn.Body)
	c.popScope()
	c.curFn = nil
}

func (c *checker) checkBlock(b *ast.Block) {
	c.pushScope()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.NopStmt:
		// nothing
	case *ast.BindStmt:
		c.checkBind(n)
	case *ast.Reassign:
		c.checkAssign(n)
	case *ast.PrintStmt:
		t := c.checkExpr(n.Value)
		if t != Invalid && !t.printable() {
			c.errorf(n.Span(), "cannot print a value of type %s", t)
		}
	case *ast.ReturnStmt:
		c.checkReturn(n)
	case *ast.BreakStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "break outside of a loop")
		}
	case *ast.ContinueStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "continue outside of a loop")
		}
	case *ast.IfStmt:
		for _, br := range n.Branches {
			c.checkCond(br.Cond)
			c.checkBlock(br.Body)
		}
		if n.Else != nil {
			c.checkBlock(n.Else)
		}
	case *ast.ForStmt:
		if n.Cond != nil {
			c.checkCond(n.Cond)
		}
		c.loopDepth++
		c.checkBlock(n.Body)
		c.loopDepth--
	case *ast.ExprStmt:
		c.checkExpr(n.X)
	}
}

func (c *checker) checkCond(e ast.Expr) {
	if t := c.checkExpr(e); t != Invalid && t != Bool {
		c.errorf(e.Span(), "condition must be bool, found %s", t)
	}
}

func (c *checker) checkBind(b *ast.BindStmt) {
	vt := c.checkExpr(b.Value)
	var typ Type
	if b.Type != nil {
		declared := c.resolveType(b.Type)
		typ = declared
		if declared != Invalid && vt != Invalid && !c.assignable(declared, b.Value, vt) {
			c.errorf(b.Span(), "cannot bind %s to a %s binding", vt, declared)
		}
	} else {
		if vt == Nil {
			c.errorf(b.Span(), "cannot infer a type from nil; use a type annotation")
			typ = Invalid
		} else {
			typ = vt
		}
	}
	c.info.BindTypes[b] = typ
	c.declare(b.Span(), b.Name, typ, b.Mut)
}

func (c *checker) checkAssign(n *ast.Reassign) {
	vt := c.checkExpr(n.Value)
	name, ok := simpleTargetName(n.Target)
	if !ok {
		// tuple / struct / field / index targets are beyond the Phase 0 checker;
		// the parser still records them so 'zerg fmt' round-trips.
		return
	}
	sym := c.lookup(name)
	if sym == nil {
		c.errorf(n.Span(), "undefined name %q", name)
		return
	}
	if !sym.mutable {
		c.errorf(n.Span(), "cannot assign to immutable binding %q", name)
	}
	if sym.typ != Invalid && vt != Invalid && !c.assignable(sym.typ, n.Value, vt) {
		c.errorf(n.Span(), "cannot assign %s to %q of type %s", vt, name, sym.typ)
	}
}

// simpleTargetName returns the bound name when a reassignment target is a bare
// identifier lvalue — the only shape the Phase 0 checker and emitter handle.
func simpleTargetName(t ast.AssignTarget) (string, bool) {
	lv, ok := t.(*ast.LValueTarget)
	if !ok {
		return "", false
	}
	id, ok := lv.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func (c *checker) checkReturn(n *ast.ReturnStmt) {
	if n.Cond != nil {
		c.checkCond(n.Cond)
	}
	want := Nil
	if c.curFn != nil {
		want = c.curFn.Ret
	}
	if n.Value == nil {
		if want != Nil {
			c.errorf(n.Span(), "return with no value in a function returning %s", want)
		}
		return
	}
	vt := c.checkExpr(n.Value)
	if want == Nil {
		c.errorf(n.Span(), "unexpected return value in a function returning nil")
		return
	}
	if vt != Invalid && !c.assignable(want, n.Value, vt) {
		c.errorf(n.Span(), "cannot return %s from a function returning %s", vt, want)
	}
}

// --- expressions --------------------------------------------------------------

func (c *checker) checkExpr(e ast.Expr) Type {
	t := c.inferExpr(e)
	c.info.ExprTypes[e] = t
	return t
}

func (c *checker) inferExpr(e ast.Expr) Type {
	switch n := e.(type) {
	case *ast.IntLit:
		return Int
	case *ast.FloatLit:
		return Float
	case *ast.BoolLit:
		return Bool
	case *ast.StrLit:
		return Str
	case *ast.NilLit:
		return Nil
	case *ast.Ident:
		return c.inferIdent(n)
	case *ast.Unary:
		return c.inferUnary(n)
	case *ast.Binary:
		return c.inferBinary(n)
	case *ast.Call:
		return c.inferCall(n)
	case *ast.MatchExpr:
		return c.inferMatch(n)
	default:
		return Invalid
	}
}

func (c *checker) inferIdent(n *ast.Ident) Type {
	if sym := c.lookup(n.Name); sym != nil {
		return sym.typ
	}
	if _, ok := c.info.Funcs[n.Name]; ok {
		c.errorf(n.Span(), "functions are not first-class values in Phase 0: %q", n.Name)
		return Invalid
	}
	c.errorf(n.Span(), "undefined name %q", n.Name)
	return Invalid
}

func (c *checker) inferUnary(n *ast.Unary) Type {
	t := c.checkExpr(n.X)
	if t == Invalid {
		return Invalid
	}
	switch n.Op {
	case token.Minus, token.MinusMod:
		if !t.numeric() {
			c.errorf(n.Span(), "operator %q requires a numeric operand, found %s", n.Op, t)
			return Invalid
		}
		return t
	case token.Not:
		if t != Bool {
			c.errorf(n.Span(), "operator 'not' requires a bool operand, found %s", t)
			return Invalid
		}
		return Bool
	case token.Tilde:
		if t != Int {
			c.errorf(n.Span(), "operator '~' requires an int operand, found %s", t)
			return Invalid
		}
		return Int
	}
	return Invalid
}

func (c *checker) inferBinary(n *ast.Binary) Type {
	lt := c.checkExpr(n.L)
	rt := c.checkExpr(n.R)
	if lt == Invalid || rt == Invalid {
		return Invalid
	}
	switch {
	case isArithOp(n.Op):
		return c.numericResult(n, lt, rt)
	case isBitOp(n.Op):
		if lt != Int || rt != Int {
			c.errorf(n.Span(), "operator %q requires int operands, found %s and %s", n.Op, lt, rt)
			return Invalid
		}
		return Int
	case isOrderOp(n.Op):
		if c.numericResult(n, lt, rt) == Invalid {
			return Invalid
		}
		return Bool
	case isEqOp(n.Op):
		if !c.comparable(n, lt, rt) {
			c.errorf(n.Span(), "cannot compare %s and %s", lt, rt)
			return Invalid
		}
		return Bool
	case isLogicOp(n.Op):
		if lt != Bool || rt != Bool {
			c.errorf(n.Span(), "operator %q requires bool operands, found %s and %s", n.Op, lt, rt)
			return Invalid
		}
		return Bool
	}
	return Invalid
}

// numericResult checks a numeric binary operation, coercing an untyped integer
// literal to float against a float operand, and returns the result type.
func (c *checker) numericResult(n *ast.Binary, lt, rt Type) Type {
	if lt == rt && lt.numeric() {
		return lt
	}
	if lt == Float && rt == Int && isIntLit(n.R) {
		c.info.ExprTypes[n.R] = Float
		return Float
	}
	if lt == Int && rt == Float && isIntLit(n.L) {
		c.info.ExprTypes[n.L] = Float
		return Float
	}
	c.errorf(n.Span(), "operator %q requires matching numeric operands, found %s and %s", n.Op, lt, rt)
	return Invalid
}

func (c *checker) comparable(n *ast.Binary, lt, rt Type) bool {
	if lt == rt {
		return true
	}
	// allow comparing an untyped int literal against a float
	if lt == Float && rt == Int && isIntLit(n.R) {
		c.info.ExprTypes[n.R] = Float
		return true
	}
	if lt == Int && rt == Float && isIntLit(n.L) {
		c.info.ExprTypes[n.L] = Float
		return true
	}
	return false
}

func (c *checker) inferCall(n *ast.Call) Type {
	name, ok := n.Callee.(*ast.Ident)
	if !ok {
		c.errorf(n.Callee.Span(), "only named functions can be called in Phase 0")
		return Invalid
	}
	sig, ok := c.info.Funcs[name.Name]
	if !ok {
		c.errorf(name.Span(), "undefined function %q", name.Name)
		// still check arguments to surface nested errors
		for _, a := range n.Args {
			c.checkExpr(a.Value)
		}
		return Invalid
	}
	if len(n.Args) != len(sig.Params) {
		c.errorf(n.Span(), "function %q expects %d argument(s), got %d", name.Name, len(sig.Params), len(n.Args))
	}
	for i, a := range n.Args {
		at := c.checkExpr(a.Value)
		if i < len(sig.Params) && at != Invalid && sig.Params[i] != Invalid &&
			!c.assignable(sig.Params[i], a.Value, at) {
			c.errorf(a.Value.Span(), "argument %d of %q: cannot use %s as %s", i+1, name.Name, at, sig.Params[i])
		}
	}
	return sig.Ret
}

// inferMatch checks a match expression and returns its result type (the common
// type of every arm's body).
func (c *checker) inferMatch(n *ast.MatchExpr) Type {
	if !isSimpleSubject(n.Subject) {
		c.errorf(n.Subject.Span(), "match subject must be a name or literal in Phase 0")
	}
	subjT := c.checkExpr(n.Subject)
	if subjT != Invalid && !subjT.printable() {
		// printable == comparable-by-value here (int/float/bool/str)
		c.errorf(n.Subject.Span(), "cannot match on a value of type %s", subjT)
	}
	if len(n.Arms) == 0 {
		c.errorf(n.Span(), "match must have at least one arm")
		return Invalid
	}

	// Exhaustiveness (Phase 0): the last arm is an unguarded catch-all, and no
	// earlier arm is (which would make the rest unreachable).
	last := n.Arms[len(n.Arms)-1]
	if last.Guard != nil || !isCatchAll(last.Pat) {
		c.errorf(n.Span(), "match must end with an unguarded '_' or binding arm (Phase 0 exhaustiveness)")
	}
	for i := 0; i < len(n.Arms)-1; i++ {
		if n.Arms[i].Guard == nil && isCatchAll(n.Arms[i].Pat) {
			c.errorf(n.Arms[i].Pat.Span(), "this catch-all arm makes the following arms unreachable")
		}
	}

	result := Invalid
	for i, arm := range n.Arms {
		c.pushScope()
		c.checkPattern(arm.Pat, subjT)
		if arm.Guard != nil {
			c.checkCond(arm.Guard)
		}
		bt := c.checkExpr(arm.Body)
		c.popScope()
		switch {
		case i == 0:
			result = bt
		case bt != Invalid && result != Invalid && !c.assignable(result, arm.Body, bt):
			c.errorf(arm.Body.Span(), "all match arms must yield the same type; found %s and %s", result, bt)
		}
	}
	return result
}

func (c *checker) checkPattern(pat ast.Pattern, subjT Type) {
	switch p := pat.(type) {
	case *ast.WildPattern:
		// matches anything, binds nothing
	case *ast.NamePattern:
		c.declare(p.Span(), p.Name, subjT, false)
	case *ast.LitPattern:
		lt := c.checkExpr(p.Lit)
		if p.Neg && !lt.numeric() {
			c.errorf(p.Span(), "'-' pattern requires a number, found %s", lt)
		}
		if lt != Invalid && subjT != Invalid && !patternMatches(subjT, p.Lit, lt) {
			c.errorf(p.Span(), "a %s literal cannot match a subject of type %s", lt, subjT)
		}
	}
}

// patternMatches reports whether a literal pattern of type lt can match a subject
// of type subjT (allowing an untyped int literal against a float subject).
func patternMatches(subjT Type, lit ast.Expr, lt Type) bool {
	if subjT == lt {
		return true
	}
	_, isInt := lit.(*ast.IntLit)
	return subjT == Float && lt == Int && isInt
}

func isSimpleSubject(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StrLit:
		return true
	}
	return false
}

func isCatchAll(pat ast.Pattern) bool {
	switch pat.(type) {
	case *ast.WildPattern, *ast.NamePattern:
		return true
	}
	return false
}

// assignable reports whether a value of type vt (produced by expr e) fits a
// target of type want, allowing an untyped int literal to become a float.
func (c *checker) assignable(want Type, e ast.Expr, vt Type) bool {
	if want == vt {
		return true
	}
	if want == Float && vt == Int && isIntLit(e) {
		c.info.ExprTypes[e] = Float
		return true
	}
	return false
}

// --- scopes -------------------------------------------------------------------

func (c *checker) pushScope() { c.scopes = append(c.scopes, map[string]*symbol{}) }
func (c *checker) popScope()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) declare(span token.Span, name string, typ Type, mutable bool) {
	scope := c.scopes[len(c.scopes)-1]
	if _, dup := scope[name]; dup {
		c.errorf(span, "%q is already declared in this scope", name)
		return
	}
	scope[name] = &symbol{typ: typ, mutable: mutable}
}

func (c *checker) lookup(name string) *symbol {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if s, ok := c.scopes[i][name]; ok {
			return s
		}
	}
	return nil
}

// --- operator classification --------------------------------------------------

func isArithOp(k token.Kind) bool {
	switch k {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent,
		token.PlusMod, token.MinusMod, token.StarMod:
		return true
	}
	return false
}

func isBitOp(k token.Kind) bool {
	switch k {
	case token.Amp, token.Pipe, token.Caret, token.Shl, token.Shr:
		return true
	}
	return false
}

func isOrderOp(k token.Kind) bool {
	switch k {
	case token.Lt, token.Gt, token.Le, token.Ge:
		return true
	}
	return false
}

func isEqOp(k token.Kind) bool { return k == token.EqEq || k == token.Ne }

func isLogicOp(k token.Kind) bool { return k == token.And || k == token.Or }

func isIntLit(e ast.Expr) bool { _, ok := e.(*ast.IntLit); return ok }
