// The U5 cluster is the group 7 surface: type expressions (the composite forms
// tuple/array/chan/fn/ptr and the optional wrapper), the type-introducing
// declarations (struct, enum, type-alias, spec, impl, init), and the shared
// machinery they rest on — generics with spec bounds, decorators, and the
// const-expr marker. Every node embeds base like the rest of the tree; 'zerg
// fmt' reprints each one and the round-trip oracle keeps the reprints honest.
//
// A const-expr keeps only the surface form in this phase: the compile-time-
// foldability restriction (GRAMMAR group 7) is a semantic rule for 1b, so a
// discriminant '0xFF' or an array length '1_000' round-trips verbatim through
// the literal nodes' preserved Text.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// Type is any type expression (GRAMMAR group 7's 'type'): a named type, a
// composite (tuple/array/chan/fn/ptr), or an optional 'T?'.
type Type interface {
	Node
	typeNode()
}

// --- type expressions ---------------------------------------------------------

// OptType is an optional type 'T?' — a trailing '?' on a base-type makes the
// value nullable (GRAMMAR group 7).
type OptType struct {
	base
	Elem Type
}

// TupleType is a tuple type '(T, U, …)' of 2 or more element types.
type TupleType struct {
	base
	Elems []Type
}

// ArrayType is a fixed-length array type '[T; N]'; the ';' before the length is
// the array position GRAMMAR keeps, and N is a const-expr.
type ArrayType struct {
	base
	Elem  Type
	Count *ConstExpr
}

// ChanDir is a channel type's direction: bidirectional, receive-only, or
// send-only (GRAMMAR group 7/9).
type ChanDir int

const (
	// ChanBidi is a bidirectional channel 'chan[T]'.
	ChanBidi ChanDir = iota
	// ChanRecv is a receive-only channel '<-chan[T]'.
	ChanRecv
	// ChanSend is a send-only channel 'chan[T]<-'.
	ChanSend
)

// ChanType is a channel type 'chan[T]' with a direction (GRAMMAR group 7).
type ChanType struct {
	base
	Elem Type
	Dir  ChanDir
}

// PtrType is a raw pointer type: the bare address 'ptr' (Elem nil) or the typed
// pointer 'ptr[T]' (GRAMMAR group 12; a type only — every operation is unsafe).
type PtrType struct {
	base
	Elem Type // nil for the bare 'ptr'
}

// ConstExpr wraps an ordinary expression used in a compile-time-constant
// position — an array length '[T; N]', an enum discriminant, a decorator
// argument, or a value-generic argument. The semantic const-foldability check is
// deferred to 1b; here the wrapper only marks the position, and the inner
// expression's literals reprint their surface Text verbatim. It also satisfies
// Type so a value-generic argument can sit beside a type in a type-args list.
type ConstExpr struct {
	base
	X Expr
}

// --- generics & bounds --------------------------------------------------------

// Generics is a generic parameter list '[T: Bound, N: int]' (GRAMMAR group 7).
type Generics struct {
	base
	Params []*TypeParam
}

// TypeParam is one generic parameter 'id (: bound)?'. A spec bound marks a type
// parameter; a concrete-type bound marks a compile-time value parameter — the
// distinction is resolved by name resolution in 1b, so both are kept uniformly.
type TypeParam struct {
	base
	Name  string
	Bound *Bound // nil for a bare type parameter '[T]'
}

// Bound is a conjunction of spec names 'A + B' — a generic parameter's bound or a
// spec's super-spec (GRAMMAR group 7). A single concrete-type name is instead a
// value parameter's type; that is a name-resolution decision for 1b.
type Bound struct {
	base
	Names []string
}

// --- decorators ---------------------------------------------------------------

// Decorator is a '#[item, …]' directive prefix that binds to the declaration it
// leads (GRAMMAR group 7's decorated-decl).
type Decorator struct {
	base
	Items []*DecoItem
}

// DecoItem is one decorator item 'id' or 'id(arg, …)' — e.g. 'derive(Encode,
// Decode)', 'align(16)'. Each argument is a type-name or a const-expr, kept
// uniformly as a const-expr wrapper (the distinction is semantic, 1b).
type DecoItem struct {
	base
	Name string
	Args []*ConstExpr
}

// --- declarations -------------------------------------------------------------

// StructDecl is a product-type declaration 'pub? struct Name generics? { fields }'.
type StructDecl struct {
	base
	Decorators []*Decorator
	Pub        bool
	Name       string
	Generics   *Generics
	Fields     []*StructField
	End        []token.Trivia // dangling trivia before the closing '}'
}

// EnumDecl is a sum-type declaration 'pub? enum Name generics? { variants }'.
type EnumDecl struct {
	base
	Decorators []*Decorator
	Pub        bool
	Name       string
	Generics   *Generics
	Variants   []*Variant
	End        []token.Trivia
}

// TypeDecl is a strong typedef 'pub? type Name generics? = Type' (GRAMMAR group 7).
type TypeDecl struct {
	base
	Decorators []*Decorator
	Pub        bool
	Name       string
	Generics   *Generics
	Alias      Type
}

// SpecDecl is a behavioral interface 'pub? spec Name generics? (: super)? { members }'.
type SpecDecl struct {
	base
	Decorators []*Decorator
	Pub        bool
	Name       string
	Generics   *Generics
	Super      *Bound // super-spec bound 'spec Ord: Eq'; nil when none
	Members    []SpecMember
	End        []token.Trivia
}

// ImplDecl is an implementation block. A spec impl 'impl[G] Spec for Target { … }'
// carries a non-nil Spec; an inherent impl 'impl Target { … }' leaves Spec nil.
type ImplDecl struct {
	base
	Decorators []*Decorator
	Generics   *Generics
	Spec       Type // the implemented spec (a named type); nil for an inherent impl
	Target     Type // the type the impl is for
	Items      []ImplItem
	End        []token.Trivia
}

// InitDecl is a lazy module-setup block 'init() { … }' (GRAMMAR group 10; the
// run-once semantics are group 10, this slice parses the declaration).
type InitDecl struct {
	base
	Decorators []*Decorator
	Body       *Block
}

// StructField is one struct field 'pub? id: Type (= default)?'. A 'pub' field is
// externally accessible; a default feeds the field-wise constructor.
type StructField struct {
	base
	Pub     bool
	Name    string
	Type    Type
	Default Expr // nil when the field has no default
}

// Variant is one enum variant 'id (payload)? (= discriminant)?'. Payload holds a
// tuple-style payload's element types; Discr holds a C-style integer discriminant
// (GRAMMAR group 7 — payload and discriminant are mutually exclusive by a
// semantic rule checked in 1b).
type Variant struct {
	base
	Name    string
	Payload []Type     // the '(A, B)' payload element types; nil when fieldless
	Discr   *ConstExpr // the '= n' discriminant; nil when none
}

// --- spec members & impl items ------------------------------------------------

// SpecMember is one member of a spec body: a required method signature, a
// provided method (a FuncDecl with a body), an associated type, or an associated
// value (GRAMMAR group 7).
type SpecMember interface {
	Node
	specMemberNode()
}

// ImplItem is one item of an impl body: a method or associated function (a
// FuncDecl), an associated-type binding, or a value binding (GRAMMAR group 7).
type ImplItem interface {
	Node
	implItemNode()
}

// FnSig is a required method signature in a spec 'unsafe? mut? fn name generics?
// (params) -> ret?' — a method with no body (GRAMMAR group 7's fn-sig).
type FnSig struct {
	base
	Unsafe   bool
	Mut      bool
	Name     string
	Generics *Generics
	Params   []Param
	Ret      Type
}

// AssocType is a spec's associated type 'type Item (: bound)?' — a type each impl
// fills in (GRAMMAR group 7).
type AssocType struct {
	base
	Name  string
	Bound *Bound // 'type Item: Ord'; nil when unbounded
}

// AssocVal is a spec's required associated value 'BITS: int' — a compile-time
// value each impl supplies (GRAMMAR group 7).
type AssocVal struct {
	base
	Name string
	Type Type
}

// AssocBind is an impl's associated-type binding 'type Item = Type' — it
// satisfies a spec's associated type (GRAMMAR group 7).
type AssocBind struct {
	base
	Name string
	Type Type
}

// ValBind is an impl's value binding 'BITS := const-expr' — it satisfies a spec's
// associated value (GRAMMAR group 7).
type ValBind struct {
	base
	Name  string
	Value *ConstExpr
}

// --- marker methods -----------------------------------------------------------

func (*TypeRef) node()   {}
func (*OptType) node()   {}
func (*TupleType) node() {}
func (*ArrayType) node() {}
func (*ChanType) node()  {}
func (*FnType) node()    {}
func (*PtrType) node()   {}
func (*ConstExpr) node() {}

func (*Generics) node()  {}
func (*TypeParam) node() {}
func (*Bound) node()     {}
func (*Decorator) node() {}
func (*DecoItem) node()  {}

func (*StructDecl) node()  {}
func (*EnumDecl) node()    {}
func (*TypeDecl) node()    {}
func (*SpecDecl) node()    {}
func (*ImplDecl) node()    {}
func (*InitDecl) node()    {}
func (*StructField) node() {}
func (*Variant) node()     {}

func (*FnSig) node()     {}
func (*AssocType) node() {}
func (*AssocVal) node()  {}
func (*AssocBind) node() {}
func (*ValBind) node()   {}

func (*TypeRef) typeNode()   {}
func (*OptType) typeNode()   {}
func (*TupleType) typeNode() {}
func (*ArrayType) typeNode() {}
func (*ChanType) typeNode()  {}
func (*FnType) typeNode()    {}
func (*PtrType) typeNode()   {}
func (*ConstExpr) typeNode() {}

func (*StructDecl) declNode() {}
func (*EnumDecl) declNode()   {}
func (*TypeDecl) declNode()   {}
func (*SpecDecl) declNode()   {}
func (*ImplDecl) declNode()   {}
func (*InitDecl) declNode()   {}

// A provided method in a spec is a FuncDecl with a body; a required one is a
// FnSig. Both are spec members, and an impl item may be a FuncDecl too.
func (*FuncDecl) specMemberNode()  {}
func (*FnSig) specMemberNode()     {}
func (*AssocType) specMemberNode() {}
func (*AssocVal) specMemberNode()  {}

func (*FuncDecl) implItemNode()  {}
func (*AssocBind) implItemNode() {}
func (*ValBind) implItemNode()   {}
