package types

// This file adds the Phase 1c spec/impl model: the pure, AST-free data the
// semantic core collects for every 'spec' and 'impl' declaration (FORK-D — the
// model lives here beside TypeDef, and the algorithms that build it live in
// internal/sema). A Decl field carries the originating *ast.* node as an 'any' so
// this leaf package keeps no dependency on the syntax tree; sema type-asserts it
// back when it needs the source form.

// SpecDef is a collected 'spec' declaration: its methods (required signatures and
// provided defaults), its associated types and values, and its resolved
// super-spec chain (the ':' bound, e.g. 'spec Ord: Eq'). Decl holds the
// *ast.SpecDecl.
type SpecDef struct {
	Name       string
	Supers     []*SpecDef
	Methods    []*SpecMethod
	AssocTypes []*AssocTypeDef
	AssocVals  []*AssocValDef
	Decl       any
}

// SpecMethod is one method of a spec: a required signature (Provided false, a
// method with no body) or a provided default (Provided true, a method with a
// body). Sig is the resolved signature; 'this' is the implicit receiver and the
// self type is the abstract 'This'. Decl holds the *ast.FnSig or *ast.FuncDecl.
type SpecMethod struct {
	Name     string
	Sig      *Fn
	Mut      bool
	Provided bool
	Decl     any
}

// AssocTypeDef is a spec's associated type 'type Item (: bound)?' — a type each
// impl must bind. Bound is the resolved super-spec conjunction the bound type
// must satisfy (empty when the associated type is unbounded).
type AssocTypeDef struct {
	Name  string
	Bound []*SpecDef
}

// AssocValDef is a spec's required associated value 'BITS: int' — a compile-time
// value of type Of that each impl must supply.
type AssocValDef struct {
	Name string
	Of   Type
}

// ImplDef is a collected 'impl' block: an inherent 'impl T { … }' (Spec nil) or a
// spec impl 'impl Spec for T { … }' (Spec set). It records the target type, its
// methods keyed by name, and any associated-type and associated-value bindings.
// Decl holds the *ast.ImplDecl.
type ImplDef struct {
	Spec      *SpecDef
	Target    Type
	Methods   map[string]*ImplMethod
	AssocBind map[string]Type
	ValBind   map[string]ConstVal
	Decl      any
}

// ImplMethod is one method or associated fn of an impl, with its resolved
// signature and a body. Decl holds the *ast.FuncDecl.
type ImplMethod struct {
	Name string
	Sig  *Fn
	Mut  bool
	Decl any
}

// MethodSet is a nominal type's single method namespace. Every method and
// associated fn on the type — from an inherent impl or a spec impl alike — shares
// it, so a duplicate name across those impls is an error (GRAMMAR group 7).
type MethodSet struct {
	Owner   *TypeDef
	Methods map[string]MethodRef
}

// MethodRef names a method's source: the impl that declares it and the method
// itself.
type MethodRef struct {
	Impl   *ImplDef
	Method *ImplMethod
}
