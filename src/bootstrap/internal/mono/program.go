// Package mono is the Phase 1c monomorphization stage. It sits between the
// semantic core (internal/sema) and the C backend (internal/emit): it walks the
// checked program and produces a Program of concrete instances the emitter
// renders.
//
// A generic function is not first-class until instantiated (GRAMMAR group 7): mono
// starts from the roots (main and every non-generic top-level function) and, at
// each call to a generic function or use of a generic type, specializes a new
// instance per distinct type/value-argument set, iterating to a fixpoint. Each
// instance carries the original AST body and a per-instance type overlay — the
// substitution that turns the body's abstract parameter types into concrete ones
// (DESIGN-1c §4, FORK-A) — so the emitter re-reads one body per specialization
// without cloning the AST.
//
// A non-generic program is the degenerate case: one instance per top-level
// function in source order, each with an empty overlay and the historic mangled
// name 'zg_<name>', so the emitted C is byte-identical to the pre-mono backend.
package mono

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Program is the monomorphized view the emitter consumes: the function instances
// to render (non-generic ones first, in source order, then specializations in
// discovery order), the specialized nominal types they use, the shared analysis
// Info that backs each overlay, and the 'main' instance (nil when absent).
type Program struct {
	Info  *sema.Info
	Funcs []*Instance
	Types []*TypeInstance
	Main  *Instance

	byMangled map[string]*Instance
	typeByKey map[string]*TypeInstance
}

// Instance is one function to emit: its source declaration (the body the emitter
// renders), the mangled C name, the concrete signature, and the per-instance
// overlay. subT/subV map the function's type and value parameters to the concrete
// arguments of this specialization; they are nil for a non-generic instance, so a
// lookup through them is the identity and the emitted C is unchanged. Calls records
// each generic call site's resolved callee mangled name.
type Instance struct {
	Origin     *ast.FuncDecl
	Mangled    string
	ParamNames []string
	Params     []types.Type
	Ret        types.Type

	subT  map[string]types.Type
	subV  map[string]types.ConstVal
	Calls map[*ast.Call]string
}

// TypeInstance is one specialized nominal type to emit as a C struct: the mangled
// C type name, the originating definition, its concrete type arguments, and its
// fields with each field type specialized to those arguments.
type TypeInstance struct {
	Mangled string
	Def     *types.TypeDef
	Args    []types.Type
	Fields  []FieldInst
}

// FieldInst is one specialized struct field: its source name and concrete type.
type FieldInst struct {
	Name string
	Type types.Type
}

// ExprType returns the concrete type of expression e within this instance, reading
// the shared analysis type through the instance's substitution overlay. For a
// non-generic instance the overlay is empty, so the result is the recorded type
// unchanged (the byte-identical guarantee, DESIGN-1c §7.2).
func (in *Instance) ExprType(info *sema.Info, e ast.Expr) types.Type {
	return sema.Substitute(info.ExprTypes[e], in.subT, in.subV)
}

// BindType returns the concrete type of a binding within this instance, read
// through the instance's overlay.
func (in *Instance) BindType(info *sema.Info, n *ast.BindStmt) types.Type {
	return sema.Substitute(info.BindTypes[n], in.subT, in.subV)
}

// CallTarget returns the mangled C name a non-generic call to the named function
// resolves to: the historic 'zg_<name>' spelling. A generic call site is resolved
// per instance through Instance.Calls instead; this is the fallback the emitter
// uses for a direct, non-generic call.
func (p *Program) CallTarget(name string) string { return mangle(name) }

// TypeName returns the mangled C type name of a specialized nominal type, and
// whether one was collected. The emitter uses it to spell a struct type.
func (p *Program) TypeName(t types.Type) (string, bool) {
	switch x := t.(type) {
	case *types.Struct:
		if ti, ok := p.typeByKey[typeFrag(x.Def, x.Args)]; ok {
			return ti.Mangled, true
		}
	case *types.Enum:
		if ti, ok := p.typeByKey[typeFrag(x.Def, x.Args)]; ok {
			return ti.Mangled, true
		}
	}
	return "", false
}
