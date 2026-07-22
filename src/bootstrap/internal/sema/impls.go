package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// SpecRegistry holds the Phase 1c spec/impl model for a compilation unit
// (DESIGN-1c §1.2): every collected spec by name, every impl, and the per-type
// method namespace that spec and inherent methods share. It hangs off Info.Specs
// so later passes (and, in later iterations, mono) can consult it.
type SpecRegistry struct {
	Specs   map[string]*types.SpecDef
	Impls   []*types.ImplDef
	Methods map[*types.TypeDef]*types.MethodSet
}

// collectSpecsAndImpls builds the SpecRegistry in three phases so a spec member
// or an impl target may refer to a type or spec declared later in the file
// (DESIGN-1c §1.2): (1) create an empty shell for each spec, (2) link each spec's
// super-specs, method signatures, and associated types/values, (3) collect each
// impl, register its methods in the per-type namespace, and validate its
// associated bindings against the spec it implements.
func (c *checker) collectSpecsAndImpls(file *ast.File) {
	reg := &SpecRegistry{
		Specs:   map[string]*types.SpecDef{},
		Methods: map[*types.TypeDef]*types.MethodSet{},
	}
	c.info.Specs = reg

	for _, d := range file.Items {
		if n, ok := d.(*ast.SpecDecl); ok {
			reg.Specs[n.Name] = &types.SpecDef{Name: n.Name, Decl: n}
		}
	}
	for _, d := range file.Items {
		if n, ok := d.(*ast.SpecDecl); ok {
			c.linkSpec(reg, n)
		}
	}
	for _, d := range file.Items {
		if n, ok := d.(*ast.ImplDecl); ok {
			c.collectImpl(reg, n)
		}
	}
}

// linkSpec fills a spec shell: it resolves the super-spec bound (each name must
// be a known spec), then records every member — a required signature, a provided
// default, an associated type (with its resolved bound), or an associated value.
// Member signatures resolve with 'This' bound to an abstract self sentinel.
func (c *checker) linkSpec(reg *SpecRegistry, n *ast.SpecDecl) {
	def := reg.Specs[n.Name]
	if n.Super != nil {
		for _, name := range n.Super.Names {
			if sup, ok := reg.Specs[name]; ok {
				def.Supers = append(def.Supers, sup)
			} else {
				c.errorf(n.Span(), "unknown super-spec %q of spec %q", name, n.Name)
			}
		}
	}

	saved := c.curSelf
	c.curSelf = &types.Param{Name: "This"} // abstract self while resolving signatures
	for _, m := range n.Members {
		switch mem := m.(type) {
		case *ast.FnSig:
			def.Methods = append(def.Methods, &types.SpecMethod{
				Name: mem.Name, Mut: mem.Mut, Provided: false,
				Sig: c.fnSig(mem.Params, mem.Ret, mem.Unsafe), Decl: mem,
			})
		case *ast.FuncDecl:
			def.Methods = append(def.Methods, &types.SpecMethod{
				Name: mem.Name, Mut: mem.Mut, Provided: true,
				Sig: c.fnSig(mem.Params, mem.Ret, mem.Unsafe), Decl: mem,
			})
		case *ast.AssocType:
			def.AssocTypes = append(def.AssocTypes, &types.AssocTypeDef{
				Name: mem.Name, Bound: c.resolveSpecBound(reg, mem.Bound),
			})
		case *ast.AssocVal:
			def.AssocVals = append(def.AssocVals, &types.AssocValDef{
				Name: mem.Name, Of: c.resolveType(mem.Type),
			})
		}
	}
	c.curSelf = saved
}

// collectImpl collects one impl block: it resolves the target type, the spec (for
// a spec impl), each method (registered in the per-type method namespace), and
// the associated-type and value bindings; then validates the bindings against the
// spec's requirements. Member signatures resolve with 'This' bound to the target.
func (c *checker) collectImpl(reg *SpecRegistry, n *ast.ImplDecl) {
	target := c.resolveType(n.Target)
	impl := &types.ImplDef{
		Target:    target,
		Methods:   map[string]*types.ImplMethod{},
		AssocBind: map[string]types.Type{},
		ValBind:   map[string]types.ConstVal{},
		Decl:      n,
	}
	if n.Spec != nil {
		impl.Spec = c.resolveSpecRef(reg, n.Spec)
	}

	saved := c.curSelf
	c.curSelf = target
	for _, item := range n.Items {
		switch it := item.(type) {
		case *ast.FuncDecl:
			m := &types.ImplMethod{
				Name: it.Name, Mut: it.Mut,
				Sig: c.fnSig(it.Params, it.Ret, it.Unsafe), Decl: it,
			}
			impl.Methods[it.Name] = m
			c.addMethod(reg, target, impl, m)
		case *ast.AssocBind:
			impl.AssocBind[it.Name] = c.resolveType(it.Type)
		case *ast.ValBind:
			if v, ok := c.foldConst(it.Value.X); ok {
				impl.ValBind[it.Name] = v
			} else {
				c.errorf(it.Span(), "associated value %q must be a compile-time constant", it.Name)
			}
		}
	}
	c.curSelf = saved

	reg.Impls = append(reg.Impls, impl)
	if impl.Spec != nil {
		c.checkAssocBindings(n, impl)
	}
}

// addMethod inserts a method into the target type's one method namespace,
// reporting a duplicate: every method and associated fn on a type — spec or
// inherent alike — shares one namespace (GRAMMAR group 7). Iteration 1 tracks the
// namespace for nominal (struct/enum) targets only; primitive and composite
// targets are deferred to a later iteration.
func (c *checker) addMethod(reg *SpecRegistry, target types.Type, impl *types.ImplDef, m *types.ImplMethod) {
	head := nominalHead(target)
	if head == nil {
		return
	}
	ms := reg.Methods[head]
	if ms == nil {
		ms = &types.MethodSet{Owner: head, Methods: map[string]types.MethodRef{}}
		reg.Methods[head] = ms
	}
	if _, dup := ms.Methods[m.Name]; dup {
		c.errorf(m.Decl.(*ast.FuncDecl).Span(),
			"type %s already declares method %q; spec and inherent methods share one namespace",
			head.Name, m.Name)
		return
	}
	ms.Methods[m.Name] = types.MethodRef{Impl: impl, Method: m}
}

// checkAssocBindings validates a spec impl's associated bindings against the
// spec's requirements (DESIGN-1c §1.4): every required associated type and value
// must be supplied, no unknown binding may be present, and each value's kind must
// match its declared type.
func (c *checker) checkAssocBindings(n *ast.ImplDecl, impl *types.ImplDef) {
	spec := impl.Spec
	haveType := map[string]bool{}
	for _, at := range spec.AssocTypes {
		haveType[at.Name] = true
		if _, ok := impl.AssocBind[at.Name]; !ok {
			c.errorf(n.Span(), "impl of spec %s is missing associated type %q", spec.Name, at.Name)
		}
	}
	haveVal := map[string]bool{}
	for _, av := range spec.AssocVals {
		haveVal[av.Name] = true
		if v, ok := impl.ValBind[av.Name]; !ok {
			c.errorf(n.Span(), "impl of spec %s is missing associated value %q", spec.Name, av.Name)
		} else if !constMatches(v, av.Of) {
			c.errorf(n.Span(), "associated value %q of spec %s: value does not match type %s",
				av.Name, spec.Name, av.Of)
		}
	}
	for name := range impl.AssocBind {
		if !haveType[name] {
			c.errorf(n.Span(), "impl of spec %s binds unknown associated type %q", spec.Name, name)
		}
	}
	for name := range impl.ValBind {
		if !haveVal[name] {
			c.errorf(n.Span(), "impl of spec %s binds unknown associated value %q", spec.Name, name)
		}
	}
}

// resolveSpecRef resolves an impl's 'Spec for' name to its collected SpecDef,
// reporting an unknown spec.
func (c *checker) resolveSpecRef(reg *SpecRegistry, t ast.Type) *types.SpecDef {
	name := specRefName(t)
	if sp, ok := reg.Specs[name]; ok {
		return sp
	}
	c.errorf(t.Span(), "unknown spec %q", name)
	return nil
}

// resolveSpecBound resolves an associated type's super-spec bound to the specs it
// names. An unknown name is skipped here (bound satisfaction is U3); only names
// that resolve to a collected spec are recorded.
func (c *checker) resolveSpecBound(reg *SpecRegistry, b *ast.Bound) []*types.SpecDef {
	if b == nil {
		return nil
	}
	var out []*types.SpecDef
	for _, name := range b.Names {
		if sp, ok := reg.Specs[name]; ok {
			out = append(out, sp)
		}
	}
	return out
}

// fnSig builds a resolved function type from a member's parameters and return.
// The implicit 'this' receiver is not a parameter, so it is not included.
func (c *checker) fnSig(params []ast.Param, ret ast.Type, unsafe bool) *types.Fn {
	fn := &types.Fn{Unsafe: unsafe}
	for i := range params {
		fn.Params = append(fn.Params, types.Param0{Type: c.resolveType(params[i].Type), ByRef: params[i].Ref})
	}
	if ret != nil {
		fn.Ret = c.resolveType(ret)
	}
	return fn
}

// nominalHead returns the *TypeDef a nominal (struct or enum) type refers to, or
// nil for a primitive or composite type.
func nominalHead(t Type) *types.TypeDef {
	switch x := t.(type) {
	case *types.Struct:
		return x.Def
	case *types.Enum:
		return x.Def
	}
	return nil
}

// specRefName reads the name of a spec reference in an impl header ('impl Spec
// for T'), which the grammar spells as a named type.
func specRefName(t ast.Type) string {
	if ref, ok := t.(*ast.TypeRef); ok {
		return ref.Name
	}
	return ""
}

// constMatches reports whether a folded compile-time value fits an associated
// value's declared type: a boolean fits a bool type, an integer fits any numeric
// type.
func constMatches(v types.ConstVal, of Type) bool {
	switch v.Kind {
	case types.KBool:
		return of.Kind() == types.KBool
	case types.KInt:
		return isNumeric(of)
	}
	return true
}
