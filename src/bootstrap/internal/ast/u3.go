// The U3 cluster is the group 3–5 surface: the remaining literals, the full
// expression ladder (ranges, membership and type tests, the null-safety and
// channel operators, the postfix chain, composite literals, f-strings), the
// reassignment target shapes, and the function forms (closures, function types,
// parameters). Every node embeds base like the rest of the tree; 'zerg fmt'
// reprints each one and the round-trip oracle keeps the reprints honest.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// --- group 3: remaining literals ----------------------------------------------

// RuneLit is a rune literal like 'a'. Text is the author's surface lexeme (quotes
// and any escape intact) so fmt reprints it verbatim; Value is the decoded rune.
type RuneLit struct {
	base
	Text  string
	Value rune
}

// ByteLit is a byte literal like b'a'. Text is the surface lexeme; Value the octet.
type ByteLit struct {
	base
	Text  string
	Value byte
}

// RawStrLit is a raw string r"…". Text is the surface lexeme (reprinted verbatim,
// since a raw string processes no escapes); Value is the content between quotes.
type RawStrLit struct {
	base
	Text  string
	Value string
}

// CmdLit is a plain command literal `…` (run directly, no shell, no holes). Text
// is the surface lexeme; Value the content between backticks.
type CmdLit struct {
	base
	Text  string
	Value string
}

// --- group 4: expression ladder -----------------------------------------------

// Range is 'lo..hi' (exclusive) or 'lo..=hi' (inclusive); Hi is nil for an open
// range 'lo..' (GRAMMAR group 4).
type Range struct {
	base
	Lo        Expr
	Hi        Expr // nil for an open range
	Inclusive bool
}

// IsExpr is the type test 'x is TypeName' (the right side is a type name, resolved
// in a later phase).
type IsExpr struct {
	base
	X        Expr
	TypeName string
}

// Coalesce is 'a ?? b' — a's value if present, else b, whose right side may be a
// Diverge (GRAMMAR group 8). It is right-associative.
type Coalesce struct {
	base
	X Expr
	Y Expr
}

// Diverge is a control-flow escape used as the right side of '??': 'break',
// 'continue', 'return e?', or 'raise e (from c)?'.
type Diverge struct {
	base
	Kw    token.Kind // Break, Continue, Return, or Raise
	Value Expr       // return/raise operand; nil for break/continue and a bare return
	From  Expr       // 'raise e from c' cause; nil otherwise
}

// Try is the propagate postfix 'x?' (unwrap Left, else early-return Right).
type Try struct {
	base
	X Expr
}

// Force is the force-unwrap postfix 'x!' (unwrap Left, else raise UnwrapError).
type Force struct {
	base
	X Expr
}

// OptChain is the optional-chain postfix 'x?.id' (read .id when present, else nil).
type OptChain struct {
	base
	X    Expr
	Name string
}

// Recv is the channel receive prefix '<-e' (GRAMMAR group 9's recv-base).
type Recv struct {
	base
	X Expr
}

// Field is the '.id' field or method access postfix.
type Field struct {
	base
	X    Expr
	Name string
}

// TupleIndex is the '.N' tuple element access postfix ('t.0'). Text is the surface
// digits so fmt reprints them verbatim.
type TupleIndex struct {
	base
	X     Expr
	Index int
	Text  string
}

// Bracket is the provisional '[ … ]' postfix (DESIGN-1a §3a): an index 'a[i]' or
// type arguments 'f[K, V]' / 'list[int]', left unresolved in this phase. Comma is
// true when the source used a comma (unambiguously type arguments).
type Bracket struct {
	base
	Base  Expr
	Elems []Expr
	Comma bool
}

// --- group 4: composite literals ----------------------------------------------

// TupleLit is a parenthesized 2+ element tuple '(a, b, …)'.
type TupleLit struct {
	base
	Elems []Expr
}

// ListLit is the comma form of a list literal '[a, b, …]' (Elems empty for '[]').
type ListLit struct {
	base
	Elems []Expr
}

// ListFill is the fill form '[v; N]' — N copies of v.
type ListFill struct {
	base
	Value Expr
	Count Expr
}

// MapLit is a map literal '{k: v, …}'. Empty Entries denotes the empty map '{:}'
// (a bare '{}' is a block, never an empty map).
type MapLit struct {
	base
	Entries []MapEntry
}

// MapEntry is one 'key: value' entry of a map literal.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// --- group 5: f-strings & f-cmds ----------------------------------------------

// FStr is an f-string f"…{expr}…": a sequence of literal-text and hole parts.
type FStr struct {
	base
	Parts []FStrPart
}

// FCmd is an interpolating command literal f`…{expr}…` (runs through a shell).
type FCmd struct {
	base
	Parts []FStrPart
}

// FStrPart is one piece of an f-string or f-cmd: literal text (Expr nil) or a hole
// '{expr =? !conv? :spec?}'. Debug records the self-documenting '='; Conv is the
// 'r'/'s'/'a' conversion (0 when absent); Spec is the opaque format spec (present
// only when HasSpec).
type FStrPart struct {
	Text    string // literal text (decoded) when Expr is nil
	Expr    Expr   // the hole expression, or nil for a text part
	Debug   bool   // the trailing '=' self-documenting marker
	Conv    byte   // 'r', 's', 'a', or 0
	Spec    string // format spec text, without the leading ':'
	HasSpec bool
}

// --- group 5: functions -------------------------------------------------------

// FnExpr is an anonymous function / closure 'fn(params) -> ret { body }'; a
// closure parameter's type may be inferred (omitted).
type FnExpr struct {
	base
	Params []ClosureParam
	Ret    *TypeRef
	Body   *Block
}

// ClosureParam is one closure parameter; Type is nil when the type is inferred.
type ClosureParam struct {
	base
	Ref     bool // 'mut &x'
	Name    string
	Type    *TypeRef // nil when inferred
	Default Expr
}

// FnType is a function's type 'unsafe? fn(param-types) -> ret' (GRAMMAR group 5).
type FnType struct {
	base
	Unsafe bool
	Params []ParamType
	Ret    *TypeRef
}

// ParamType is one parameter position of a function type.
type ParamType struct {
	Ref  bool // 'mut &'
	Type *TypeRef
}

// --- group 4: reassignment ----------------------------------------------------

// Reassign writes an existing lvalue, tuple shape, or struct shape:
// 'assign-target = expr' (no leading ':', which is what tells it from a binding).
type Reassign struct {
	base
	Target AssignTarget
	Value  Expr
}

// AssignTarget is the left side of a reassignment: an lvalue, a tuple shape, or a
// struct shape whose leaves are themselves assign-targets.
type AssignTarget interface {
	Node
	assignTargetNode()
}

// LValueTarget wraps an lvalue expression (an Ident, Field, TupleIndex, or
// Bracket chain) used as an assignment target.
type LValueTarget struct {
	base
	X Expr
}

// TupleTarget is a tuple-shape target '(a, b, …)' for destructuring reassignment.
type TupleTarget struct {
	base
	Elems []AssignTarget
}

// StructTarget is a struct-shape target 'Type{f, g: t, ..}'; Rest records a
// trailing '..' that ignores the remaining fields.
type StructTarget struct {
	base
	TypeName string
	Fields   []FieldTarget
	Rest     bool
}

// FieldTarget is one field of a struct-shape target: a bare 'f' shorthand (Target
// nil) or 'f: target'.
type FieldTarget struct {
	Name   string
	Target AssignTarget // nil for the shorthand form
}

// --- marker methods -----------------------------------------------------------

func (*RuneLit) node()      {}
func (*ByteLit) node()      {}
func (*RawStrLit) node()    {}
func (*CmdLit) node()       {}
func (*Range) node()        {}
func (*IsExpr) node()       {}
func (*Coalesce) node()     {}
func (*Diverge) node()      {}
func (*Try) node()          {}
func (*Force) node()        {}
func (*OptChain) node()     {}
func (*Recv) node()         {}
func (*Field) node()        {}
func (*TupleIndex) node()   {}
func (*Bracket) node()      {}
func (*TupleLit) node()     {}
func (*ListLit) node()      {}
func (*ListFill) node()     {}
func (*MapLit) node()       {}
func (*FStr) node()         {}
func (*FCmd) node()         {}
func (*FnExpr) node()       {}
func (*FnType) node()       {}
func (*Reassign) node()     {}
func (*LValueTarget) node() {}
func (*TupleTarget) node()  {}
func (*StructTarget) node() {}

func (*RuneLit) exprNode()    {}
func (*ByteLit) exprNode()    {}
func (*RawStrLit) exprNode()  {}
func (*CmdLit) exprNode()     {}
func (*Range) exprNode()      {}
func (*IsExpr) exprNode()     {}
func (*Coalesce) exprNode()   {}
func (*Diverge) exprNode()    {}
func (*Try) exprNode()        {}
func (*Force) exprNode()      {}
func (*OptChain) exprNode()   {}
func (*Recv) exprNode()       {}
func (*Field) exprNode()      {}
func (*TupleIndex) exprNode() {}
func (*Bracket) exprNode()    {}
func (*TupleLit) exprNode()   {}
func (*ListLit) exprNode()    {}
func (*ListFill) exprNode()   {}
func (*MapLit) exprNode()     {}
func (*FStr) exprNode()       {}
func (*FCmd) exprNode()       {}
func (*FnExpr) exprNode()     {}
func (*FnType) exprNode()     {}

// Block is also an expression (GRAMMAR group 4 primary): its value is its last
// statement's value.
func (*Block) exprNode() {}

func (*Reassign) stmtNode() {}

func (*LValueTarget) assignTargetNode() {}
func (*TupleTarget) assignTargetNode()  {}
func (*StructTarget) assignTargetNode() {}
