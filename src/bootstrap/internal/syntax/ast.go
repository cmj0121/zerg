package syntax

import "fmt"

// Program is the root AST node — a flat sequence of top-level statements.
//
// MonoFns (v0.6 Unit 7) holds specialised FnDecl clones produced by
// monomorphisation. Each entry is the result of one (generic-fn, type-args)
// instantiation whose generic decl lives in this Program; downstream consumers
// (codegen) emit one C function per entry. The original generic FnDecl stays
// in Statements but is skipped by emit because its body type-refs are
// unsubstituted.
//
// HeadComments (v0.10 Unit 1) holds file-head `#` lines — every leading
// comment on the very top of the source above the first decl. Includes the
// `# requires:` line, license headers, attribution, and shebangs. Empty for
// programs that start with a decl on line 1. The slice carries the raw
// comment body (the `#` is stripped); fmt emits these verbatim above the
// first decl. Stored on Program rather than on the first decl so a file
// that opens with `# requires: ...` plus a header still round-trips
// cleanly even if the user edits the first decl out of the file.
//
// Comments (v0.10 Unit 1) is the full per-source side-channel of comments
// captured by LexWithComments. Empty for programs parsed via the comment-
// stripping path (Lex / Parse). The formatter uses this to emit any inline
// trailing comments that the parser couldn't attach to a node — at v0.10
// inline trailing comments are stripped (documented limitation), so the
// slice is unused outside the test corpus.
type Program struct {
	Statements []Stmt
	MonoFns    []*FnDecl
	// MonoImpls (v0.6 Unit 7) holds synthetic ImplDecl instances produced by
	// generic-impl monomorphisation. Each entry pairs one generic ImplDecl
	// with one concrete receiver instantiation; methods are deep-cloned so
	// downstream consumers (codegen) emit one C function per
	// (impl-method, mono-receiver) tuple. The original generic ImplDecl
	// stays in Statements but is skipped by emit because its receiver type
	// is unsubstituted.
	MonoImpls    []*ImplDecl
	HeadComments []string
	Comments     []CommentToken
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
//
// TypeArgs (v0.6) carries generic type arguments at the use site, e.g. the
// `int` slot in `Box[int]` or the `int, str` slots in `Result[int, str]`. It
// is meaningful for TypeRefNamed only; TypeRefList / TypeRefTuple have their
// own dedicated Element / Elements slots and ignore this field. Nil/empty for
// every non-generic use site — every v0.0–v0.5 program carries this at its
// zero value.
//
// Nullable (v0.6) records a postfix `?` on the type — `int?`, `Option[T]?`,
// `list[int]?`. v0.6 Unit 2 desugars `T?` to `Option[T]` at the type-resolve
// step; the parser only records the bit. Double-nullable (`T??`) is rejected
// at parse time per PLAN.md.
type TypeRef struct {
	Pos      Position
	Kind     TypeRefKind
	Name     string // TypeRefNamed
	// Module, when non-empty, is the local binding name of an imported
	// module that qualifies a TypeRefNamed (`mod.Color`). v0.5 Unit 3
	// resolves this against the importing module's import-binding table to
	// route the type lookup into the foreign module's decl tables. Empty
	// for in-module type references — every v0.0–v0.4 corpus carries this
	// field at its zero value.
	Module   string
	Element  *TypeRef   // TypeRefList
	Elements []*TypeRef // TypeRefTuple
	TypeArgs []*TypeRef // v0.6 generic type-args at use site (TypeRefNamed only)
	Nullable bool       // v0.6 postfix `?` — desugars to Option[T] at typeck
	Resolved *Type
}

// String renders the type reference in source-text form. Used by error
// messages and the typed-AST debug dumps.
func (r *TypeRef) String() string {
	if r == nil {
		return "<nil>"
	}
	var s string
	switch r.Kind {
	case TypeRefNamed:
		s = r.Name
		if len(r.TypeArgs) > 0 {
			var b []byte
			b = append(b, s...)
			b = append(b, '[')
			for i, a := range r.TypeArgs {
				if i > 0 {
					b = append(b, ", "...)
				}
				b = append(b, a.String()...)
			}
			b = append(b, ']')
			s = string(b)
		}
	case TypeRefList:
		s = "list[" + r.Element.String() + "]"
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
		s = string(b)
	default:
		return fmt.Sprintf("TypeRef(%d)", int(r.Kind))
	}
	if r.Nullable {
		s += "?"
	}
	return s
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

// TupleBinding is the parenthesised LHS of an immutable/mut/const tuple
// destructure declaration: `(a, b) := pair` / `mut (x, y, z) := triple`. v0.2
// admits only flat name lists (≥ 2 names); annotated tuple destructure
// (`(a, b): tuple[int, int] = ...`) is deferred — the RHS drives inference.
// Each name becomes a fresh binding in the surrounding scope; typeck rejects
// repeated names at the point of declare().
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

// LetStmt represents the immutable-binding AST node: `name [: T] = expr` /
// `name := expr`. Type is nil when the user wrote the walrus form and
// inference must do the work. (The Go type name LetStmt is retained from the
// pre-v0.11 era when the source carried a `let` keyword; v0.11 retired the
// keyword from the parser surface but kept the AST node name unchanged.)
//
// v0.2 also admits the tuple-destructure form `(a, b) := expr`. When the
// LHS is parenthesised the parser populates Tuple instead of Name; Type is
// not allowed on the destructure form (typeck infers from the RHS).
//
// LeadingComments (v0.10 Unit 1) is the slice of `#` comment bodies that
// preceded this statement on its own lines. nil/empty for programs parsed
// without comment threading (Parse / ParseWithOptions); fmt uses the slice
// to emit comments verbatim above the stmt.
type LetStmt struct {
	Pos             Position
	Name            string        // empty when Tuple != nil
	Tuple           *TupleBinding // nil for the single-name form
	Type            *TypeRef      // nil ⇒ inferred from Value
	Value           Expr
	LeadingComments []string
}

func (*LetStmt) stmtNode()           {}
func (s *LetStmt) StmtPos() Position { return s.Pos }

// MutStmt is `mut name [: T] = expr` / `mut name := expr`. Same shape as
// LetStmt; the distinction is whether the binding is later assignable. The
// tuple-destructure form `mut (a, b) := expr` is admitted on the same terms
// as LetStmt.
type MutStmt struct {
	Pos             Position
	Name            string
	Tuple           *TupleBinding
	Type            *TypeRef
	Value           Expr
	LeadingComments []string
}

func (*MutStmt) stmtNode()           {}
func (s *MutStmt) StmtPos() Position { return s.Pos }

// ConstStmt is `const name [: T] = expr` / `const name := expr`. The parser
// accepts any expression on the right-hand side; the type checker is
// responsible for asserting that it's a constant expression. Tuple
// destructure on a const is admitted by the parser; typeck rejects it
// because v0.2 has no const-evaluable composite expressions.
type ConstStmt struct {
	Pos             Position
	Name            string
	Tuple           *TupleBinding
	Type            *TypeRef
	Value           Expr
	LeadingComments []string
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
	Pos             Position
	Target          Expr
	Op              AssignOp
	Value           Expr
	LeadingComments []string
}

func (*AssignStmt) stmtNode()           {}
func (s *AssignStmt) StmtPos() Position { return s.Pos }

// MultiAssignStmt is `a, b, ... = e1, e2, ...` — tuple parallel reassignment
// (v0.15). The RHS is evaluated entirely into a tuple-shaped temporary BEFORE
// any LHS slot is written; the temp is then unpacked into the named targets.
// That sequencing — owned by cgen / run, not by the borrow checker — is what
// makes `a, b = b, a + b` read OLD `a` and `b` on the right.
//
// Targets is parser-restricted to *IdentExpr (bare-ident lvalues only at this
// iteration; field-access and index-assign LHS are deferred). Value is either
// a synthetic *TupleLit built by the parser from the bare comma-list RHS, or
// any single Expr that types as a tuple of matching arity (e.g. a function
// call returning a tuple, so `a, b = divmod(10, 3)` is admitted on the same
// terms as the existing bind form `(a, b) := divmod(10, 3)`).
type MultiAssignStmt struct {
	Pos             Position
	Targets         []Expr     // ≥ 2 *IdentExpr; parser narrows
	TargetPos       []Position // 1:1 with Targets, for slot-level diagnostics
	Value           Expr       // single Expr (TupleLit or tuple-typed)
	LeadingComments []string
}

func (*MultiAssignStmt) stmtNode()           {}
func (s *MultiAssignStmt) StmtPos() Position { return s.Pos }

// ExprStmt wraps an expression used for its side effects. v0.1 only admits
// function calls in expression-statement position, but the parser holds the
// general Expr and validates the kind, so we get one good error message
// instead of a generic "expected statement".
type ExprStmt struct {
	Pos             Position
	Expr            Expr
	LeadingComments []string
}

func (*ExprStmt) stmtNode()           {}
func (s *ExprStmt) StmtPos() Position { return s.Pos }

// PrintStmt represents `print expr`. v0.0 stored the string directly; v0.1
// generalises to any expression. The interpreter and codegen at v0.0 handle
// only the StringLit case for back-compat — see run.go and cgen.go.
type PrintStmt struct {
	Pos             Position
	Expr            Expr
	LeadingComments []string
}

func (*PrintStmt) stmtNode()           {}
func (s *PrintStmt) StmtPos() Position { return s.Pos }

// ReturnStmt represents `return [expr] [if cond]`. Either field is nilable:
//   - bare `return` ⇒ Value == nil, Guard == nil
//   - `return expr` ⇒ Value != nil, Guard == nil
//   - `return if cond` ⇒ Value == nil, Guard != nil
//   - `return expr if cond` ⇒ both set
type ReturnStmt struct {
	Pos             Position
	Value           Expr // nil for a bare `return`
	Guard           Expr // nil for unconditional return
	LeadingComments []string
}

func (*ReturnStmt) stmtNode()           {}
func (s *ReturnStmt) StmtPos() Position { return s.Pos }

// BreakStmt represents `break [if cond]`.
type BreakStmt struct {
	Pos             Position
	Guard           Expr // nil for unconditional break
	LeadingComments []string
}

func (*BreakStmt) stmtNode()           {}
func (s *BreakStmt) StmtPos() Position { return s.Pos }

// ContinueStmt represents `continue [if cond]`.
type ContinueStmt struct {
	Pos             Position
	Guard           Expr // nil for unconditional continue
	LeadingComments []string
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

// TypeParam is one declared generic type parameter on a generic decl
// (`fn[T: Bound]`, `struct Box[T]`, `enum Pair[T, U]`, `spec Iterable[T]`,
// `impl[T: Bound] Type[T] for Spec`). Bounds are the spec constraints that
// follow `:` — multi-bound is encoded as a slice (`T: A + B` ⇒ Bounds = [A,
// B]). The Bounds list is empty for unconstrained parameters.
//
// At Unit 1 typeck does not consume TypeParam; v0.6 Unit 3 walks the slice
// during monomorphization to validate that each bound has a matching
// `impl <conc> for <Spec>` block in scope.
type TypeParam struct {
	Name   string
	Pos    Position
	Bounds []*TypeRef
}

// FnDecl represents `fn name(p: T, ...) -> R { ... }`. The return type is
// optional — a nil Return means the function returns no value.
//
// Pub records the v0.5 visibility modifier: true when the source wrote
// `pub fn ...`, false (the default) for an unprefixed `fn ...`. The bit is
// also set on impl-method FnDecls when the inner method was prefixed
// (`impl T { pub fn m() ... }`). At v0.5 Unit 1a typeck does not yet
// consume the bit; it carries through unchanged for Unit 3 to gate
// cross-module access.
type FnDecl struct {
	Pos        Position
	Name       string
	TypeParams []TypeParam // v0.6 generic type parameters; nil for non-generic fns
	Params     []FnParam
	Return     *TypeRef // nil ⇒ no return value
	Body       *Block
	Pub        bool
	// HasDefers (v0.7 Unit 3) is set when the body contains at least one
	// DeferStmt. Set by the typeck collect pass that walks fn bodies for
	// closure / defer analysis; downstream halves use this bit to skip
	// per-frame defer-stack setup on fns that don't need it.
	HasDefers bool
	// BuiltinName (v0.8 Unit 1) is the bareword identifier following a
	// `__builtin <name>` fn-decl tail. Empty for ordinary fns. When non-empty
	// Body is nil — the marker REPLACES the body. Typeck (Unit 2) validates
	// the name against the closed builtin registry; the interpreter / cgen
	// route the call to the host implementation keyed by this string.
	BuiltinName     string
	BuiltinNamePos  Position
	LeadingComments []string
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
	Pos             Position
	Cond            Expr
	Then            *Block
	Elifs           []ElifClause
	Else            *Block // nil ⇒ no else
	LeadingComments []string
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
	ForChan                    // for v in ch { ... } — receive until close (v0.7)
)

// ForStmt covers all four for-loop shapes via Kind. Only the fields relevant
// to Kind are populated; the rest are zero values.
//
// ForRange uses Var/VarPos plus Range; ForIter uses Var/VarPos plus Iter.
// The two list-iteration shapes share the loop variable + body machinery and
// diverge only in how the per-iteration value is produced.
type ForStmt struct {
	Pos             Position
	Kind            ForKind
	Cond            Expr       // ForCond
	Var             string     // ForRange / ForIter — the bound variable name
	VarPos          Position   // ForRange / ForIter — position of the variable name
	Range           *RangeExpr // ForRange — the iteration range
	Iter            Expr       // ForIter — the list-typed expression to iterate
	Body            *Block
	LeadingComments []string
}

func (*ForStmt) stmtNode()           {}
func (s *ForStmt) StmtPos() Position { return s.Pos }

// NopStmt represents the literal `nop` keyword. Carries over from v0.0 with
// no shape change.
type NopStmt struct {
	Pos             Position
	LeadingComments []string
}

func (*NopStmt) stmtNode()           {}
func (s *NopStmt) StmtPos() Position { return s.Pos }

// ImportDecl is one resolved-at-parse-time import. v0.5 Unit 1b admits three
// surface shapes — `import "name"`, `import "name" as alias`, and the grouped
// `import (...)` form. The grouped form is desugared in the parser into one
// ImportDecl per entry so downstream layers (loader, typeck, run, build) only
// see the flat single-import shape.
//
//   - Path is the verbatim contents of the string literal — no path resolution
//     happens at parse time. The loader (Unit 2) is responsible for mapping
//     the string to a sibling file.
//   - PathPos is the position of the string literal inside the source, used by
//     diagnostics and by the loader when reporting a load failure.
//   - Alias is empty when the import had no `as` clause; otherwise it is the
//     local binding name. The bare form `import "name"` uses Path itself as
//     the binding (the parser rejects at parse time when Path is a reserved
//     keyword, per the §Resolution rules tenth-man pin).
//   - AliasPos is the zero Position when Alias is empty; otherwise it is the
//     position of the alias identifier.
//
// At Unit 1b ImportDecl is parser-only: typeck/borrow/run/cgen each treat it
// as a no-op so existing v0.0–v0.4 corpora keep working unchanged. Unit 2
// wires the node into the module loader.
type ImportDecl struct {
	Pos             Position
	Path            string
	PathPos         Position
	Alias           string
	AliasPos        Position
	LeadingComments []string
}

func (*ImportDecl) stmtNode()           {}
func (s *ImportDecl) StmtPos() Position { return s.Pos }

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

// InterpolatedStringLit is `"foo {x} bar {y}"`. Pieces alternate literal
// and variable; empty literal pieces are dropped by the parser.
type InterpolatedStringLit struct {
	typed
	Pos    Position
	Pieces []StringPiece
}

func (*InterpolatedStringLit) exprNode()           {}
func (e *InterpolatedStringLit) ExprPos() Position { return e.Pos }

// StringPiece is one segment of an interpolated string — either a literal
// chunk (*StringLitPiece) or a `{ident}` slot (*StringVarPiece).
type StringPiece interface {
	stringPieceNode()
}

type StringLitPiece struct{ Text string }

func (*StringLitPiece) stringPieceNode() {}

// StringVarPiece wraps the slot's *IdentExpr so typeck's ident-resolution
// + type-decoration applies unchanged.
type StringVarPiece struct{ Ident *IdentExpr }

func (*StringVarPiece) stringPieceNode() {}

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
//
// v0.17 operator-spec wiring: Lowered is set by typeck when the operator
// lowers to a user-type method call (`a + b` → `a.add(b)` when BigInt
// implements Arithmetic, etc.). cgen / run / borrow walkers read this
// field and route through the method call instead of the surface op.
// fmt's pretty-printer keeps walking Left/Right so it can render the
// surface form.
//
// LoweredNot is set to true when the surface op needs the bool result
// negated:
//   - `!=` via `eq` (negate)
//   - `>=` via `lt` (negate)
//   - `<=` via swapped `lt` (swap encoded in Lowered, then negate)
//
// Operand swap for `>` and `<=` is encoded directly in Lowered's
// receiver / args; the emitter sees a normal method call shape.
type BinaryExpr struct {
	typed
	Pos        Position
	Op         BinaryOp
	Left       Expr
	Right      Expr
	Lowered    *MethodCallExpr // v0.17 operator-spec desugar
	LoweredNot bool            // v0.17: true → emit `!(lowered)`
}

func (*BinaryExpr) exprNode()           {}
func (e *BinaryExpr) ExprPos() Position { return e.Pos }

// UnaryExpr is a prefix operator application: -, not, ~.
//
// v0.17 operator-spec wiring: Lowered is set by typeck when `-x` lowers to
// `x.neg()` (user-type Neg impl). cgen / run / borrow walkers route through
// the method call when Lowered is non-nil.
type UnaryExpr struct {
	typed
	Pos     Position
	Op      UnaryOp
	Operand Expr
	Lowered *MethodCallExpr // v0.17 operator-spec desugar for `-x`
}

func (*UnaryExpr) exprNode()           {}
func (e *UnaryExpr) ExprPos() Position { return e.Pos }

// CallExpr is `callee(args)`. v0.1 only admits an IdentExpr as Callee but the
// parser is general so future call-on-expression doesn't require a re-shape.
//
// Specialised (v0.6 Unit 7) is set by typeck when the callee resolved to a
// generic-fn instantiation. It points at the monomorphised FnDecl clone so
// codegen can route the call to the specialised symbol; the surface Callee
// IdentExpr keeps the original generic name for diagnostics.
type CallExpr struct {
	typed
	Pos         Position
	Callee      Expr
	Args        []Expr
	Specialised *FnDecl
	// Bare-variant constructor sugar. When the callee is `Ok` or `Err` and
	// no local binding shadows the name, typeck lowers the call to the
	// equivalent Result.<variant> enum-lit and stashes it here; cgen and
	// interp route through Lowered when present, ignoring the surface Callee.
	Lowered *EnumLit
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
//
// Pub records the v0.5 decl-level visibility modifier. Field-level `pub` is
// out of scope at v0.5; only the decl as a whole is gated.
type StructDecl struct {
	Pos             Position
	Name            string
	TypeParams      []TypeParam // v0.6 generic type parameters; nil for non-generic
	Fields          []FieldDecl
	Pub             bool
	LeadingComments []string
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
//
// Pub records the v0.5 decl-level visibility modifier. Variants inherit the
// enum's visibility; per-variant `pub` is not a v0.5 surface.
type EnumDecl struct {
	Pos             Position
	Name            string
	TypeParams      []TypeParam // v0.6 generic type parameters; nil for non-generic
	Variants        []VariantDecl
	Pub             bool
	LeadingComments []string
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
	Pos             Position
	Subject         Expr
	Arms            []MatchArm
	LeadingComments []string
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
	// Module, when non-empty, is the local binding name of an imported
	// module that qualifies a cross-module struct construction
	// (`mod.MyStruct { ... }`). The parser produces this shape when it sees
	// `Ident DOT Ident` immediately followed by a struct-literal body.
	// Empty for in-module struct literals — backward-compatible with every
	// v0.0–v0.4 corpus program.
	Module   string
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

// FieldAccessExpr is `receiver.fieldName` and (v0.6) the safe-navigation
// `receiver?.fieldName`. At parse time the receiver may be any expression —
// typeck disambiguates between struct field access (when the receiver is a
// value) and enum variant access (when the receiver is a bare IdentExpr that
// resolves to an enum type).
//
// Safe (v0.6) records whether the operator was `?.` — chosen over a separate
// SafeFieldAccessExpr node because the only structural difference is the
// nullable lowering at typeck, and every consumer (parser printers, typeck,
// borrow walker, run, cgen) needs one line of branching either way.
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
	Safe      bool // v0.6 set when source spelled `?.`
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
//
// Pub records the v0.5 visibility modifier on a spec-method declaration.
// The bit is inert at Unit 1a; Unit 3 will use it to gate cross-module
// dispatch on default-method bodies.
type SpecMethod struct {
	Pos        Position
	Name       string
	TypeParams []TypeParam // v0.6 per-method generic type parameters
	Params     []FnParam
	Return     *TypeRef // nil ⇒ no return value
	Body       *Block   // nil ⇒ signature only
	Pub        bool
	// HasDefers (v0.7 Unit 3) is set when the default-impl body contains at
	// least one defer. Mirrors FnDecl.HasDefers so downstream halves can
	// gate defer-frame setup without a body walk.
	HasDefers bool
}

// SpecDecl represents `spec Name { method_decl* }`. Methods may be
// signature-only (no body) or default implementations (body present); the
// parser admits both shapes and the v0.4 typeck pass distinguishes them.
//
// Pub records the v0.5 decl-level visibility modifier on the spec itself;
// individual method visibility is recorded on each SpecMethod.
type SpecDecl struct {
	Pos             Position
	Name            string
	TypeParams      []TypeParam // v0.6 generic type parameters; nil for non-generic specs
	Methods         []*SpecMethod
	Pub             bool
	LeadingComments []string
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
	// TypeModule, when non-empty, is the local binding name of an
	// imported module that defined Type (`impl util.Counter for ...`).
	// v0.5 typeck routes the receiver-type lookup through the importing
	// module's import table when this is set.
	TypeModule string
	// TypeArgs (v0.6) carries the receiver-type's generic type arguments,
	// e.g. the `int` slot in `impl Box[int] for Printable` or the `T` slot
	// in `impl[T] Box[T] for Printable`. Nil/empty for non-generic receivers
	// — every v0.0–v0.5 program carries this at its zero value.
	TypeArgs []*TypeRef
	// TypeParams (v0.6) carries the impl-level generic parameters declared
	// immediately after `impl` (`impl[T: Bound] LocalType[T] for SomeSpec`).
	// Nil for impls that take no impl-level parameters.
	TypeParams []TypeParam
	Spec    string // empty when inherent; otherwise the spec name
	// SpecModule, when non-empty, is the local binding name of an
	// imported module that defined Spec (`impl T for util.Printable`).
	SpecModule string
	Methods []*FnDecl
	// Receiver is the canonical *Type pointer typeck resolved Type/
	// TypeModule to. Set during the resolveImpls / resolveImplsCross
	// pass and read by downstream consumers (interp's RunBundle, build's
	// EmitBundle) that need pointer-equality dispatch on the receiver.
	// Two modules each declaring `struct Counter` get distinct *Type
	// pointers here, so impl tables can disambiguate by canonical pointer
	// rather than bare name. Nil for impls that fail to resolve (typeck
	// rejects those before this gets read).
	Receiver        *Type
	LeadingComments []string
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
//
// LoweredCall is set by typeck when the method-call shape on a list[T]
// receiver is one of the v0.3 list builtins — `xs.push(v)` desugars to
// `push(xs, v)`, `xs.clone()` to `clone(xs)`, `xs.len()` to `len(xs)`. The
// rewrite hands a synthetic CallExpr to the rest of the pipeline so the
// existing builtin paths in run / cgen handle method-form calls without
// special-casing. Diagnostics still anchor on the surface MethodCallExpr.
type MethodCallExpr struct {
	typed
	Pos         Position
	Receiver    Expr
	Method      string
	MethodPos   Position
	Args        []Expr
	Lowered     *EnumLit
	LoweredCall *CallExpr
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
	Pos        Position
	EnumName   string
	// Module, when non-empty, is the local binding name of an imported
	// module that defined the enum (set by v0.5 Unit 3 typeck when lowering
	// `mod.Color.Red` / `mod.Token.Ident(x)`). Empty for in-module enum
	// literals.
	Module     string
	Variant    string
	VariantPos Position
	Payload    []Expr
}

func (*EnumLit) exprNode()           {}
func (e *EnumLit) ExprPos() Position { return e.Pos }

// ---------------------------------------------------------------------------
// v0.6 null-safety expression nodes.
// ---------------------------------------------------------------------------

// NilLit is the bare `nil` keyword in expression position. Typeck (Unit 4)
// resolves it to `Option[T].None` for the contextually-inferred T; outside an
// inferable position the diagnostic is `cannot infer type of nil — annotate
// the binding`.
type NilLit struct {
	typed
	Pos Position
}

func (*NilLit) exprNode()           {}
func (e *NilLit) ExprPos() Position { return e.Pos }

// PropagateExpr is the postfix `?` propagation operator. Inner is the
// receiver; the operator is only legal inside a fn whose return type is
// Result[U, E] (compatible E) or Option[U]. Typeck (Unit 4) lowers the
// operator to a match-and-early-return; the parser only records the shape.
type PropagateExpr struct {
	typed
	Pos   Position
	Inner Expr
}

func (*PropagateExpr) exprNode()           {}
func (e *PropagateExpr) ExprPos() Position { return e.Pos }

// CoalesceExpr is the right-associative infix `??` operator at the lowest
// precedence. LHS must be Option[T] or Result[T, E]; RHS must be T. The
// node is kept distinct from BinaryExpr because the operand-type rule is
// not parameterisable over the existing BinaryOp set, and the lowering
// (Unit 4) emits a 2-arm match that the binary-op walker would not produce.
type CoalesceExpr struct {
	typed
	Pos   Position
	Left  Expr
	Right Expr
}

func (*CoalesceExpr) exprNode()           {}
func (e *CoalesceExpr) ExprPos() Position { return e.Pos }

// ---------------------------------------------------------------------------
// v0.7 concurrency expression / statement nodes.
// ---------------------------------------------------------------------------

// Capture (v0.7 Unit 3) records one outer-scope binding referenced from
// inside an AnonFnExpr body. Name is the identifier, Pos is the use-site
// position of one of the references (used for diagnostics), and Type is the
// resolved *Type of the binding at capture-analysis time. Captures are
// recorded once per name regardless of how many times the body references
// it. The slot lives on AnonFnExpr.Captures.
type Capture struct {
	Name string
	Pos  Position
	Type *Type
}

// AnonFnExpr is `fn(params) -> R { body }` (or `fn(params) { body }` with no
// return) used in expression position. Same shape as FnDecl minus the name.
// The parser produces this node when it sees `fn` immediately followed by `(`
// — the tenth-man-pinned disambiguation rule. Capture analysis lives in
// typeck (Unit 3); this node only carries the syntactic surface.
//
// Captures (v0.7 Unit 3) records the outer-scope bindings the body references
// — populated by typeck capture analysis. Captures from inner declarations
// (params, lets inside the body) are NOT recorded; only free variables that
// resolve to bindings declared outside the AnonFnExpr appear here. Each entry
// is added at most once even if the body references the name many times.
//
// HasDefers (v0.7 Unit 3) is set when the body (any nesting depth, but the
// parser only admits top-level fn-body defers) contains at least one
// DeferStmt. Downstream halves use this bit to skip per-frame defer-stack
// setup on closures that don't need it.
type AnonFnExpr struct {
	typed
	Pos       Position
	Params    []FnParam
	Return    *TypeRef // nil ⇒ no return value
	Body      *Block
	Captures  []Capture
	HasDefers bool
}

func (*AnonFnExpr) exprNode()           {}
func (e *AnonFnExpr) ExprPos() Position { return e.Pos }

// SpawnStmt is `spawn <fn-call-expr>`. The parser narrows the inner expression
// at parse time per the grammar's "spawn admits only fn calls" rule. The
// admitted shapes are:
//
//   - *CallExpr — bare named fn (`spawn do_work()`) or an anon-fn IIFE
//     (`spawn fn() { ... }()`). The callee is an IdentExpr or AnonFnExpr.
//   - *MethodCallExpr — qualified cross-module fn (`spawn mod.do_work()`)
//     and method-form calls. v0.5 typeck's checkCrossModuleFnCall already
//     consumes MethodCallExpr for the cross-module fn-call path; spawn
//     reuses that machinery untouched.
//
// Call is typed as Expr so both shapes flow through one field; the parser
// guarantees it is one of the two concrete types above.
type SpawnStmt struct {
	Pos             Position
	Call            Expr
	LeadingComments []string
}

func (*SpawnStmt) stmtNode()           {}
func (s *SpawnStmt) StmtPos() Position { return s.Pos }

// DeferStmt is `defer <stmt>` or `defer <block>`. The block form is recorded
// as-is; the single-statement form is wrapped in a one-element Block at parse
// time so downstream consumers walk a single shape.
//
// v0.7 admits defer only at fn-body top-level scope (not nested inside if /
// for / match / inner blocks); the parser enforces that with a precise
// diagnostic. typeck (Unit 3) takes over for the per-fn defer-stack
// bookkeeping; the parser only records the syntactic shape.
type DeferStmt struct {
	Pos             Position
	Body            *Block
	LeadingComments []string
}

func (*DeferStmt) stmtNode()           {}
func (s *DeferStmt) StmtPos() Position { return s.Pos }

// ChanConstructorExpr is `chan[T]()` (unbuffered, rendezvous) or `chan[T](N)`
// (buffered, capacity N). The parser produces this node when it sees the
// IDENT `chan` followed by `[T]` then `(...)` — the only place `chan` can
// appear in expression position. Element carries the parsed element type;
// Capacity is non-nil only for the buffered form. typeck (Unit 2) resolves
// Element and validates that Capacity (when present) is an int.
type ChanConstructorExpr struct {
	typed
	Pos      Position
	Element  *TypeRef
	Capacity Expr // nil ⇒ unbuffered (rendezvous)
}

func (*ChanConstructorExpr) exprNode()           {}
func (e *ChanConstructorExpr) ExprPos() Position { return e.Pos }

// SendStmt is `chan_expr <- value_expr`. Statement-only — a send produces no
// value and so cannot appear in expression position. The parser detects the
// shape after parsing the LHS expression at statement position and seeing
// `<-` next; chained sends (`a <- b <- c`) are rejected at parse time per
// PLAN.md so users must split with parens.
type SendStmt struct {
	Pos             Position
	Chan            Expr
	Value           Expr
	LeadingComments []string
}

func (*SendStmt) stmtNode()           {}
func (s *SendStmt) StmtPos() Position { return s.Pos }

// RecvExpr is the prefix `<-` channel-receive operator: `<- ch`. Yields
// `T?` at typeck (Option[T]) so the closed-channel case lands as `None`.
// Same precedence rung as the other prefix unaries (`-`, `~`, `not`).
type RecvExpr struct {
	typed
	Pos  Position
	Chan Expr
}

func (*RecvExpr) exprNode()           {}
func (e *RecvExpr) ExprPos() Position { return e.Pos }

// SelectOpKind tags which of the four channel-op shapes a SelectArm carries.
// The four shapes mirror the grammar's `select_op` production:
//
//   - SelectRecvBind:     IDENT ":=" "<-" expr   — binds the received value
//   - SelectRecvDiscard:  "<-" expr              — drops the received value
//   - SelectSend:         expr "<-" expr         — sends a value
//   - SelectDefault:      "_"                    — non-blocking default arm
type SelectOpKind int

// Select-arm channel-op kinds.
const (
	SelectRecvBind SelectOpKind = iota
	SelectRecvDiscard
	SelectSend
	SelectDefault
)

// SelectArm is one arm of a `select`: a channel op + a body. Single-statement
// arm bodies are wrapped in a one-element Block at parse time so downstream
// consumers always walk a Block (mirrors MatchArm.Body).
//
//   - BindName / BindNamePos are populated only for SelectRecvBind.
//   - Chan is the channel expression for SelectRecvBind / SelectRecvDiscard /
//     SelectSend; nil for SelectDefault.
//   - Value is the value being sent for SelectSend; nil for the other shapes.
type SelectArm struct {
	Pos         Position
	Op          SelectOpKind
	BindName    string
	BindNamePos Position
	Chan        Expr
	Value       Expr
	Body        *Block
}

// SelectStmt is `select { arm; arm; ... }`. Multiplexed channel wait — blocks
// until one arm is ready (the default arm, when present, makes the select
// non-blocking). The parser rejects an empty arm list at parse time.
type SelectStmt struct {
	Pos             Position
	Arms            []SelectArm
	LeadingComments []string
}

func (*SelectStmt) stmtNode()           {}
func (s *SelectStmt) StmtPos() Position { return s.Pos }

// AsmChunkKind tags the two flavours of fragment that compose an AsmBlock
// body: a literal text run that the cgen emits into the GCC __asm__ template
// verbatim, and a `${name}` interpolation reference that the cgen lowers into
// a `%N` operand placeholder bound to an input from the surrounding Zerg
// scope.
type AsmChunkKind int

// AsmChunk kinds.
const (
	// AsmChunkText is a literal byte run from the asm body. The chunk's Text
	// field carries the bytes; Name / NamePos are zero.
	AsmChunkText AsmChunkKind = iota
	// AsmChunkInterp is a `${name}` interpolation reference. The chunk's
	// Name field carries the bare identifier text (no `${` or `}`); Text is
	// zero. U2 only validates the surface shape — that the body between
	// `${` and `}` is a non-empty valid identifier. U3 attaches the binder
	// resolution + typecheck; U4 lowers each interp into a GCC inline-asm
	// operand.
	AsmChunkInterp
)

// AsmChunk is one fragment of an `asm { body }` body. The parser splits the
// raw body produced by the lexer into chunks at `${name}` markers; everything
// between markers (and any prefix / suffix outside a marker) becomes an
// AsmChunkText. Empty text chunks are admitted — a body that opens with
// `${x} mov ...` produces an empty text chunk before the interp so the
// chunk order matches the byte order of the source, simplifying U4's emit.
type AsmChunk struct {
	// Pos is the source position of this chunk's first byte (the `$` of an
	// interp, or the first byte of a text run). Diagnostics anchor on
	// chunk-level positions so U3's "unknown binding" / "wrong type" errors
	// point at the actual `${name}` site.
	Pos Position
	// Kind selects between AsmChunkText (literal run) and AsmChunkInterp
	// (named interp reference).
	Kind AsmChunkKind
	// Text carries the literal bytes for AsmChunkText. Empty for interp
	// chunks. The bytes are passed through to cgen unchanged at U4.
	Text string
	// Name is the bare interp identifier for AsmChunkInterp ("xs" for
	// `${xs}`). Empty for text chunks. NamePos points at the first byte of
	// the identifier (the byte after `${`) so U3's diagnostics line up with
	// the source.
	Name    string
	NamePos Position
	// BoundType is the binding's resolved type, populated by typeck (U3)
	// after the name lookup succeeds. Cgen (U4) reads this to decide
	// whether the interp lowers to a `byte` immediate operand or a
	// `list[byte].data` pointer operand. Nil for AsmChunkText chunks and
	// for any chunk reached before typeck has run.
	BoundType *Type
	// IsOutput is true iff the binding referenced by this AsmChunkInterp
	// is mutable and the type admits output-operand lowering (int / byte).
	// Cgen emits these as GCC `"+r"` inout operands (the asm body may
	// read the binding's initial value and writes back at block exit).
	// list[byte] is never an output even when the binding is mut — the
	// cgen contract there lowers `.data`, and mutating the pointer to a
	// different buffer is not a supported surface. Set by typeck; false
	// for AsmChunkText and for any input-only interp.
	IsOutput bool
}

// AsmBlock is the v0.13 `asm { body }` statement. The block body is the only
// place in the language where raw target-machine assembly appears in source
// — every other construct routes through cgen's C templating. The parser
// captures BodyRaw verbatim (between but excluding the braces) so the
// formatter can round-trip the body byte-for-byte; the same string drives
// the U4 lowering into a GCC __asm__ volatile template.
//
// Surface gating (per v0.13 PLAN pin 5):
//   - `asm` is reserved at v0.13. v0.12 and earlier lex it as KindIdent so
//     older source keeps parsing.
//   - The interpreter (zerg run) rejects every AsmBlock at run time — the
//     interpreter cannot execute machine code. The exact diagnostic lives
//     in run.go alongside the rejection.
//   - Pin 8 contract: x29 (fp) is user-preserve, NOT clobbered by cgen.
//     There is no parser-level enforcement; the contract is a hard rule.
type AsmBlock struct {
	// Pos is the position of the `asm` keyword.
	Pos Position
	// OpenBracePos is the position of the opening `{`. Used by diagnostics
	// that want to anchor on the block, not the keyword (e.g. an
	// unterminated-block error from the lexer).
	OpenBracePos Position
	// Chunks is the parsed body, split at `${name}` interp markers.
	// Adjacent text chunks are not merged at parse time — each chunk
	// carries its own source position for diagnostics.
	Chunks []AsmChunk
	// BodyRaw is the verbatim body string captured by the lexer (no
	// surrounding braces). The formatter uses this to round-trip the body
	// without re-serialising chunks; cgen uses Chunks because it needs the
	// interp split to emit operand placeholders.
	BodyRaw         string
	LeadingComments []string
}

func (*AsmBlock) stmtNode()           {}
func (s *AsmBlock) StmtPos() Position { return s.Pos }
