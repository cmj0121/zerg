// Package ast defines the abstract syntax tree the parser builds for the Phase 0
// subset of Zerg: functions, the simple statements, and the expression ladder.
// Every node embeds base, which carries a source Span (so sema and emit can
// anchor diagnostics) and the node-attached lead/trail trivia (so 'zerg fmt' can
// reprint comments and blank lines); type information is attached separately by
// sema (see package sema) rather than stored on the nodes.
//
// The tree grows with the language; groups beyond Phase 0 (types, generics,
// pattern matching, concurrency) add node kinds here without disturbing these.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// Node is any AST node: it carries a span and its attached trivia.
type Node interface {
	node()
	Span() token.Span
	Lead() []token.Trivia
	Trail() []token.Trivia
}

// Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// Stmt is a statement inside a block.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// File is a whole source file: a sequence of declarations. End holds any
// dangling trivia after the last declaration (comments at end of file).
type File struct {
	base
	Decls []Decl
	End   []token.Trivia
}

// TypeRef names a type in source: a type-name with optional type arguments
// ('list[int]') and an optional associated-type projection chain ('I.Item.Sub',
// GRAMMAR group 7's base-type). It is the named form of a Type; the composite
// forms (tuple/array/chan/fn/ptr) and the optional wrapper live in u5.go.
type TypeRef struct {
	base
	Name string
	Args []Type   // type arguments 'list[int]', 'Matrix[3, 4]'; nil when none
	Proj []string // associated-type projection segments after '.'; nil when none
}

// Param is one function parameter. Ref marks the 'mut &x' mutable-reference form
// (GRAMMAR group 5); a nil Default means the parameter has no default value.
type Param struct {
	base
	Ref     bool // 'mut &x' — passed by mutable reference
	Name    string
	Type    Type
	Default Expr
}

// FuncDecl is a function declaration — top level, or a method / associated
// function of a spec or impl. A nil Ret means the function returns nil (no
// '-> type'); a nil Generics means it is not generic; Decorators carries any
// '#[...]' prefix (GRAMMAR group 7's decorated-decl).
type FuncDecl struct {
	base
	Decorators []*Decorator
	Pub        bool // 'pub fn' — the visibility keyword the surface carried
	Unsafe     bool // 'unsafe fn' — the function is unsafe throughout (group 5/12)
	Mut        bool // 'mut fn' — a method that mutates its receiver (group 5)
	Name       string
	NameEnd    token.Pos // end of the name, for signature diagnostics
	Generics   *Generics // '[T: Bound, N: int]'; nil when non-generic
	Params     []Param
	Ret        Type
	Body       *Block
}

// Block is a brace-delimited statement list. End holds any dangling trivia
// between the last statement and the closing '}'.
type Block struct {
	base
	Stmts []Stmt
	End   []token.Trivia
}

// --- statements ---------------------------------------------------------------

// NopStmt is the empty statement 'nop'.
type NopStmt struct{ base }

// BindStmt introduces a name: 'x := e', 'mut x := e', or 'x: T = e'. A nil Type
// means the type is inferred from Value.
type BindStmt struct {
	base
	Mut   bool
	Const bool
	Name  string
	Type  Type
	Value Expr
}

// PrintStmt is 'print e'.
type PrintStmt struct {
	base
	Value Expr
}

// ReturnStmt is 'return' with an optional value and an optional trailing
// condition: 'return', 'return e', 'return if c', or 'return e if c' (the last
// two are the conditional early-exit sugar).
type ReturnStmt struct {
	base
	Value Expr // nil for a bare 'return'
	Cond  Expr // nil when unconditional; the 'if c' condition otherwise
}

// BreakStmt is 'break'.
type BreakStmt struct{ base }

// ContinueStmt is 'continue'.
type ContinueStmt struct{ base }

// IfBranch is one 'if'/'else if' condition and its body.
type IfBranch struct {
	Cond Expr
	Body *Block
}

// IfStmt is an if/else-if/else chain (statement form; no value).
type IfStmt struct {
	base
	Branches []IfBranch
	Else     *Block // nil when there is no 'else'
}

// ForStmt is 'for { }' (infinite when Cond is nil) or 'for cond { }' (while).
type ForStmt struct {
	base
	Cond Expr
	Body *Block
}

// ExprStmt is an expression evaluated for effect (Phase 0: a call).
type ExprStmt struct {
	base
	X Expr
}

// --- expressions --------------------------------------------------------------

// IntLit is an integer literal. Value is the decoded number sema and emit use;
// Text is the author's original lexeme (base prefix and '_' grouping intact) so
// 'zerg fmt' can reprint '0xFF'/'0b1100'/'1_000' verbatim instead of collapsing
// them to decimal.
type IntLit struct {
	base
	Value int64
	Text  string
}

// FloatLit is a floating-point literal. Value is the decoded number sema and emit
// use; Text is the author's original lexeme (exponent form and '_' grouping
// intact) so 'zerg fmt' reprints '1.5e3'/'1_000.5' verbatim instead of rewriting
// them to a canonical float (the same surface-preservation lesson as IntLit).
type FloatLit struct {
	base
	Value float64
	Text  string
}

// BoolLit is 'true' or 'false'.
type BoolLit struct {
	base
	Value bool
}

// StrLit is a string literal with its decoded value.
type StrLit struct {
	base
	Value string
}

// NilLit is the 'nil' literal.
type NilLit struct{ base }

// Ident is a name reference.
type Ident struct {
	base
	Name string
}

// Unary is a prefix operation: -x, not x, ~x.
type Unary struct {
	base
	Op token.Kind
	X  Expr
}

// Binary is a binary operation; Op is the operator token kind.
type Binary struct {
	base
	Op   token.Kind
	L, R Expr
}

// Call applies a callee to arguments. Each Arg is positional or named
// ('name: value'); GRAMMAR group 5 requires the positional arguments to precede
// the named ones.
type Call struct {
	base
	Callee Expr
	Args   []Arg
}

// Arg is one call argument: positional (Name empty) or named 'Name: Value'.
type Arg struct {
	Name  string // "" for a positional argument
	Value Expr
}

// MatchExpr matches a subject against arms in order, yielding the first fit's
// body. It is an expression: every arm's body has the same type.
type MatchExpr struct {
	base
	Subject Expr
	Arms    []MatchArm
}

// MatchArm is one 'pattern (if guard)? -> body' clause of a match. It embeds
// base so each arm keeps its own line's lead/trail trivia.
type MatchArm struct {
	base
	Pat   Pattern
	Guard Expr // nil when the arm has no 'if' guard
	Body  Expr
}

// Pattern is a match pattern (Phase 0: literal, binding, or wildcard).
type Pattern interface {
	Node
	patternNode()
}

// LitPattern matches a value equal to a literal ('-' allowed on a number).
type LitPattern struct {
	base
	Lit Expr // an IntLit/FloatLit/BoolLit/StrLit
	Neg bool // a leading '-' on a numeric literal
}

// BindPattern binds the subject to a fresh name (matches anything).
type BindPattern struct {
	base
	Name string
}

// WildPattern is '_' — matches anything and binds nothing.
type WildPattern struct{ base }

func (*File) node() {}

func (*NopStmt) node()      {}
func (*BindStmt) node()     {}
func (*PrintStmt) node()    {}
func (*ReturnStmt) node()   {}
func (*BreakStmt) node()    {}
func (*ContinueStmt) node() {}
func (*IfStmt) node()       {}
func (*ForStmt) node()      {}
func (*ExprStmt) node()     {}
func (*Block) node()        {}
func (*FuncDecl) node()     {}

func (*IntLit) node()      {}
func (*FloatLit) node()    {}
func (*BoolLit) node()     {}
func (*StrLit) node()      {}
func (*NilLit) node()      {}
func (*Ident) node()       {}
func (*Unary) node()       {}
func (*Binary) node()      {}
func (*Call) node()        {}
func (*MatchExpr) node()   {}
func (*LitPattern) node()  {}
func (*BindPattern) node() {}
func (*WildPattern) node() {}

func (*FuncDecl) declNode() {}

func (*NopStmt) stmtNode()      {}
func (*BindStmt) stmtNode()     {}
func (*PrintStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*IfStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*ExprStmt) stmtNode()     {}

func (*IntLit) exprNode()    {}
func (*FloatLit) exprNode()  {}
func (*BoolLit) exprNode()   {}
func (*StrLit) exprNode()    {}
func (*NilLit) exprNode()    {}
func (*Ident) exprNode()     {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}
func (*Call) exprNode()      {}
func (*MatchExpr) exprNode() {}

func (*LitPattern) patternNode()  {}
func (*BindPattern) patternNode() {}
func (*WildPattern) patternNode() {}
