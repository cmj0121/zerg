package build

import "github.com/cmj/zerg/src/bootstrap/internal/syntax"

// v0.9 Unit 1 — `never` bottom type cgen support.
//
// The C type for `never` is `void`: no value flows through a `-> never`
// fn-decl's return position. The fn carries `__attribute__((noreturn))` so
// the C compiler accepts the absence of a return statement and the caller
// site does not need a phantom value at the call boundary.
//
// Unit 1 only stages the attribute — Unit 3 lands the os.exit fn that
// actually raises the divergence. A user-authored `-> never` fn (today
// only synthesisable via an infinite for-loop) takes the same path.

// fnReturnsNever reports whether fn's declared return type is `never`. The
// resolved Type is consulted so any future never-aliasing path also fires.
func fnReturnsNever(fn *syntax.FnDecl) bool {
	if fn == nil || fn.Return == nil {
		return false
	}
	t := fn.Return.Resolved
	return t != nil && t.Kind == syntax.TypeNever
}
