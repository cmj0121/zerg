// Package mono is the Phase 1c monomorphization stage. It sits between the
// semantic core (internal/sema) and the C backend (internal/emit): it walks the
// checked program and produces a Program of concrete instances the emitter
// renders.
//
// Phase 1c iteration 1 lands the identity rail (DESIGN-1c §8, slice S0). Every
// program of this iteration is non-generic, so Build is a pass-through: one
// Instance per top-level function, in source order, each carrying its original
// AST body and the mangled C name 'zg_<name>'. The overlay the emitter reads is
// the global sema.Info. The result is byte-identical C to the pre-mono backend —
// the seam that later iterations extend with true specialization without
// regressing the examples.
package mono

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// Program is the monomorphized view the emitter consumes: the function instances
// to render in source order, the shared analysis Info that backs the type
// overlay, and the 'main' instance (nil when the program has none).
type Program struct {
	Info  *sema.Info
	Funcs []*Instance
	Main  *Instance

	byName map[string]*Instance
}

// Instance is one function to emit: its source declaration (the body the emitter
// renders) and the mangled C name. In iteration 1 every function is non-generic,
// so Mangled is 'zg_<name>' and the read overlay is the global Info.
type Instance struct {
	Origin  *ast.FuncDecl
	Mangled string
}

// Build walks the checked file and produces the monomorphized Program. For a
// non-generic program — all of Phase 1c iteration 1 — it is a pass-through: one
// instance per top-level function, in source order, mangled 'zg_<name>'.
func Build(file *ast.File, info *sema.Info) *Program {
	p := &Program{Info: info, byName: map[string]*Instance{}}
	for _, d := range file.Items {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		inst := &Instance{Origin: fn, Mangled: mangle(fn.Name)}
		p.Funcs = append(p.Funcs, inst)
		p.byName[fn.Name] = inst
		if fn.Name == "main" {
			p.Main = inst
		}
	}
	return p
}

// CallTarget returns the mangled C name a call to the named function resolves to.
// Iteration 1 has only non-generic free functions, so this is the source name
// mangled 'zg_<name>'; the lookup is the seam later iterations extend to resolve
// a generic call to its specialized instance.
func (p *Program) CallTarget(name string) string {
	if inst, ok := p.byName[name]; ok {
		return inst.Mangled
	}
	return mangle(name)
}

// mangle is the Phase 1c name-mangling scheme (FORK-B). A non-generic function
// keeps its historic 'zg_<name>' spelling, so the examples' emitted C is
// unchanged.
func mangle(name string) string { return "zg_" + name }
