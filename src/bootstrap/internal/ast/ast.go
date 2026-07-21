// Package ast defines the abstract syntax tree the parser builds for the Phase 0
// subset of Zerg: functions, the simple statements, and the expression ladder.
// Nodes carry a source Span so sema and emit can anchor diagnostics; type
// information is attached separately by sema (see package sema) rather than stored
// on the nodes.
//
// The tree grows with the language; groups beyond Phase 0 (types, generics,
// pattern matching, concurrency) add node kinds here without disturbing these.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// Node is any AST node.
type Node interface{ node() }

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

// File is a whole source file: a sequence of declarations.
type File struct {
	Decls []Decl
}

// TypeRef names a type in source (Phase 0: a bare built-in name like int/float).
type TypeRef struct {
	Name string
	Span token.Span
}

// Param is one function parameter.
type Param struct {
	Name string
	Type *TypeRef
	Span token.Span
}

// FuncDecl is a top-level function declaration. A nil Ret means the function
// returns nil (no '-> type').
type FuncDecl struct {
	Name    string
	NameEnd token.Pos // end of the name, for signature diagnostics
	Params  []Param
	Ret     *TypeRef
	Body    *Block
	Span    token.Span
}

// Block is a brace-delimited statement list.
type Block struct {
	Stmts []Stmt
	Span  token.Span
}

// --- statements ---------------------------------------------------------------

// NopStmt is the empty statement 'nop'.
type NopStmt struct{ Span token.Span }

// BindStmt introduces a name: 'x := e', 'mut x := e', or 'x: T = e'. A nil Type
// means the type is inferred from Value.
type BindStmt struct {
	Mut   bool
	Const bool
	Name  string
	Type  *TypeRef
	Value Expr
	Span  token.Span
}

// AssignStmt reassigns an existing name: 'x = e' (Phase 0 targets a bare name).
type AssignStmt struct {
	Name  string
	Value Expr
	Span  token.Span
}

// PrintStmt is 'print e'.
type PrintStmt struct {
	Value Expr
	Span  token.Span
}

// ReturnStmt is 'return' with an optional value.
type ReturnStmt struct {
	Value Expr // nil for a bare 'return'
	Span  token.Span
}

// BreakStmt is 'break'.
type BreakStmt struct{ Span token.Span }

// ContinueStmt is 'continue'.
type ContinueStmt struct{ Span token.Span }

// IfBranch is one 'if'/'else if' condition and its body.
type IfBranch struct {
	Cond Expr
	Body *Block
}

// IfStmt is an if/else-if/else chain (statement form; no value).
type IfStmt struct {
	Branches []IfBranch
	Else     *Block // nil when there is no 'else'
	Span     token.Span
}

// ForStmt is 'for { }' (infinite when Cond is nil) or 'for cond { }' (while).
type ForStmt struct {
	Cond Expr
	Body *Block
	Span token.Span
}

// ExprStmt is an expression evaluated for effect (Phase 0: a call).
type ExprStmt struct {
	X    Expr
	Span token.Span
}

// --- expressions --------------------------------------------------------------

// IntLit is an integer literal with its already-parsed value.
type IntLit struct {
	Value int64
	Span  token.Span
}

// FloatLit is a floating-point literal.
type FloatLit struct {
	Value float64
	Span  token.Span
}

// BoolLit is 'true' or 'false'.
type BoolLit struct {
	Value bool
	Span  token.Span
}

// StrLit is a string literal with its decoded value.
type StrLit struct {
	Value string
	Span  token.Span
}

// NilLit is the 'nil' literal.
type NilLit struct{ Span token.Span }

// Ident is a name reference.
type Ident struct {
	Name string
	Span token.Span
}

// Unary is a prefix operation: -x, not x, ~x.
type Unary struct {
	Op   token.Kind
	X    Expr
	Span token.Span
}

// Binary is a binary operation; Op is the operator token kind.
type Binary struct {
	Op   token.Kind
	L, R Expr
	Span token.Span
}

// Call applies a callee to positional arguments (Phase 0: callee is a name).
type Call struct {
	Callee Expr
	Args   []Expr
	Span   token.Span
}

func (*File) node() {}

func (*NopStmt) node()      {}
func (*BindStmt) node()     {}
func (*AssignStmt) node()   {}
func (*PrintStmt) node()    {}
func (*ReturnStmt) node()   {}
func (*BreakStmt) node()    {}
func (*ContinueStmt) node() {}
func (*IfStmt) node()       {}
func (*ForStmt) node()      {}
func (*ExprStmt) node()     {}
func (*Block) node()        {}
func (*FuncDecl) node()     {}

func (*IntLit) node()   {}
func (*FloatLit) node() {}
func (*BoolLit) node()  {}
func (*StrLit) node()   {}
func (*NilLit) node()   {}
func (*Ident) node()    {}
func (*Unary) node()    {}
func (*Binary) node()   {}
func (*Call) node()     {}

func (*FuncDecl) declNode() {}

func (*NopStmt) stmtNode()      {}
func (*BindStmt) stmtNode()     {}
func (*AssignStmt) stmtNode()   {}
func (*PrintStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*IfStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*ExprStmt) stmtNode()     {}

func (*IntLit) exprNode()   {}
func (*FloatLit) exprNode() {}
func (*BoolLit) exprNode()  {}
func (*StrLit) exprNode()   {}
func (*NilLit) exprNode()   {}
func (*Ident) exprNode()    {}
func (*Unary) exprNode()    {}
func (*Binary) exprNode()   {}
func (*Call) exprNode()     {}
