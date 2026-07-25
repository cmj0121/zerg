package sema

import "github.com/cmj0121/zerg/src/bootstrap/internal/ast"

// This file is the decorator-validation pass. Decorators are a fixed, compiler-owned
// set (GRAMMAR group 7, docs/decorators.md): a user cannot define one (Zerg has no
// macros). An unknown name is therefore a typo or an invented attribute that would
// otherwise compile clean with NO effect, so it is rejected here. A recognized-but-
// unimplemented layout decorator is rejected with a distinct "not yet supported"
// message, so a typo (`#[deriv]`) stays distinguishable from a real directive the
// compiler does not honor yet (`#[repr]`).

// decoratorStatus classifies a decorator name against the compiler-owned set.
type decoratorStatus int

const (
	decoUnknown  decoratorStatus = iota // not a compiler decorator at all
	decoKnown                           // recognized and implemented
	decoReserved                        // recognized but not yet implemented
)

// knownDecorators is the fixed set of compiler decorators. IMPLEMENTED (accepted):
// derive/dyn/test. RESERVED (docs/decorators.md "Reserved" — the layout family plus
// the ctor-sealing directive): sealed/align/packed/repr — recognized so a typo is
// distinguishable, but rejected loudly until each is actually built.
//
//nolint:gochecknoglobals // a fixed, compiler-owned decorator set.
var knownDecorators = map[string]decoratorStatus{
	"derive": decoKnown,
	"dyn":    decoKnown,
	"test":   decoKnown,
	"sealed": decoReserved,
	"align":  decoReserved,
	"packed": decoReserved,
	"repr":   decoReserved,
}

// checkDecorators walks every declaration's decorators, descending into a module-
// level `unsafe { }` group, and reports an unknown decorator or a recognized-but-
// unimplemented one, so neither silently no-ops.
func (c *checker) checkDecorators(file *ast.File) {
	for _, it := range file.Items {
		c.checkItemDecorators(it)
	}
}

// checkItemDecorators validates the decorators of one top-level item. Every
// decorated-decl kind (fn, struct, enum, type, spec, impl, init) is covered, and an
// `unsafe { }` group is descended into so a decorator on an item inside it is checked
// the same way.
func (c *checker) checkItemDecorators(it ast.Stmt) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		c.validateDecorators(n.Decorators)
	case *ast.StructDecl:
		c.validateDecorators(n.Decorators)
	case *ast.EnumDecl:
		c.validateDecorators(n.Decorators)
	case *ast.TypeDecl:
		c.validateDecorators(n.Decorators)
	case *ast.SpecDecl:
		c.validateDecorators(n.Decorators)
	case *ast.ImplDecl:
		c.validateDecorators(n.Decorators)
	case *ast.InitDecl:
		c.validateDecorators(n.Decorators)
	case *ast.UnsafeGroup:
		c.validateDecorators(n.Decorators)
		for _, sub := range n.Items {
			c.checkItemDecorators(sub)
		}
	}
}

// validateDecorators reports each decorator item whose name is not a recognized
// compiler decorator (`unknown decorator`), or is recognized but not yet built
// (`#[name] is not yet supported`).
func (c *checker) validateDecorators(decos []*ast.Decorator) {
	for _, deco := range decos {
		for _, item := range deco.Items {
			switch knownDecorators[item.Name] {
			case decoKnown:
				// recognized and implemented — accepted.
			case decoReserved:
				c.errorf(item.Span(), "#[%s] is not yet supported", item.Name)
			case decoUnknown:
				c.errorf(item.Span(), "unknown decorator %q", item.Name)
			}
		}
	}
}
