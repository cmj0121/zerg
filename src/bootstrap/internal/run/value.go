package run

import "github.com/cmj/zerg/src/bootstrap/internal/syntax"

// Value is the interpreter's runtime value. We use a struct-with-tag rather
// than an interface because the type checker has already constrained operand
// types statically — so a binary `+` knows at parse time whether to read .Int
// or .Float, with no runtime type assertion needed. The tagged-struct shape
// also lets a fresh Value be a stack value with no allocation.
//
// Type points at one of the syntax package's *Type singletons (TInt, TFloat,
// TBool, TStr, TByte, TRune) for primitives, or at a canonical composite
// *Type (list, tuple, struct, enum) for v0.2 shapes. Equality on Type is
// pointer equality for primitives and structural equality (via Type.Equals)
// for composites.
//
// Composite-shape fields:
//   - List: backing slice of element values. The slice is logically owned by
//     this Value; the deep-copy helpers (copyValue) duplicate it on every
//     bind / arg-pass / destructure to honour the v0.2 value-semantics rule.
//   - Tuple: per-position slice of values; same ownership rules as List.
//   - Fields: ordered struct field values, paired 1:1 with Type.Fields by
//     index (Type.Fields holds the names; Fields holds the runtime values).
//   - VariantIndex / VariantName: enum tag and human-readable name.
type Value struct {
	Type  *syntax.Type
	Int   int64   // valid when Type == syntax.TInt() / TByte() / TRune()
	Float float64 // valid when Type == syntax.TFloat()
	Bool  bool    // valid when Type == syntax.TBool()
	Str   string  // valid when Type == syntax.TStr()

	// Composite payloads. Only the field matching Type.Kind is read.
	List         []Value // TypeList
	Tuple        []Value // TypeTuple
	Fields       []Value // TypeStruct, indexed by Type.Fields position
	VariantIndex int     // TypeEnum
	VariantName  string  // TypeEnum
	Payload      []Value // TypeEnum, per-position payload values (v0.4)

	// v0.4 spec-as-type fat pointer. Set when Type.Kind == syntax.TypeSpec.
	// Data is the wrapped concrete value (struct or enum); ConcreteType is
	// the wrapped value's static type name so vtable lookup can route to the
	// right (Type, Spec) impl.
	Data         *Value // TypeSpec — the wrapped concrete value
	ConcreteType string // TypeSpec — the wrapped value's nominal type name
}

// intVal builds an int Value.
func intVal(x int64) Value { return Value{Type: syntax.TInt(), Int: x} }

// floatVal builds a float Value.
func floatVal(x float64) Value { return Value{Type: syntax.TFloat(), Float: x} }

// boolVal builds a bool Value.
func boolVal(b bool) Value { return Value{Type: syntax.TBool(), Bool: b} }

// strVal builds a str Value.
func strVal(s string) Value { return Value{Type: syntax.TStr(), Str: s} }

// byteVal builds a byte Value carrying an unsigned 0..255 codepoint.
func byteVal(c int64) Value { return Value{Type: syntax.TByte(), Int: c} }

// runeVal builds a rune Value carrying a Unicode codepoint.
func runeVal(c int64) Value { return Value{Type: syntax.TRune(), Int: c} }

// listVal builds a list Value with the given element type and elements. The
// elements slice is taken as-is — callers must already have copied where the
// value-semantics contract requires it.
func listVal(elem *syntax.Type, elems []Value) Value {
	return Value{Type: syntax.NewListType(elem), List: elems}
}

// tupleVal builds a tuple Value with the given per-position types and
// elements. The elements slice is taken as-is.
func tupleVal(elemTypes []*syntax.Type, elems []Value) Value {
	return Value{Type: syntax.NewTupleType(elemTypes), Tuple: elems}
}

// structVal builds a struct Value. structType is the canonical *Type from the
// type table; fields are in declaration order matching structType.Fields.
func structVal(structType *syntax.Type, fields []Value) Value {
	return Value{Type: structType, Fields: fields}
}

// enumVal builds an enum Value carrying the variant tag and name. v0.4 also
// carries the per-position payload slice for non-bare variants; callers pass
// nil for bare variants.
func enumVal(enumType *syntax.Type, idx int, name string, payload []Value) Value {
	return Value{Type: enumType, VariantIndex: idx, VariantName: name, Payload: payload}
}

// specVal wraps a concrete value into a spec-typed fat pointer. specType is
// the canonical *Type with Kind == syntax.TypeSpec; data is the underlying
// concrete value (struct or enum). The wrapper aliases data — typeck has
// already invalidated the source binding at the wrap site, so no observable
// aliasing surfaces.
func specVal(specType *syntax.Type, data Value) Value {
	d := data
	return Value{Type: specType, Data: &d, ConcreteType: data.Type.Name}
}

// copyValue returns a deep copy of v. Primitives copy by value (no-op);
// composites recursively duplicate their backing slices and any nested
// composites so a later mutation of the source can never leak into the copy.
//
// PLAN.md "value-copied lists": every let / mut / parameter pass / slice /
// destructure bind crosses copyValue. Tuples and structs follow the same
// rule because they may transitively contain a list. Enums are trivially
// value-copied.
func copyValue(v Value) Value {
	if v.Type == nil {
		return v
	}
	switch v.Type.Kind {
	case syntax.TypeList:
		out := make([]Value, len(v.List))
		for i, e := range v.List {
			out[i] = copyValue(e)
		}
		v.List = out
	case syntax.TypeTuple:
		out := make([]Value, len(v.Tuple))
		for i, e := range v.Tuple {
			out[i] = copyValue(e)
		}
		v.Tuple = out
	case syntax.TypeStruct:
		out := make([]Value, len(v.Fields))
		for i, e := range v.Fields {
			out[i] = copyValue(e)
		}
		v.Fields = out
	case syntax.TypeEnum:
		if len(v.Payload) > 0 {
			out := make([]Value, len(v.Payload))
			for i, e := range v.Payload {
				out[i] = copyValue(e)
			}
			v.Payload = out
		}
	case syntax.TypeSpec:
		if v.Data != nil {
			d := copyValue(*v.Data)
			v.Data = &d
		}
	}
	return v
}
