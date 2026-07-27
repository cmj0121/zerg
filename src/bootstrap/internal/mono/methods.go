package mono

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file resolves the calls iteration 4 first got wrong (B1/B2): a bound-method
// call in a body ('x.tag()'), a comparison on a nominal or specialized type-param
// operand ('a == b', 'a < b'), and an explicit type/value-argument call
// ('f[int](x)'). Each resolves the concrete impl method for the receiver (or
// operand) type, enqueues that method as a by-value instance, and records the
// dispatch on the caller's overlay so the emitter calls the method instead of
// emitting a nil callee or a raw C operator.

// compareMethod maps a comparison operator to the spec method that backs it, so a
// comparison on a nominal type lowers to the impl (derived or manual) of Eq/Ord.
//
//nolint:gochecknoglobals // a fixed, compiler-owned operator→method table.
var compareMethod = map[token.Kind]string{
	token.EqEq: "eq",
	token.Ne:   "ne",
	token.Lt:   "lt",
	token.Le:   "le",
	token.Gt:   "gt",
	token.Ge:   "ge",
}

// walkMethodCall records a bound-method call 'recv.method(args)' (B1): it resolves
// the impl method for the receiver's concrete type, enqueues it as a by-value
// method instance, and records the dispatch. It returns false when the callee is
// not a resolvable method (leaving other callee shapes to the caller).
func (w *worker) walkMethodCall(in *Instance, n *ast.Call) bool {
	field, ok := n.Callee.(*ast.Field)
	if !ok {
		return false
	}
	recv := in.ExprType(w.info, field.X)
	_, m := w.resolveMethod(recv, field.Name)
	if m == nil {
		return false
	}
	inst := w.enqueueMethodInstance(recv, m)
	in.MethodCalls[n] = &MethodDispatch{Mangled: inst.Mangled}
	return true
}

// lowerCompare records a comparison on a nominal (or specialized type-parameter)
// operand as a call to the operand type's Eq/Ord impl method (B2). A primitive
// comparison keeps its native C operator and is left untouched.
func (w *worker) lowerCompare(in *Instance, n *ast.Binary) {
	method, ok := compareMethod[n.Op]
	if !ok {
		return
	}
	lt := in.ExprType(w.info, n.L)
	if nominalHeadT(lt) == nil {
		return
	}
	_, m := w.resolveMethod(lt, method)
	if m == nil {
		return
	}
	inst := w.enqueueMethodInstance(lt, m)
	in.OpCalls[n] = &MethodDispatch{Mangled: inst.Mangled}
}

// explicitCall records an explicit type/value-argument call 'f[T](args)' (B1): it
// binds the callee's generic parameters from the bracket's arguments, infers any
// remaining from the value arguments, and enqueues the specialization.
func (w *worker) explicitCall(in *Instance, br *ast.Bracket, n *ast.Call) {
	id, ok := br.Base.(*ast.Ident)
	if !ok {
		return
	}
	sig, ok := w.info.Funcs[id.Name]
	if !ok || sig.Generic == nil {
		return
	}
	subT := map[string]types.Type{}
	subV := map[string]types.ConstVal{}
	args := w.info.Brackets[br].Args
	for i, name := range sig.Generic.Names {
		if _, isVal := sig.Generic.Params[name].(*types.ValParam); isVal {
			if i < len(br.Elems) {
				if v, ok := constFrom(br.Elems[i]); ok {
					subV[name] = v
					continue
				}
			}
		}
		if i < len(args) && args[i] != nil && args[i].Kind() != types.KUnknown {
			subT[name] = args[i]
		}
	}
	exprs := pairArgs(sig, n)
	for i := range sig.Params {
		if exprs[i] == nil {
			continue
		}
		sema.Unify(sig.Params[i], in.ExprType(w.info, exprs[i]), subT, subV)
	}
	callee := w.enqueueFn(sig.Decl, subT, subV)
	in.Calls[n] = callee.Mangled
}

// resolveMethod finds the impl and method backing a name on a nominal receiver
// type, consulting the type's one method namespace (spec and inherent alike).
func (w *worker) resolveMethod(recv types.Type, name string) (*types.ImplDef, *types.ImplMethod) {
	reg := w.info.Specs
	if reg == nil {
		return nil, nil
	}
	head := nominalHeadT(recv)
	if head == nil {
		return nil, nil
	}
	ms := reg.Methods[head]
	if ms == nil {
		return nil, nil
	}
	ref, ok := ms.Methods[name]
	if !ok {
		return nil, nil
	}
	return ref.Impl, ref.Method
}

// enqueueMethodInstance creates (once) a by-value impl-method instance: the
// receiver is the first, by-value parameter 'this', and the method's own parameters
// and return are specialized to the receiver's type arguments (so a method on
// 'Box[int]' reads its 'T' as int).
func (w *worker) enqueueMethodInstance(recv types.Type, m *types.ImplMethod) *Instance {
	fn, _ := m.Decl.(*ast.FuncDecl)
	mangled := methodMangle(recv, m.Name)
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	subT := nominalSubT(recv)
	// A PROVIDED spec default body was type-checked with the abstract self 'This'; map
	// it to the concrete receiver so the shared body's This-typed sub-expressions and its
	// `this.other()` self-calls resolve concretely during the walk and the emit (the same
	// overlay the `#[dyn]` erased-receiver instance uses in mono/dyn.go). An impl-written
	// method was already checked against the concrete type, so it leaves This unset and
	// stays byte-identical.
	if m.Provided {
		subT["This"] = recv
	}
	ret := m.Sig.Ret
	if ret == nil {
		ret = types.Nil // a method with no '-> type' returns nil
	}
	in := &Instance{
		Origin:      fn,
		Mangled:     mangled,
		Recv:        recv,
		Ret:         sema.Substitute(ret, subT, nil),
		subT:        subT,
		Calls:       map[*ast.Call]string{},
		MethodCalls: map[*ast.Call]*MethodDispatch{},
		OpCalls:     map[*ast.Binary]*MethodDispatch{},
	}
	if fn != nil {
		for _, p := range fn.Params {
			in.ParamNames = append(in.ParamNames, p.Name)
		}
	}
	for _, p := range m.Sig.Params {
		in.Params = append(in.Params, sema.Substitute(p.Type, subT, nil))
	}
	w.prog.byMangled[mangled] = in
	w.prog.Funcs = append(w.prog.Funcs, in)
	w.queue = append(w.queue, in)
	return in
}

// nominalHeadT returns the *TypeDef of a nominal (struct or enum) type, or nil.
func nominalHeadT(t types.Type) *types.TypeDef {
	switch x := t.(type) {
	case *types.Struct:
		return x.Def
	case *types.Enum:
		return x.Def
	}
	return nil
}

// nominalSubT maps a nominal type's declared parameters to its use-site arguments,
// so a method body's abstract 'T' specializes to the concrete argument.
func nominalSubT(recv types.Type) map[string]types.Type {
	subT := map[string]types.Type{}
	var def *types.TypeDef
	var args []types.Type
	switch x := recv.(type) {
	case *types.Struct:
		def, args = x.Def, x.Args
	case *types.Enum:
		def, args = x.Def, x.Args
	}
	if def != nil {
		for i, p := range def.Params {
			if i < len(args) {
				subT[p.Name] = args[i]
			}
		}
	}
	return subT
}

// constFrom reads a compile-time value from an integer or boolean literal, for an
// explicit value-generic argument 'fill[3](…)'.
func constFrom(e ast.Expr) (types.ConstVal, bool) {
	switch x := e.(type) {
	case *ast.IntLit:
		return types.ConstVal{Kind: types.KInt, I: x.Value, Known: true}, true
	case *ast.BoolLit:
		return types.ConstVal{Kind: types.KBool, B: x.Value, Known: true}, true
	}
	return types.ConstVal{}, false
}
