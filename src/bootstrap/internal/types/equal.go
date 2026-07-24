package types

// Identical reports whether two types are the same type. Zerg has no subtyping
// and generics are invariant, so this is plain structural equality: primitives by
// singleton identity, composites recursively, nominal types by *TypeDef identity
// with their type arguments identical pairwise, and generic parameters by
// pointer identity.
//
// KInvalid and KUnknown are treated as compatible with any type so that a prior
// error, or an un-modelled cross-module value, does not cascade into a storm of
// follow-on diagnostics.
func Identical(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b { // fast path: interned singletons and shared pointers
		return true
	}
	if a.Kind() == KInvalid || b.Kind() == KInvalid ||
		a.Kind() == KUnknown || b.Kind() == KUnknown {
		return true
	}
	if a.Kind() != b.Kind() {
		return false
	}
	switch x := a.(type) {
	case *Primitive:
		return x.k == b.(*Primitive).k
	case *Fixed:
		y := b.(*Fixed)
		return x.Bits == y.Bits && x.Signed == y.Signed && x.Float == y.Float
	case *List:
		return Identical(x.Elem, b.(*List).Elem)
	case *Set:
		return Identical(x.Elem, b.(*Set).Elem)
	case *Map:
		y := b.(*Map)
		return Identical(x.Key, y.Key) && Identical(x.Val, y.Val)
	case *Tuple:
		return identicalList(x.Elems, b.(*Tuple).Elems)
	case *Array:
		y := b.(*Array)
		return Identical(x.Elem, y.Elem) && constEqual(x.N, y.N)
	case *Chan:
		y := b.(*Chan)
		return x.Dir == y.Dir && Identical(x.Elem, y.Elem)
	case *Fn:
		return fnIdentical(x, b.(*Fn))
	case *Ptr:
		y := b.(*Ptr)
		if x.Elem == nil || y.Elem == nil {
			return x.Elem == nil && y.Elem == nil
		}
		return Identical(x.Elem, y.Elem)
	case *Ref:
		return Identical(x.Elem, b.(*Ref).Elem)
	case *Opt:
		return Identical(x.Elem, b.(*Opt).Elem)
	case *Either:
		y := b.(*Either)
		return Identical(x.Left, y.Left) && Identical(x.Right, y.Right)
	case *Struct:
		y := b.(*Struct)
		return x.Def == y.Def && identicalList(x.Args, y.Args)
	case *Enum:
		y := b.(*Enum)
		return x.Def == y.Def && identicalList(x.Args, y.Args)
	case *Param:
		return x == b.(*Param) // a type parameter equals only itself
	case *ValParam:
		return x == b.(*ValParam)
	case *Proj:
		y := b.(*Proj)
		return Identical(x.On, y.On) && sameStrings(x.Path, y.Path)
	default:
		return false
	}
}

func fnIdentical(x, y *Fn) bool {
	if x.Unsafe != y.Unsafe || len(x.Params) != len(y.Params) {
		return false
	}
	for i := range x.Params {
		if x.Params[i].ByRef != y.Params[i].ByRef ||
			!Identical(x.Params[i].Type, y.Params[i].Type) {
			return false
		}
	}
	// A function with no `-> type` returns nil, spelled either as a Go-nil Ret (a
	// resolved `fn(...)` type) or the `nil` primitive (a declaration's signature). The
	// two mean the same, so normalize before comparing.
	if retNil(x.Ret) != retNil(y.Ret) {
		return false
	}
	return retNil(x.Ret) || Identical(x.Ret, y.Ret)
}

// retNil reports whether a function-result type means "returns nil": an absent result
// (Go nil) or the `nil` primitive.
func retNil(t Type) bool {
	return t == nil || t.Kind() == KNil
}

func identicalList(a, b []Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !Identical(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// constEqual reports whether two compile-time array lengths are the same. An
// unknown length — an unfolded position or a symbolic value-generic parameter — is
// compatible with any length, so an unresolved '[T; N]' does not cascade errors
// (the checker binds a symbolic length by local inference before comparing).
func constEqual(a, b ConstVal) bool {
	if !a.Known || !b.Known {
		return true
	}
	return a.Kind == b.Kind && a.I == b.I && a.B == b.B
}
