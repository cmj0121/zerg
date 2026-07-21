// The U6 cluster is the group 6 surface (control flow & pattern matching) plus
// the two remaining group 8 nodes (guard-expr, raise). It adds the expression
// forms 'if' and 'guard' can take, the 'for' iterate/while/infinite loop's
// companions 'with' and 'raise', and the full pattern grammar the Phase 0 subset
// (literal / bare-name / wildcard) only sketched: variant, struct, tuple, list,
// or-, and as-patterns, plus the match-only range arm. Every node embeds base
// like the rest of the tree; 'zerg fmt' reprints each one and the round-trip
// oracle keeps the reprints honest.
//
// Two provisional nodes carry surface form the resolver settles in 1b: a bare
// name in pattern position is a NamePattern (variant-nullary vs binding, DESIGN
// §3b), and a range arm's bounds are compile-time constants kept verbatim.
package ast

// --- group 6: control-flow expressions ----------------------------------------

// IfExpr is 'if' in expression position (GRAMMAR group 4's primary): unlike the
// statement form its trailing 'else' is mandatory, and it yields the taken
// branch's block value. It shares IfBranch with IfStmt (a binding head sets
// Bind).
type IfExpr struct {
	base
	Branches []IfBranch
	Else     *Block // never nil — an if-expr requires a trailing 'else'
}

// GuardExpr is 'guard { block }' (GRAMMAR group 8): it demotes an abort inside
// the block back to a value, yielding Result[T] where T is the block's value.
type GuardExpr struct {
	base
	Body *Block
}

// --- group 6/8: statements ----------------------------------------------------

// WithStmt is 'with e (as id)? block' (GRAMMAR group 6): it binds a scoped
// resource for the block and guarantees its teardown on every exit. Var is empty
// when the resource is used only for its scope (no 'as').
type WithStmt struct {
	base
	Resource Expr
	Var      string // the 'as id' binding; "" when absent
	Body     *Block
}

// RaiseStmt is the simple statement 'raise e (from c)?' (GRAMMAR group 8): it
// aborts with an Err, recording c as the cause when 'from c' is present. (The
// '??'-right-side divergent 'raise' is instead a Diverge, see u3.go.)
type RaiseStmt struct {
	base
	Value Expr
	From  Expr // the 'from c' cause; nil when absent
}

// --- group 6: patterns --------------------------------------------------------

// NamePattern is the provisional bare-name pattern (DESIGN §3b): a name in
// pattern position that is not followed by '(' or '{' is either a nullary
// variant or a fresh binding — resolved in 1b. The surface name is kept verbatim.
type NamePattern struct {
	base
	Name string
}

// VariantPattern is 'type-name ( pattern-list )' (GRAMMAR group 6): a name
// FOLLOWED BY '(' is unambiguously a variant with a payload, so the parser emits
// it directly rather than the provisional NamePattern.
type VariantPattern struct {
	base
	Name  string
	Elems []Pattern
}

// StructPattern is 'type-name { field-pats }' (GRAMMAR group 6). Rest records a
// trailing '..' that ignores the remaining fields (a struct '..' only ignores;
// it never binds).
type StructPattern struct {
	base
	Name   string
	Fields []FieldPattern
	Rest   bool
}

// FieldPattern is one field of a struct pattern: the shorthand 'id' (Pat nil) or
// 'id: pattern'.
type FieldPattern struct {
	Name string
	Pat  Pattern // nil for the shorthand form
}

// TuplePattern is '( pattern-list )' (GRAMMAR group 6) — also a bind-target.
type TuplePattern struct {
	base
	Elems []Pattern
}

// ListPattern is '[ list-pat-elem* ]' (GRAMMAR group 6): a sequence of element
// patterns with at most one rest element '..name' (a bare '..' drops the run).
type ListPattern struct {
	base
	Elems []ListPatElem
}

// ListPatElem is one element of a list pattern: an ordinary pattern, or a rest
// '..name' (Rest true; Name is the binding, "" for a bare '..' that drops it).
type ListPatElem struct {
	Rest bool
	Name string  // the rest binding when Rest is true; "" for a bare '..'
	Pat  Pattern // the element pattern when Rest is false
}

// AsPattern is 'pattern-core as name' (GRAMMAR group 6): it binds the whole
// matched value to Name while Inner keeps destructuring.
type AsPattern struct {
	base
	Inner Pattern
	Name  string
}

// OrPattern is 'sub-pattern (| sub-pattern)+' (GRAMMAR group 6): it matches when
// any alternative does, and every alternative binds the same names.
type OrPattern struct {
	base
	Alts []Pattern
}

// RangeArm is a match-only range arm 'range-bound (.. range-bound? | ..= bound)'
// (GRAMMAR group 6): sugar for a containment guard, so it matches by '..'
// membership, not equality, and binds nothing. Hi is nil for the open form
// '500.. =>'; Inclusive marks the '..=' form. The bounds are compile-time
// constants ('-'? literal or an identifier) kept verbatim through the literal
// nodes' surface Text.
type RangeArm struct {
	base
	Lo        Expr
	Hi        Expr // nil for the open range 'lo.. =>'
	Inclusive bool
}

// --- marker methods -----------------------------------------------------------

func (*IfExpr) node()         {}
func (*GuardExpr) node()      {}
func (*WithStmt) node()       {}
func (*RaiseStmt) node()      {}
func (*NamePattern) node()    {}
func (*VariantPattern) node() {}
func (*StructPattern) node()  {}
func (*TuplePattern) node()   {}
func (*ListPattern) node()    {}
func (*AsPattern) node()      {}
func (*OrPattern) node()      {}
func (*RangeArm) node()       {}

func (*IfExpr) exprNode()    {}
func (*GuardExpr) exprNode() {}

func (*WithStmt) stmtNode()  {}
func (*RaiseStmt) stmtNode() {}

func (*NamePattern) patternNode()    {}
func (*VariantPattern) patternNode() {}
func (*StructPattern) patternNode()  {}
func (*TuplePattern) patternNode()   {}
func (*ListPattern) patternNode()    {}
func (*AsPattern) patternNode()      {}
func (*OrPattern) patternNode()      {}

// A range arm stands where a pattern does in a match arm, so it satisfies
// Pattern even though it binds nothing and matches by containment.
func (*RangeArm) patternNode() {}
