package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// The `mut &` mutable-reference parameter (GRAMMAR group 5, docs/core/memory.md) — Zerg's
// one explicit by-ref path, and the only way code shares storage short of a `chan` or
// a `Ref[T]` box.
//
// Two independent controls meet at a call: the CALLER decides whether its variable is
// `mut`, and the CALLEE decides through `mut &` whether it writes back. A
// caller-visible mutation needs both, so the argument must be a `mut` lvalue. There is
// no call-site marker — the signature is the contract.
//
// The reference is valid only for the duration of the call, which is what makes it
// safe with no borrow checker: it cannot ESCAPE, and two `mut &` arguments in one call
// must not ALIAS, a guarantee the callee is entitled to rely on.

// checkByRefArgs enforces that contract at a call site. It runs after the ordinary
// argument binding, so a type error is reported once, by that pass.
func (c *checker) checkByRefArgs(sig *FuncSig, n *ast.Call) {
	if sig == nil || sig.Decl == nil {
		return
	}
	if !anyByRefParam(sig) {
		return
	}
	// Positional binding is what the by-ref rules are written against. A named or
	// defaulted argument list re-orders that mapping; rather than guess which argument
	// reaches a `mut &` parameter, report the gap.
	if hasNamedArg(n.Args) {
		c.errorf(n.Span(), "a named argument to a `mut &` parameter is not yet supported; pass it positionally")
		return
	}
	roots := map[string]ast.Expr{}
	for i, p := range sig.Decl.Params {
		if !p.Ref || i >= len(n.Args) {
			continue
		}
		arg := n.Args[i].Value
		sym, name, ok := c.rootBinding(arg)
		if !ok {
			c.errorf(arg.Span(), "a `mut &` parameter needs a variable to write back to, but argument %d is not one", i+1)
			continue
		}
		if !sym.mutable {
			c.errorf(arg.Span(), "cannot pass immutable %q to the `mut &` parameter %q; declare it `mut`", name, p.Name)
			continue
		}
		// Two `mut &` arguments never share storage. Naming the same root twice is the
		// static case the docs make a compile error. Where the root is shared but the
		// paths might still differ (`f(xs[i], xs[j])`) the docs call for a runtime
		// AliasError; that check is not built, so the conservative rejection stands in
		// for it rather than emitting a call whose aliasing goes unpoliced.
		if prev, dup := roots[name]; dup {
			_ = prev
			c.errorf(arg.Span(), "cannot pass %q to two `mut &` parameters in one call; they must not alias", name)
			continue
		}
		roots[name] = arg
	}
}

// anyByRefParam reports whether a signature declares a `mut &` parameter.
func anyByRefParam(sig *FuncSig) bool {
	for _, p := range sig.Decl.Params {
		if p.Ref {
			return true
		}
	}
	return false
}

// rootBinding walks an lvalue expression to the local binding it is rooted at — the
// storage a `mut &` argument would write back through — and reports the binding, its
// source name, and whether the expression is an lvalue at all. It never reports a
// diagnostic: an unresolved name has already been reported by the argument pass.
func (c *checker) rootBinding(e ast.Expr) (*symbol, string, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if sym := c.lookup(x.Name); sym != nil {
			return sym, x.Name, true
		}
	case *ast.Field:
		return c.rootBinding(x.X)
	case *ast.Bracket:
		// only an index is an lvalue path; a type-argument bracket is not.
		if res, ok := c.info.Brackets[x]; ok && res.Kind != BracketIndex {
			return nil, "", false
		}
		return c.rootBinding(x.Base)
	}
	return nil, "", false
}
