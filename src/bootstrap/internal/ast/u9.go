// The U9 cluster is the final surface: group 9 (concurrency — spawn, send, the
// chan constructor, select), group 10 (modules & programs — import and the
// top-level stmt-list), group 11 (resource cleanup — defer, del), and group 12
// (unsafe — the block-expression, the module-level declaration group, and inline
// assembly). Every node embeds base like the rest of the tree; 'zerg fmt'
// reprints each one and the round-trip oracle keeps the reprints honest.
//
// A source file's top level is a stmt-list (GRAMMAR group 1/10): a statement may
// be an import, a module-level binding, or a decorated declaration. So a
// declaration is ALSO a statement (the Decl interface embeds Stmt, see ast.go),
// and File.Items holds that mixed stmt-list.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// --- group 9: concurrency -----------------------------------------------------

// SpawnStmt is 'spawn expr' (GRAMMAR group 9): a fire-and-forget coroutine whose
// expr is a call.
type SpawnStmt struct {
	base
	Call Expr
}

// SendStmt is the send statement 'ch <- v' (GRAMMAR group 9): it sends Value on
// the channel Chan and yields no value.
type SendStmt struct {
	base
	Chan  Expr
	Value Expr
}

// ChanNew is the channel constructor expression 'chan[T](cap?)' (GRAMMAR group
// 9) — distinct from the chan TYPE 'chan[T]'. Cap is the optional capacity
// (nil for the unbuffered rendezvous form 'chan[T]()').
type ChanNew struct {
	base
	Elem Type
	Cap  Expr // nil when no capacity is given
}

// SelectArmKind tags which of the four select-arm shapes an arm is.
type SelectArmKind int

const (
	// SelectRecv is a receive arm '((id|_) :=)? <- expr => expr'.
	SelectRecv SelectArmKind = iota
	// SelectSend is a send arm 'expr <- expr => expr'.
	SelectSend
	// SelectDefault is the '_ => expr' arm (fires when nothing is ready). There is no
	// terminal arm: a select PICKS, and 'for select { … }' is the loop that ENDS when
	// every watched receive channel has.
	SelectDefault
)

// SelectStmt is 'select { arm+ }' (GRAMMAR group 9): the one multi-way wait, it
// runs the first ready arm. Loop marks the 'for select { arm+ }' spelling, which is
// the same wait as a LOOP — one ready arm per round, ending when every watched
// receive channel has. The seed does not lower either (tier 2), but it parses both
// so a program using one is refused by name rather than by a parse error.
type SelectStmt struct {
	base
	Arms []SelectArm
	Loop bool
}

// SelectArm is one arm of a select. Kind picks the shape: a recv arm carries the
// channel in Chan and an optional bind (Bind, HasBind — Bind is "_" for the
// '_ := <-ch' wild bind); a send arm carries the channel in Chan and the value
// in Value; the '_' arm carries only Body. It embeds base so each arm
// keeps its own line's lead/trail trivia (like a match arm).
type SelectArm struct {
	base
	Kind    SelectArmKind
	Bind    string // recv: the '(id|_) :=' bound name; "" when no bind
	HasBind bool   // recv: a '(id|_) :=' target was present
	Chan    Expr   // recv: the channel after '<-'; send: the channel before '<-'
	Value   Expr   // send: the value; nil otherwise
	Body    Expr
}

// --- group 10: modules & programs ---------------------------------------------

// ImportStmt is 'import (spec | '(' spec* ')')' (GRAMMAR group 10). Grouped marks
// the parenthesized multi-spec form; End holds any dangling trivia before the
// group's closing ')'.
type ImportStmt struct {
	base
	Grouped bool
	Specs   []*ImportSpec
	End     []token.Trivia
}

// ImportSpec is one import spec 'pub? path (as id)?' (GRAMMAR group 10): Path is
// the decoded string path, Alias the optional 'as' rename. It embeds base so a
// spec in the grouped form keeps its own line's lead/trail trivia.
type ImportSpec struct {
	base
	Pub   bool
	Path  string // the decoded string path, e.g. "util/text"
	Alias string // the 'as id' rename; "" when absent

	// Module is the canonical C-mangle tag the module loader (internal/module)
	// assigns to the module this spec resolves to: the canonical module path with
	// '/' replaced by '_' (so "util/text" -> "util_text", a flat stdlib "io" ->
	// "io"). It is empty until the loader resolves the graph; sema keys a member
	// mangling on it (falling back to the local binding name when unset, which
	// keeps a directly-checked file without a loader pass working as before).
	Module string

	// Reexports lists the canonical C-mangle tags of the modules the module THIS
	// spec resolves to re-exports through its own `import pub "…"` specs (one level,
	// Phase 1g S2). The loader fills it after the graph is resolved; sema uses it so
	// a member reached through this namespace that the module does not expose
	// directly resolves onto a re-exported module's public surface. Empty for a plain
	// import (or before the loader runs).
	Reexports []string
}

// --- group 11: resource cleanup -----------------------------------------------

// DeferStmt is 'defer expr' (GRAMMAR group 11): expr runs at the enclosing
// block's exit on every path out.
type DeferStmt struct {
	base
	Call Expr
}

// DelStmt is 'del identifier' (GRAMMAR group 11): it revokes the name's access to
// its storage now.
type DelStmt struct {
	base
	Name string
}

// --- group 12: unsafe ---------------------------------------------------------

// UnsafeExpr is the function-body 'unsafe { block }' block-expression (GRAMMAR
// group 12): raw operations are legal inside, and it yields the block's value.
// The module-level 'unsafe { … }' declaration group is UnsafeGroup instead —
// the two are told apart by position (DESIGN-1a §5's U6 cluster).
type UnsafeExpr struct {
	base
	Body *Block
}

// UnsafeGroup is the module-level 'unsafe { … }' declaration group (GRAMMAR group
// 12): every item inside is unsafe. An item is a decorated declaration or a
// binding (a 'mut' binding here is a module-private mutable global). It is a
// declaration, and — being a top-level item — also a statement. End holds any
// dangling trivia before the closing '}'.
type UnsafeGroup struct {
	base
	Decorators []*Decorator
	Items      []Stmt
	End        []token.Trivia
}

// AsmOpKind tags which of the four asm operand forms an operand is.
type AsmOpKind int

const (
	// AsmIn is 'in(constraint) expr' — a value into a register/constraint.
	AsmIn AsmOpKind = iota
	// AsmOut is 'out(constraint) lvalue' — a register into an lvalue.
	AsmOut
	// AsmInout is 'inout(constraint) lvalue' — both directions.
	AsmInout
	// AsmClobber is 'clobber(constraint, …)' — the registers the asm clobbers.
	AsmClobber
)

// AsmExpr is inline assembly 'asm(template (, operand)*)' (GRAMMAR group 12),
// legal only inside an unsafe context. Template is the decoded template string.
type AsmExpr struct {
	base
	Template string
	Operands []*AsmOperand
}

// AsmOperand is one asm operand. For AsmIn/AsmOut/AsmInout, Constraint is the
// single constraint string and Value is the value or lvalue expression; for
// AsmClobber, Clobbers holds the clobbered constraint strings. The operand heads
// 'out'/'inout'/'clobber' are contextual (special only here); 'in' is a keyword.
type AsmOperand struct {
	base
	Kind       AsmOpKind
	Constraint string   // the single constraint for in/out/inout
	Value      Expr     // the value (in) or lvalue (out/inout); nil for clobber
	Clobbers   []string // the clobbered constraints for AsmClobber
}

// --- marker methods -----------------------------------------------------------

func (*SpawnStmt) node()   {}
func (*SendStmt) node()    {}
func (*ChanNew) node()     {}
func (*SelectStmt) node()  {}
func (*ImportStmt) node()  {}
func (*ImportSpec) node()  {}
func (*DeferStmt) node()   {}
func (*DelStmt) node()     {}
func (*UnsafeExpr) node()  {}
func (*UnsafeGroup) node() {}
func (*AsmExpr) node()     {}
func (*AsmOperand) node()  {}

func (*SpawnStmt) stmtNode()   {}
func (*SendStmt) stmtNode()    {}
func (*SelectStmt) stmtNode()  {}
func (*ImportStmt) stmtNode()  {}
func (*DeferStmt) stmtNode()   {}
func (*DelStmt) stmtNode()     {}
func (*UnsafeGroup) stmtNode() {}

func (*ChanNew) exprNode()    {}
func (*UnsafeExpr) exprNode() {}
func (*AsmExpr) exprNode()    {}

// UnsafeGroup is a module-level declaration group (GRAMMAR group 12's
// declaration), and — being a top-level item — also a statement.
func (*UnsafeGroup) declNode() {}

// A declaration is also a statement (GRAMMAR group 1's decorated-decl): the top
// level is a stmt-list, so every declaration node satisfies Stmt.
func (*FuncDecl) stmtNode()   {}
func (*StructDecl) stmtNode() {}
func (*EnumDecl) stmtNode()   {}
func (*TypeDecl) stmtNode()   {}
func (*SpecDecl) stmtNode()   {}
func (*ImplDecl) stmtNode()   {}
func (*InitDecl) stmtNode()   {}
