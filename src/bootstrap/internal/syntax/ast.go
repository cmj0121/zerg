package syntax

import "fmt"

// Program is the root AST node — a flat sequence of top-level statements.
type Program struct {
	Statements []Stmt
}

// ---------------------------------------------------------------------------
// Common interfaces.
// ---------------------------------------------------------------------------

// Stmt is the marker interface for statements. Concrete switching on type is
// fine — the language is small and the alternative (a Visitor) buys nothing
// at this scale.
type Stmt interface {
	stmtNode()
	StmtPos() Position
}

// Expr is the marker interface for expressions. Same reasoning as Stmt.
//
// Type returns the type assigned by the type checker (typeck.Check). It is nil
// before Check runs and non-nil for every Expr afterwards on a successful
// pass. We expose a getter rather than a public field so the type checker can
// own writes via setType while readers (run.go, cgen.go) get a stable accessor.
type Expr interface {
	exprNode()
	ExprPos() Position
	Type() *Type
	setType(*Type)
}

// typed is embedded by every concrete Expr to share the typ field and the
// Type/setType pair. The field is unexported because only typeck (and the
// node's own setType) is allowed to write it.
type typed struct {
	typ *Type
}

// Type returns the type recorded by the type checker; nil before Check runs.
func (t *typed) Type() *Type { return t.typ }

// setType is package-private — only typeck calls it.
func (t *typed) setType(ty *Type) { t.typ = ty }

// ---------------------------------------------------------------------------
// Operator enums. Each carries a String() method tuned for diagnostic prose
// rather than source reproduction — error messages quote the operator with
// surrounding back-ticks via fmt's %q where useful.
// ---------------------------------------------------------------------------

// AssignOp identifies which kind of assignment a node represents.
type AssignOp int

// Assignment operators in the order they appear in the grammar table.
const (
	AssignSet AssignOp = iota // =
	AssignAdd                 // +=
	AssignSub                 // -=
	AssignMul                 // *=
	AssignDiv                 // /=
	AssignMod                 // %=
	AssignAnd                 // &=
	AssignOr                  // |=
	AssignXor                 // ^=
	AssignShl                 // <<=
	AssignShr                 // >>=
)

// String returns the literal source-text form of the operator.
func (op AssignOp) String() string {
	switch op {
	case AssignSet:
		return "="
	case AssignAdd:
		return "+="
	case AssignSub:
		return "-="
	case AssignMul:
		return "*="
	case AssignDiv:
		return "/="
	case AssignMod:
		return "%="
	case AssignAnd:
		return "&="
	case AssignOr:
		return "|="
	case AssignXor:
		return "^="
	case AssignShl:
		return "<<="
	case AssignShr:
		return ">>="
	default:
		return fmt.Sprintf("AssignOp(%d)", int(op))
	}
}

// BinaryOp identifies the operator of a BinaryExpr.
type BinaryOp int

// Binary operators. The order tracks the precedence table in PLAN.md from
// low to high, but BinaryOp's value carries no semantic weight — the parser
// has already encoded precedence via tree shape.
const (
	BinOr       BinaryOp = iota // or
	BinXor                      // xor (logical)
	BinAnd                      // and
	BinEq                       // ==
	BinNE                       // !=
	BinLT                       // <
	BinGT                       // >
	BinLE                       // <=
	BinGE                       // >=
	BinBitOr                    // |
	BinBitXor                   // ^
	BinBitAnd                   // &
	BinShl                      // <<
	BinShr                      // >>
	BinAdd                      // +
	BinSub                      // -
	BinMul                      // *
	BinDiv                      // /
	BinFloorDiv                 // //
	BinMod                      // %
)

// String returns the literal source-text form of the operator.
func (op BinaryOp) String() string {
	switch op {
	case BinOr:
		return "or"
	case BinXor:
		return "xor"
	case BinAnd:
		return "and"
	case BinEq:
		return "=="
	case BinNE:
		return "!="
	case BinLT:
		return "<"
	case BinGT:
		return ">"
	case BinLE:
		return "<="
	case BinGE:
		return ">="
	case BinBitOr:
		return "|"
	case BinBitXor:
		return "^"
	case BinBitAnd:
		return "&"
	case BinShl:
		return "<<"
	case BinShr:
		return ">>"
	case BinAdd:
		return "+"
	case BinSub:
		return "-"
	case BinMul:
		return "*"
	case BinDiv:
		return "/"
	case BinFloorDiv:
		return "//"
	case BinMod:
		return "%"
	default:
		return fmt.Sprintf("BinaryOp(%d)", int(op))
	}
}

// IsComparison reports whether op is one of the non-associative comparisons.
// The parser uses this to enforce non-associativity ("a < b < c" is rejected)
// without having to inline the operator list at the call site.
func (op BinaryOp) IsComparison() bool {
	switch op {
	case BinEq, BinNE, BinLT, BinGT, BinLE, BinGE:
		return true
	default:
		return false
	}
}

// UnaryOp identifies the operator of a UnaryExpr.
type UnaryOp int

// Unary operators in the order they appear in the grammar.
const (
	UnaryNeg    UnaryOp = iota // -  (numeric negation)
	UnaryNot                   // not (logical not)
	UnaryBitNot                // ~  (bitwise not)
)

// String returns the literal source-text form of the operator.
func (op UnaryOp) String() string {
	switch op {
	case UnaryNeg:
		return "-"
	case UnaryNot:
		return "not"
	case UnaryBitNot:
		return "~"
	default:
		return fmt.Sprintf("UnaryOp(%d)", int(op))
	}
}

// ---------------------------------------------------------------------------
// Type references.
//
// At v0.1 the only valid type names are int, float, bool, str. The parser is
// liberal — any identifier in type position parses fine — and the type
// checker rejects unknowns. That keeps grammatical errors and semantic
// errors clearly separated.
// ---------------------------------------------------------------------------

// TypeRef is an explicit type annotation. v0.1 has no compound type syntax
// (no `[]int`, no generics, no function types as values), so a type ref is
// nothing but a name plus a position for diagnostics.
//
// Resolved is filled in by the type checker to point at the canonical *Type
// singleton for Name. It is nil before Check runs.
type TypeRef struct {
	Name     string
	Pos      Position
	Resolved *Type
}

// ---------------------------------------------------------------------------
// Statements.
// ---------------------------------------------------------------------------

// Block is a `{ ... }` brace-delimited sequence of statements. v0.1 admits
// blocks only as bodies of `if`, `elif`, `else`, `for`, and `fn`. The lone
// `{}` form at statement position is a parse error — pleasantly.
type Block struct {
	Pos        Position
	Statements []Stmt
}

// LetStmt represents `let name [: T] = expr` / `let name := expr`. Type is
// nil when the user wrote the walrus form and inference must do the work.
type LetStmt struct {
	Pos   Position
	Name  string
	Type  *TypeRef // nil ⇒ inferred from Value
	Value Expr
}

func (*LetStmt) stmtNode()           {}
func (s *LetStmt) StmtPos() Position { return s.Pos }

// MutStmt is `mut name [: T] = expr` / `mut name := expr`. Same shape as
// LetStmt; the distinction is whether the binding is later assignable.
type MutStmt struct {
	Pos   Position
	Name  string
	Type  *TypeRef
	Value Expr
}

func (*MutStmt) stmtNode()           {}
func (s *MutStmt) StmtPos() Position { return s.Pos }

// ConstStmt is `const name [: T] = expr` / `const name := expr`. The parser
// accepts any expression on the right-hand side; the type checker is
// responsible for asserting that it's a constant expression.
type ConstStmt struct {
	Pos   Position
	Name  string
	Type  *TypeRef
	Value Expr
}

func (*ConstStmt) stmtNode()           {}
func (s *ConstStmt) StmtPos() Position { return s.Pos }

// AssignStmt is `target OP value`. v0.1 restricts targets to bare identifiers
// (no field access, no index), so Target is *IdentExpr — keeping the type
// concrete here means callers don't have to type-assert.
type AssignStmt struct {
	Pos    Position
	Target *IdentExpr
	Op     AssignOp
	Value  Expr
}

func (*AssignStmt) stmtNode()           {}
func (s *AssignStmt) StmtPos() Position { return s.Pos }

// ExprStmt wraps an expression used for its side effects. v0.1 only admits
// function calls in expression-statement position, but the parser holds the
// general Expr and validates the kind, so we get one good error message
// instead of a generic "expected statement".
type ExprStmt struct {
	Pos  Position
	Expr Expr
}

func (*ExprStmt) stmtNode()           {}
func (s *ExprStmt) StmtPos() Position { return s.Pos }

// PrintStmt represents `print expr`. v0.0 stored the string directly; v0.1
// generalises to any expression. The interpreter and codegen at v0.0 handle
// only the StringLit case for back-compat — see run.go and cgen.go.
type PrintStmt struct {
	Pos  Position
	Expr Expr
}

func (*PrintStmt) stmtNode()           {}
func (s *PrintStmt) StmtPos() Position { return s.Pos }

// ReturnStmt represents `return [expr] [if cond]`. Either field is nilable:
//   - bare `return` ⇒ Value == nil, Guard == nil
//   - `return expr` ⇒ Value != nil, Guard == nil
//   - `return if cond` ⇒ Value == nil, Guard != nil
//   - `return expr if cond` ⇒ both set
type ReturnStmt struct {
	Pos   Position
	Value Expr // nil for a bare `return`
	Guard Expr // nil for unconditional return
}

func (*ReturnStmt) stmtNode()           {}
func (s *ReturnStmt) StmtPos() Position { return s.Pos }

// BreakStmt represents `break [if cond]`.
type BreakStmt struct {
	Pos   Position
	Guard Expr // nil for unconditional break
}

func (*BreakStmt) stmtNode()           {}
func (s *BreakStmt) StmtPos() Position { return s.Pos }

// ContinueStmt represents `continue [if cond]`.
type ContinueStmt struct {
	Pos   Position
	Guard Expr // nil for unconditional continue
}

func (*ContinueStmt) stmtNode()           {}
func (s *ContinueStmt) StmtPos() Position { return s.Pos }

// FnParam is one positional parameter of a function declaration. v0.1
// requires every parameter to be type-annotated; no defaults, no names-only.
type FnParam struct {
	Name string
	Type *TypeRef
	Pos  Position
}

// FnDecl represents `fn name(p: T, ...) -> R { ... }`. The return type is
// optional — a nil Return means the function returns no value.
type FnDecl struct {
	Pos    Position
	Name   string
	Params []FnParam
	Return *TypeRef // nil ⇒ no return value
	Body   *Block
}

func (*FnDecl) stmtNode()           {}
func (s *FnDecl) StmtPos() Position { return s.Pos }

// ElifClause is one `elif cond { ... }` arm of an if-chain.
type ElifClause struct {
	Pos  Position
	Cond Expr
	Body *Block
}

// IfStmt represents `if cond {} [elif cond {}]* [else {}]`. Else is nil when
// the source omits it.
type IfStmt struct {
	Pos   Position
	Cond  Expr
	Then  *Block
	Elifs []ElifClause
	Else  *Block // nil ⇒ no else
}

func (*IfStmt) stmtNode()           {}
func (s *IfStmt) StmtPos() Position { return s.Pos }

// ForKind selects which of the three for-loop shapes a ForStmt represents.
type ForKind int

// For-loop shapes.
const (
	ForInfinite ForKind = iota // for { ... }
	ForCond                    // for cond { ... }
	ForRange                   // for x in start..end { ... }
)

// ForStmt covers all three for-loop shapes via Kind. Only the fields relevant
// to Kind are populated; the rest are zero values.
type ForStmt struct {
	Pos   Position
	Kind  ForKind
	Cond  Expr       // ForCond
	Var   string     // ForRange — the bound variable name
	VarPos Position  // ForRange — position of the variable name
	Range *RangeExpr // ForRange — the iteration range
	Body  *Block
}

func (*ForStmt) stmtNode()           {}
func (s *ForStmt) StmtPos() Position { return s.Pos }

// NopStmt represents the literal `nop` keyword. Carries over from v0.0 with
// no shape change.
type NopStmt struct {
	Pos Position
}

func (*NopStmt) stmtNode()           {}
func (s *NopStmt) StmtPos() Position { return s.Pos }

// ---------------------------------------------------------------------------
// Expressions.
//
// Numeric literals are stored as the lexer's already-cleaned text, NOT as
// parsed numeric values. Reasoning:
//   - The lexer has already done the only non-trivial textual work (`_`
//     separator stripping, prefix preservation).
//   - strconv.ParseInt / ParseFloat are cheap; running them at parse time
//     buys us nothing while costing us a clean parser/typeck split.
//   - Constant folding belongs in the type checker, where the diagnostic
//     position can be tied to the same code that decides whether the literal
//     fits its declared / inferred type.
//
// IntLit.Text is the prefix-preserved form ("0xff", "255", "0b10").
// FloatLit.Text is the canonical "<int>.<frac>" form. Both have `_` already
// stripped by the lexer.
// ---------------------------------------------------------------------------

// IntLit is an integer literal. Text is the prefix-preserved form ready for
// strconv.ParseInt with base 0.
//
// Int is the parsed numeric value, populated by the type checker. It is the
// authoritative value for run.go and cgen.go — they MUST NOT re-parse Text,
// because typeck has already validated that the literal fits int64.
type IntLit struct {
	typed
	Pos  Position
	Text string
	Int  int64
}

func (*IntLit) exprNode()           {}
func (e *IntLit) ExprPos() Position { return e.Pos }

// FloatLit is a floating-point literal. Text is the "<int>.<frac>" form.
//
// Float is the parsed numeric value, populated by the type checker. typeck
// rejects literals that overflow to ±Inf at v0.1, so Float is always finite.
type FloatLit struct {
	typed
	Pos   Position
	Text  string
	Float float64
}

func (*FloatLit) exprNode()           {}
func (e *FloatLit) ExprPos() Position { return e.Pos }

// StringLit is a double-quoted string literal whose contents have already been
// unescaped by the lexer.
type StringLit struct {
	typed
	Pos   Position
	Value string
}

func (*StringLit) exprNode()           {}
func (e *StringLit) ExprPos() Position { return e.Pos }

// BoolLit is `true` or `false`.
type BoolLit struct {
	typed
	Pos   Position
	Value bool
}

func (*BoolLit) exprNode()           {}
func (e *BoolLit) ExprPos() Position { return e.Pos }

// IdentExpr is a bare identifier reference.
type IdentExpr struct {
	typed
	Pos  Position
	Name string
}

func (*IdentExpr) exprNode()           {}
func (e *IdentExpr) ExprPos() Position { return e.Pos }

// BinaryExpr is any infix operator application.
type BinaryExpr struct {
	typed
	Pos   Position
	Op    BinaryOp
	Left  Expr
	Right Expr
}

func (*BinaryExpr) exprNode()           {}
func (e *BinaryExpr) ExprPos() Position { return e.Pos }

// UnaryExpr is a prefix operator application: -, not, ~.
type UnaryExpr struct {
	typed
	Pos     Position
	Op      UnaryOp
	Operand Expr
}

func (*UnaryExpr) exprNode()           {}
func (e *UnaryExpr) ExprPos() Position { return e.Pos }

// CallExpr is `callee(args)`. v0.1 only admits an IdentExpr as Callee but the
// parser is general so future call-on-expression doesn't require a re-shape.
type CallExpr struct {
	typed
	Pos    Position
	Callee Expr
	Args   []Expr
}

func (*CallExpr) exprNode()           {}
func (e *CallExpr) ExprPos() Position { return e.Pos }

// RangeExpr is `start..end` or `start..=end`. v0.1 restricts ranges to the
// head of `for x in ...` — the parser only constructs a RangeExpr there and
// produces a clear error elsewhere. RangeExpr satisfies Expr but its Type()
// is left nil at v0.1: range values cannot be stored, so no consumer needs
// a meaningful type for them.
type RangeExpr struct {
	typed
	Pos       Position
	Start     Expr
	End       Expr
	Inclusive bool
}

func (*RangeExpr) exprNode()           {}
func (e *RangeExpr) ExprPos() Position { return e.Pos }

// ParenExpr is `(inner)`. We keep parentheses in the tree (rather than
// folding them away) because faithful diagnostic positions and any future
// pretty-printer benefit from knowing the user wrote them.
type ParenExpr struct {
	typed
	Pos   Position
	Inner Expr
}

func (*ParenExpr) exprNode()           {}
func (e *ParenExpr) ExprPos() Position { return e.Pos }
