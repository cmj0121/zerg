// Package types is the Phase 1b type IR: a small, AST-free representation of
// Zerg's types that both the semantic core (internal/sema) and the C backend
// (internal/emit) can share without a dependency cycle.
//
// Primitives are interned singletons (Int, Float, Bool, Str, Nil, …) so equality
// is pointer identity — the property the emitter relies on when it switches on a
// type. Composites (list/map/set/tuple/array/chan/fn/ptr, the optional wrapper,
// and Either) and nominal user types (struct/enum, identified by their *TypeDef)
// are ordinary heap values compared structurally by Identical. Generic parameters
// are modelled abstractly (Param / ValParam) and associated-type projections by
// Proj; Phase 1b never instantiates them.
//
// Zerg has no subtyping and generics are invariant, so the only relation is
// Identical (see equal.go); "assignable" adds a single literal-defaulting
// relaxation and lives in the checker, not here.
package types

// Kind tags a Type's top-level constructor. It lets a consumer dispatch on the
// shape of a type without a type switch.
type Kind int

const (
	// KInvalid is the error type: it propagates a prior error and is compatible
	// with every type so a single mistake does not cascade into many.
	KInvalid Kind = iota
	// KUnknown is a value whose type is known to exist but is not yet modelled —
	// a cross-module member or an un-modelled stdlib result (Phase 1b FORK-2/4).
	// It, too, is compatible with every type.
	KUnknown

	// primitives
	KInt
	KUint
	KFloat
	KBool
	KRune
	KByte
	KStr
	KNil
	// KFixedInt is a fixed-width numeric (i8..i64 / u8..u64 / f32 / f64).
	KFixedInt

	// composites
	KList
	KMap
	KSet
	KTuple
	KArray
	KChan
	KFn
	KPtr
	// KRef is the refcounted heap box 'Ref[T]' (memory.md): a value shared by
	// reference, not copied. It is the one non-POD box Phase 1d models.
	KRef
	// KOpt is the optional wrapper T?.
	KOpt
	// KEither is Either[X, Y] (Result[T] = Either[T, Err]); T? uses KOpt instead.
	KEither
	// KRange is a half-open/inclusive numeric range 'lo..hi' / 'lo..=hi' used as a
	// value (membership 'v in r', or a bound name). Its element type is integral.
	KRange

	// user-declared nominal types
	KStruct
	KEnum
	// KNamed is a strong typedef 'type X = Y' (a newtype): a distinct nominal type
	// over an underlying representation.
	KNamed

	// generics
	KParam    // an abstract, bound-constrained type parameter
	KValParam // a compile-time value parameter '[N: int]'
	KProj     // an associated-type projection 'I.Item'
)

// Type is a Zerg type. The unexported typ marker keeps the interface closed to
// this package; String renders the type as it is spelled in source (for
// diagnostics); Kind reports the top-level constructor.
type Type interface {
	typ()
	String() string
	Kind() Kind
}

// --- primitives ---------------------------------------------------------------

// Primitive is a built-in scalar type without further structure. Every primitive
// is an interned singleton, so two references to the same primitive are the same
// pointer and compare equal with ==.
type Primitive struct{ k Kind }

func (*Primitive) typ()         {}
func (p *Primitive) Kind() Kind { return p.k }

// String renders the primitive's source spelling.
func (p *Primitive) String() string {
	switch p.k {
	case KInvalid:
		return "<invalid>"
	case KUnknown:
		return "<unknown>"
	case KInt:
		return "int"
	case KUint:
		return "uint"
	case KFloat:
		return "float"
	case KBool:
		return "bool"
	case KRune:
		return "rune"
	case KByte:
		return "byte"
	case KStr:
		return "str"
	case KNil:
		return "nil"
	default:
		return "<primitive>"
	}
}

// The interned primitive singletons. Consumers compare against these by identity.
//
//nolint:gochecknoglobals // interned singletons are the type IR's public identity.
var (
	Invalid = &Primitive{KInvalid}
	Unknown = &Primitive{KUnknown}
	Int     = &Primitive{KInt}
	Uint    = &Primitive{KUint}
	Float   = &Primitive{KFloat}
	Bool    = &Primitive{KBool}
	Rune    = &Primitive{KRune}
	Byte    = &Primitive{KByte}
	Str     = &Primitive{KStr}
	Nil     = &Primitive{KNil}
)

// Fixed is a fixed-width numeric type: an integer of Bits width (Signed picks
// i/u) or, when Float is set, an IEEE float (f32/f64).
type Fixed struct {
	Bits   int
	Signed bool
	Float  bool
}

func (*Fixed) typ()       {}
func (*Fixed) Kind() Kind { return KFixedInt }

// String renders a fixed-width numeric's source spelling (e.g. i32, u8, f64).
func (f *Fixed) String() string {
	prefix := "i"
	switch {
	case f.Float:
		prefix = "f"
	case !f.Signed:
		prefix = "u"
	}
	return prefix + itoa(f.Bits)
}

// --- composites ---------------------------------------------------------------

// List is the homogeneous growable sequence 'list[T]'.
type List struct{ Elem Type }

func (*List) typ()             {}
func (*List) Kind() Kind       { return KList }
func (l *List) String() string { return "list[" + l.Elem.String() + "]" }

// Set is the homogeneous set 'set[T]'.
type Set struct{ Elem Type }

func (*Set) typ()             {}
func (*Set) Kind() Kind       { return KSet }
func (s *Set) String() string { return "set[" + s.Elem.String() + "]" }

// Map is the key/value mapping 'map[K, V]'.
type Map struct{ Key, Val Type }

func (*Map) typ()             {}
func (*Map) Kind() Kind       { return KMap }
func (m *Map) String() string { return "map[" + m.Key.String() + ", " + m.Val.String() + "]" }

// Tuple is the heterogeneous fixed-arity product '(A, B, …)'.
type Tuple struct{ Elems []Type }

func (*Tuple) typ()       {}
func (*Tuple) Kind() Kind { return KTuple }

// String renders the tuple's source spelling '(A, B, …)'.
func (t *Tuple) String() string {
	s := "("
	for i, e := range t.Elems {
		if i > 0 {
			s += ", "
		}
		s += e.String()
	}
	return s + ")"
}

// Array is the fixed-length array '[T; N]'; N is a compile-time value.
type Array struct {
	Elem Type
	N    ConstVal
}

func (*Array) typ()             {}
func (*Array) Kind() Kind       { return KArray }
func (a *Array) String() string { return "[" + a.Elem.String() + "; " + a.N.String() + "]" }

// ChanDir is a channel's direction: bidirectional, receive-only, or send-only.
// It mirrors ast.ChanDir but is redeclared here to keep this package AST-free.
type ChanDir int

const (
	// ChanBidi is a bidirectional channel 'chan[T]'.
	ChanBidi ChanDir = iota
	// ChanRecv is a receive-only channel '<-chan[T]'.
	ChanRecv
	// ChanSend is a send-only channel 'chan[T]<-'.
	ChanSend
)

// Chan is a channel 'chan[T]' with a direction.
type Chan struct {
	Elem Type
	Dir  ChanDir
}

func (*Chan) typ()       {}
func (*Chan) Kind() Kind { return KChan }

// String spells the DIRECTION, because a name that drops it says the same thing on both
// sides of a mismatch: "cannot bind chan[int] to a chan[int] binding" reported a real
// error while naming nothing that differed, and "cannot send on a receive-only channel
// chan[int]" named the rule it was enforcing and then printed the type that does not
// break it. Spelling is the source's: `<-chan[T]` receives, `chan[T]<-` sends.
func (c *Chan) String() string {
	switch c.Dir {
	case ChanRecv:
		return "<-chan[" + c.Elem.String() + "]"
	case ChanSend:
		return "chan[" + c.Elem.String() + "]<-"
	}
	return "chan[" + c.Elem.String() + "]"
}

// Param0 is one parameter position of a function type: its type and whether it is
// passed by mutable reference ('mut &').
type Param0 struct {
	Type  Type
	ByRef bool
}

// Fn is a function type 'unsafe? fn(params) -> ret'.
type Fn struct {
	Params []Param0
	Ret    Type
	Unsafe bool
}

func (*Fn) typ()       {}
func (*Fn) Kind() Kind { return KFn }

// String renders the function type's source spelling.
func (f *Fn) String() string {
	s := ""
	if f.Unsafe {
		s += "unsafe "
	}
	s += "fn("
	for i, p := range f.Params {
		if i > 0 {
			s += ", "
		}
		if p.ByRef {
			s += "mut &"
		}
		s += p.Type.String()
	}
	s += ")"
	// A function with no `-> type` returns nil, whether its Ret is a Go-nil (a resolved
	// `fn(...)` type) or the `nil` primitive (a declaration's signature). Render both the
	// same, so the same function type has one spelling — and one generated typedef.
	if !retNil(f.Ret) {
		s += " -> " + f.Ret.String()
	}
	return s
}

// Ptr is a raw pointer 'ptr[T]'; a nil Elem is the bare untyped pointer 'ptr'.
type Ptr struct{ Elem Type }

func (*Ptr) typ()       {}
func (*Ptr) Kind() Kind { return KPtr }

// String renders the pointer's source spelling.
func (p *Ptr) String() string {
	if p.Elem == nil {
		return "ptr"
	}
	return "ptr[" + p.Elem.String() + "]"
}

// Ref is the refcounted heap box 'Ref[T]' (memory.md): a reference-counted value
// carrying a T and a drop action, shared by reference so every copy names one
// referent and the last holder frees it. The referent is fixed at construction,
// which keeps refcounting cycle-free. Unlike every primitive/struct/tuple, a Ref
// is NOT plain-old-data: copying it retains and dropping it releases (Phase 1d U3).
type Ref struct{ Elem Type }

func (*Ref) typ()             {}
func (*Ref) Kind() Kind       { return KRef }
func (r *Ref) String() string { return "Ref[" + r.Elem.String() + "]" }

// Opt is the optional wrapper 'T?'.
type Opt struct{ Elem Type }

func (*Opt) typ()             {}
func (*Opt) Kind() Kind       { return KOpt }
func (o *Opt) String() string { return o.Elem.String() + "?" }

// Range is a numeric range value 'lo..hi' (half-open) or 'lo..=hi' (inclusive),
// over an integral element type. Inclusive marks the '..=' form; a range used only
// to drive a for-in loop never becomes a Range value (that stays sugar), so this
// type appears only for a range membership 'v in r' or a range bound to a name.
type Range struct {
	Elem      Type
	Inclusive bool
}

func (*Range) typ()             {}
func (*Range) Kind() Kind       { return KRange }
func (r *Range) String() string { return "range[" + r.Elem.String() + "]" }

// Either is the sum 'Either[X, Y]' (Result[T] = Either[T, Err]).
type Either struct{ Left, Right Type }

func (*Either) typ()       {}
func (*Either) Kind() Kind { return KEither }

// String renders the either's source spelling 'Either[X, Y]'.
func (e *Either) String() string {
	return "Either[" + e.Left.String() + ", " + e.Right.String() + "]"
}

// --- generics & projection ----------------------------------------------------

// Param is an abstract, bound-constrained type parameter. Under Phase 1b's
// abstract checking a Param is identical only to itself (pointer identity), and
// its body may use only the methods its Bounds' specs provide. Phase 1c resolves
// Bounds to the concrete specs the parameter carries: '[T: Ord]' yields the Ord
// spec closed over its super-specs (so Eq is present too), which the bound-
// satisfaction and operator-resolution passes consult. The slice is empty for an
// unbounded parameter. A Param is still compared purely by identity.
type Param struct {
	Name   string
	Bounds []*SpecDef
}

func (*Param) typ()             {}
func (*Param) Kind() Kind       { return KParam }
func (p *Param) String() string { return p.Name }

// ValParam is a compile-time value parameter '[N: int]': a named value of type Of
// that a call site fixes by local structural inference.
type ValParam struct {
	Name string
	Of   Type
}

func (*ValParam) typ()             {}
func (*ValParam) Kind() Kind       { return KValParam }
func (v *ValParam) String() string { return v.Name }

// Proj is an associated-type projection 'I.Item.Sub': the path is read off On
// (a Param with an Iterator-like bound, or a concrete type). It is opaque but
// distinct under abstract checking; a concrete impl binding is deferred to 1c.
type Proj struct {
	On   Type
	Path []string
}

func (*Proj) typ()       {}
func (*Proj) Kind() Kind { return KProj }

// String renders the projection's source spelling 'On.Seg.Seg'.
func (p *Proj) String() string {
	s := p.On.String()
	for _, seg := range p.Path {
		s += "." + seg
	}
	return s
}

// --- nominal user types -------------------------------------------------------

// TypeDef is the declaration-side structure of a user type, hung off a type
// symbol. A struct sets Struct, an enum sets Enum, a strong typedef sets Alias;
// Params are the declared generic parameters.
type TypeDef struct {
	Name   string
	Params []*Param
	Struct *StructDef
	Enum   *EnumDef
	Alias  Type // 'type X = Y' underlying type; nil unless this is an alias
}

// StructDef is a struct's field list.
type StructDef struct{ Fields []FieldDef }

// FieldDef is one struct field: its name, type, visibility, and whether it has a
// default value feeding the field-wise constructor.
type FieldDef struct {
	Name       string
	Type       Type
	Pub        bool
	HasDefault bool
}

// EnumDef is an enum's variant list. CStyle is true when every variant is
// fieldless, the only shape that may carry an integer discriminant.
type EnumDef struct {
	Variants []*VariantDef
	CStyle   bool
}

// VariantDef is one enum variant: its name, its payload element types (nil when
// fieldless), and its integer discriminant (nil unless C-style).
type VariantDef struct {
	Name    string
	Payload []Type
	Discr   *int64
}

// Struct is a use-site struct type: a reference to its TypeDef with any type
// arguments applied.
type Struct struct {
	Def  *TypeDef
	Args []Type
}

func (*Struct) typ()             {}
func (*Struct) Kind() Kind       { return KStruct }
func (s *Struct) String() string { return nominalString(s.Def.Name, s.Args) }

// Enum is a use-site enum type: a reference to its TypeDef with any type
// arguments applied.
type Enum struct {
	Def  *TypeDef
	Args []Type
}

func (*Enum) typ()             {}
func (*Enum) Kind() Kind       { return KEnum }
func (e *Enum) String() string { return nominalString(e.Def.Name, e.Args) }

// Named is a strong typedef 'type X = Y' (a newtype): a distinct nominal type over
// an underlying representation. Its Def carries the declared name and, once the
// checker has resolved it, the underlying type in Def.Alias. Two Named types are
// identical only when they share a *TypeDef, so a Celsius is not an int; a value
// crosses the boundary only through an explicit conversion ('Celsius(x)' / 'int(c)').
// It lowers to its underlying type in the backend (no runtime or emit cost), so the
// distinction lives purely at the type layer.
type Named struct{ Def *TypeDef }

func (*Named) typ()             {}
func (*Named) Kind() Kind       { return KNamed }
func (n *Named) String() string { return n.Def.Name }

// Underlying unwraps a strong typedef to its representation type, following a chain
// of newtypes ('type B = A' over 'type A = int') to the non-newtype it ultimately
// stands for; every other type is returned unchanged. Backend lowering and the
// scalar-conversion machinery use it so a newtype behaves as its underlying type once
// its identity has been checked.
func Underlying(t Type) Type {
	for {
		n, ok := t.(*Named)
		if !ok || n.Def == nil || n.Def.Alias == nil {
			return t
		}
		t = n.Def.Alias
	}
}

// nominalString renders a nominal type's source spelling, appending its type
// arguments '[A, B]' when applied, so a diagnostic distinguishes 'Box[int]' from
// 'Box[bool]' rather than printing a bare 'Box' for both.
func nominalString(name string, args []Type) string {
	if len(args) == 0 {
		return name
	}
	s := name + "["
	for i, a := range args {
		if i > 0 {
			s += ", "
		}
		s += a.String()
	}
	return s + "]"
}

// --- compile-time values ------------------------------------------------------

// ConstVal is a folded compile-time value, used for an array length '[T; N]' and
// (later) enum discriminants and value-generic arguments. Phase 1b only needs the
// integer and boolean cases; Known is false for an unfolded position.
//
// Name is set when the length is a symbolic value-generic parameter — an array
// element of a generic function's parameter type, '[int; N]'. A symbolic length is
// structurally compatible with any concrete length (constEqual); the checker binds
// it to a concrete value by local structural inference at each call site.
type ConstVal struct {
	Kind  Kind
	I     int64
	B     bool
	Known bool
	Name  string
}

// Symbolic reports whether this length is an unbound value-generic parameter.
func (v ConstVal) Symbolic() bool { return v.Name != "" && !v.Known }

// String renders the constant value for diagnostics.
func (v ConstVal) String() string {
	switch {
	case v.Symbolic():
		return v.Name
	case !v.Known:
		return "?"
	case v.Kind == KBool:
		if v.B {
			return "true"
		}
		return "false"
	default:
		return itoa64(v.I)
	}
}

// --- tiny integer formatting (avoids a strconv import in this leaf package) ----

func itoa(n int) string { return itoa64(int64(n)) }

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
