package syntax

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Borrow checker — v0.3 Unit 3.
//
// The borrow checker runs AFTER typeck has annotated every Expr with a Type.
// Its sole job is to enforce v0.3's ownership rules:
//
//   * Composite values are MOVED on whole-binding rebind, return, struct/
//     tuple/list aggregation, BindPat in match, and tuple destructure. Reads
//     (print, len, clone, index, slice, field access, fn-call args, for-iter)
//     do NOT move.
//   * Function call composite arguments are implicit shared borrows: the
//     callee can read but cannot move/mutate. Inside a fn body, parameters of
//     composite type sit in BorrowedShared state.
//   * `for v in xs` shared-borrows xs for the body's duration; xs is restored
//     to its entry state after the loop.
//   * `match scrutinee { ... }` shared-borrows scrutinee during arm tests
//     and non-bind arm bodies. After match, if any arm consumes the scrutinee
//     (BindPat or destructuring pattern that binds inner names), scrutinee is
//     treated as Moved (worst-case static rule per PLAN tenth-man revision).
//   * Branch-agree rule: in `if/elif/else`, each branch is checked from a
//     snapshot of the entry state; at the join, all branches must agree on
//     each binding's end-state. A branch that diverges (return/break/continue
//     reached unconditionally) is exempt from the agreement check.
//   * Loop-body rule: a binding declared OUTSIDE a loop body cannot be moved
//     INSIDE that body — first iteration would succeed but subsequent
//     iterations would observe the moved value.
//   * Mutation: `xs[i] = v` and `push(xs, v)` require xs to be in Owned state.
//     Mutation of a BorrowedShared list (e.g. iterable inside its for body)
//     is rejected here; bindKind-based "must be mut" checks are still typeck's
//     job and run before the borrow checker.
//
// Primitives (int, float, bool, str, byte, rune) are tracked uniformly so the
// rules don't have a hole, but the checker NEVER reports an error against a
// primitive binding — moves of primitives are equivalent to copies.
// ---------------------------------------------------------------------------

// borrowState enumerates per-binding ownership state.
type borrowState int

const (
	bsOwned          borrowState = iota // fully usable; reads/writes/moves OK
	bsMoved                             // any use is an error
	bsBorrowedShared                    // read OK, mutation/move are errors
)

// borrowEntry is the per-binding tracking record. We carry the binding's type
// so error reporting can suppress diagnostics on primitive bindings, plus a
// "declared depth" so the loop-body rule can check whether a move targets a
// binding declared outside the current loop.
type borrowEntry struct {
	state        borrowState
	typ          *Type
	movePos      Position // last move site (used for use-after-move messages)
	borrowReason string   // human-readable reason for BorrowedShared state
	declDepth    int      // loop nesting depth at the time the name was declared
}

// borrowScope is one rung of the borrow-check scope stack. Names declared in
// a scope leave state behind when the scope pops.
type borrowScope struct {
	names  map[string]*borrowEntry
	parent *borrowScope
}

func newBorrowScope(parent *borrowScope) *borrowScope {
	return &borrowScope{names: map[string]*borrowEntry{}, parent: parent}
}

// lookup finds the *borrowEntry pointer (so callers can mutate state in place)
// and the scope that owns it.
func (s *borrowScope) lookup(name string) (*borrowEntry, *borrowScope) {
	for cur := s; cur != nil; cur = cur.parent {
		if e, ok := cur.names[name]; ok {
			return e, cur
		}
	}
	return nil, nil
}

// declare introduces name at this scope rung. Caller is responsible for
// guarding against same-scope redeclaration — typeck already enforces that
// rule and the borrow checker mirrors typeck's scope shape.
func (s *borrowScope) declare(name string, e *borrowEntry) {
	s.names[name] = e
}

// snapshotAll produces a deep copy of every name reachable from s up the
// parent chain. The branch-agree rule needs this to fork per-branch state and
// reset it between branches.
func (s *borrowScope) snapshotAll() map[string]borrowEntry {
	out := map[string]borrowEntry{}
	// Walk inner-to-outer; only keep the innermost binding per name.
	for cur := s; cur != nil; cur = cur.parent {
		for n, e := range cur.names {
			if _, seen := out[n]; !seen {
				out[n] = *e
			}
		}
	}
	return out
}

// applyTo restores the state of each entry in `snap` if the binding is still
// reachable. Names introduced after the snapshot are left untouched.
func (s *borrowScope) applyTo(snap map[string]borrowEntry) {
	for n, want := range snap {
		entry, _ := s.lookup(n)
		if entry == nil {
			continue
		}
		*entry = want
	}
}

// ---------------------------------------------------------------------------
// Branch-agree machinery.
//
// branchOutcome captures one branch's state at its exit point. diverged means
// control-flow left the function/loop body (return, break, continue) before
// the join; such a branch is exempt from the agreement check because no value
// from it ever participates in the join.
// ---------------------------------------------------------------------------

type branchOutcome struct {
	end      map[string]borrowEntry
	diverged bool
}

// ---------------------------------------------------------------------------
// BorrowError.
// ---------------------------------------------------------------------------

// BorrowError is the error returned by the borrow checker. We keep a separate
// type from TypeError so callers (and tests) can distinguish them at a glance,
// but both implement error and both come out of Check() unchanged.
type BorrowError struct {
	Pos     Position
	Message string
}

func (e *BorrowError) Error() string {
	return fmt.Sprintf("borrow error at %s: %s", e.Pos, e.Message)
}

func borrowErr(pos Position, format string, args ...any) error {
	return &BorrowError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Checker state.
// ---------------------------------------------------------------------------

// borrowChecker holds transient state for one borrow-check pass. The pass
// runs per-fn-body plus the top-level statement sequence; ownership state is
// purely lexical and resets at each fn boundary.
type borrowChecker struct {
	scope     *borrowScope
	loopDepth int
	// diverged is set when the current control-flow path has unconditionally
	// left the enclosing scope (return / break / continue). It's reset by
	// callers that step into a fresh branch.
	diverged bool
	// fns is the typeck function table; we use it to resolve callees and
	// recognise builtins (`len`, `push`, `clone`).
	fns map[string]fnSig
	// structs / enums / specs are typeck's resolved type tables. The borrow
	// checker uses them to resolve a receiver type from an ImplDecl.Type name
	// when registering `this` in a method-body scope.
	structs map[string]*Type
	enums   map[string]*Type
	specs   map[string]*Spec
}

// borrowCheck is the entry point. It runs after typeck has filled in every
// Expr.Type and the function table.
func borrowCheck(prog *Program, fns map[string]fnSig, structs, enums map[string]*Type, specs map[string]*Spec) error {
	c := &borrowChecker{
		fns:     fns,
		structs: structs,
		enums:   enums,
		specs:   specs,
	}
	// Top-level: only fn / struct / enum / const are admitted at file scope
	// per v0.1+ rules. We walk fn bodies; struct/enum/const decls have no
	// borrow-check work.
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
			continue
		}
		// v0.8 Unit 2: __builtin fn-decls carry no body — the host runtime
		// supplies the implementation. Borrow check has nothing to walk.
		if fn.BuiltinName != "" {
			continue
		}
		if err := c.checkFn(fn); err != nil {
			return err
		}
	}
	// v0.4: walk impl method bodies as if they were free fns plus an implicit
	// `this` BorrowedShared receiver. The receiver type is the impl's Type
	// (struct / enum); the borrow checker registers `this` in the body scope
	// so use-after-borrow rules fire on `y := this` / `return this`.
	for _, stmt := range prog.Statements {
		id, ok := stmt.(*ImplDecl)
		if !ok {
			continue
		}
		recv := c.lookupReceiverType(id.Type)
		for _, fn := range id.Methods {
			if err := c.checkMethodFn(fn, recv); err != nil {
				return err
			}
		}
	}
	// Walk spec default bodies similarly. `this` inside a default body is
	// BorrowedShared with the spec type — same borrow shape as a concrete
	// receiver, since the body never moves through it.
	for _, stmt := range prog.Statements {
		sd, ok := stmt.(*SpecDecl)
		if !ok {
			continue
		}
		var recv *Type
		if c.specs != nil {
			if sp := c.specs[sd.Name]; sp != nil {
				recv = sp.typ
			}
		}
		for _, m := range sd.Methods {
			if m.Body == nil {
				continue
			}
			fakeFn := &FnDecl{Pos: m.Pos, Name: m.Name, Params: m.Params, Return: m.Return, Body: m.Body}
			if err := c.checkMethodFn(fakeFn, recv); err != nil {
				return err
			}
		}
	}
	// Top-level let/mut/const at file scope is OUT at v0.1+ except const, but
	// the file-scope walker still needs to honour any ExprStmt / PrintStmt
	// that lands at the top level (REPL-style invocation). Reuse the fn-body
	// walker with a fresh scope for those.
	c.scope = newBorrowScope(nil)
	c.loopDepth = 0
	c.diverged = false
	for _, stmt := range prog.Statements {
		switch stmt.(type) {
		case *FnDecl, *StructDecl, *EnumDecl:
			continue
		case *SpecDecl, *ImplDecl:
			// v0.4 Unit 1: parser-only landing — typeck rejects these before
			// borrowCheck runs in practice, but stay defensive in case the
			// caller ever invokes borrowCheck on a tree that somehow slipped
			// past typeck.
			continue
		case *ImportDecl:
			// v0.5 Unit 1b: imports are resolved by the loader (Unit 2)
			// before borrow check sees the merged program. A stray
			// ImportDecl at this layer is a no-op.
			continue
		}
		if err := c.checkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// checkFn enters a fresh scope, registers each parameter as BorrowedShared
// (for composites) or Owned (for primitives), and walks the fn body. We
// register all parameters — including primitives — so a recursive walker
// doesn't have to special-case "is this name even tracked?".
func (c *borrowChecker) checkFn(fn *FnDecl) error {
	return c.checkMethodFn(fn, nil)
}

// checkMethodFn is the shared body walker for free fns and impl/spec methods.
// When `recv` is non-nil, the method body sees an implicit `this` binding in
// BorrowedShared state — symmetric with how a fn parameter of composite type
// is handled. `this` cannot be moved or returned; the existing consume()
// machinery enforces that uniformly via the BorrowedShared state.
func (c *borrowChecker) checkMethodFn(fn *FnDecl, recv *Type) error {
	saveScope, saveDepth, saveDiverged := c.scope, c.loopDepth, c.diverged
	c.scope = newBorrowScope(nil)
	c.loopDepth = 0
	c.diverged = false
	defer func() {
		c.scope = saveScope
		c.loopDepth = saveDepth
		c.diverged = saveDiverged
	}()

	if recv != nil {
		// `this` is the implicit receiver. Composite receivers (struct, enum,
		// spec) are BorrowedShared during the method body so any move-out via
		// `y := this` or `return this` is rejected by the consume() guard.
		state := bsOwned
		reason := ""
		if isComposite(recv) {
			state = bsBorrowedShared
			reason = "method receiver (implicit shared borrow at v0.4)"
		}
		c.scope.declare("this", &borrowEntry{
			state:        state,
			typ:          recv,
			borrowReason: reason,
			declDepth:    0,
		})
	}

	for _, p := range fn.Params {
		var t *Type
		if p.Type != nil {
			t = p.Type.Resolved
		}
		state := bsOwned
		reason := ""
		if isComposite(t) {
			state = bsBorrowedShared
			reason = "fn parameter (implicit shared borrow at v0.3)"
		}
		c.scope.declare(p.Name, &borrowEntry{
			state:        state,
			typ:          t,
			borrowReason: reason,
			declDepth:    0,
		})
	}
	for _, st := range fn.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// lookupReceiverType resolves a struct or enum name to its canonical *Type
// for use as an impl receiver. Returns nil when the type is unknown — the
// surrounding typeck pass would have already rejected an unknown type, so
// this fallback is only relevant for programs that somehow slip past typeck.
func (c *borrowChecker) lookupReceiverType(name string) *Type {
	if t, ok := c.structs[name]; ok {
		return t
	}
	if t, ok := c.enums[name]; ok {
		return t
	}
	return nil
}

// ---------------------------------------------------------------------------
// Composite predicate.
//
// Primitives (and TypeUnknown / TypeVoid) are exempt from move tracking.
// Lists, tuples, structs, enums are tracked. Tracking enums as composite is
// per PLAN — they're sized like ints at runtime but the surface uniformity
// keeps clone() working through the same path.
// ---------------------------------------------------------------------------

func isComposite(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeList, TypeTuple, TypeStruct, TypeEnum:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Statement walking.
// ---------------------------------------------------------------------------

func (c *borrowChecker) checkStmt(stmt Stmt) error {
	if c.diverged {
		// Code after an unconditional terminator is unreachable; we don't
		// need to walk it for borrow-check purposes (typeck has already
		// validated types). Return early to keep state stable.
		return nil
	}
	switch s := stmt.(type) {
	case *NopStmt:
		return nil
	case *PrintStmt:
		return c.checkExprRead(s.Expr)
	case *LetStmt:
		return c.checkLetLikeDecl(s.Pos, s.Name, s.Tuple, s.Value)
	case *MutStmt:
		return c.checkLetLikeDecl(s.Pos, s.Name, s.Tuple, s.Value)
	case *ConstStmt:
		return c.checkLetLikeDecl(s.Pos, s.Name, s.Tuple, s.Value)
	case *AssignStmt:
		return c.checkAssign(s)
	case *ExprStmt:
		if err := c.checkExprRead(s.Expr); err != nil {
			return err
		}
		return nil
	case *IfStmt:
		return c.checkIf(s)
	case *ForStmt:
		return c.checkFor(s)
	case *FnDecl:
		// Nested functions are rejected by typeck. Should never reach here.
		return nil
	case *ReturnStmt:
		return c.checkReturn(s)
	case *BreakStmt:
		if s.Guard != nil {
			if err := c.checkExprRead(s.Guard); err != nil {
				return err
			}
		}
		// A bare break unconditionally diverges; a guarded break only
		// diverges if the guard fires, so we conservatively treat both as
		// "potentially diverged" — but the branch-agree machinery only cares
		// about UNCONDITIONAL divergence. A guarded break inside an if-then
		// isn't diverged from the if's perspective (the join after the if
		// may still observe both states). Mark diverged only on unguarded.
		if s.Guard == nil {
			c.diverged = true
		}
		return nil
	case *ContinueStmt:
		if s.Guard != nil {
			if err := c.checkExprRead(s.Guard); err != nil {
				return err
			}
		}
		if s.Guard == nil {
			c.diverged = true
		}
		return nil
	case *StructDecl, *EnumDecl:
		return nil
	case *MatchStmt:
		return c.checkMatch(s)
	case *SpecDecl, *ImplDecl:
		// v0.4 Unit 1: typeck already rejected these, but if a borrow walk
		// somehow reaches them treat as a no-op rather than an internal
		// error so the typeck diagnostic is the one the user sees.
		return nil
	case *ImportDecl:
		// v0.5 Unit 1b: defensive no-op. Imports are top-level only (the
		// parser enforces this) so a borrow walk shouldn't reach them, but
		// staying lenient keeps the borrow check from raising an internal
		// error on a tree shape it doesn't understand.
		return nil
	case *SendStmt:
		return c.checkSend(s)
	case *SpawnStmt:
		return c.checkSpawn(s)
	case *DeferStmt:
		return c.checkDefer(s)
	case *SelectStmt:
		return c.checkSelect(s)
	case *AsmBlock:
		// v0.13 inline asm bodies are opaque to the borrow checker at U2.
		// The body is target-machine assembly — there is no Zerg expression
		// inside it for the move/share rules to apply to. `${name}` interps
		// are resolved by U3's typeck binder; once those reference real
		// scope bindings, the borrow check picks them up the same way it
		// handles any other expression-position read. Until then, the
		// AsmBlock contributes no move/share edges to the graph.
		return nil
	}
	return borrowErr(stmt.StmtPos(), "internal: unhandled statement %T", stmt)
}

// ---------------------------------------------------------------------------
// Declarations: immutable / mut / const + tuple destructure.
// ---------------------------------------------------------------------------

// checkLetLikeDecl handles all three of immutable/mut/const, which share the
// same borrow-check shape: walk the RHS as a "consume" site (move-out of the
// RHS happens here when the RHS is a single named binding), then introduce
// the new binding(s) as Owned in the current scope.
func (c *borrowChecker) checkLetLikeDecl(pos Position, name string, tuple *TupleBinding, value Expr) error {
	if tuple != nil {
		return c.checkTupleDestructure(pos, tuple, value)
	}
	// Whole-binding rebind. If the RHS is a bare ident or `this`, this is a
	// move site; otherwise the RHS is a temporary value with no source binding
	// to move.
	if id, ok := value.(*IdentExpr); ok {
		if err := c.consume(id, "moved by binding rebind"); err != nil {
			return err
		}
	} else if th, ok := value.(*ThisExpr); ok {
		if err := c.consumeThis(th, "moved by binding rebind"); err != nil {
			return err
		}
	} else {
		if err := c.checkExprConsume(value); err != nil {
			return err
		}
	}
	t := value.Type()
	c.scope.declare(name, &borrowEntry{
		state:     bsOwned,
		typ:       t,
		declDepth: c.loopDepth,
	})
	return nil
}

// checkTupleDestructure handles tuple-destructure binding `(a, b) := pair` (and the mut form). The
// RHS must be either a bare ident (which we move) or a fresh tuple value.
// Each name on the LHS becomes Owned with its element type.
func (c *borrowChecker) checkTupleDestructure(_ Position, tb *TupleBinding, value Expr) error {
	// If the RHS is a single ident, move-out.
	if id, ok := value.(*IdentExpr); ok {
		if err := c.consume(id, "moved by tuple destructure"); err != nil {
			return err
		}
	} else {
		if err := c.checkExprConsume(value); err != nil {
			return err
		}
	}
	tt := value.Type()
	if tt == nil || tt.Kind != TypeTuple {
		// typeck would have rejected this already; defensively pass.
		return nil
	}
	for i, n := range tb.Names {
		var et *Type
		if i < len(tt.Tuple) {
			et = tt.Tuple[i]
		}
		c.scope.declare(n, &borrowEntry{
			state:     bsOwned,
			typ:       et,
			declDepth: c.loopDepth,
		})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Assignment.
// ---------------------------------------------------------------------------

// checkAssign handles `x = value` and `xs[i] = value`. The bindKind / mut
// rules are typeck's responsibility; here we enforce that the target is
// Owned (not Moved or BorrowedShared) and that any RHS move-out is recorded.
func (c *borrowChecker) checkAssign(s *AssignStmt) error {
	switch lhs := s.Target.(type) {
	case *IdentExpr:
		// Plain `x = value`. typeck has already required mut. The RHS is a
		// fresh value or another binding; if RHS is an ident and value is
		// composite, that's a move-out of RHS into x. We DO NOT flip x to
		// Moved — x's state stays as it was (Owned) because we just wrote
		// to it. But we should reject if x is currently BorrowedShared
		// (mutating a borrowed binding) — typeck would already reject
		// "xs := ..." mutation, so this guard mostly helps fn params.
		if e, _ := c.scope.lookup(lhs.Name); e != nil {
			if e.state == bsBorrowedShared && isComposite(e.typ) {
				return borrowErr(s.Pos, "cannot mutate %q while it is borrowed (%s)", lhs.Name, e.borrowReason)
			}
			if e.state == bsMoved && isComposite(e.typ) {
				return borrowErr(s.Pos, "use of moved value: %q (moved at %s)", lhs.Name, e.movePos)
			}
		}
		// Walk RHS for moves.
		if id, ok := s.Value.(*IdentExpr); ok {
			if err := c.consume(id, "moved by assignment"); err != nil {
				return err
			}
		} else {
			if err := c.checkExprConsume(s.Value); err != nil {
				return err
			}
		}
		// Identifier assignment in v0.3 is only meaningful for primitives
		// (mut int, etc.) since composite "rebind via =" is out of scope at
		// v0.3 (only the := binding form moves). We leave the identifier's
		// own state as Owned regardless.
		return nil
	case *IndexExpr:
		return c.checkIndexAssign(s, lhs)
	}
	return borrowErr(s.Pos, "internal: unsupported assignment target %T", s.Target)
}

// checkIndexAssign handles `xs[i] = v`. The receiver must be a bare ident
// (chained indexing was rejected at parse time) bound mut, and the binding
// must be in Owned state. The value's type matches the list's element type
// (already enforced by typeck below — we re-validate the state-side here).
func (c *borrowChecker) checkIndexAssign(s *AssignStmt, idx *IndexExpr) error {
	id, ok := idx.Receiver.(*IdentExpr)
	if !ok {
		return borrowErr(s.Pos, "list-element assignment requires a named list on the left")
	}
	e, _ := c.scope.lookup(id.Name)
	if e == nil {
		return borrowErr(id.Pos, "undefined name %q", id.Name)
	}
	if e.state == bsMoved {
		return borrowErr(s.Pos, "use of moved value: %q (moved at %s)", id.Name, e.movePos)
	}
	if e.state == bsBorrowedShared {
		return borrowErr(s.Pos, "cannot mutate %q while it is borrowed (%s)", id.Name, e.borrowReason)
	}
	// Walk the index (read-only).
	if err := c.checkExprRead(idx.Index); err != nil {
		return err
	}
	// Walk the value: if it's an ident with composite type, that's a move.
	if rid, ok := s.Value.(*IdentExpr); ok {
		if err := c.consume(rid, "moved by list-element assignment"); err != nil {
			return err
		}
	} else {
		if err := c.checkExprConsume(s.Value); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Return.
// ---------------------------------------------------------------------------

// checkReturn validates the return value. If it's a bare ident, that's a
// move site; otherwise we walk it for embedded moves. Setting diverged so
// downstream code in the current branch is treated as unreachable.
func (c *borrowChecker) checkReturn(s *ReturnStmt) error {
	if s.Guard != nil {
		if err := c.checkExprRead(s.Guard); err != nil {
			return err
		}
	}
	if s.Value != nil {
		if id, ok := s.Value.(*IdentExpr); ok {
			if err := c.consume(id, "moved by return"); err != nil {
				return err
			}
		} else if th, ok := s.Value.(*ThisExpr); ok {
			if err := c.consumeThis(th, "moved by return"); err != nil {
				return err
			}
		} else {
			if err := c.checkExprConsume(s.Value); err != nil {
				return err
			}
		}
	}
	if s.Guard == nil {
		c.diverged = true
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reads vs. consumes.
//
// "Read" means the expression can be observed (printed, indexed, sliced,
// passed to a fn, etc.) without flipping any source binding to Moved. The
// distinction is at expression position: an *IdentExpr in a value position
// only moves when the surrounding statement designates it as a consume site
// (let-rebind, return, struct/tuple/list element, push/clone receivers are
// observation rather than consumption).
//
// checkExprRead and checkExprConsume share most of their walking logic via
// checkExprWalk; the boolean `consuming` controls whether the leaf
// IdentExprs at *aggregation* points get moved. The distinction matters at
// list/tuple/struct literal construction: `[a, b]` consumes a and b, even
// though `a` and `b` are in expression position inside the literal.
// ---------------------------------------------------------------------------

// checkExprRead walks expr in read mode — every IdentExpr leaf is a read,
// not a move. Used for print, expr-statement, condition expressions, indices,
// guards, and fn-call arguments (which are implicit shared borrows).
func (c *borrowChecker) checkExprRead(expr Expr) error {
	return c.walkExpr(expr, false)
}

// checkExprConsume walks expr in consume mode — at AGGREGATION points
// (ListLit, TupleLit, StructLit FieldInit values), bare ident leaves move
// the source binding. Used at sites where the surrounding statement consumes
// the expression result: binding RHS, return, etc. The top-level *IdentExpr
// case is handled by the caller (consume()) so this only matters for the
// nested aggregate case.
func (c *borrowChecker) checkExprConsume(expr Expr) error {
	return c.walkExpr(expr, true)
}

// walkExpr is the unified expression walker. `consuming` is true when the
// surrounding context wants the expression to consume named bindings at
// aggregation boundaries (literals).
func (c *borrowChecker) walkExpr(expr Expr, consuming bool) error {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *IntLit, *FloatLit, *StringLit, *BoolLit, *RuneLit, *NilLit:
		return nil
	case *IdentExpr:
		// At a leaf, a bare ident is a READ. Use-after-move is reported here.
		entry, _ := c.scope.lookup(e.Name)
		if entry == nil {
			return nil // typeck would have rejected
		}
		if entry.state == bsMoved && isComposite(entry.typ) {
			return borrowErr(e.Pos, "use of moved value: %q (moved at %s)", e.Name, entry.movePos)
		}
		return nil
	case *BinaryExpr:
		if err := c.walkExpr(e.Left, false); err != nil {
			return err
		}
		return c.walkExpr(e.Right, false)
	case *UnaryExpr:
		return c.walkExpr(e.Operand, false)
	case *CallExpr:
		return c.walkCall(e)
	case *ParenExpr:
		return c.walkExpr(e.Inner, consuming)
	case *RangeExpr:
		if err := c.walkExpr(e.Start, false); err != nil {
			return err
		}
		return c.walkExpr(e.End, false)
	case *ListLit:
		// Each element: if it's a bare ident, it's a move into the list.
		// Otherwise walk recursively in consume mode for nested aggregates.
		for _, el := range e.Elements {
			if err := c.consumeOrWalk(el, "moved by list literal element"); err != nil {
				return err
			}
		}
		return nil
	case *TupleLit:
		for _, el := range e.Elements {
			if err := c.consumeOrWalk(el, "moved by tuple literal element"); err != nil {
				return err
			}
		}
		return nil
	case *StructLit:
		for _, fi := range e.Fields {
			if err := c.consumeOrWalk(fi.Value, fmt.Sprintf("moved by struct field %q init", fi.Name)); err != nil {
				return err
			}
		}
		return nil
	case *IndexExpr:
		// Index read: the receiver is observed (NOT moved) and the index is
		// a primitive expression. If the receiver is a bare ident, do NOT
		// move it — this is a read site.
		if err := c.walkExpr(e.Receiver, false); err != nil {
			return err
		}
		return c.walkExpr(e.Index, false)
	case *SliceExpr:
		if err := c.walkExpr(e.Receiver, false); err != nil {
			return err
		}
		if err := c.walkExpr(e.Low, false); err != nil {
			return err
		}
		return c.walkExpr(e.High, false)
	case *FieldAccessExpr:
		// Field access: receiver is read, not moved. Enum variant access
		// (Color.Red) has the receiver as the enum type identifier — typeck
		// has set Type already; we treat it as a read like any other.
		return c.walkExpr(e.Receiver, false)
	case *MethodCallExpr:
		// If typeck lowered the call to a fn-form CallExpr (list[T] receiver
		// + push / clone / len), walk that synthetic call so the v0.3 push
		// borrow rule (Owned receiver required) fires. Otherwise treat the
		// receiver and args as read positions — same shape as a fn-call.
		if e.LoweredCall != nil {
			return c.walkCall(e.LoweredCall)
		}
		if err := c.walkExpr(e.Receiver, false); err != nil {
			return err
		}
		for _, a := range e.Args {
			if err := c.walkExpr(a, false); err != nil {
				return err
			}
		}
		return nil
	case *ThisExpr:
		// `this` is bound by the receiver as BorrowedShared inside method
		// bodies. Reading is fine; consume sites (let-rebind, return,
		// aggregation) route through consumeThis() and reject the move.
		entry, _ := c.scope.lookup("this")
		if entry == nil {
			return nil // typeck would have rejected
		}
		if entry.state == bsMoved && isComposite(entry.typ) {
			return borrowErr(e.Pos, "use of moved value: %q (moved at %s)", "this", entry.movePos)
		}
		return nil
	case *EnumLit:
		// v0.6: typeck lowers MethodCallExpr / FieldAccessExpr enum-variant
		// constructions to EnumLit, and Unit 3's T → T? lift wraps a value
		// in a synthetic EnumLit. Each payload position is a consume site
		// (the same shape as a struct field or list element).
		for _, p := range e.Payload {
			if err := c.consumeOrWalk(p, "moved by enum payload"); err != nil {
				return err
			}
		}
		return nil
	case *PropagateExpr:
		// `?` is a move-out site on its receiver: the Ok / Some payload moves
		// out as the expression's value, and the Err / None path returns the
		// original (also a move). Mirror the let-rebind / return shape — bare
		// idents move; nested aggregates walk in consume mode.
		return c.consumeOrWalk(e.Inner, "moved by ? propagation")
	case *ChanConstructorExpr:
		// v0.7 Unit 2: typeck-only landing. The capacity expression is read
		// (no move). Element type is metadata; nothing to walk.
		if e.Capacity != nil {
			return c.walkExpr(e.Capacity, false)
		}
		return nil
	case *RecvExpr:
		// v0.7 Unit 2: typeck-only landing. The channel operand is read (the
		// channel handle isn't moved); the resulting value is owned by the
		// caller. Unit 5 will refine the move-in semantics.
		return c.walkExpr(e.Chan, false)
	case *AnonFnExpr:
		return c.walkAnonFn(e)
	case *CoalesceExpr:
		// `??` LHS is the match-scrutinee; whichever arm fires moves the
		// bound value out (Some(v) ⇒ v moves, None ⇒ rhs moves). The borrow
		// check is conservative — both arms are assumed to fire, so LHS is
		// treated as consumed at the operator. RHS is also a consume site
		// for the same reason: when the None arm fires, an ident-rhs would
		// be moved out of into the result.
		if err := c.consumeOrWalk(e.Left, "moved by ?? coalesce (LHS consumed conservatively)"); err != nil {
			return err
		}
		return c.consumeOrWalk(e.Right, "moved by ?? coalesce (RHS arm)")
	}
	return borrowErr(expr.ExprPos(), "internal: unhandled expression %T", expr)
}

// consumeOrWalk is the helper for aggregation positions: if the sub-expression
// is a bare ident or `this`, treat it as a move; otherwise walk it in read
// mode (its own internal aggregations will already trigger moves recursively
// if any land on bare idents).
func (c *borrowChecker) consumeOrWalk(e Expr, why string) error {
	if id, ok := e.(*IdentExpr); ok {
		return c.consume(id, why)
	}
	if th, ok := e.(*ThisExpr); ok {
		return c.consumeThis(th, why)
	}
	return c.walkExpr(e, true)
}

// consumeThis is the ThisExpr analogue of consume. The borrow checker tracks
// `this` as a BorrowedShared composite when the receiver is a struct / enum /
// spec, so any consume site (binding rebind, return, aggregation element)
// fails with the same diagnostic shape used for borrowed names. When the
// receiver is a primitive (e.g. a future v0.6 spec impl on int), tracking is
// disabled and consumeThis is a no-op.
func (c *borrowChecker) consumeThis(th *ThisExpr, why string) error {
	_ = why
	entry, _ := c.scope.lookup("this")
	if entry == nil {
		// Outside a method body — typeck has already rejected this case.
		return nil
	}
	if !isComposite(entry.typ) {
		return nil
	}
	if entry.state == bsBorrowedShared {
		return borrowErr(th.Pos, "cannot move borrowed value: %q (%s)", "this", entry.borrowReason)
	}
	if entry.state == bsMoved {
		return borrowErr(th.Pos, "use of moved value: %q (moved at %s)", "this", entry.movePos)
	}
	// Receiver was Owned (impossible at v0.4 because every method-body `this`
	// starts BorrowedShared, but the branch keeps the helper symmetric with
	// consume()).
	entry.state = bsMoved
	entry.movePos = th.Pos
	return nil
}

// consume marks the source binding as Moved, after first validating that it
// is currently usable (not already Moved, not BorrowedShared). Composite
// only — primitive moves are no-ops because copying primitives is fine.
//
// `why` is reserved for a future, richer "move site" message. It is not
// surfaced today; the existing diagnostics already pin source position
// (id.Pos) and prior-move position (entry.movePos), which has been enough
// for every test in the v0.3 corpus. Keep the parameter so call sites read
// like documentation of intent.
func (c *borrowChecker) consume(id *IdentExpr, why string) error {
	_ = why
	entry, _ := c.scope.lookup(id.Name)
	if entry == nil {
		return nil // typeck would have rejected
	}
	if !isComposite(entry.typ) {
		// Primitives don't get tracked-with-errors — they always copy.
		return nil
	}
	if entry.state == bsMoved {
		return borrowErr(id.Pos, "use of moved value: %q (moved at %s)", id.Name, entry.movePos)
	}
	if entry.state == bsBorrowedShared {
		return borrowErr(id.Pos, "cannot move borrowed value: %q (%s)", id.Name, entry.borrowReason)
	}
	// Loop-body rule: a binding declared OUTSIDE the current loop body
	// cannot be moved INSIDE it. declDepth captures the loop depth at
	// declaration time; if it's strictly less than the current depth, the
	// binding lives outside the innermost active loop.
	if c.loopDepth > 0 && entry.declDepth < c.loopDepth {
		return borrowErr(id.Pos, "cannot move %q inside loop body — first iteration would succeed but subsequent iterations would observe a moved value", id.Name)
	}
	entry.state = bsMoved
	entry.movePos = id.Pos
	return nil
}

// walkCall checks a function call expression. Every argument is an implicit
// shared borrow at v0.3: walk in read mode regardless of what's inside.
// Builtin special-cases:
//   * len(xs) — read
//   * clone(xs) — read (not a move)
//   * push(xs, v) — first arg must be Owned (typeck already required mut);
//     mutation through a BorrowedShared list is rejected here.
func (c *borrowChecker) walkCall(call *CallExpr) error {
	ident, ok := call.Callee.(*IdentExpr)
	if !ok {
		// typeck rejects non-ident callees; defensively walk.
		return c.walkExpr(call.Callee, false)
	}
	if sig, ok := c.fns[ident.Name]; ok && sig.builtin && ident.Name == "push" {
		if len(call.Args) == 2 {
			id, ok := call.Args[0].(*IdentExpr)
			if !ok {
				return c.walkExpr(call.Args[1], false)
			}
			entry, _ := c.scope.lookup(id.Name)
			if entry == nil {
				return nil
			}
			if entry.state == bsMoved {
				return borrowErr(id.Pos, "use of moved value: %q (moved at %s)", id.Name, entry.movePos)
			}
			if entry.state == bsBorrowedShared {
				return borrowErr(call.Pos, "cannot push to %q while it is borrowed (%s)", id.Name, entry.borrowReason)
			}
			return c.walkExpr(call.Args[1], false)
		}
	}
	// All other calls: every argument is read-only (implicit shared borrow).
	for _, a := range call.Args {
		if err := c.walkExpr(a, false); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// if / elif / else — the branch-agree rule.
// ---------------------------------------------------------------------------

// checkIf snapshots state at entry, then runs each branch in turn from a
// reset of that snapshot, recording the end-state. Branches that diverged
// (return/break/continue reached unconditionally) are exempt from the
// agreement check. Surviving branches must agree on every binding's state.
func (c *borrowChecker) checkIf(s *IfStmt) error {
	if err := c.checkExprRead(s.Cond); err != nil {
		return err
	}
	entry := c.scope.snapshotAll()

	type branch struct {
		body *Block
		pos  Position
		desc string
	}
	branches := []branch{{body: s.Then, pos: s.Then.Pos, desc: "if branch"}}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		if err := c.checkExprRead(ec.Cond); err != nil {
			return err
		}
		branches = append(branches, branch{body: ec.Body, pos: ec.Pos, desc: fmt.Sprintf("elif branch #%d", i+1)})
	}
	if s.Else != nil {
		branches = append(branches, branch{body: s.Else, pos: s.Else.Pos, desc: "else branch"})
	}

	outcomes := make([]branchOutcome, len(branches))
	for i, br := range branches {
		c.scope.applyTo(entry)
		savedDiverged := c.diverged
		c.diverged = false
		err := c.checkBlock(br.body)
		brDiverged := c.diverged
		c.diverged = savedDiverged
		if err != nil {
			return err
		}
		outcomes[i] = branchOutcome{end: c.scope.snapshotAll(), diverged: brDiverged}
	}

	// If there's no else clause, the implicit no-op branch starts at entry
	// and ends at entry (no divergence).
	if s.Else == nil {
		outcomes = append(outcomes, branchOutcome{end: entry, diverged: false})
	}

	// Branch-agree: for every binding declared OUTSIDE the if (i.e. present
	// in entry), all non-diverged branches must agree on its end state.
	if err := c.joinBranches(s.Pos, entry, outcomes); err != nil {
		return err
	}

	// Adopt the agreed state. Pick any non-diverged outcome; if every branch
	// diverged, mark current path as diverged too.
	allDiverged := true
	for _, o := range outcomes {
		if !o.diverged {
			allDiverged = false
			c.scope.applyTo(o.end)
			break
		}
	}
	if allDiverged {
		c.diverged = true
		// Restore entry so any later (unreachable) walk sees a consistent
		// scope. checkStmt's diverged-early-out prevents further work.
		c.scope.applyTo(entry)
	}
	return nil
}

// joinBranches verifies that every binding present at entry has the same
// end-state across all non-diverged outcomes. Disagreement is reported with
// a precise diagnostic naming the binding and the conflicting branches.
func (c *borrowChecker) joinBranches(pos Position, entry map[string]borrowEntry, outcomes []branchOutcome) error {
	// Collect non-diverged outcomes only.
	var live []branchOutcome
	for _, o := range outcomes {
		if !o.diverged {
			live = append(live, o)
		}
	}
	if len(live) <= 1 {
		return nil
	}
	for name, eEntry := range entry {
		if !isComposite(eEntry.typ) {
			continue
		}
		// Find the first non-diverged outcome's state for this name.
		baseIdx := 0
		baseState := live[0].end[name].state
		for i := 1; i < len(live); i++ {
			st := live[i].end[name].state
			if st != baseState {
				return borrowErr(pos,
					"branch states disagree on %q: %s — make the move explicit on every branch or move it before the if",
					name, formatBranchState(baseIdx, baseState, i, st))
			}
		}
	}
	return nil
}

// formatBranchState renders the "branch A says X, branch B says Y" suffix in
// a single line. Branch numbering is 1-based for user-facing output.
func formatBranchState(ai int, as borrowState, bi int, bs borrowState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "branch %d says %s, branch %d says %s", ai+1, stateLabel(as), bi+1, stateLabel(bs))
	return b.String()
}

func stateLabel(s borrowState) string {
	switch s {
	case bsOwned:
		return "owned"
	case bsMoved:
		return "moved"
	case bsBorrowedShared:
		return "borrowed"
	}
	return "unknown"
}

// checkBlock walks a brace-block in a fresh scope. Names declared inside the
// block leave with the scope; outer-scope state mutations persist.
func (c *borrowChecker) checkBlock(b *Block) error {
	c.scope = newBorrowScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for _, st := range b.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// for loops.
// ---------------------------------------------------------------------------

// checkFor walks all four for-loop shapes. The list-iter form holds an
// implicit shared borrow on the iterable for the body's duration. All forms
// run the body under an incremented loopDepth so the move-inside-loop rule
// can fire.
func (c *borrowChecker) checkFor(s *ForStmt) error {
	c.loopDepth++
	defer func() { c.loopDepth-- }()
	switch s.Kind {
	case ForInfinite:
		return c.checkLoopBody(s.Body)
	case ForCond:
		if err := c.checkExprRead(s.Cond); err != nil {
			return err
		}
		return c.checkLoopBody(s.Body)
	case ForRange:
		if err := c.checkExprRead(s.Range.Start); err != nil {
			return err
		}
		if err := c.checkExprRead(s.Range.End); err != nil {
			return err
		}
		// Body runs in a fresh scope with the loop var (int) bound Owned.
		c.scope = newBorrowScope(c.scope)
		defer func() { c.scope = c.scope.parent }()
		c.scope.declare(s.Var, &borrowEntry{
			state:     bsOwned,
			typ:       TInt(),
			declDepth: c.loopDepth,
		})
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				return err
			}
		}
		// A bare `break` inside the loop body shouldn't propagate divergence
		// past the loop boundary — control flow re-enters the surrounding
		// scope. Reset diverged here.
		c.diverged = false
		return nil
	case ForIter:
		// Walk the iterable (no move). If it's a bare ident, mark it as
		// BorrowedShared for the body's duration.
		if err := c.walkExpr(s.Iter, false); err != nil {
			return err
		}
		var iterEntry *borrowEntry
		var iterPrior borrowEntry
		if id, ok := s.Iter.(*IdentExpr); ok {
			if e, _ := c.scope.lookup(id.Name); e != nil {
				if e.state == bsMoved {
					return borrowErr(id.Pos, "use of moved value: %q (moved at %s)", id.Name, e.movePos)
				}
				// Save and override for the body.
				iterEntry = e
				iterPrior = *e
				e.state = bsBorrowedShared
				e.borrowReason = "borrowed by for-iter loop"
			}
		}
		// Loop-element type: if list[T] with composite T, the element
		// binding is in BorrowedShared state for the body.
		var elemType *Type
		var elemBorrowed bool
		if t := s.Iter.Type(); t != nil && t.Kind == TypeList {
			elemType = t.Element
			elemBorrowed = isComposite(elemType)
		}
		c.scope = newBorrowScope(c.scope)
		state := bsOwned
		reason := ""
		if elemBorrowed {
			state = bsBorrowedShared
			reason = "borrowed from for-iter iterable"
		}
		c.scope.declare(s.Var, &borrowEntry{
			state:        state,
			typ:          elemType,
			borrowReason: reason,
			declDepth:    c.loopDepth,
		})
		var bodyErr error
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				bodyErr = err
				break
			}
		}
		c.scope = c.scope.parent
		// Restore the iterable's pre-loop state.
		if iterEntry != nil {
			*iterEntry = iterPrior
		}
		if bodyErr != nil {
			return bodyErr
		}
		c.diverged = false
		return nil
	case ForChan:
		// v0.7 Unit 2: typeck-only landing for `for v in ch`. The full
		// borrow rules (move-in per-iteration) land in Unit 5; today we
		// walk the channel as a read and bind v as Owned in the body
		// scope so basic move-out / use-after-move from inside the body
		// fires the standard diagnostics.
		if err := c.walkExpr(s.Iter, false); err != nil {
			return err
		}
		var elemType *Type
		if t := s.Iter.Type(); t != nil && t.Kind == TypeChan {
			elemType = t.Element
		}
		c.scope = newBorrowScope(c.scope)
		c.scope.declare(s.Var, &borrowEntry{
			state:     bsOwned,
			typ:       elemType,
			declDepth: c.loopDepth,
		})
		var bodyErr error
		for _, st := range s.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				bodyErr = err
				break
			}
		}
		c.scope = c.scope.parent
		if bodyErr != nil {
			return bodyErr
		}
		c.diverged = false
		return nil
	}
	return borrowErr(s.Pos, "internal: unknown for kind")
}

// checkLoopBody walks a loop body that has no implicit borrow on an iterable
// (infinite / cond shapes). Loop-body move detection still applies — the body
// runs at an elevated loopDepth.
func (c *borrowChecker) checkLoopBody(b *Block) error {
	c.scope = newBorrowScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	for _, st := range b.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	c.diverged = false
	return nil
}

// ---------------------------------------------------------------------------
// match.
// ---------------------------------------------------------------------------

// checkMatch walks a match statement. The scrutinee is shared-borrowed during
// arm tests + non-bind arm bodies. Each arm is checked from a fresh snapshot
// of the entry state. After match: if any arm is a BindPat or a destructuring
// pattern that introduces inner names, treat the scrutinee as Moved (the
// PLAN tenth-man worst-case rule). Otherwise the scrutinee remains Owned.
func (c *borrowChecker) checkMatch(s *MatchStmt) error {
	if err := c.walkExpr(s.Subject, false); err != nil {
		return err
	}

	// Decide whether ANY arm pattern would consume the subject. Bind-style
	// patterns (BindPat) and destructuring patterns that introduce inner
	// names (TuplePat / StructPat) consume; literal / wildcard / enum / non-
	// binding struct patterns observe.
	consumes := false
	for _, arm := range s.Arms {
		if patternConsumes(arm.Pattern) {
			consumes = true
			break
		}
	}

	// Identify the scrutinee binding (if it's a bare ident) so we can borrow
	// it for the duration and optionally flip to Moved on exit.
	var subjEntry *borrowEntry
	var subjId *IdentExpr
	if id, ok := s.Subject.(*IdentExpr); ok {
		if e, _ := c.scope.lookup(id.Name); e != nil && isComposite(e.typ) {
			subjEntry = e
			subjId = id
		}
	}
	// Capture the entry state BEFORE we override to BorrowedShared. The
	// post-match flip rule depends on this: if the scrutinee was already
	// BorrowedShared at entry (e.g. a fn parameter), it never owned the
	// value, so we must NOT flip it to Moved at exit even if some arm's
	// destructure pattern would otherwise be a "consume". A BindPat arm on
	// a BorrowedShared scrutinee is rejected separately at the bind site
	// because BindPat is genuinely a move and `consume` already refuses
	// to move a borrowed value.
	var entryState borrowState
	var subjPrior borrowEntry
	if subjEntry != nil {
		entryState = subjEntry.state
		subjPrior = *subjEntry
		subjEntry.state = bsBorrowedShared
		subjEntry.borrowReason = "borrowed by match"
	}

	// Walk arms. Each arm runs in a fresh snapshot from the post-borrow
	// state. We keep the arm-level diverged separate so an arm's `return`
	// doesn't mark the outer match as diverged unless all arms diverge.
	entry := c.scope.snapshotAll()
	outcomes := make([]branchOutcome, 0, len(s.Arms))
	for _, arm := range s.Arms {
		c.scope.applyTo(entry)
		c.scope = newBorrowScope(c.scope)
		// Bind names introduced by the arm pattern. Inside a Bind-arm body,
		// the scrutinee is consumed into the bound name — so we ALSO flip
		// the scrutinee to Moved while the arm body runs (then restore via
		// the entry-state apply at the next iteration).
		bound := bindPatternNames(arm.Pattern, s.Subject.Type())
		for _, b := range bound {
			c.scope.declare(b.name, &borrowEntry{
				state:     bsOwned,
				typ:       b.typ,
				declDepth: c.loopDepth,
			})
		}
		// If this arm is a BindPat (whole-scrutinee bind), mark scrutinee as
		// Moved during the arm body — BindPat is a genuine consume. A
		// BindPat arm on a BorrowedShared scrutinee (e.g. a fn parameter)
		// is rejected here so the diagnostic fires at the arm itself
		// rather than via a downstream "use of moved value".
		// Destructuring patterns (TuplePat / StructPat) READ fields rather
		// than consume the receiver; they leave the scrutinee usable in
		// the arm body, so we DON'T flip it to Moved during destructure
		// arms. (v0.3 doesn't track per-field state, so the bind names
		// inside the destructure get their declared types and any later
		// move of those names is checked normally.)
		var bindArmRestoreSubj *borrowEntry
		var bindArmPrior borrowEntry
		if subjEntry != nil {
			if _, isBind := arm.Pattern.(*BindPat); isBind {
				if entryState == bsBorrowedShared {
					return borrowErr(arm.Pattern.PatPos(),
						"cannot move borrowed value: %q (%s)",
						subjId.Name, subjPrior.borrowReason)
				}
				bindArmRestoreSubj = subjEntry
				bindArmPrior = *subjEntry
				subjEntry.state = bsMoved
				if subjId != nil {
					subjEntry.movePos = subjId.Pos
				}
			}
		}
		savedDiverged := c.diverged
		c.diverged = false
		if arm.Guard != nil {
			if err := c.checkExprRead(arm.Guard); err != nil {
				if bindArmRestoreSubj != nil {
					*bindArmRestoreSubj = bindArmPrior
				}
				c.scope = c.scope.parent
				return err
			}
		}
		var armErr error
		for _, st := range arm.Body.Statements {
			if err := c.checkStmt(st); err != nil {
				armErr = err
				break
			}
		}
		armDiverged := c.diverged
		c.diverged = savedDiverged
		if bindArmRestoreSubj != nil {
			*bindArmRestoreSubj = bindArmPrior
		}
		c.scope = c.scope.parent
		if armErr != nil {
			return armErr
		}
		outcomes = append(outcomes, branchOutcome{end: c.scope.snapshotAll(), diverged: armDiverged})
	}

	// Restore the scrutinee's pre-match state.
	if subjEntry != nil {
		*subjEntry = subjPrior
	}

	// Worst-case static rule: if any arm consumed the scrutinee, mark it
	// Moved at the join. Otherwise the borrow ends and scrutinee returns to
	// Owned.
	//
	// Special case: if the scrutinee was already BorrowedShared at match
	// entry (a fn parameter, a for-iter binding, etc.), the borrow re-
	// asserts on exit — the scrutinee never owned the value, so we cannot
	// flip it to Moved. The BindPat arm path is the only thing that can
	// produce a genuine consume of a BorrowedShared scrutinee, and that's
	// already rejected up-front in the per-arm walk above.
	if consumes && subjEntry != nil && entryState != bsBorrowedShared {
		subjEntry.state = bsMoved
		if subjId != nil {
			subjEntry.movePos = subjId.Pos
		}
	}

	// Branch-agree across non-diverged arms (only for outer bindings).
	if err := c.joinBranches(s.Pos, entry, outcomes); err != nil {
		return err
	}

	// If every arm diverged, current path is diverged.
	allDiverged := true
	for _, o := range outcomes {
		if !o.diverged {
			allDiverged = false
			break
		}
	}
	if allDiverged && len(outcomes) > 0 {
		c.diverged = true
	}
	return nil
}

// boundName captures one name introduced by a pattern, plus its element type.
type boundName struct {
	name string
	typ  *Type
}

// patternConsumes reports whether the pattern, if executed, would consume the
// scrutinee (whole or in part). BindPat consumes; TuplePat / StructPat that
// bind any inner names consume; LitPat / WildcardPat / EnumPat / non-binding
// StructPat observe.
func patternConsumes(p Pattern) bool {
	switch x := p.(type) {
	case *BindPat:
		return true
	case *TuplePat:
		for _, sub := range x.Elements {
			if patternBinds(sub) {
				return true
			}
		}
		return false
	case *StructPat:
		for _, f := range x.Fields {
			if patternBinds(f.Pattern) {
				return true
			}
		}
		return false
	}
	return false
}

// patternBinds is the recursive "does this sub-pattern introduce any names?"
// helper used by patternConsumes.
func patternBinds(p Pattern) bool {
	switch x := p.(type) {
	case *BindPat:
		return true
	case *TuplePat:
		for _, sub := range x.Elements {
			if patternBinds(sub) {
				return true
			}
		}
	case *StructPat:
		for _, f := range x.Fields {
			if patternBinds(f.Pattern) {
				return true
			}
		}
	}
	return false
}

// bindPatternNames collects every (name, type) pair introduced by a pattern.
// Types are sourced from typeck's already-resolved subject Type by walking
// the pattern shape against it: a BindPat at the top takes the whole subject
// type; a TuplePat element i takes subject.Tuple[i]; a StructPat field f
// takes the named field's Type. When the shape doesn't line up (defensive
// — typeck would normally reject mismatches), we fall back to nil so the
// borrow logic conservatively treats the bound name as primitive.
func bindPatternNames(p Pattern, subjectType *Type) []boundName {
	var out []boundName
	var walk func(p Pattern, t *Type)
	walk = func(p Pattern, t *Type) {
		switch x := p.(type) {
		case *BindPat:
			out = append(out, boundName{name: x.Name, typ: t})
		case *TuplePat:
			var subT *Type
			for i, sub := range x.Elements {
				if t != nil && t.Kind == TypeTuple && i < len(t.Tuple) {
					subT = t.Tuple[i]
				} else {
					subT = nil
				}
				walk(sub, subT)
			}
		case *StructPat:
			for _, f := range x.Fields {
				var fieldT *Type
				if t != nil && t.Kind == TypeStruct {
					for _, df := range t.Fields {
						if df.Name == f.Name {
							fieldT = df.Type
							break
						}
					}
				}
				walk(f.Pattern, fieldT)
			}
		}
	}
	walk(p, subjectType)
	return out
}

// ---------------------------------------------------------------------------
// v0.7 Unit 5 — concurrency surface borrow rules.
//
// PLAN.md §Borrow rules for concurrency pins:
//
//   * Send (`ch <- v`) moves the value into the channel. The channel handle
//     itself is read-only; sends and receives never move the chan binding.
//   * Recv (`<- ch`) returns a fresh Option[T] value owned by the caller; the
//     channel handle is read-only.
//   * Spawn closure capture deep-copies composite captures at the spawn site.
//     Each capture is a clone-read on the source binding; the spawning fn
//     keeps full ownership of the originals. Anon-fn evaluation outside a
//     spawn shares the same shape (a closure value carries deep-copied
//     captures regardless of whether it ever escapes).
//   * Defer body is checked as if it ran at the defer-statement site. Inner
//     moves of outer bindings register against the surrounding scope.
//   * Select arms are branch-merged like match arms: every binding's
//     end-state must agree across non-diverged arms. Recv-bind names are
//     fresh locals scoped to the arm body.
// ---------------------------------------------------------------------------

// checkSend implements `ch <- v`: the channel is read; the value is consumed
// (composite values are moved into the channel, primitives copy).
func (c *borrowChecker) checkSend(s *SendStmt) error {
	if err := c.walkExpr(s.Chan, false); err != nil {
		return err
	}
	if id, ok := s.Value.(*IdentExpr); ok {
		return c.consume(id, "moved by channel send")
	}
	if th, ok := s.Value.(*ThisExpr); ok {
		return c.consumeThis(th, "moved by channel send")
	}
	return c.checkExprConsume(s.Value)
}

// checkSpawn implements `spawn <call>`: the call's arguments follow the
// regular fn-call read rules (implicit shared borrow). Capture-by-clone
// semantics for an anon-fn callee land via walkAnonFn — the captures are
// reads on the source bindings, so the spawning fn keeps ownership of the
// originals.
func (c *borrowChecker) checkSpawn(s *SpawnStmt) error {
	return c.walkExpr(s.Call, false)
}

// checkDefer walks a deferred body at the defer-statement site. v0.7 admits
// defer only at fn-body top-level scope (parser enforced); cross-defer
// ordering and the `?` early-return interaction are Unit 5.5's responsibility.
func (c *borrowChecker) checkDefer(s *DeferStmt) error {
	for _, st := range s.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// checkSelect runs each arm's channel op + body in turn from a fresh snapshot
// of the entry state, then verifies branches agree on every outer binding's
// end-state. The recv-bind name is a fresh Owned local inside its arm body.
// Arms whose channel op or body move an outer binding must agree with arms
// that don't — disagreement reports the standard "branch states disagree"
// diagnostic.
func (c *borrowChecker) checkSelect(s *SelectStmt) error {
	entry := c.scope.snapshotAll()
	outcomes := make([]branchOutcome, 0, len(s.Arms))
	for i := range s.Arms {
		arm := &s.Arms[i]
		c.scope.applyTo(entry)
		savedDiverged := c.diverged
		c.diverged = false
		if err := c.checkSelectArm(arm); err != nil {
			c.diverged = savedDiverged
			return err
		}
		armDiverged := c.diverged
		c.diverged = savedDiverged
		outcomes = append(outcomes, branchOutcome{end: c.scope.snapshotAll(), diverged: armDiverged})
	}
	if err := c.joinBranches(s.Pos, entry, outcomes); err != nil {
		return err
	}
	allDiverged := true
	for _, o := range outcomes {
		if !o.diverged {
			allDiverged = false
			c.scope.applyTo(o.end)
			break
		}
	}
	if allDiverged && len(outcomes) > 0 {
		c.diverged = true
		c.scope.applyTo(entry)
	}
	return nil
}

// checkSelectArm dispatches one arm's channel op (send / recv / default) and
// then walks the arm body in a fresh scope rooted at the surrounding scope —
// any recv-bind name vanishes at the arm boundary, mirroring the per-arm
// scope shape used by checkMatch.
func (c *borrowChecker) checkSelectArm(arm *SelectArm) error {
	switch arm.Op {
	case SelectSend:
		if err := c.walkExpr(arm.Chan, false); err != nil {
			return err
		}
		if id, ok := arm.Value.(*IdentExpr); ok {
			if err := c.consume(id, "moved by select send"); err != nil {
				return err
			}
		} else if th, ok := arm.Value.(*ThisExpr); ok {
			if err := c.consumeThis(th, "moved by select send"); err != nil {
				return err
			}
		} else {
			if err := c.checkExprConsume(arm.Value); err != nil {
				return err
			}
		}
	case SelectRecvDiscard:
		if err := c.walkExpr(arm.Chan, false); err != nil {
			return err
		}
	case SelectRecvBind:
		if err := c.walkExpr(arm.Chan, false); err != nil {
			return err
		}
	case SelectDefault:
		// no channel op
	}
	c.scope = newBorrowScope(c.scope)
	defer func() { c.scope = c.scope.parent }()
	if arm.Op == SelectRecvBind {
		var bt *Type
		if arm.Chan != nil {
			if t := arm.Chan.Type(); t != nil && t.Kind == TypeChan {
				bt = t.Element
			}
		}
		c.scope.declare(arm.BindName, &borrowEntry{
			state:     bsOwned,
			typ:       bt,
			declDepth: c.loopDepth,
		})
	}
	for _, st := range arm.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// walkAnonFn handles the borrow-walk side of an anon-fn expression. Each
// recorded capture is a clone-read on the source binding (deep-copy at
// closure construction means the original is observed, not moved). The body
// then walks in a fresh borrow scope where each captured name resolves to a
// fresh-immutable composite — the inner walk cannot move out of a captured
// value because the clone is the closure's own private copy.
func (c *borrowChecker) walkAnonFn(anon *AnonFnExpr) error {
	for _, cap := range anon.Captures {
		entry, _ := c.scope.lookup(cap.Name)
		if entry == nil {
			continue
		}
		if entry.state == bsMoved && isComposite(entry.typ) {
			return borrowErr(cap.Pos,
				"use of moved value: %q (moved at %s)",
				cap.Name, entry.movePos)
		}
	}
	saveScope, saveDepth, saveDiverged := c.scope, c.loopDepth, c.diverged
	c.scope = newBorrowScope(nil)
	c.loopDepth = 0
	c.diverged = false
	defer func() {
		c.scope = saveScope
		c.loopDepth = saveDepth
		c.diverged = saveDiverged
	}()
	for _, cap := range anon.Captures {
		state := bsOwned
		reason := ""
		if isComposite(cap.Type) {
			state = bsBorrowedShared
			reason = "captured by closure (immutable deep-copy)"
		}
		c.scope.declare(cap.Name, &borrowEntry{
			state:        state,
			typ:          cap.Type,
			borrowReason: reason,
			declDepth:    0,
		})
	}
	for _, p := range anon.Params {
		var t *Type
		if p.Type != nil {
			t = p.Type.Resolved
		}
		state := bsOwned
		reason := ""
		if isComposite(t) {
			state = bsBorrowedShared
			reason = "fn parameter (implicit shared borrow at v0.3)"
		}
		c.scope.declare(p.Name, &borrowEntry{
			state:        state,
			typ:          t,
			borrowReason: reason,
			declDepth:    0,
		})
	}
	for _, st := range anon.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			return err
		}
	}
	return nil
}
