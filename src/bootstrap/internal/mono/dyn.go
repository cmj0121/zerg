package mono

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c '#[dyn]' lowering (DESIGN-1c §6, U6). A '#[dyn]'
// generic is NOT monomorphized per type argument; it compiles to one shared,
// type-erased body that dispatches its bound spec's methods through a witness
// table (a struct of function pointers). At each concrete call site the worker
// records the witness to pass — building, once per (spec, type) pair, a witness
// whose slots are the impl methods for that type — and the call site erases its
// type-parameter argument to an opaque pointer.

// dynCall lowers a call to a '#[dyn]' function: it ensures the single erased body
// exists, resolves the concrete witness for the call's type argument, and records
// the dispatch on the caller instance (DESIGN-1c §6.2). Unlike an ordinary generic
// call it enqueues no per-type specialization of the callee.
func (w *worker) dynCall(in *Instance, sig *sema.FuncSig, n *ast.Call) {
	dyn := w.enqueueDyn(sig)
	spec := dynBoundSpec(sig)
	exprs := pairArgs(sig, n)

	erased := make([]bool, len(sig.Params))
	var concrete types.Type
	for i, p := range sig.Params {
		if _, ok := p.(*types.Param); ok {
			erased[i] = true
			if concrete == nil && exprs[i] != nil {
				concrete = in.ExprType(w.info, exprs[i])
			}
		}
	}

	var wit *Witness
	if spec != nil && concrete != nil {
		wit = w.buildWitness(spec, concrete)
	}
	in.DynSites[n] = &DynSite{Callee: dyn.Mangled, Witness: wit, Erased: erased}
}

// enqueueDyn creates (once) the single erased body of a '#[dyn]' function: its
// type-parameter parameters are marked erased (rendered 'const void*') and it
// carries the bound spec so the emitter appends a witness pointer (DESIGN-1c §6.1).
func (w *worker) enqueueDyn(sig *sema.FuncSig) *Instance {
	mangled := "zgd_" + sig.Name
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	in := &Instance{
		Origin:      sig.Decl,
		Mangled:     mangled,
		ParamNames:  sig.ParamNames,
		Params:      make([]types.Type, len(sig.Params)),
		Ret:         orNil(sig.Ret),
		Dyn:         true,
		DynSpec:     dynBoundSpec(sig),
		Erased:      make([]bool, len(sig.Params)),
		DynParam:    "w",
		Calls:       map[*ast.Call]string{},
		DynSites:    map[*ast.Call]*DynSite{},
		MethodCalls: map[*ast.Call]*MethodDispatch{},
		OpCalls:     map[*ast.Binary]*MethodDispatch{},
	}
	for i, p := range sig.Params {
		in.Params[i] = p
		if _, ok := p.(*types.Param); ok {
			in.Erased[i] = true
		}
	}
	w.prog.byMangled[mangled] = in
	w.prog.Funcs = append(w.prog.Funcs, in)
	w.queue = append(w.queue, in)
	return in
}

// buildWitness builds (once) the concrete witness table for a (spec, type) pair:
// it resolves the impl, enqueues each spec method's impl body as an erased-receiver
// instance, and fills one slot per method with that body's mangled name (DESIGN-1c
// §6.2). Every witness of the same spec shares a witness struct type.
func (w *worker) buildWitness(spec *types.SpecDef, concrete types.Type) *Witness {
	global := witnessGlobalName(spec.Name, concrete)
	if wit, ok := w.prog.witByGlobal[global]; ok {
		return wit
	}
	wit := &Witness{Global: global, Struct: witnessStructName(spec.Name), Spec: spec}
	w.prog.witByGlobal[global] = wit

	reg := w.info.Specs
	if reg == nil {
		w.prog.Witnesses = append(w.prog.Witnesses, wit)
		return wit
	}
	for _, sp := range reg.SpecClosure(spec) {
		impl := reg.ResolveImpl(sp, concrete)
		if impl == nil {
			continue
		}
		for _, m := range sp.Methods {
			im := impl.Methods[m.Name]
			selfSub := types.Type(nil)
			if im == nil {
				// A10: the impl does not override this method. When the spec supplies a
				// default body (Provided), build an erased-receiver instance from THAT
				// body and fill the slot; otherwise the required method is missing (a
				// coherence error caught earlier) and there is nothing to point at. Filling
				// the provided slot keeps wit.Slots aligned 1:1 with specMethods() — the
				// order witnessStructs()/witnessGlobals() emit — so no slot is left null.
				if !m.Provided {
					continue
				}
				im = &types.ImplMethod{Name: m.Name, Sig: m.Sig, Mut: m.Mut, Decl: m.Decl}
				selfSub = concrete // the default body's abstract This resolves to concrete
			}
			inst := w.enqueueMethod(concrete, im, selfSub)
			wit.Slots = append(wit.Slots, WitnessSlot{Method: m.Name, Fn: inst.Mangled})
		}
	}
	w.prog.Witnesses = append(w.prog.Witnesses, wit)
	return wit
}

// walkProvidedSelfCall handles a `this.<method>()` self-call inside a synthesized
// spec provided-method body (RecvErased) where <method> is ANOTHER provided method
// the impl does not override (W3). Such a method is absent from the type's impl
// method namespace, so walkMethodCall/resolveMethod cannot find it and the call would
// lower to a null callee ('0()') that fails cc. Here we locate the provided spec
// method for the receiver's concrete type and enqueue its erased-receiver instance —
// the same instance (identical mangled name) buildWitness fills the witness slot with
// — so the self-call dispatches through it. It returns false when the call is not such
// a provided-method self-call, leaving the ordinary paths untouched (byte-identical).
func (w *worker) walkProvidedSelfCall(in *Instance, n *ast.Call) bool {
	if !in.RecvErased {
		return false
	}
	field, ok := n.Callee.(*ast.Field)
	if !ok {
		return false
	}
	recv := in.ExprType(w.info, field.X)
	m := w.providedMethod(recv, field.Name)
	if m == nil {
		return false
	}
	inst := w.enqueueMethod(recv, m, recv)
	in.MethodCalls[n] = &MethodDispatch{Mangled: inst.Mangled, Erased: true}
	return true
}

// providedMethod finds a provided (default-body) spec method named `name` available
// on the concrete receiver type but NOT overridden by its impl — the exact method
// buildWitness synthesizes for an unoverridden slot. It returns a fresh ImplMethod
// carrying the spec method's signature and default-body decl, or nil when no such
// unoverridden provided method matches (an overridden or required method is handled
// by the ordinary resolveMethod path).
func (w *worker) providedMethod(recv types.Type, name string) *types.ImplMethod {
	reg := w.info.Specs
	if reg == nil {
		return nil
	}
	head := nominalHeadT(recv)
	if head == nil {
		return nil
	}
	for _, impl := range reg.Impls {
		if impl.Spec == nil || nominalHeadT(impl.Target) != head {
			continue
		}
		if impl.Methods[name] != nil {
			continue // overridden by the impl — resolveMethod already resolves it
		}
		for _, sp := range reg.SpecClosure(impl.Spec) {
			for _, m := range sp.Methods {
				if m.Name == name && m.Provided {
					return &types.ImplMethod{Name: m.Name, Sig: m.Sig, Mut: m.Mut, Decl: m.Decl}
				}
			}
		}
	}
	return nil
}

// enqueueMethod creates (once) the C body of an impl method used by a witness: an
// erased-receiver instance whose receiver is passed as 'const void*' and cast to
// the concrete type in a prologue (DESIGN-1c §6.1). Its own parameters keep their
// concrete types.
func (w *worker) enqueueMethod(concrete types.Type, m *types.ImplMethod, selfSub types.Type) *Instance {
	fn, _ := m.Decl.(*ast.FuncDecl)
	mangled := "zge_" + typeCode(concrete) + "_" + m.Name
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	// A10: a spec DEFAULT body was type-checked with the abstract self 'This'; supply an
	// overlay mapping This -> the concrete receiver so its `this.other()` calls (and its
	// This-typed sub-expressions) resolve concretely during the walk and the emit. An
	// ordinary impl method was already checked against the concrete type, so it passes a
	// nil selfSub and keeps an empty overlay (byte-identical).
	var subT map[string]types.Type
	if selfSub != nil {
		subT = map[string]types.Type{"This": selfSub}
	}
	in := &Instance{
		Origin:      fn,
		Mangled:     mangled,
		Recv:        concrete,
		RecvErased:  true,
		Ret:         sema.Substitute(orNil(m.Sig.Ret), subT, nil),
		subT:        subT,
		Calls:       map[*ast.Call]string{},
		DynSites:    map[*ast.Call]*DynSite{},
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

// dynBoundSpec returns the spec a dyn function's single type parameter is bound by
// — the spec whose methods the witness table dispatches. It is the primary
// (non-super) bound: '[T: Ord]' yields Ord (super Eq is reached through the
// closure at witness-build time).
func dynBoundSpec(sig *sema.FuncSig) *types.SpecDef {
	if sig.Generic == nil {
		return nil
	}
	for _, name := range sig.Generic.Names {
		if p, ok := sig.Generic.Params[name].(*types.Param); ok && len(p.Bounds) > 0 {
			return p.Bounds[0]
		}
	}
	return nil
}

// orNil returns t, or the nil type when t is nil, so an instance's return type is
// always set.
func orNil(t types.Type) types.Type {
	if t == nil {
		return types.Nil
	}
	return t
}
