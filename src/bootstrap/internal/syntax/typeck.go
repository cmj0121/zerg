package syntax

import (
	"fmt"
	"math"
	"strconv"
)

// ---------------------------------------------------------------------------
// Type representation.
//
// Singletons are returned by the package-level helpers below. Every Expr
// annotated with a type points at one of those singletons, so type equality
// is pointer equality (`==`). That keeps consumers (run.go, cgen.go) from
// having to walk a tree to compare types.
//
// TypeKind values are exported so consumers can `switch` on a stable enum
// without embedding the v0.1-specific singleton list. Adding a new primitive
// later means appending a new TypeKind and a new singleton — no existing
// consumer code breaks.
// ---------------------------------------------------------------------------

// TypeKind enumerates v0.1 primitive types plus a void marker for functions
// declared without a return type.
type TypeKind int

// Type kinds. The ordering is stable; adding new kinds appends.
const (
	TypeUnknown TypeKind = iota
	TypeInt
	TypeFloat
	TypeBool
	TypeStr
	TypeVoid // functions without a declared return type
)

// Type is a v0.1 primitive type descriptor. Use the package-level singletons
// (TInt, TFloat, TBool, TStr, TVoid) — never construct a Type literal by hand,
// because pointer equality breaks immediately if two distinct *Type values
// share a Kind.
type Type struct {
	Kind TypeKind
}

// String returns the type name as it appears in source.
func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "bool"
	case TypeStr:
		return "str"
	case TypeVoid:
		return "()"
	default:
		return fmt.Sprintf("Type(%d)", int(t.Kind))
	}
}

// Package-level singletons. Tests outside this package should never compare
// types except through pointer equality on these values.
var (
	tInt   = &Type{Kind: TypeInt}
	tFloat = &Type{Kind: TypeFloat}
	tBool  = &Type{Kind: TypeBool}
	tStr   = &Type{Kind: TypeStr}
	tVoid  = &Type{Kind: TypeVoid}
)

// TInt returns the canonical int singleton.
func TInt() *Type { return tInt }

// TFloat returns the canonical float singleton.
func TFloat() *Type { return tFloat }

// TBool returns the canonical bool singleton.
func TBool() *Type { return tBool }

// TStr returns the canonical str singleton.
func TStr() *Type { return tStr }

// TVoid returns the canonical void singleton (used for functions without a
// declared return type).
func TVoid() *Type { return tVoid }

// ---------------------------------------------------------------------------
// TypeError.
// ---------------------------------------------------------------------------

// TypeError is the error returned by Check.
type TypeError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *TypeError) Error() string {
	return fmt.Sprintf("type error at %s: %s", e.Pos, e.Message)
}

func typeErr(pos Position, format string, args ...any) error {
	return &TypeError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Scope and binding.
// ---------------------------------------------------------------------------

// bindKind tags whether a name was introduced by let, mut, or const so the
// assignment checker can report a precise reason when assignment is illegal.
type bindKind int

const (
	bindLet bindKind = iota
	bindMut
	bindConst
)

// binding is a single name → type mapping inside a scope.
type binding struct {
	kind bindKind
	typ  *Type
}

// scope is one rung of the lexical-scope stack. names are flat — there's no
// nested-namespace gymnastics at v0.1 because functions live in their own
// table.
type scope struct {
	names  map[string]binding
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{names: map[string]binding{}, parent: parent}
}

// declare binds name in this scope. Returns false if name is already bound at
// this rung; same-name in an inner scope (shadowing) is allowed and handled
// by callers walking the parent chain.
func (s *scope) declare(name string, b binding) bool {
	if _, exists := s.names[name]; exists {
		return false
	}
	s.names[name] = b
	return true
}

// lookup walks the scope chain. Returns the binding and true on a hit.
func (s *scope) lookup(name string) (binding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.names[name]; ok {
			return b, true
		}
	}
	return binding{}, false
}

// fnSig is the signature of a top-level function — separate namespace from
// variables at v0.1 (a fn name and a let name cannot collide today, but they
// also do not share a slot, so callers always check the right table).
type fnSig struct {
	params []*Type
	ret    *Type // tVoid when the function declares no return type
	pos    Position
}

// ---------------------------------------------------------------------------
// Checker state and entry point.
// ---------------------------------------------------------------------------

// checker holds all transient state for a single Check pass.
//
// loopDepth and currentFn drive the flow-sensitive checks for break/continue
// and return: a break outside a loop or a return outside a function is an
// error at type-check time, before any code runs.
type checker struct {
	scope     *scope
	fns       map[string]fnSig
	currentFn *fnSig // nil at top level
	loopDepth int
}

// Check is the public entry point. It walks prog, annotates every Expr with
// a type, resolves every TypeRef, and returns the FIRST type or scope error
// encountered. Multi-error reporting is deferred to v0.10+.
func Check(prog *Program) error {
	c := &checker{
		scope: newScope(nil),
		fns:   map[string]fnSig{},
	}
	// Two-pass over top-level: collect function signatures first so calls
	// can refer to functions defined later in the file. Variable decls keep
	// strict top-down order.
	if err := c.collectTopLevel(prog); err != nil {
		return err
	}
	for _, stmt := range prog.Statements {
		if err := c.checkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// collectTopLevel registers every top-level FnDecl's signature in the
// function table without walking its body. A duplicate name is rejected here
// rather than during the second pass so the diagnostic points at the second
// declaration's position.
func (c *checker) collectTopLevel(prog *Program) error {
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
			continue
		}
		if _, dup := c.fns[fn.Name]; dup {
			return typeErr(fn.Pos, "function %q already declared", fn.Name)
		}
		// Resolve param and return types up front so the signature is
		// usable from peer-and-later call sites.
		params := make([]*Type, len(fn.Params))
		for i := range fn.Params {
			t, err := c.resolveTypeRef(fn.Params[i].Type)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(fn.Params[i].Pos, "parameter %q cannot have void type", fn.Params[i].Name)
			}
			params[i] = t
		}
		ret := tVoid
		if fn.Return != nil {
			t, err := c.resolveTypeRef(fn.Return)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(fn.Return.Pos, "use no return annotation instead of declaring a void return")
			}
			ret = t
		}
		c.fns[fn.Name] = fnSig{params: params, ret: ret, pos: fn.Pos}
	}
	return nil
}

// resolveTypeRef maps a TypeRef.Name to a *Type singleton, populates
// TypeRef.Resolved, and reports unknown names with a clear position.
func (c *checker) resolveTypeRef(ref *TypeRef) (*Type, error) {
	if ref == nil {
		return nil, nil
	}
	var t *Type
	switch ref.Name {
	case "int":
		t = tInt
	case "float":
		t = tFloat
	case "bool":
		t = tBool
	case "str":
		t = tStr
	default:
		return nil, typeErr(ref.Pos, "unknown type %q", ref.Name)
	}
	ref.Resolved = t
	return t, nil
}

// ---------------------------------------------------------------------------
// Statement walking.
// ---------------------------------------------------------------------------

func (c *checker) checkStmt(stmt Stmt) error {
	switch s := stmt.(type) {
	case *NopStmt:
		return nil
	case *PrintStmt:
		return c.checkPrint(s)
	case *LetStmt:
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindLet)
	case *MutStmt:
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindMut)
	case *ConstStmt:
		if !isConstExpr(s.Value) {
			return typeErr(s.Pos, "const initialiser must be a constant expression")
		}
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindConst)
	case *AssignStmt:
		return c.checkAssign(s)
	case *ExprStmt:
		_, err := c.checkExpr(s.Expr)
		return err
	case *IfStmt:
		return c.checkIf(s)
	case *ForStmt:
		return c.checkFor(s)
	case *FnDecl:
		return c.checkFnDecl(s)
	case *ReturnStmt:
		return c.checkReturn(s)
	case *BreakStmt:
		if c.loopDepth == 0 {
			return typeErr(s.Pos, "'break' outside of a loop")
		}
		return c.checkGuard(s.Guard)
	case *ContinueStmt:
		if c.loopDepth == 0 {
			return typeErr(s.Pos, "'continue' outside of a loop")
		}
		return c.checkGuard(s.Guard)
	}
	return typeErr(stmt.StmtPos(), "internal: unhandled statement %T", stmt)
}

// checkPrint validates `print expr`. Per the v0.1 print format table, the
// expression must be a primitive — no void. The expression's type is stored
// on the node by checkExpr; the codegen reads expr.Type() to dispatch to the
// matching helper.
func (c *checker) checkPrint(s *PrintStmt) error {
	t, err := c.checkExpr(s.Expr)
	if err != nil {
		return err
	}
	if !isPrintable(t) {
		return typeErr(s.Pos, "cannot print value of type %s", t)
	}
	return nil
}

func isPrintable(t *Type) bool {
	switch t {
	case tInt, tFloat, tBool, tStr:
		return true
	}
	return false
}

// checkDecl handles let/mut/const. Same logic for all three; the binding kind
// only changes how the new name is recorded.
func (c *checker) checkDecl(pos Position, name string, ref *TypeRef, value Expr, kind bindKind) error {
	annotated, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	observed, err := c.checkExpr(value)
	if err != nil {
		return err
	}
	final := observed
	if annotated != nil {
		if observed != annotated {
			return typeErr(pos, "cannot assign %s to %s for %q", observed, annotated, name)
		}
		final = annotated
	}
	if final == tVoid {
		return typeErr(pos, "cannot bind %q to a value of type ()", name)
	}
	if !c.scope.declare(name, binding{kind: kind, typ: final}) {
		return typeErr(pos, "name %q already declared in this scope", name)
	}
	return nil
}

// checkAssign validates the lhs is mut, then matches operator semantics.
func (c *checker) checkAssign(s *AssignStmt) error {
	b, ok := c.scope.lookup(s.Target.Name)
	if !ok {
		return typeErr(s.Target.Pos, "undefined name %q", s.Target.Name)
	}
	switch b.kind {
	case bindLet:
		return typeErr(s.Pos, "cannot assign to %q (declared with let)", s.Target.Name)
	case bindConst:
		return typeErr(s.Pos, "cannot assign to %q (declared with const)", s.Target.Name)
	}
	// Annotate the target ident expression.
	s.Target.setType(b.typ)
	rhs, err := c.checkExpr(s.Value)
	if err != nil {
		return err
	}
	switch s.Op {
	case AssignSet:
		if rhs != b.typ {
			return typeErr(s.Pos, "cannot assign %s to %q (declared %s)", rhs, s.Target.Name, b.typ)
		}
	case AssignAdd, AssignSub, AssignMul, AssignDiv, AssignMod:
		// Numeric compound; same as `lhs + rhs` typing rules — both must
		// match a numeric primitive. `+=` on str+str is also fine.
		if s.Op == AssignAdd && b.typ == tStr && rhs == tStr {
			break
		}
		if !isNumeric(b.typ) || rhs != b.typ {
			return typeErr(s.Pos, "operator %s requires numeric operands of the same type, got %s and %s", s.Op, b.typ, rhs)
		}
	case AssignAnd, AssignOr, AssignXor, AssignShl, AssignShr:
		// Bitwise — int only.
		if b.typ != tInt || rhs != tInt {
			return typeErr(s.Pos, "operator %s requires int operands, got %s and %s", s.Op, b.typ, rhs)
		}
	default:
		return typeErr(s.Pos, "internal: unknown assignment operator %s", s.Op)
	}
	return nil
}

// checkIf type-checks the condition (must be bool) and walks each branch in a
// fresh scope. No truthy coercion at v0.1.
func (c *checker) checkIf(s *IfStmt) error {
	condT, err := c.checkExpr(s.Cond)
	if err != nil {
		return err
	}
	if condT != tBool {
		return typeErr(s.Cond.ExprPos(), "'if' condition must be bool, got %s", condT)
	}
	if err := c.checkBlock(s.Then); err != nil {
		return err
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		ct, err := c.checkExpr(ec.Cond)
		if err != nil {
			return err
		}
		if ct != tBool {
			return typeErr(ec.Cond.ExprPos(), "'elif' condition must be bool, got %s", ct)
		}
		if err := c.checkBlock(ec.Body); err != nil {
			return err
		}
	}
	if s.Else != nil {
		if err := c.checkBlock(s.Else); err != nil {
			return err
		}
	}
	return nil
}

// checkFor handles all three for-loop shapes.
func (c *checker) checkFor(s *ForStmt) error {
	c.loopDepth++
	defer func() { c.loopDepth-- }()
	switch s.Kind {
	case ForInfinite:
		return c.checkBlock(s.Body)
	case ForCond:
		ct, err := c.checkExpr(s.Cond)
		if err != nil {
			return err
		}
		if ct != tBool {
			return typeErr(s.Cond.ExprPos(), "'for' condition must be bool, got %s", ct)
		}
		return c.checkBlock(s.Body)
	case ForRange:
		// Range bounds must be int. We use checkExpr (not the Range form)
		// because the parser only assembles a RangeExpr in the for-in head;
		// no other call site produces one.
		startT, err := c.checkExpr(s.Range.Start)
		if err != nil {
			return err
		}
		if startT != tInt {
			return typeErr(s.Range.Start.ExprPos(), "range start must be int, got %s", startT)
		}
		endT, err := c.checkExpr(s.Range.End)
		if err != nil {
			return err
		}
		if endT != tInt {
			return typeErr(s.Range.End.ExprPos(), "range end must be int, got %s", endT)
		}
		// The loop variable is bound to int inside a body-local scope.
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: tInt}) {
			// Empty fresh scope can't have a duplicate, but be defensive.
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
		// Walk the body's statements directly so the loop variable is in
		// scope (we already pushed our own scope, no need for checkBlock to
		// push another).
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(s.Pos, "internal: unknown for kind")
}

// checkBlock pushes a new scope and walks statements.
func (c *checker) checkBlock(b *Block) error {
	c.scope = newScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for _, st := range b.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// checkFnDecl walks a top-level function body. Nested fn decls (FnDecl
// observed inside another body's traversal) are rejected — checkBlockBody
// is reached only via the top-level dispatcher, but to be safe we set
// currentFn here and re-check in checkStmt indirectly via the FnDecl branch.
func (c *checker) checkFnDecl(fn *FnDecl) error {
	if c.currentFn != nil {
		return typeErr(fn.Pos, "nested functions are not supported at v0.1")
	}
	sig := c.fns[fn.Name]
	c.currentFn = &sig
	defer func() { c.currentFn = nil }()

	c.scope = newScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for i, p := range fn.Params {
		if !c.scope.declare(p.Name, binding{kind: bindLet, typ: sig.params[i]}) {
			return typeErr(p.Pos, "parameter %q already declared", p.Name)
		}
	}
	for _, st := range fn.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// checkReturn validates the return is inside a fn and the value (if any)
// matches the declared return type. A guard expression must be bool.
func (c *checker) checkReturn(s *ReturnStmt) error {
	if c.currentFn == nil {
		return typeErr(s.Pos, "'return' outside of a function")
	}
	if err := c.checkGuard(s.Guard); err != nil {
		return err
	}
	if s.Value == nil {
		if c.currentFn.ret != tVoid {
			return typeErr(s.Pos, "function declared to return %s but bare 'return'", c.currentFn.ret)
		}
		return nil
	}
	t, err := c.checkExpr(s.Value)
	if err != nil {
		return err
	}
	if c.currentFn.ret == tVoid {
		return typeErr(s.Pos, "function returns no value but 'return' has expression of type %s", t)
	}
	if t != c.currentFn.ret {
		return typeErr(s.Pos, "return type mismatch: function returns %s, got %s", c.currentFn.ret, t)
	}
	return nil
}

// checkGuard validates a break/continue/return guard expression.
func (c *checker) checkGuard(g Expr) error {
	if g == nil {
		return nil
	}
	t, err := c.checkExpr(g)
	if err != nil {
		return err
	}
	if t != tBool {
		return typeErr(g.ExprPos(), "guard must be bool, got %s", t)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Expression walking. Every path returns the Type assigned to expr and also
// stores it on the node via setType.
// ---------------------------------------------------------------------------

func (c *checker) checkExpr(expr Expr) (*Type, error) {
	switch e := expr.(type) {
	case *IntLit:
		// strconv.ParseInt with base 0 reads the prefix-preserved form the
		// lexer produced. Overflow is a compile-time error at v0.1.
		v, err := strconv.ParseInt(e.Text, 0, 64)
		if err != nil {
			return nil, typeErr(e.Pos, "integer literal %s overflows int", e.Text)
		}
		e.Int = v
		e.setType(tInt)
		return tInt, nil
	case *FloatLit:
		v, err := strconv.ParseFloat(e.Text, 64)
		// strconv returns ±Inf with no error when the literal exceeds
		// float64 range; we treat that as a compile-time error so v0.1
		// programs never silently print Inf.
		if err != nil || math.IsInf(v, 0) {
			return nil, typeErr(e.Pos, "float literal %s overflows float", e.Text)
		}
		e.Float = v
		e.setType(tFloat)
		return tFloat, nil
	case *StringLit:
		e.setType(tStr)
		return tStr, nil
	case *BoolLit:
		e.setType(tBool)
		return tBool, nil
	case *IdentExpr:
		b, ok := c.scope.lookup(e.Name)
		if !ok {
			return nil, typeErr(e.Pos, "undefined name %q", e.Name)
		}
		e.setType(b.typ)
		return b.typ, nil
	case *BinaryExpr:
		return c.checkBinary(e)
	case *UnaryExpr:
		return c.checkUnary(e)
	case *CallExpr:
		return c.checkCall(e)
	case *ParenExpr:
		t, err := c.checkExpr(e.Inner)
		if err != nil {
			return nil, err
		}
		e.setType(t)
		return t, nil
	case *RangeExpr:
		// At v0.1 the parser only constructs RangeExpr inside a for-in head
		// and the for-in checker handles bounds directly. Reaching this
		// point means a future parser path produced a Range somewhere else;
		// reject explicitly rather than silently typing.
		return nil, typeErr(e.Pos, "range expressions cannot be used as values at v0.1")
	}
	return nil, typeErr(expr.ExprPos(), "internal: unhandled expression %T", expr)
}

// checkBinary validates per-operator typing rules from PLAN.md.
func (c *checker) checkBinary(e *BinaryExpr) (*Type, error) {
	lt, err := c.checkExpr(e.Left)
	if err != nil {
		return nil, err
	}
	rt, err := c.checkExpr(e.Right)
	if err != nil {
		return nil, err
	}
	var result *Type
	switch e.Op {
	case BinAdd:
		// Numeric or str+str.
		if lt == tStr && rt == tStr {
			result = tStr
		} else if isNumeric(lt) && lt == rt {
			result = lt
		} else {
			return nil, typeErr(e.Pos, "operator + requires numeric or str operands of the same type, got %s and %s", lt, rt)
		}
	case BinSub, BinMul, BinDiv, BinFloorDiv, BinMod:
		if !isNumeric(lt) || lt != rt {
			return nil, typeErr(e.Pos, "operator %s requires numeric operands of the same type, got %s and %s", e.Op, lt, rt)
		}
		result = lt
	case BinBitOr, BinBitXor, BinBitAnd, BinShl, BinShr:
		if lt != tInt || rt != tInt {
			return nil, typeErr(e.Pos, "operator %s requires int operands, got %s and %s", e.Op, lt, rt)
		}
		result = tInt
	case BinEq, BinNE:
		// Same-typed primitive operands; void is not comparable.
		if lt != rt || !isPrintable(lt) {
			return nil, typeErr(e.Pos, "operator %s requires same-typed primitive operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	case BinLT, BinGT, BinLE, BinGE:
		// Numeric or str ordering. Bool is not ordered.
		if lt != rt || !(isNumeric(lt) || lt == tStr) {
			return nil, typeErr(e.Pos, "operator %s requires same-typed numeric or str operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	case BinAnd, BinOr, BinXor:
		if lt != tBool || rt != tBool {
			return nil, typeErr(e.Pos, "operator %s requires bool operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	default:
		return nil, typeErr(e.Pos, "internal: unknown binary operator %s", e.Op)
	}
	e.setType(result)
	return result, nil
}

func (c *checker) checkUnary(e *UnaryExpr) (*Type, error) {
	t, err := c.checkExpr(e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case UnaryNeg:
		if !isNumeric(t) {
			return nil, typeErr(e.Pos, "unary - requires a numeric operand, got %s", t)
		}
	case UnaryBitNot:
		if t != tInt {
			return nil, typeErr(e.Pos, "unary ~ requires an int operand, got %s", t)
		}
	case UnaryNot:
		if t != tBool {
			return nil, typeErr(e.Pos, "unary not requires a bool operand, got %s", t)
		}
	default:
		return nil, typeErr(e.Pos, "internal: unknown unary operator %s", e.Op)
	}
	e.setType(t)
	return t, nil
}

// checkCall enforces v0.1 callee shape: bare identifier resolving to a
// declared function. Function names are NOT first-class values at v0.1, so a
// CallExpr is the only context in which an IdentExpr resolves against the
// function table.
func (c *checker) checkCall(e *CallExpr) (*Type, error) {
	ident, ok := e.Callee.(*IdentExpr)
	if !ok {
		return nil, typeErr(e.Pos, "callee must be a function name at v0.1")
	}
	sig, ok := c.fns[ident.Name]
	if !ok {
		// Distinguish "ident is a variable, not a function" from "no such
		// name" to give a more useful error.
		if _, isVar := c.scope.lookup(ident.Name); isVar {
			return nil, typeErr(ident.Pos, "%q is not a function", ident.Name)
		}
		return nil, typeErr(ident.Pos, "undefined function %q", ident.Name)
	}
	if len(e.Args) != len(sig.params) {
		return nil, typeErr(e.Pos, "function %q expects %d argument(s), got %d", ident.Name, len(sig.params), len(e.Args))
	}
	for i, a := range e.Args {
		at, err := c.checkExpr(a)
		if err != nil {
			return nil, err
		}
		if at != sig.params[i] {
			return nil, typeErr(a.ExprPos(), "argument %d to %q has type %s, expected %s", i+1, ident.Name, at, sig.params[i])
		}
	}
	// Annotate the callee ident with the function's return type. That keeps
	// expr.Type() meaningful for every node — including the Callee — even
	// though "function value" isn't a first-class concept yet.
	ident.setType(sig.ret)
	e.setType(sig.ret)
	return sig.ret, nil
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func isNumeric(t *Type) bool { return t == tInt || t == tFloat }

// isConstExpr reports whether expr is admissible on the right-hand side of a
// `const` declaration. Defined per PLAN.md as a literal, a unary on a
// const-expr, or a binary of const-exprs. ParenExpr is transparent. Identifier
// references — even to other consts — are NOT permitted at v0.1; that keeps
// the checker free of a constant-evaluation pass.
func isConstExpr(e Expr) bool {
	switch x := e.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit:
		return true
	case *ParenExpr:
		return isConstExpr(x.Inner)
	case *UnaryExpr:
		return isConstExpr(x.Operand)
	case *BinaryExpr:
		return isConstExpr(x.Left) && isConstExpr(x.Right)
	}
	return false
}
