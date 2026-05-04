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

// TypeRefKind tags which shape a TypeRef carries: a bare named type, a
// `list[T]` constructor, or a `tuple[T1, T2, ...]` constructor. Adding new
// kinds appends.
type TypeRefKind int

// TypeRef shape kinds.
const (
	TypeRefNamed TypeRefKind = iota
	TypeRefList
	TypeRefTuple
)

// TypeRef is an explicit type annotation. v0.2 admits compound type syntax
// for list and tuple; the parser fills in the relevant fields by Kind.
//
//   - TypeRefNamed: Name is the identifier ("int", "Point", "Color"). Element
//     and Elements are nil/empty.
//   - TypeRefList: Element holds the inner element TypeRef. Name is empty;
//     printers reconstruct "list[T]" from Element.
//   - TypeRefTuple: Elements is the per-position TypeRef slice (≥ 2 entries).
//     Name is empty; printers reconstruct "tuple[T1, T2]".
//
// Resolved is filled in by the type checker to point at the canonical *Type
// singleton for the resolved shape. It is nil before Check runs.
type TypeRef struct {
	Pos      Position
	Kind     TypeRefKind
	Name     string     // TypeRefNamed
	Element  *TypeRef   // TypeRefList
	Elements []*TypeRef // TypeRefTuple
	Resolved *Type
}

// String renders the type reference in source-text form. Used by error
// messages and the typed-AST debug dumps.
func (r *TypeRef) String() string {
	if r == nil {
		return "<nil>"
	}
	switch r.Kind {
	case TypeRefNamed:
		return r.Name
	case TypeRefList:
		return "list[" + r.Element.String() + "]"
	case TypeRefTuple:
		var b []byte
		b = append(b, "tuple["...)
		for i, e := range r.Elements {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = append(b, e.String()...)
		}
		b = append(b, ']')
		return string(b)
	}
	return fmt.Sprintf("TypeRef(%d)", int(r.Kind))
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

// TupleBinding is the parenthesised LHS of a let/mut/const tuple destructure
// declaration: `let (a, b) := pair` / `mut (x, y, z) := triple`. v0.2 admits
// only flat name lists (≥ 2 names); annotated tuple destructure (`let (a, b):
// tuple[int, int] = ...`) is deferred — the RHS drives inference. Each name
// becomes a fresh binding in the surrounding scope; typeck rejects repeated
// names at the point of declare().
//
// LetStmt/MutStmt/ConstStmt embed an optional *TupleBinding (Tuple). When
// Tuple is non-nil, Name is empty and Type is nil — the parser enforces both.
// When Tuple is nil the declaration is the v0.1 single-name form and Name
// carries the bound identifier.
type TupleBinding struct {
	Pos       Position
	Names     []string   // ≥ 2 names; parser rejects shorter lists
	NamePos   []Position // 1:1 with Names; used for precise diagnostics
}

// LetStmt represents `let name [: T] = expr` / `let name := expr`. Type is
// nil when the user wrote the walrus form and inference must do the work.
//
// v0.2 also admits the tuple-destructure form `let (a, b) := expr`. When the
// LHS is parenthesised the parser populates Tuple instead of Name; Type is
// not allowed on the destructure form (typeck infers from the RHS).
type LetStmt struct {
	Pos   Position
	Name  string        // empty when Tuple != nil
	Tuple *TupleBinding // nil for the single-name form
	Type  *TypeRef      // nil ⇒ inferred from Value
	Value Expr
}

func (*LetStmt) stmtNode()           {}
func (s *LetStmt) StmtPos() Position { return s.Pos }

// MutStmt is `mut name [: T] = expr` / `mut name := expr`. Same shape as
// LetStmt; the distinction is whether the binding is later assignable. The
// tuple-destructure form `mut (a, b) := expr` is admitted on the same terms
// as LetStmt.
type MutStmt struct {
	Pos   Position
	Name  string
	Tuple *TupleBinding
	Type  *TypeRef
	Value Expr
}

func (*MutStmt) stmtNode()           {}
func (s *MutStmt) StmtPos() Position { return s.Pos }

// ConstStmt is `const name [: T] = expr` / `const name := expr`. The parser
// accepts any expression on the right-hand side; the type checker is
// responsible for asserting that it's a constant expression. Tuple
// destructure on a const is admitted by the parser; typeck rejects it
// because v0.2 has no const-evaluable composite expressions.
type ConstStmt struct {
	Pos   Position
	Name  string
	Tuple *TupleBinding
	Type  *TypeRef
	Value Expr
}

func (*ConstStmt) stmtNode()           {}
func (s *ConstStmt) StmtPos() Position { return s.Pos }

// AssignStmt is `target OP value`. v0.1 admitted only bare identifiers as the
// target. v0.3 broadens Target to Expr so list-element assignment
// (`xs[i] = v`) can share the same node. The parser still narrows the LHS at
// parse time: only *IdentExpr and *IndexExpr are accepted, every other shape
// (call results, chained indexing, slices, field access at v0.3) is rejected
// with a precise diagnostic. Compound operators (`+=`, etc.) remain
// identifier-only at v0.3 — list-element compound assignment is sugar that
// belongs to a later unit.
type AssignStmt struct {
	Pos    Position
	Target Expr
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

// ForKind selects which of the four for-loop shapes a ForStmt represents.
type ForKind int

// For-loop shapes.
const (
	ForInfinite ForKind = iota // for { ... }
	ForCond                    // for cond { ... }
	ForRange                   // for x in start..end { ... }
	ForIter                    // for x in xs { ... } — iterate over a list value
)

// ForStmt covers all four for-loop shapes via Kind. Only the fields relevant
// to Kind are populated; the rest are zero values.
//
// ForRange uses Var/VarPos plus Range; ForIter uses Var/VarPos plus Iter.
// The two list-iteration shapes share the loop variable + body machinery and
// diverge only in how the per-iteration value is produced.
type ForStmt struct {
	Pos    Position
	Kind   ForKind
	Cond   Expr       // ForCond
	Var    string     // ForRange / ForIter — the bound variable name
	VarPos Position   // ForRange / ForIter — position of the variable name
	Range  *RangeExpr // ForRange — the iteration range
	Iter   Expr       // ForIter — the list-typed expression to iterate
	Body   *Block
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

// ---------------------------------------------------------------------------
// v0.2 composite-data declarations.
//
// Top-level only; collected by typeck's first pass so forward references work.
// ---------------------------------------------------------------------------

// FieldDecl is one declared field on a struct. Field order is significant —
// PLAN.md pins struct print order to declaration order, so we keep an ordered
// slice rather than a map.
type FieldDecl struct {
	Name string
	Type *TypeRef
	Pos  Position
}

// StructDecl represents `struct Name { f1: T1, f2: T2, ... }`. Empty field
// lists are accepted at parse time; typeck rejects them per the PLAN.
type StructDecl struct {
	Pos    Position
	Name   string
	Fields []FieldDecl
}

func (*StructDecl) stmtNode()           {}
func (s *StructDecl) StmtPos() Position { return s.Pos }

// VariantDecl is one declared variant of an enum.
//
// v0.2 introduced bare-name variants; v0.4 (Unit 2) adds optional payload
// types: `Ident(str)`, `Number(int, int)`. Payload is the per-position type
// list — zero-length for the bare form (`Eof`). The parser rejects an empty
// `()` after the variant name; payloadful variants must declare at least one
// type. Resolved types are filled in by typeck (Unit 3); the parser only
// records the TypeRef shapes.
type VariantDecl struct {
	Name    string
	Pos     Position
	Payload []*TypeRef // nil/empty for bare variants
}

// EnumDecl represents `enum Name { V1, V2, ... }`.
type EnumDecl struct {
	Pos      Position
	Name     string
	Variants []VariantDecl
}

func (*EnumDecl) stmtNode()           {}
func (s *EnumDecl) StmtPos() Position { return s.Pos }

// ---------------------------------------------------------------------------
// match statement.
// ---------------------------------------------------------------------------

// MatchArm is one arm of a `match`: a pattern, an optional guard, and a body
// block. Single-statement arm bodies are wrapped in a one-element Block at
// parse time so downstream consumers always walk a Block.
type MatchArm struct {
	Pos     Position
	Pattern Pattern
	Guard   Expr // nil when the arm has no `if guard`
	Body    *Block
}

// MatchStmt is `match expr { arm1; arm2; ... }`. The arms are tested
// top-to-bottom by both the interpreter and codegen.
type MatchStmt struct {
	Pos     Position
	Subject Expr
	Arms    []MatchArm
}

func (*MatchStmt) stmtNode()           {}
func (s *MatchStmt) StmtPos() Position { return s.Pos }

// ---------------------------------------------------------------------------
// v0.2 expression atoms.
// ---------------------------------------------------------------------------

// RuneLit is a `'X'` literal. Value is the Unicode code-point parsed from the
// lexer's decimal-string Token.Value. typeck classifies the literal as
// `byte` (codepoint < 128) or `rune` based on Value.
type RuneLit struct {
	typed
	Pos   Position
	Value int64
}

func (*RuneLit) exprNode()           {}
func (e *RuneLit) ExprPos() Position { return e.Pos }

// ListLit is `[e1, e2, ...]`. Empty lists are admitted by the parser but
// typeck rejects them outside annotated contexts where the element type can
// be inferred.
type ListLit struct {
	typed
	Pos      Position
	Elements []Expr
}

func (*ListLit) exprNode()           {}
func (e *ListLit) ExprPos() Position { return e.Pos }

// TupleLit is `(e1, e2, ...)` with at least 2 elements. The parser emits a
// ParenExpr (not a 1-tuple) for `(e)` — PLAN.md pins tuples as ≥ 2 elements.
type TupleLit struct {
	typed
	Pos      Position
	Elements []Expr
}

func (*TupleLit) exprNode()           {}
func (e *TupleLit) ExprPos() Position { return e.Pos }

// FieldInit is one field initialiser inside a struct literal. v0.2 requires
// every declared field to be initialised; the parser is liberal and typeck
// enforces the completeness rule.
type FieldInit struct {
	Name  string
	Value Expr
	Pos   Position
}

// StructLit is `Name { f1: v1, f2: v2 }`. The parser disambiguates this from
// a brace-block by peeking after the `{` for an `IDENT :` shape.
type StructLit struct {
	typed
	Pos      Position
	TypeName string
	Fields   []FieldInit
}

func (*StructLit) exprNode()           {}
func (e *StructLit) ExprPos() Position { return e.Pos }

// IndexExpr is `receiver[index]`. Slice forms (with `..`/`..=` inside the
// brackets) parse as SliceExpr instead.
type IndexExpr struct {
	typed
	Pos      Position
	Receiver Expr
	Index    Expr
}

func (*IndexExpr) exprNode()           {}
func (e *IndexExpr) ExprPos() Position { return e.Pos }

// SliceExpr is `receiver[low..high]` / `[low..=high]` and the half-open
// variants. Low and High are nilable for `[..b]`, `[a..]`, and `[..]`.
// Inclusive is true for `..=`.
type SliceExpr struct {
	typed
	Pos       Position
	Receiver  Expr
	Low       Expr // nil when omitted
	High      Expr // nil when omitted
	Inclusive bool
}

func (*SliceExpr) exprNode()           {}
func (e *SliceExpr) ExprPos() Position { return e.Pos }

// FieldAccessExpr is `receiver.fieldName`. At parse time the receiver may be
// any expression — typeck disambiguates between struct field access (when
// the receiver is a value) and enum variant access (when the receiver is a
// bare IdentExpr that resolves to an enum type).
//
// Lowered is set by typeck when the access shape is recognised as a bare-
// variant enum construction (`Token.Eof`). Downstream consumers can use the
// pointer to dispatch to enum-lit handling.
type FieldAccessExpr struct {
	typed
	Pos       Position
	Receiver  Expr
	FieldName string
	NamePos   Position
	Lowered   *EnumLit
}

func (*FieldAccessExpr) exprNode()           {}
func (e *FieldAccessExpr) ExprPos() Position { return e.Pos }

// ---------------------------------------------------------------------------
// Patterns (match arm heads).
//
// A separate marker interface keeps patterns out of Expr/Stmt — they are not
// values and they are not statements. The arm parser validates that what it
// reads is a Pattern and only a Pattern.
// ---------------------------------------------------------------------------

// Pattern is the marker interface for match patterns. Concrete switching is
// fine — the language is small and the alternative (a Visitor) buys nothing
// at this scale.
type Pattern interface {
	patternNode()
	PatPos() Position
}

// LitPat matches a literal value. Lit is one of *IntLit, *FloatLit, *BoolLit,
// *StringLit, *RuneLit, optionally wrapped by a UnaryExpr{Op: UnaryNeg} for
// numeric negation.
type LitPat struct {
	Pos Position
	Lit Expr
}

func (*LitPat) patternNode()         {}
func (p *LitPat) PatPos() Position { return p.Pos }

// WildcardPat is `_`. Always matches; binds nothing.
type WildcardPat struct {
	Pos Position
}

func (*WildcardPat) patternNode()         {}
func (p *WildcardPat) PatPos() Position { return p.Pos }

// BindPat is a bare identifier. Always matches and binds the name.
type BindPat struct {
	Pos  Position
	Name string
}

func (*BindPat) patternNode()         {}
func (p *BindPat) PatPos() Position { return p.Pos }

// TuplePat is `(p1, p2, ...)` with ≥ 2 elements. PLAN-pinned: `(_)` is
// grouping, not a 1-tuple pattern; the parser rejects 1-element parens here
// the same way TupleLit rejects 1-tuples in expression position.
type TuplePat struct {
	Pos      Position
	Elements []Pattern
}

func (*TuplePat) patternNode()         {}
func (p *TuplePat) PatPos() Position { return p.Pos }

// StructPatField is one field of a struct pattern. Pattern is the per-field
// sub-pattern; the parser desugars shorthand `Point { x }` into
// `Point { x: x }` (i.e. a BindPat with the same name).
type StructPatField struct {
	Name    string
	Pattern Pattern
	Pos     Position
}

// StructPat is `Name { f1: pat1, f2 }` with optional `..` rest at the end.
type StructPat struct {
	Pos      Position
	TypeName string
	Fields   []StructPatField
	Rest     bool
}

func (*StructPat) patternNode()         {}
func (p *StructPat) PatPos() Position { return p.Pos }

// EnumPat is `EnumName.VariantName` with an optional payload destructure.
//
// v0.2 admitted bare-variant patterns only; v0.4 (Unit 2) adds payload
// patterns: `Token.Ident(name)`, `Token.Number(0, _)`. Payload is the
// per-position sub-pattern slice — zero-length for the bare form. The parser
// rejects an empty `()` after the variant name (matching the variant-decl
// rule that bare variants do not carry parentheses). Typeck (Unit 3) is
// responsible for arity and per-position type checking.
type EnumPat struct {
	Pos         Position
	TypeName    string
	VariantName string
	Payload     []Pattern // nil/empty for bare variants
}

func (*EnumPat) patternNode()       {}
func (p *EnumPat) PatPos() Position { return p.Pos }

// ---------------------------------------------------------------------------
// v0.4 polymorphism: spec / impl declarations + method-call expressions.
//
// At parse time we collect spec and impl bodies as flat AST nodes; typeck
// (Unit 3) walks them to build the spec/impl tables, validates collisions,
// and routes method calls. The parser stays liberal — any structural error
// surfaces at typeck with a focused diagnostic.
// ---------------------------------------------------------------------------

// SpecMethod is one method declared inside a spec body. Body == nil means
// signature-only (every implementing type MUST provide an override). Body
// non-nil is a default implementation that an impl may inherit or override.
//
// Reusing FnParam keeps the parameter-shape parser shared with FnDecl.
type SpecMethod struct {
	Pos    Position
	Name   string
	Params []FnParam
	Return *TypeRef // nil ⇒ no return value
	Body   *Block   // nil ⇒ signature only
}

// SpecDecl represents `spec Name { method_decl* }`. Methods may be
// signature-only (no body) or default implementations (body present); the
// parser admits both shapes and the v0.4 typeck pass distinguishes them.
type SpecDecl struct {
	Pos     Position
	Name    string
	Methods []*SpecMethod
}

func (*SpecDecl) stmtNode()           {}
func (s *SpecDecl) StmtPos() Position { return s.Pos }

// ImplDecl represents both inherent and for-spec impl blocks:
//
//   - `impl Counter { fn double() -> int { ... } }` (inherent)
//   - `impl Counter for Printable { fn to_string() -> str { ... } }` (for-spec)
//
// Spec is the empty string for the inherent form; for the for-spec form it is
// the spec name. Methods reuse FnDecl unchanged — the only routing difference
// (an implicit `this` receiver) is handled at typeck and codegen.
type ImplDecl struct {
	Pos     Position
	Type    string // the type name being implemented
	Spec    string // empty when inherent; otherwise the spec name
	Methods []*FnDecl
}

func (*ImplDecl) stmtNode()           {}
func (s *ImplDecl) StmtPos() Position { return s.Pos }

// MethodCallExpr is `receiver.method(args)`. The parser produces this when an
// `expr DOT IDENT` is followed by an open-paren; the no-paren form continues
// to parse as FieldAccessExpr. Receiver, Method, and Args are walked by
// typeck (Unit 3) for resolution; until then the node simply records the
// shape that was parsed.
//
// Lowered is set by typeck when the call shape is recognised as an enum-lit
// construction (`Token.Ident("hello")`). Downstream consumers (run, build)
// can short-circuit to the EnumLit form via this pointer; the surface
// MethodCallExpr remains in the tree for diagnostic positioning.
type MethodCallExpr struct {
	typed
	Pos       Position
	Receiver  Expr
	Method    string
	MethodPos Position
	Args      []Expr
	Lowered   *EnumLit
}

func (*MethodCallExpr) exprNode()           {}
func (e *MethodCallExpr) ExprPos() Position { return e.Pos }

// ThisExpr is the literal `this` keyword used inside method bodies. Typeck
// (Unit 3) rejects occurrences outside method bodies; the parser accepts it
// in any expression position so the diagnostic comes with full type context.
type ThisExpr struct {
	typed
	Pos Position
}

func (*ThisExpr) exprNode()           {}
func (e *ThisExpr) ExprPos() Position { return e.Pos }

// EnumLit is a typed enum-variant construction expression: `Token.Eof`,
// `Token.Ident("foo")`, `Token.Number(10, 16)`.
//
// The parser does NOT produce EnumLit directly. v0.2 reads `Type.Variant`
// (no parens) as a FieldAccessExpr; v0.4 Unit 1 reads `Type.Variant(...)`
// (with parens) as a MethodCallExpr. Typeck (Unit 3) walks both shapes and
// lowers them to EnumLit when the receiver is recognised as a known enum
// type. Until Unit 3 lights up the lowering, this node is unused — declared
// here so the AST surface is stable across Units 2 and 3.
//
// Payload carries the per-position argument slice; zero-length for bare
// variants (the FieldAccessExpr-derived shape).
type EnumLit struct {
	typed
	Pos         Position
	EnumName    string
	Variant     string
	VariantPos  Position
	Payload     []Expr
}

func (*EnumLit) exprNode()           {}
func (e *EnumLit) ExprPos() Position { return e.Pos }
