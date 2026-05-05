package syntax

// v0.9 Unit 1 — `never` bottom type.
//
// `never` is the type with no values. Practically:
//
//   - A fn declared `-> never` cannot return; every code path must diverge.
//   - At any value-position, a `-> never` call typechecks as the surrounding
//     expected type because never <: T for every concrete T.
//   - Match / select branch-merge already treats unconditional return / break /
//     continue as diverging; v0.9 extends "diverging" to include a tail
//     expression whose static type is `never`.
//
// `never` is a regular IDENT at lex level. resolveTypeRef recognizes the
// bareword `never` only at type position — value position keeps existing
// semantics so a v0.0-v0.8 program that calls a fn named `never` still
// compiles. `struct never` / `enum never` / `spec never` reject at typeck
// with the same reservation diagnostic the v0.6 / v0.7 reserved-name path
// uses.
//
// The rest of the type system needs only one extension: assignableTo accepts
// `tNever` as a source for any target. Subtyping in the other direction
// (a non-never value flowing into a `never` slot) is rejected, so user code
// cannot construct a value of type `never`.

// isReservedV09TypeName reports whether name collides with a v0.9 reserved
// type name. Used by collectTopLevel to reject `struct never` / `enum
// never` / `spec never` (and any future v0.9 type names).
func isReservedV09TypeName(name string) bool {
	return name == "never"
}

// exprIsDivergingNeverCall reports whether expr is a call (or method-call
// lowered to one) whose static return type is `tNever`. Used by the
// fn-decl `-> never` walker to recognize a tail call to another `-> never`
// fn as a divergent terminator.
func exprIsDivergingNeverCall(expr Expr) bool {
	if expr == nil {
		return false
	}
	t := expr.Type()
	if t == nil || t.Kind != TypeNever {
		return false
	}
	switch expr.(type) {
	case *CallExpr, *MethodCallExpr:
		return true
	}
	return false
}

// stmtAlwaysDiverges reports whether stmt unconditionally exits its
// enclosing block (return / break without guard / a tail call to a
// `-> never` fn / an if-with-else where every branch diverges / a match
// where every arm body diverges / a for-loop with no break path).
//
// Used by the fn-decl `-> never` walker. Conservative: if we can't prove
// divergence we return false (no false negatives on valid `-> never`
// fns means an infinite-loop body still type-checks because the for-
// without-break case is recognized).
func stmtAlwaysDiverges(stmt Stmt) bool {
	switch s := stmt.(type) {
	case *ReturnStmt:
		return s.Guard == nil
	case *BreakStmt:
		return s.Guard == nil
	case *ContinueStmt:
		return s.Guard == nil
	case *ExprStmt:
		return exprIsDivergingNeverCall(s.Expr)
	case *IfStmt:
		if s.Else == nil {
			return false
		}
		if !blockAlwaysDiverges(s.Then) {
			return false
		}
		for _, ec := range s.Elifs {
			if !blockAlwaysDiverges(ec.Body) {
				return false
			}
		}
		return blockAlwaysDiverges(s.Else)
	case *MatchStmt:
		if len(s.Arms) == 0 {
			return false
		}
		for _, arm := range s.Arms {
			if arm.Guard != nil {
				return false
			}
			if !blockAlwaysDiverges(arm.Body) {
				return false
			}
		}
		return true
	case *ForStmt:
		// Infinite loop with no break path diverges. `break` inside the
		// body breaks out, so a body that contains any unconditional
		// break / guarded-break path would let control fall through. We
		// approximate: a `for { ... }` body that does NOT mention a
		// (possibly guarded) break diverges.
		if s.Kind != ForInfinite {
			return false
		}
		return !blockMayBreak(s.Body)
	}
	return false
}

// blockAlwaysDiverges reports whether the block's last statement
// unconditionally diverges. Empty blocks do not diverge.
func blockAlwaysDiverges(b *Block) bool {
	if b == nil || len(b.Statements) == 0 {
		return false
	}
	last := b.Statements[len(b.Statements)-1]
	return stmtAlwaysDiverges(last)
}

// blockMayBreak reports whether the block (or any nested non-loop block)
// contains a break statement. Breaks inside an inner for-loop are local
// to that loop and don't escape; we skip nested loop bodies.
func blockMayBreak(b *Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Statements {
		if stmtMayBreak(st) {
			return true
		}
	}
	return false
}

func stmtMayBreak(stmt Stmt) bool {
	switch s := stmt.(type) {
	case *BreakStmt:
		return true
	case *IfStmt:
		if blockMayBreak(s.Then) {
			return true
		}
		for _, ec := range s.Elifs {
			if blockMayBreak(ec.Body) {
				return true
			}
		}
		if s.Else != nil && blockMayBreak(s.Else) {
			return true
		}
	case *MatchStmt:
		for _, arm := range s.Arms {
			if blockMayBreak(arm.Body) {
				return true
			}
		}
	case *SelectStmt:
		for _, arm := range s.Arms {
			if blockMayBreak(arm.Body) {
				return true
			}
		}
	}
	// Nested for-loops swallow their own breaks; nothing else introduces
	// a break that escapes the enclosing for-infinite body.
	return false
}

// checkFnDeclNeverDiverges enforces the v0.9 rule that a fn declared
// `-> never` must have every code path diverge. Called from checkFnDecl
// after the body walk completes. Returns a focused diagnostic when the
// body's tail does not unconditionally diverge.
func checkFnDeclNeverDiverges(fn *FnDecl) error {
	if fn == nil || fn.Return == nil {
		return nil
	}
	if fn.Return.Resolved == nil || fn.Return.Resolved.Kind != TypeNever {
		return nil
	}
	if fn.Body == nil {
		return nil
	}
	if !blockAlwaysDiverges(fn.Body) {
		return typeErr(fn.Pos,
			"function %q declared to return never but has a non-diverging path", fn.Name)
	}
	return nil
}
