package sema

import (
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1i U1 test-discovery layer. `#[test]` is a compiler-owned
// decorator — a fixed member of GRAMMAR group 7's decorator set, recognized here the
// same way `#[derive]`/`#[extern]`/`#[dyn]` are, not a user-defined attribute. It
// marks a top-level function the `zerg test` runner executes; `zerg build` never sees
// one (the driver filters test functions out before this pass), so a `#[test]` beside
// ordinary code cannot change a normal build's emitted C.

// HasTestDecorator reports whether a function carries the `#[test]` decorator. It is
// exported so the build driver can strip test functions from a normal `zerg build`,
// keeping the emitted C byte-identical to the same program without them.
func HasTestDecorator(fn *ast.FuncDecl) bool {
	return hasDecorator(fn.Decorators, "test")
}

// collectTest validates a `#[test]` function's shape and, when it is well-formed,
// records its signature in Info.Tests with a human-readable TestLabel. A `#[test]`
// function must take no parameters, be non-generic (there is no type argument to
// instantiate a test at), and return `nil` or `Result[nil]` (the two shapes the C
// test driver can run and score — DESIGN-1i §2.2). Any violation is a span
// diagnostic, and the malformed test is not collected.
func (c *checker) collectTest(fn *ast.FuncDecl, sig *FuncSig) {
	if sig.Generic != nil || len(sig.Params) != 0 || !isTestRet(sig.Ret) {
		c.errorf(fn.Span(),
			"#[test] function %q must be non-generic, take no parameters, and return nil or Result[nil]", fn.Name)
		return
	}
	sig.TestLabel = testLabel(fn.Name)
	c.info.Tests = append(c.info.Tests, sig)
}

// isTestRet reports whether a type is an admissible `#[test]` return: `nil` or
// `Result[nil]` (the sum whose Left is nil). It mirrors the emitter's isResultNil so
// discovery and lowering agree on the accepted shapes.
func isTestRet(t Type) bool {
	if t == Nil {
		return true
	}
	if e, ok := t.(*types.Either); ok {
		return e.Left == types.Nil || e.Left.Kind() == types.KUnknown
	}
	return false
}

// testLabel turns a `#[test]` function's flattened whole-program name into the
// human-readable `module::surface` label the runner prints. An imported module's
// function is mangled `<tag>__surface` by the loader, so the first `__` splits the
// module tag from the surface name (`geom__test_area` -> `geom::test_area`); an
// entry-module test has no tag and keeps its bare name.
func testLabel(name string) string {
	if i := strings.Index(name, "__"); i >= 0 {
		return name[:i] + "::" + name[i+len("__"):]
	}
	return name
}
