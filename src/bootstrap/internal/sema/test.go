package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// This file is the Phase 1i U1 test-discovery layer. `#[test]` is a compiler-owned
// decorator — a fixed member of GRAMMAR group 7's decorator set, recognized here the
// same way `#[derive]`/`#[dyn]` are, not a user-defined attribute. It
// marks a top-level function the `zerg test` runner executes; `zerg build` never sees
// one (the driver filters test functions out before this pass), so a `#[test]` beside
// ordinary code cannot change a normal build's emitted C.

// HasTestDecorator reports whether a function carries the `#[test]` decorator. It is
// exported so the build driver can strip test functions from a normal `zerg build`,
// keeping the emitted C byte-identical to the same program without them.
func HasTestDecorator(fn *ast.FuncDecl) bool {
	return hasDecorator(fn.Decorators, "test")
}
