package run

import "github.com/cmj/zerg/src/bootstrap/internal/syntax"

// Value is the interpreter's runtime value. We use a struct-with-tag rather
// than an interface because the type checker has already constrained operand
// types statically — so a binary `+` knows at parse time whether to read .Int
// or .Float, with no runtime type assertion needed. The tagged-struct shape
// also lets a fresh Value be a stack value with no allocation.
//
// Type points at one of the syntax package's *Type singletons (TInt, TFloat,
// TBool, TStr). Equality on Type is pointer equality by construction.
type Value struct {
	Type  *syntax.Type
	Int   int64   // valid when Type == syntax.TInt()
	Float float64 // valid when Type == syntax.TFloat()
	Bool  bool    // valid when Type == syntax.TBool()
	Str   string  // valid when Type == syntax.TStr()
}

// intVal builds an int Value.
func intVal(x int64) Value { return Value{Type: syntax.TInt(), Int: x} }

// floatVal builds a float Value.
func floatVal(x float64) Value { return Value{Type: syntax.TFloat(), Float: x} }

// boolVal builds a bool Value.
func boolVal(b bool) Value { return Value{Type: syntax.TBool(), Bool: b} }

// strVal builds a str Value.
func strVal(s string) Value { return Value{Type: syntax.TStr(), Str: s} }
