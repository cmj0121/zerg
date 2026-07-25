package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Primitive conversion — `T(x)` (docs/types.md, "Type Conversion").
//
// Zerg converts by RE-CONSTRUCTION, never by reinterpretation: `T(x)` builds a new T
// from x's value, the way a constructor does. There is no C-style cast that views one
// type's bytes as another, and none is offered. Conversion is explicit by default —
// an `int` is not a `bool`; you build one with `bool(8)` — and the primitive
// conversions are compiler built-in, so a user type cannot add one to a primitive.
//
// Narrowing can lose the value, so it is checked like arithmetic: a conversion whose
// value does not fit the target RAISES OverflowError, which `guard { byte(x) }`
// demotes to a Result. The check itself lives in the backend (emit_conv.go) over the
// runtime's zrt_conv_* helpers; this file is the type-level half — the scalar shape
// both halves agree on, and the rule for which conversions exist at all.

// ScalarClass is the conversion-relevant shape of a scalar type: what the value is,
// independent of how wide it is.
type ScalarClass int

const (
	// ScalarNone is a type that is not a convertible scalar.
	ScalarNone ScalarClass = iota
	ScalarSigned
	ScalarUnsigned
	ScalarFloat
	ScalarBool
)

// Scalar is a scalar type's conversion shape: its class and its width in bits. It
// flattens the two spellings the type system carries — the named primitives
// (int/uint/byte/rune/float/bool) and the fixed-width family (i8..i64, u8..u64,
// f32/f64) — into one description, so the conversion rules are written once.
type Scalar struct {
	Class ScalarClass
	Bits  int
}

// ScalarOf reports a type's scalar shape, and whether it has one at all. `str` and
// `nil` deliberately do not: a str is built from a list[byte]/list[rune] with
// validation (docs/collections.md), which is a different mechanism.
func ScalarOf(t Type) (Scalar, bool) {
	// a strong typedef behaves as its underlying representation once its identity has
	// been checked, so `int(c)` extracts a Celsius and `Celsius(x)` re-constructs one.
	t = types.Underlying(t)
	switch x := t.(type) {
	case *types.Primitive:
		switch x.Kind() {
		case types.KInt:
			return Scalar{ScalarSigned, 64}, true
		case types.KUint:
			return Scalar{ScalarUnsigned, 64}, true
		case types.KRune:
			return Scalar{ScalarSigned, 32}, true
		case types.KByte:
			return Scalar{ScalarUnsigned, 8}, true
		case types.KFloat:
			return Scalar{ScalarFloat, 64}, true
		case types.KBool:
			return Scalar{ScalarBool, 1}, true
		}
	case *types.Fixed:
		switch {
		case x.Float:
			return Scalar{ScalarFloat, x.Bits}, true
		case x.Signed:
			return Scalar{ScalarSigned, x.Bits}, true
		default:
			return Scalar{ScalarUnsigned, x.Bits}, true
		}
	}
	return Scalar{}, false
}

// ConversionTarget reports the scalar a call is converting TO: the callee name must
// spell a primitive type, and the call's result type must be that type. The backend
// uses it to tell `byte(x)` — a conversion, since `byte` names a type — from a call
// of some user function. The comparison goes through types.Identical rather than
// pointer equality, because the fixed-width family (i32, u16, ...) is built fresh per
// mention and is not interned the way the named primitives are.
func ConversionTarget(name string, result Type) (Scalar, bool) {
	if named, ok := result.(*types.Named); ok {
		// a newtype conversion `X(x)`: the callee spells the newtype's own name and the
		// conversion targets its underlying scalar (ScalarOf unwraps it).
		if named.Def != nil && named.Def.Name == name {
			return ScalarOf(result)
		}
		return Scalar{}, false
	}
	t := primitiveNamed(name)
	if t == nil || result == nil || !types.Identical(t, result) {
		return Scalar{}, false
	}
	return ScalarOf(result)
}

// Lossless reports whether every value of the source scalar is representable in the
// destination, which is what lets the backend emit a plain C cast with no range check
// and no runtime call.
func Lossless(src, dst Scalar) bool {
	switch dst.Class {
	case ScalarBool:
		// `bool(x)` is a zero test, never out of range.
		return true
	case ScalarFloat:
		// Widening into a float never raises: an out-of-range float is +-Inf, which
		// docs/types.md makes an IEEE value rather than an abort. A float-to-float
		// narrowing (f64 -> f32) likewise saturates to +-Inf by IEEE rules.
		return true
	case ScalarSigned:
		switch src.Class {
		case ScalarBool:
			return true
		case ScalarSigned:
			return src.Bits <= dst.Bits
		case ScalarUnsigned:
			// an unsigned source needs one more bit than it has, for the sign.
			return src.Bits < dst.Bits
		}
	case ScalarUnsigned:
		switch src.Class {
		case ScalarBool:
			return true
		case ScalarUnsigned:
			return src.Bits <= dst.Bits
		case ScalarSigned:
			// a signed source may be negative, which no unsigned target holds.
			return false
		}
	}
	return false
}

// scalarConversion types a primitive conversion `T(x)`: exactly one argument, whose
// type must itself be a scalar, yielding T. It is reached only for a callee naming a
// primitive type that is not shadowed by a user binding.
func (c *checker) scalarConversion(n *ast.Call, name string, target Type) Type {
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "conversion %s(x) takes exactly one value, got %d", name, len(n.Args))
		c.synthArgs(n)
		return target
	}
	src := c.synth(n.Args[0].Value)
	if bad(src) {
		return target
	}
	// `int(s)` / `uint(s)` / `float(s)` parse a number from a str — a checked re-construction
	// that raises on a malformed or out-of-range string (docs/types.md). Any other target
	// (e.g. `bool(s)` / `byte(s)`) is not a parse.
	if src == Str {
		if target != Int && target != types.Uint && target != Float {
			c.errorf(n.Span(), "cannot parse a str to %s; only int / uint / float parse a string", name)
		}
		return target
	}
	// `int(v)` on an enum READS its stored discriminant (docs/types.md, GRAMMAR group 7).
	// Only a payload-free (C-style) enum carries a meaningful discriminant — its native
	// integer repr; a payload enum keeps its tag opaque and match-only, so reading it as
	// an int is rejected. `E.of(n)` (enumOfCall) is the reverse.
	if en, ok := types.Underlying(src).(*types.Enum); ok {
		if target != Int {
			c.errorf(n.Span(), "an enum discriminant reads as int, not %s", name)
			return target
		}
		if en.Def == nil || en.Def.Enum == nil || !en.Def.Enum.CStyle {
			c.errorf(n.Span(), "cannot read a discriminant from %s: only a payload-free (C-style) enum has one", src)
		}
		return Int
	}
	if _, ok := ScalarOf(src); !ok {
		c.errorf(n.Span(), "cannot convert %s to %s: only a scalar converts by re-construction", src, name)
		return target
	}
	return target
}
