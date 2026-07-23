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
	Info      *sema.Info
	Funcs     []*Instance
	Types     []*TypeInstance
	Witnesses []*Witness
	Main      *Instance

	// Inits is the module-initialization plan the emitter lowers (Phase 1g S3): the
	// modules that own an `init()` block or a module constant, in dependency order.
	// InitCtx is the shared, non-generic instance context under which every init body
	// and constant initializer was monomorphized, so the emitter reads their generic
	// call targets and type overlays through it. Both empty/nil for a program with no
	// init and no module constant, which therefore stays byte-identical.
	Inits   []InitGroup
	InitCtx *Instance

	byMangled   map[string]*Instance
	typeByKey   map[string]*TypeInstance
	witByGlobal map[string]*Witness
}

// InitGroup is one module's initialization content the emitter renders into a
// per-module init function: the module's C-mangle tag (empty for the entry module),
// its `init()` blocks in declaration order, and its module constants in declaration
// order (evaluated before the init blocks run).
type InitGroup struct {
	Tag    string
	Inits  []*ast.InitDecl
	Consts []*ast.BindStmt
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

	// Recv is the receiver type of an impl-method instance (nil for a free
	// function). RecvErased marks a witness-table method whose receiver is passed as
	// an opaque 'const void*' and cast to Recv in a prologue (DESIGN-1c §6.1).
	Recv       types.Type
	RecvErased bool

	// Dyn marks the single erased body of a '#[dyn]' generic (DESIGN-1c §6): its
	// type-parameter-typed parameters (Erased[i] true) render as 'const void*' and
	// it takes a trailing witness-table pointer for DynSpec. DynSites records, per
	// call to this dyn function from the current body, the concrete witness to pass.
	Dyn      bool
	DynSpec  *types.SpecDef
	Erased   []bool
	DynParam string // the witness pointer parameter name inside a dyn body ("w")

	subT        map[string]types.Type
	subV        map[string]types.ConstVal
	Calls       map[*ast.Call]string
	DynSites    map[*ast.Call]*DynSite
	MethodCalls map[*ast.Call]*MethodDispatch
	OpCalls     map[*ast.Binary]*MethodDispatch
}

// MethodDispatch is a resolved method or operator call within a body: the mangled
// name of the impl-method instance it dispatches to. The receiver (a method call's
// receiver, or a comparison's left operand) is passed by value as the first
// argument (DESIGN-1c §3.3/§6, B1/B2).
type MethodDispatch struct {
	Mangled string
	// Erased marks a dispatch whose target instance takes an opaque 'const void*'
	// receiver (a spec provided-method body called from another provided body, W3):
	// the receiver is address-taken and cast, not passed by value.
	Erased bool
}

// DynSite is a call to a '#[dyn]' function from within some body: the witness the
// call must pass and which positional arguments are erased (address-taken to a
// 'const void*'). The emitter reads it to spell the dispatch.
type DynSite struct {
	Callee  string   // the dyn function's mangled name
	Witness *Witness // the concrete witness table to pass
	Erased  []bool   // per argument, whether it is erased to an opaque pointer
}

// Witness is one concrete witness table for a (spec, type) pair (DESIGN-1c §6.1):
// a C global of the spec's witness struct whose slots hold the impl methods'
// addresses. It is emitted once and shared by every dyn call at that type.
type Witness struct {
	Global string // the C global variable name, e.g. 'zg_witness_Show__Wrap'
	Struct string // the witness struct type name, e.g. 'zg_witness_Show'
	Spec   *types.SpecDef
	Slots  []WitnessSlot
}

// WitnessSlot binds one spec method name to the mangled C name of the impl method
// that fills it for this type.
type WitnessSlot struct {
	Method string
	Fn     string
}

// TypeInstance is one specialized nominal type to emit: the mangled C type name,
// the originating definition, and its concrete type arguments. A struct sets
// Fields (each field type specialized to those arguments); an enum sets IsEnum and
// Variants (each variant's payload specialized) so the emitter renders a tagged
// union.
type TypeInstance struct {
	Mangled  string
	Def      *types.TypeDef
	Args     []types.Type
	Fields   []FieldInst
	Variants []VariantInst
	IsEnum   bool
}

// FieldInst is one specialized struct field: its source name and concrete type.
type FieldInst struct {
	Name string
	Type types.Type
}

// VariantInst is one specialized enum variant: its source name, its zero-based tag
// (the discriminant the emitter tests and derive orders by), and its payload
// element types specialized to the instance's type arguments.
type VariantInst struct {
	Name    string
	Tag     int
	Payload []types.Type
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
		if ti, ok := p.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return ti.Mangled, true
		}
	case *types.Enum:
		if ti, ok := p.typeByKey[typeKey(x.Def, x.Args)]; ok {
			return ti.Mangled, true
		}
	}
	return "", false
}

// EnumInstance returns the collected specialized enum for an enum-typed value, so
// the emitter can spell a variant construction or a match test. It returns nil for
// a non-enum type or one that was never collected.
func (p *Program) EnumInstance(t types.Type) *TypeInstance {
	x, ok := t.(*types.Enum)
	if !ok {
		return nil
	}
	if ti, ok := p.typeByKey[typeKey(x.Def, x.Args)]; ok && ti.IsEnum {
		return ti
	}
	return nil
}

// StructInstance returns the collected specialized struct for a struct-typed value,
// so the emitter can read each field's CONCRETE type when destructuring a struct
// pattern (a generic struct's field 'T' reads as the instance's type argument). It
// returns nil for a non-struct type or one that was never collected.
func (p *Program) StructInstance(t types.Type) *TypeInstance {
	x, ok := t.(*types.Struct)
	if !ok {
		return nil
	}
	if ti, ok := p.typeByKey[typeKey(x.Def, x.Args)]; ok && !ti.IsEnum {
		return ti
	}
	return nil
}

// Variant returns the specialized variant of the named case and whether it exists,
// so the emitter can read its tag and payload types.
func (ti *TypeInstance) Variant(name string) (VariantInst, bool) {
	for _, v := range ti.Variants {
		if v.Name == name {
			return v, true
		}
	}
	return VariantInst{}, false
}
