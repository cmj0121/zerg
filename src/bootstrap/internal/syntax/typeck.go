package syntax

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Type representation.
//
// v0.1's design relied on every Type being a package-level singleton so equality
// was pointer-equality. v0.2 keeps that invariant for primitives — TInt, TStr,
// etc. remain singletons returned by package-level helpers — but adds composite
// kinds (list, tuple, struct, enum) where the same shape can legitimately be
// constructed at multiple call sites. Those use *structural* equality via
// Type.Equals so two `list[int]` types compare equal regardless of which
// constructor produced them.
//
// Struct and enum types are stored in a per-program type table keyed by name,
// and every reference to `Point` in a v0.2 program shares the single canonical
// *Type pointer. Equality on those is therefore still effectively pointer
// equality, but Equals handles the general case so external callers don't have
// to know which path produced a given Type.
// ---------------------------------------------------------------------------

// TypeKind enumerates v0.2 primitive types, the v0.1 void marker, and the
// composite kinds. Adding a new kind appends.
type TypeKind int

// Type kinds. Stable ordering — appending preserves wire compatibility for any
// future serialized typed-AST.
const (
	TypeUnknown TypeKind = iota // empty list literal awaiting context
	TypeInt
	TypeFloat
	TypeBool
	TypeStr
	TypeByte
	TypeRune
	TypeVoid
	TypeList   // Element non-nil
	TypeTuple  // Tuple non-nil (≥ 2 elements)
	TypeStruct // Name + Fields populated; canonical instance stored in struct table
	TypeEnum   // Name + Variants populated; canonical instance stored in enum table
)

// NamedField is one field of a struct type. Order matches declaration order
// because PLAN.md pins struct print order to declaration order.
type NamedField struct {
	Name string
	Type *Type
}

// Type is a v0.2 type descriptor. Use the package-level helpers (TInt, TByte,
// NewListType, NewStructType, ...) — never construct a Type literal by hand
// because primitives rely on pointer equality and composites rely on the
// type table for struct/enum canonicalisation.
type Type struct {
	Kind     TypeKind
	Element  *Type        // TypeList
	Tuple    []*Type      // TypeTuple
	Name     string       // TypeStruct / TypeEnum
	Fields   []NamedField // TypeStruct, declaration order
	Variants []string     // TypeEnum, declaration order
}

// String returns the type spelled the way the source spells it. Used in error
// messages.
func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case TypeUnknown:
		return "<unknown>"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "bool"
	case TypeStr:
		return "str"
	case TypeByte:
		return "byte"
	case TypeRune:
		return "rune"
	case TypeVoid:
		return "()"
	case TypeList:
		return "list[" + t.Element.String() + "]"
	case TypeTuple:
		var b strings.Builder
		b.WriteString("tuple[")
		for i, e := range t.Tuple {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(e.String())
		}
		b.WriteString("]")
		return b.String()
	case TypeStruct, TypeEnum:
		return t.Name
	}
	return fmt.Sprintf("Type(%d)", int(t.Kind))
}

// Equals reports whether t and u describe the same shape. Primitives compare
// by pointer (singletons); composites compare structurally.
func (t *Type) Equals(u *Type) bool {
	if t == u {
		return true
	}
	if t == nil || u == nil {
		return false
	}
	if t.Kind != u.Kind {
		return false
	}
	switch t.Kind {
	case TypeList:
		return t.Element.Equals(u.Element)
	case TypeTuple:
		if len(t.Tuple) != len(u.Tuple) {
			return false
		}
		for i := range t.Tuple {
			if !t.Tuple[i].Equals(u.Tuple[i]) {
				return false
			}
		}
		return true
	case TypeStruct, TypeEnum:
		// Canonical name suffices because the type table guarantees one
		// instance per name. Equal names ⇒ equal types.
		return t.Name == u.Name
	}
	// Primitives only reach here when pointer-equality already held above; for
	// safety we still treat same-Kind primitives as equal so a stray non-canonical
	// instance constructed by a future caller doesn't silently mis-compare.
	return true
}

// Package-level singletons. Pointer equality on these works exactly like v0.1.
var (
	tInt   = &Type{Kind: TypeInt}
	tFloat = &Type{Kind: TypeFloat}
	tBool  = &Type{Kind: TypeBool}
	tStr   = &Type{Kind: TypeStr}
	tByte  = &Type{Kind: TypeByte}
	tRune  = &Type{Kind: TypeRune}
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

// TByte returns the canonical byte singleton (v0.2).
func TByte() *Type { return tByte }

// TRune returns the canonical rune singleton (v0.2).
func TRune() *Type { return tRune }

// TVoid returns the canonical void singleton.
func TVoid() *Type { return tVoid }

// NewListType constructs a list[T] type. The receiver is fresh, but Equals
// makes structural equality safe — callers do not need to share Type pointers
// across construction sites.
func NewListType(elem *Type) *Type {
	return &Type{Kind: TypeList, Element: elem}
}

// NewTupleType constructs a tuple[T1, T2, ...] type. The slice is owned by the
// returned Type.
func NewTupleType(elems []*Type) *Type {
	return &Type{Kind: TypeTuple, Tuple: elems}
}

// NewStructType constructs a struct type. Only the typeck collector should call
// this — one canonical struct lives in the type table per name.
func NewStructType(name string, fields []NamedField) *Type {
	return &Type{Kind: TypeStruct, Name: name, Fields: fields}
}

// NewEnumType constructs an enum type. Only the typeck collector should call
// this — one canonical enum lives in the type table per name.
func NewEnumType(name string, variants []string) *Type {
	return &Type{Kind: TypeEnum, Name: name, Variants: variants}
}

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

// scope is one rung of the lexical-scope stack.
type scope struct {
	names  map[string]binding
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{names: map[string]binding{}, parent: parent}
}

// declare binds name in this scope. Returns false if name is already bound at
// this rung; same-name in an inner scope (shadowing) is allowed.
func (s *scope) declare(name string, b binding) bool {
	if _, exists := s.names[name]; exists {
		return false
	}
	s.names[name] = b
	return true
}

// lookup walks the scope chain.
func (s *scope) lookup(name string) (binding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.names[name]; ok {
			return b, true
		}
	}
	return binding{}, false
}

// fnSig is the signature of a top-level function. builtin marks `len` so we
// can detect (and reject) user attempts to redefine it.
type fnSig struct {
	params  []*Type
	ret     *Type
	pos     Position
	builtin bool
}

// ---------------------------------------------------------------------------
// Checker state and entry point.
// ---------------------------------------------------------------------------

// checker holds all transient state for a single Check pass. structs and enums
// share a single namespace at the top level because they live in the same
// type-name space (a struct and an enum can't share a name).
type checker struct {
	scope     *scope
	fns       map[string]fnSig
	structs   map[string]*Type     // Name → struct *Type (Kind TypeStruct)
	enums     map[string]*Type     // Name → enum *Type (Kind TypeEnum)
	structAST map[string]*StructDecl // Name → AST node, for the second-pass field resolution
	currentFn *fnSig                 // nil at top level
	loopDepth int
}

// Check is the public entry point. It walks prog, annotates every Expr with
// a type, resolves every TypeRef, and returns the FIRST type or scope error
// encountered.
func Check(prog *Program) error {
	c := &checker{
		scope:     newScope(nil),
		fns:       map[string]fnSig{},
		structs:   map[string]*Type{},
		enums:     map[string]*Type{},
		structAST: map[string]*StructDecl{},
	}
	// Pre-populate the function table with `len`. It's resolved as a built-in
	// at call time — the params slot here is a placeholder that the call-site
	// resolver special-cases (any list[T] is admissible).
	c.fns["len"] = fnSig{
		params:  []*Type{NewListType(tInt)}, // documentation-only sentinel
		ret:     tInt,
		builtin: true,
	}

	if err := c.collectTopLevel(prog); err != nil {
		return err
	}
	if err := c.resolveStructFields(prog); err != nil {
		return err
	}
	if err := c.detectStructCycles(); err != nil {
		return err
	}
	if err := c.resolveFnSignatures(prog); err != nil {
		return err
	}
	for _, stmt := range prog.Statements {
		if err := c.checkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// collectTopLevel registers every top-level struct, enum, and fn name across
// a single shared name space. PLAN: a struct and an enum cannot share a name,
// nor can a struct and a fn (the table is checked across all three groups).
// Field types and fn signatures are NOT resolved here — that needs the full
// table populated first so forward references work.
func (c *checker) collectTopLevel(prog *Program) error {
	declared := map[string]Position{} // name → position of first declaration, used for diagnostics

	register := func(name string, pos Position, kind string) error {
		if prev, exists := declared[name]; exists {
			_ = prev
			return typeErr(pos, "%s %q already declared", kind, name)
		}
		declared[name] = pos
		return nil
	}

	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *StructDecl:
			if err := register(s.Name, s.Pos, "struct"); err != nil {
				return err
			}
			if len(s.Fields) == 0 {
				// PLAN: empty struct/enum decls are rejected. The grammar
				// admits them syntactically; typeck is the authoritative gate.
				return typeErr(s.Pos, "struct %q must declare at least one field", s.Name)
			}
			// Reject duplicate field names within the same struct so the
			// struct table never contains an ambiguous field set.
			seen := map[string]bool{}
			for _, f := range s.Fields {
				if seen[f.Name] {
					return typeErr(f.Pos, "duplicate field %q in struct %q", f.Name, s.Name)
				}
				seen[f.Name] = true
			}
			c.structs[s.Name] = NewStructType(s.Name, nil) // fields filled by second pass
			c.structAST[s.Name] = s
		case *EnumDecl:
			if err := register(s.Name, s.Pos, "enum"); err != nil {
				return err
			}
			if len(s.Variants) == 0 {
				return typeErr(s.Pos, "enum %q must declare at least one variant", s.Name)
			}
			seen := map[string]bool{}
			variants := make([]string, len(s.Variants))
			for i, v := range s.Variants {
				if seen[v.Name] {
					return typeErr(v.Pos, "duplicate variant %q in enum %q", v.Name, s.Name)
				}
				seen[v.Name] = true
				variants[i] = v.Name
			}
			c.enums[s.Name] = NewEnumType(s.Name, variants)
		case *FnDecl:
			// PLAN-pinned: cannot redefine the built-in `len`.
			if s.Name == "len" {
				return typeErr(s.Pos, "cannot redefine built-in 'len'")
			}
			if err := register(s.Name, s.Pos, "function"); err != nil {
				return err
			}
			c.fns[s.Name] = fnSig{pos: s.Pos} // signature filled by resolveFnSignatures
		}
	}
	return nil
}

// resolveStructFields resolves field types now that every top-level name is
// known. Forward references between structs and back-references to enums
// resolve here.
func (c *checker) resolveStructFields(_ *Program) error {
	for name, decl := range c.structAST {
		fields := make([]NamedField, len(decl.Fields))
		for i, f := range decl.Fields {
			t, err := c.resolveTypeRef(f.Type)
			if err != nil {
				return err
			}
			if t == tVoid {
				return typeErr(f.Pos, "field %q cannot have void type", f.Name)
			}
			fields[i] = NamedField{Name: f.Name, Type: t}
		}
		c.structs[name].Fields = fields
	}
	return nil
}

// detectStructCycles walks every struct's fields with DFS and rejects direct
// or transitive recursion. Lists-through-self count as cycles too: lists are
// value-copied at v0.2 so a `struct A { xs: list[A] }` would still imply
// infinite size at value-semantics. Tuples are inline shapes, so a tuple
// containing a struct also contributes to a cycle.
func (c *checker) detectStructCycles() error {
	const (
		gray  = 1
		black = 2
	)
	state := map[string]int{}

	var visit func(name string, viaPos Position, viaDesc string) error
	visit = func(name string, viaPos Position, viaDesc string) error {
		switch state[name] {
		case black:
			return nil
		case gray:
			return typeErr(viaPos, "recursive struct %q is not allowed at v0.2 (%s)", name, viaDesc)
		}
		state[name] = gray
		st := c.structs[name]
		decl := c.structAST[name]
		for i, f := range st.Fields {
			if err := visitType(f.Type, decl.Fields[i].Pos, fmt.Sprintf("via field %q", f.Name), visit); err != nil {
				return err
			}
		}
		state[name] = black
		return nil
	}

	for name := range c.structs {
		if state[name] == 0 {
			if err := visit(name, c.structAST[name].Pos, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// visitType walks a Type's struct references for cycle detection. Lists, tuples,
// and nested structs all pull the cycle through.
func visitType(t *Type, viaPos Position, viaDesc string, visit func(string, Position, string) error) error {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case TypeStruct:
		return visit(t.Name, viaPos, viaDesc)
	case TypeList:
		return visitType(t.Element, viaPos, viaDesc+" (through list)", visit)
	case TypeTuple:
		for _, e := range t.Tuple {
			if err := visitType(e, viaPos, viaDesc+" (through tuple)", visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveFnSignatures fills in every top-level fn's signature now that struct
// and enum types are known. The built-in `len` was inserted before the AST
// walk and is left untouched here.
func (c *checker) resolveFnSignatures(prog *Program) error {
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
			continue
		}
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
		// Preserve the existing entry (with its position) and overwrite the
		// signature fields. Builtin entries never reach this loop because the
		// AST has no FnDecl for `len`.
		sig := c.fns[fn.Name]
		sig.params = params
		sig.ret = ret
		sig.pos = fn.Pos
		c.fns[fn.Name] = sig
	}
	return nil
}

// resolveTypeRef maps a TypeRef to a *Type, populates TypeRef.Resolved, and
// reports unknown names with a clear position. List and tuple type-refs
// resolve recursively.
func (c *checker) resolveTypeRef(ref *TypeRef) (*Type, error) {
	if ref == nil {
		return nil, nil
	}
	switch ref.Kind {
	case TypeRefNamed:
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
		case "byte":
			t = tByte
		case "rune":
			t = tRune
		default:
			if st, ok := c.structs[ref.Name]; ok {
				t = st
			} else if en, ok := c.enums[ref.Name]; ok {
				t = en
			} else {
				return nil, typeErr(ref.Pos, "unknown type %q", ref.Name)
			}
		}
		ref.Resolved = t
		return t, nil
	case TypeRefList:
		elem, err := c.resolveTypeRef(ref.Element)
		if err != nil {
			return nil, err
		}
		if elem == tVoid {
			return nil, typeErr(ref.Pos, "list element type cannot be void")
		}
		t := NewListType(elem)
		ref.Resolved = t
		return t, nil
	case TypeRefTuple:
		elems := make([]*Type, len(ref.Elements))
		for i, sub := range ref.Elements {
			t, err := c.resolveTypeRef(sub)
			if err != nil {
				return nil, err
			}
			if t == tVoid {
				return nil, typeErr(sub.Pos, "tuple element type cannot be void")
			}
			elems[i] = t
		}
		t := NewTupleType(elems)
		ref.Resolved = t
		return t, nil
	}
	return nil, typeErr(ref.Pos, "internal: unknown TypeRef kind %d", int(ref.Kind))
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
		if s.Tuple != nil {
			return c.checkTupleDestructure(s.Pos, s.Tuple, s.Value, bindLet)
		}
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindLet)
	case *MutStmt:
		if s.Tuple != nil {
			return c.checkTupleDestructure(s.Pos, s.Tuple, s.Value, bindMut)
		}
		return c.checkDecl(s.Pos, s.Name, s.Type, s.Value, bindMut)
	case *ConstStmt:
		if s.Tuple != nil {
			// PLAN: composites are not const-evaluable at v0.2 (isConstExpr
			// rejects every tuple/list/struct shape), so a destructured const
			// is unreachable in practice. Emit the precise diagnostic anyway
			// so the parser-accepted form fails with a clear reason rather
			// than an opaque "not a constant expression".
			return typeErr(s.Pos, "tuple destructure is not allowed on const at v0.2 (no const-evaluable composite expressions)")
		}
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
	case *StructDecl:
		// Already collected and resolved; nothing to do during the body walk.
		return nil
	case *EnumDecl:
		return nil
	case *MatchStmt:
		return c.checkMatch(s)
	}
	return typeErr(stmt.StmtPos(), "internal: unhandled statement %T", stmt)
}

// checkPrint validates `print expr`. v0.2 accepts every printable shape:
// primitives, lists, tuples, structs, enums. Void is rejected.
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

// isPrintable reports whether print accepts a value of t. Per PLAN.md every
// concrete v0.2 shape (primitive, list, tuple, struct, enum) is admissible.
// Unknown / void are not.
func isPrintable(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeInt, TypeFloat, TypeBool, TypeStr, TypeByte, TypeRune:
		return true
	case TypeList:
		// Forbid printing a list whose element is non-printable (mostly defends
		// against an empty-list-with-unknown-elem leaking into print).
		return isPrintable(t.Element)
	case TypeTuple:
		for _, e := range t.Tuple {
			if !isPrintable(e) {
				return false
			}
		}
		return true
	case TypeStruct, TypeEnum:
		return true
	}
	return false
}

// checkDecl handles let/mut/const. The annotated type, when present, drives
// inference for empty-list literals.
func (c *checker) checkDecl(pos Position, name string, ref *TypeRef, value Expr, kind bindKind) error {
	annotated, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	// Pass the annotated type as a hint so empty-list literals can latch onto
	// it. Other expression forms ignore the hint.
	observed, err := c.checkExprHint(value, annotated)
	if err != nil {
		return err
	}
	final := observed
	if annotated != nil {
		if !typeEq(observed, annotated) {
			return typeErr(pos, "cannot assign %s to %s for %q", observed, annotated, name)
		}
		final = annotated
	}
	if final == nil || final.Kind == TypeUnknown {
		return typeErr(pos, "cannot infer type of %q (provide an annotation)", name)
	}
	if final == tVoid {
		return typeErr(pos, "cannot bind %q to a value of type ()", name)
	}
	if !c.scope.declare(name, binding{kind: kind, typ: final}) {
		return typeErr(pos, "name %q already declared in this scope", name)
	}
	return nil
}

// checkTupleDestructure handles `let (a, b) := expr` (and the mut form). The
// RHS must be a tuple type with arity matching the LHS name list; each name
// is then bound in the surrounding scope with its element type. The parser
// has already rejected repeated names; the only diagnostics generated here
// are for arity / shape mismatch and shadowing-in-the-same-scope.
//
// Annotated destructure (`let (a, b): tuple[int, int] = ...`) is rejected by
// the parser so we never see a non-nil TypeRef here.
func (c *checker) checkTupleDestructure(pos Position, tb *TupleBinding, value Expr, kind bindKind) error {
	observed, err := c.checkExpr(value)
	if err != nil {
		return err
	}
	if observed == nil || observed.Kind != TypeTuple {
		return typeErr(pos, "tuple destructure requires a tuple value, got %s", observed)
	}
	if len(observed.Tuple) != len(tb.Names) {
		return typeErr(pos, "destructure expects %d element(s), value has %d", len(tb.Names), len(observed.Tuple))
	}
	for i, name := range tb.Names {
		elemT := observed.Tuple[i]
		if elemT == nil || elemT.Kind == TypeUnknown {
			return typeErr(tb.NamePos[i], "cannot infer type of %q from tuple element %d", name, i+1)
		}
		if elemT == tVoid {
			return typeErr(tb.NamePos[i], "cannot bind %q to a value of type ()", name)
		}
		if !c.scope.declare(name, binding{kind: kind, typ: elemT}) {
			return typeErr(tb.NamePos[i], "name %q already declared in this scope", name)
		}
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
	s.Target.setType(b.typ)
	rhs, err := c.checkExprHint(s.Value, b.typ)
	if err != nil {
		return err
	}
	switch s.Op {
	case AssignSet:
		if !typeEq(rhs, b.typ) {
			return typeErr(s.Pos, "cannot assign %s to %q (declared %s)", rhs, s.Target.Name, b.typ)
		}
	case AssignAdd, AssignSub, AssignMul, AssignDiv, AssignMod:
		if s.Op == AssignAdd && b.typ == tStr && rhs == tStr {
			break
		}
		if !isNumeric(b.typ) || !typeEq(rhs, b.typ) {
			return typeErr(s.Pos, "operator %s requires numeric operands of the same type, got %s and %s", s.Op, b.typ, rhs)
		}
	case AssignAnd, AssignOr, AssignXor, AssignShl, AssignShr:
		if b.typ != tInt || rhs != tInt {
			return typeErr(s.Pos, "operator %s requires int operands, got %s and %s", s.Op, b.typ, rhs)
		}
	default:
		return typeErr(s.Pos, "internal: unknown assignment operator %s", s.Op)
	}
	return nil
}

// checkIf type-checks the condition and walks each branch in a fresh scope.
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
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: tInt}) {
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		return nil
	case ForIter:
		// `for x in xs { ... }` — iterate over a list-typed expression. The
		// loop variable's type is the element type. Empty lists are allowed
		// (the body never runs); typeck rejects unannotated empty literals
		// upstream so the iterable's element type is always concrete here.
		iterT, err := c.checkExpr(s.Iter)
		if err != nil {
			return err
		}
		if iterT == nil || iterT.Kind != TypeList {
			return typeErr(s.Iter.ExprPos(), "'for ... in' iterable must be a list, got %s", iterT)
		}
		c.scope = newScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		if !c.scope.declare(s.Var, binding{kind: bindLet, typ: iterT.Element}) {
			return typeErr(s.VarPos, "name %q already declared in this scope", s.Var)
		}
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

// checkFnDecl walks a top-level function body.
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

// checkReturn validates the return is inside a fn and the value matches.
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
	t, err := c.checkExprHint(s.Value, c.currentFn.ret)
	if err != nil {
		return err
	}
	if c.currentFn.ret == tVoid {
		return typeErr(s.Pos, "function returns no value but 'return' has expression of type %s", t)
	}
	if !typeEq(t, c.currentFn.ret) {
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
// Match statements.
// ---------------------------------------------------------------------------

// checkMatch walks a match statement: type-checks the subject, then for each
// arm validates the pattern against the subject type, opens a fresh scope
// populated with pattern bindings, checks the optional guard (must be bool),
// and finally walks the arm body.
func (c *checker) checkMatch(s *MatchStmt) error {
	subjT, err := c.checkExpr(s.Subject)
	if err != nil {
		return err
	}
	if subjT == nil || subjT == tVoid {
		return typeErr(s.Subject.ExprPos(), "cannot match on value of type %s", subjT)
	}
	for i := range s.Arms {
		arm := &s.Arms[i]
		// Per-arm scope so bindings vanish at arm boundary.
		armScope := newScope(c.scope)
		bindings := map[string]bool{}
		if err := c.checkPattern(arm.Pattern, subjT, armScope, bindings); err != nil {
			return err
		}
		// Walk guard and body with the arm scope active.
		saved := c.scope
		c.scope = armScope
		if arm.Guard != nil {
			gt, err := c.checkExpr(arm.Guard)
			if err != nil {
				c.scope = saved
				return err
			}
			if gt != tBool {
				c.scope = saved
				return typeErr(arm.Guard.ExprPos(), "match guard must be bool, got %s", gt)
			}
		}
		// The body itself is a Block; checkBlock pushes another scope on top
		// of the arm scope so the body is free to redeclare names without
		// clobbering pattern bindings.
		err := c.checkBlock(arm.Body)
		c.scope = saved
		if err != nil {
			return err
		}
	}
	return nil
}

// checkPattern validates pat against expected. Bindings are recorded in
// armScope; the bindings map tracks names within a single pattern so `Point
// { x, x }` is rejected.
func (c *checker) checkPattern(pat Pattern, expected *Type, armScope *scope, bindings map[string]bool) error {
	switch p := pat.(type) {
	case *WildcardPat:
		return nil
	case *BindPat:
		if bindings[p.Name] {
			return typeErr(p.Pos, "name %q already bound in this pattern", p.Name)
		}
		bindings[p.Name] = true
		if !armScope.declare(p.Name, binding{kind: bindLet, typ: expected}) {
			return typeErr(p.Pos, "name %q already declared in this scope", p.Name)
		}
		return nil
	case *LitPat:
		t, err := c.checkExpr(p.Lit)
		if err != nil {
			return err
		}
		if !typeEq(t, expected) {
			return typeErr(p.Pos, "literal pattern of type %s does not match subject type %s", t, expected)
		}
		return nil
	case *TuplePat:
		if expected == nil || expected.Kind != TypeTuple {
			return typeErr(p.Pos, "tuple pattern cannot match subject of type %s", expected)
		}
		if len(p.Elements) != len(expected.Tuple) {
			return typeErr(p.Pos, "tuple pattern has %d element(s), subject has %d", len(p.Elements), len(expected.Tuple))
		}
		for i, sub := range p.Elements {
			if err := c.checkPattern(sub, expected.Tuple[i], armScope, bindings); err != nil {
				return err
			}
		}
		return nil
	case *StructPat:
		if expected == nil || expected.Kind != TypeStruct {
			return typeErr(p.Pos, "struct pattern cannot match subject of type %s", expected)
		}
		if expected.Name != p.TypeName {
			return typeErr(p.Pos, "struct pattern type %q does not match subject type %s", p.TypeName, expected)
		}
		// Build a quick name → declared field type map for lookup, plus a set
		// of fields the pattern names so we can validate completeness.
		fieldByName := map[string]*Type{}
		for _, f := range expected.Fields {
			fieldByName[f.Name] = f.Type
		}
		named := map[string]bool{}
		for _, f := range p.Fields {
			ft, ok := fieldByName[f.Name]
			if !ok {
				return typeErr(f.Pos, "struct %q has no field %q", expected.Name, f.Name)
			}
			if named[f.Name] {
				return typeErr(f.Pos, "field %q repeated in struct pattern", f.Name)
			}
			named[f.Name] = true
			if err := c.checkPattern(f.Pattern, ft, armScope, bindings); err != nil {
				return err
			}
		}
		if !p.Rest {
			// Every declared field must appear when `..` is absent.
			for _, f := range expected.Fields {
				if !named[f.Name] {
					return typeErr(p.Pos, "struct pattern is missing field %q (use '..' to skip remaining fields)", f.Name)
				}
			}
		}
		return nil
	case *EnumPat:
		if expected == nil || expected.Kind != TypeEnum {
			return typeErr(p.Pos, "enum pattern cannot match subject of type %s", expected)
		}
		if expected.Name != p.TypeName {
			return typeErr(p.Pos, "enum pattern type %q does not match subject type %s", p.TypeName, expected)
		}
		for _, v := range expected.Variants {
			if v == p.VariantName {
				return nil
			}
		}
		return typeErr(p.Pos, "enum %q has no variant %q", expected.Name, p.VariantName)
	}
	return typeErr(pat.PatPos(), "internal: unhandled pattern %T", pat)
}

// ---------------------------------------------------------------------------
// Expression walking.
// ---------------------------------------------------------------------------

// checkExpr walks an expression with no contextual hint. Equivalent to
// checkExprHint(expr, nil); kept as a thin wrapper because the bulk of call
// sites have no hint.
func (c *checker) checkExpr(expr Expr) (*Type, error) {
	return c.checkExprHint(expr, nil)
}

// checkExprHint is the hint-aware form. The hint is consumed only by ListLit
// (empty-list inference) and StructLit (no current use, kept for symmetry);
// every other shape ignores it.
func (c *checker) checkExprHint(expr Expr, hint *Type) (*Type, error) {
	switch e := expr.(type) {
	case *IntLit:
		v, err := strconv.ParseInt(e.Text, 0, 64)
		if err != nil {
			return nil, typeErr(e.Pos, "integer literal %s overflows int", e.Text)
		}
		e.Int = v
		e.setType(tInt)
		return tInt, nil
	case *FloatLit:
		v, err := strconv.ParseFloat(e.Text, 64)
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
	case *RuneLit:
		// PLAN: codepoint < 128 is byte; otherwise rune. Reject codepoints
		// outside valid Unicode range so the codegen and interpreter never
		// see junk values.
		if e.Value < 0 || e.Value > 0x10FFFF {
			return nil, typeErr(e.Pos, "rune codepoint %d is out of valid Unicode range (0..0x10FFFF)", e.Value)
		}
		var t *Type
		if e.Value < 128 {
			t = tByte
		} else {
			t = tRune
		}
		e.setType(t)
		return t, nil
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
		t, err := c.checkExprHint(e.Inner, hint)
		if err != nil {
			return nil, err
		}
		e.setType(t)
		return t, nil
	case *RangeExpr:
		return nil, typeErr(e.Pos, "range expressions cannot be used as values at v0.1")
	case *ListLit:
		return c.checkListLit(e, hint)
	case *TupleLit:
		return c.checkTupleLit(e, hint)
	case *StructLit:
		return c.checkStructLit(e)
	case *IndexExpr:
		return c.checkIndex(e)
	case *SliceExpr:
		return c.checkSlice(e)
	case *FieldAccessExpr:
		return c.checkFieldAccess(e)
	}
	return nil, typeErr(expr.ExprPos(), "internal: unhandled expression %T", expr)
}

// checkListLit handles `[e1, e2, ...]` and the empty-list special case. With
// elements present, every element type must equal the first; the result is
// list[T]. With zero elements, the literal latches onto a list-shaped hint
// (annotated let / call argument / function return); otherwise it errors out.
func (c *checker) checkListLit(e *ListLit, hint *Type) (*Type, error) {
	if len(e.Elements) == 0 {
		// Empty list: needs context.
		if hint != nil && hint.Kind == TypeList {
			e.setType(hint)
			return hint, nil
		}
		// Mark the literal as "needs context" so the surrounding decl/return
		// path can produce a precise diagnostic. The setType is for AST
		// completeness — typeck still errors out below.
		unk := &Type{Kind: TypeUnknown}
		e.setType(unk)
		return nil, typeErr(e.Pos, "cannot infer element type of empty list literal")
	}
	// Use the hint's element type as a hint for each element so an annotated
	// `let xs: list[list[int]] = [[]]` can fill in the inner empty list.
	var elemHint *Type
	if hint != nil && hint.Kind == TypeList {
		elemHint = hint.Element
	}
	first, err := c.checkExprHint(e.Elements[0], elemHint)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(e.Elements); i++ {
		t, err := c.checkExprHint(e.Elements[i], elemHint)
		if err != nil {
			return nil, err
		}
		if !typeEq(t, first) {
			return nil, typeErr(e.Elements[i].ExprPos(), "list element %d has type %s, expected %s", i+1, t, first)
		}
	}
	t := NewListType(first)
	e.setType(t)
	return t, nil
}

// checkTupleLit handles `(e1, e2, ...)`. The hint, if a tuple of matching
// arity, is forwarded element-wise so nested empty lists can latch on.
func (c *checker) checkTupleLit(e *TupleLit, hint *Type) (*Type, error) {
	elems := make([]*Type, len(e.Elements))
	for i, el := range e.Elements {
		var sub *Type
		if hint != nil && hint.Kind == TypeTuple && len(hint.Tuple) == len(e.Elements) {
			sub = hint.Tuple[i]
		}
		t, err := c.checkExprHint(el, sub)
		if err != nil {
			return nil, err
		}
		if t == tVoid {
			return nil, typeErr(el.ExprPos(), "tuple element cannot have void type")
		}
		elems[i] = t
	}
	t := NewTupleType(elems)
	e.setType(t)
	return t, nil
}

// checkStructLit validates `Name { f1: v1, ... }`. The literal must match the
// declared field set exactly — same names, no missing, no extras. Field order
// in the literal can differ from declaration order.
func (c *checker) checkStructLit(e *StructLit) (*Type, error) {
	st, ok := c.structs[e.TypeName]
	if !ok {
		return nil, typeErr(e.Pos, "unknown struct type %q", e.TypeName)
	}
	declared := map[string]*Type{}
	for _, f := range st.Fields {
		declared[f.Name] = f.Type
	}
	provided := map[string]bool{}
	for _, init := range e.Fields {
		ft, ok := declared[init.Name]
		if !ok {
			return nil, typeErr(init.Pos, "struct %q has no field %q", e.TypeName, init.Name)
		}
		if provided[init.Name] {
			return nil, typeErr(init.Pos, "field %q already initialised in struct literal", init.Name)
		}
		provided[init.Name] = true
		vt, err := c.checkExprHint(init.Value, ft)
		if err != nil {
			return nil, err
		}
		if !typeEq(vt, ft) {
			return nil, typeErr(init.Pos, "field %q expects %s, got %s", init.Name, ft, vt)
		}
	}
	for _, f := range st.Fields {
		if !provided[f.Name] {
			return nil, typeErr(e.Pos, "struct %q literal is missing field %q", e.TypeName, f.Name)
		}
	}
	e.setType(st)
	return st, nil
}

// checkIndex handles `xs[i]`. Receiver must be list[T] (result T) or str
// (result rune); index must be int.
func (c *checker) checkIndex(e *IndexExpr) (*Type, error) {
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	it, err := c.checkExpr(e.Index)
	if err != nil {
		return nil, err
	}
	if it != tInt {
		return nil, typeErr(e.Index.ExprPos(), "index must be int, got %s", it)
	}
	switch {
	case rt != nil && rt.Kind == TypeList:
		// Note: negative indexing is OUT at v0.2 — runtime is expected to
		// reject `i < 0`. typeck does not constant-fold the index value.
		e.setType(rt.Element)
		return rt.Element, nil
	case rt == tStr:
		e.setType(tRune)
		return tRune, nil
	}
	return nil, typeErr(e.Pos, "cannot index value of type %s", rt)
}

// checkSlice handles `xs[a..b]` etc. Receiver must be list[T] (result list[T]).
// String slicing is deferred to v0.3 and rejected here per PLAN.
func (c *checker) checkSlice(e *SliceExpr) (*Type, error) {
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	check := func(b Expr, label string) error {
		if b == nil {
			return nil
		}
		t, err := c.checkExpr(b)
		if err != nil {
			return err
		}
		if t != tInt {
			return typeErr(b.ExprPos(), "slice %s must be int, got %s", label, t)
		}
		return nil
	}
	if err := check(e.Low, "low"); err != nil {
		return nil, err
	}
	if err := check(e.High, "high"); err != nil {
		return nil, err
	}
	if rt == tStr {
		return nil, typeErr(e.Pos, "string slicing is deferred to v0.3")
	}
	if rt == nil || rt.Kind != TypeList {
		return nil, typeErr(e.Pos, "cannot slice value of type %s", rt)
	}
	e.setType(rt)
	return rt, nil
}

// checkFieldAccess handles `expr.name`. PLAN-pinned dual role: when the
// receiver is a bare ident naming a known enum, the FieldAccessExpr resolves
// as a variant access; otherwise the receiver must be a struct value and the
// name must be one of its declared fields. Receiver-as-enum is recorded by
// setting the receiver IdentExpr's type to the enum type itself, which mirrors
// how `Color.Red` evaluates at run time.
func (c *checker) checkFieldAccess(e *FieldAccessExpr) (*Type, error) {
	if id, ok := e.Receiver.(*IdentExpr); ok {
		if en, isEnum := c.enums[id.Name]; isEnum {
			for _, v := range en.Variants {
				if v == e.FieldName {
					id.setType(en)
					e.setType(en)
					return en, nil
				}
			}
			return nil, typeErr(e.NamePos, "enum %q has no variant %q", id.Name, e.FieldName)
		}
	}
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	if rt == nil || rt.Kind != TypeStruct {
		return nil, typeErr(e.Pos, "cannot access field on value of type %s", rt)
	}
	for _, f := range rt.Fields {
		if f.Name == e.FieldName {
			e.setType(f.Type)
			return f.Type, nil
		}
	}
	return nil, typeErr(e.NamePos, "struct %q has no field %q", rt.Name, e.FieldName)
}

// checkBinary validates per-operator typing rules.
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
		if lt != rt || !isComparablePrimitive(lt) {
			return nil, typeErr(e.Pos, "operator %s requires same-typed primitive operands, got %s and %s", e.Op, lt, rt)
		}
		result = tBool
	case BinLT, BinGT, BinLE, BinGE:
		if lt != rt || !(isNumeric(lt) || lt == tStr || lt == tByte || lt == tRune) {
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
// declared function. The built-in `len` is special-cased: it accepts any
// list[T] argument and returns int.
func (c *checker) checkCall(e *CallExpr) (*Type, error) {
	ident, ok := e.Callee.(*IdentExpr)
	if !ok {
		return nil, typeErr(e.Pos, "callee must be a function name at v0.1")
	}
	sig, ok := c.fns[ident.Name]
	if !ok {
		if _, isVar := c.scope.lookup(ident.Name); isVar {
			return nil, typeErr(ident.Pos, "%q is not a function", ident.Name)
		}
		return nil, typeErr(ident.Pos, "undefined function %q", ident.Name)
	}
	if sig.builtin && ident.Name == "len" {
		// `len(xs)` accepts exactly one list argument and returns int. We do
		// not promote `len` to a generic in the type system; this is the only
		// generic-feeling intrinsic at v0.2 and the special-case keeps the
		// rest of the type system monomorphic.
		if len(e.Args) != 1 {
			return nil, typeErr(e.Pos, "function %q expects 1 argument, got %d", ident.Name, len(e.Args))
		}
		at, err := c.checkExpr(e.Args[0])
		if err != nil {
			return nil, err
		}
		if at == nil || at.Kind != TypeList {
			return nil, typeErr(e.Args[0].ExprPos(), "argument to len must be a list, got %s", at)
		}
		ident.setType(tInt)
		e.setType(tInt)
		return tInt, nil
	}
	if len(e.Args) != len(sig.params) {
		return nil, typeErr(e.Pos, "function %q expects %d argument(s), got %d", ident.Name, len(sig.params), len(e.Args))
	}
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, sig.params[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, sig.params[i]) {
			return nil, typeErr(a.ExprPos(), "argument %d to %q has type %s, expected %s", i+1, ident.Name, at, sig.params[i])
		}
	}
	ident.setType(sig.ret)
	e.setType(sig.ret)
	return sig.ret, nil
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// typeEq is the type-equality wrapper used everywhere. Pointer equality first,
// structural equality for composites; primitives short-circuit to pointer.
func typeEq(a, b *Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equals(b)
}

func isNumeric(t *Type) bool {
	return t == tInt || t == tFloat || t == tByte || t == tRune
}

// isComparablePrimitive reports whether == / != is admissible on t. Lists,
// tuples, structs, enums are not compared with == at v0.2 (no structural
// equality operator); primitives only.
func isComparablePrimitive(t *Type) bool {
	switch t {
	case tInt, tFloat, tBool, tStr, tByte, tRune:
		return true
	}
	return false
}

// isConstExpr reports whether expr is admissible on the right-hand side of a
// `const` declaration. Composites are not const-evaluable at v0.2.
func isConstExpr(e Expr) bool {
	switch x := e.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *RuneLit:
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
