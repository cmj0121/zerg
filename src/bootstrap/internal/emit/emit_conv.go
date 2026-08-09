package emit

import (
	"fmt"
	"math"
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file lowers the primitive conversion `T(x)` (docs/core/types.md, "Type
// Conversion"). Zerg converts by RE-CONSTRUCTION: the emitted C builds a new T from
// x's value, and a narrowing conversion whose value does not fit RAISES OverflowError
// through the runtime's zrt_conv_* helpers — which `guard { byte(x) }` catches.
//
// A conversion the type system proves lossless (byte -> int, say) emits a plain C
// cast with no call, so only the conversions that can actually fail pay for a check.

// convCallEmit lowers a primitive conversion `T(x)`, reporting false for any other
// call so the caller falls through to the ordinary call paths. It runs AFTER
// ptrCallEmit, which owns the `uint(p)` pointer cast.
func (e *emitter) convCallEmit(n *ast.Call) (string, bool) {
	id, ok := n.Callee.(*ast.Ident)
	if !ok || len(n.Args) != 1 {
		return "", false
	}
	dst := e.cur.ExprType(e.info, n)
	// A name bound to a user symbol is that symbol's call, never a conversion — EXCEPT
	// a newtype conversion `X(x)`, whose callee legitimately resolves to the type X.
	if !newtypeConv(id, dst) {
		if _, shadowed := e.info.Refs[id]; shadowed {
			return "", false
		}
	}
	dstS, ok := sema.ConversionTarget(id.Name, dst)
	if !ok {
		return "", false
	}
	// `int(s)` / `uint(s)` / `float(s)` parse a number from a str through the runtime
	// (raising on a malformed or out-of-range string), not a scalar re-construction.
	if e.cur.ExprType(e.info, n.Args[0].Value) == sema.Str {
		switch id.Name {
		case "int":
			return fmt.Sprintf("zrt_parse_int(%s)", e.expr(n.Args[0].Value)), true
		case "uint":
			return fmt.Sprintf("zrt_parse_uint(%s)", e.expr(n.Args[0].Value)), true
		case "float":
			return fmt.Sprintf("zrt_parse_float(%s)", e.expr(n.Args[0].Value)), true
		}
	}
	// `int(v)` on a C-style enum READS its stored discriminant — the enum's tagged-union
	// `tag` field holds the discriminant for a payload-free enum (sema restricts it there).
	if id.Name == "int" {
		if _, ok := e.cur.ExprType(e.info, n.Args[0].Value).(*types.Enum); ok {
			return fmt.Sprintf("((int64_t)(%s).tag)", e.expr(n.Args[0].Value)), true
		}
	}
	srcS, ok := sema.ScalarOf(e.cur.ExprType(e.info, n.Args[0].Value))
	if !ok {
		return "", false
	}
	return e.convExpr(dstS, srcS, e.ctype(dst), e.expr(n.Args[0].Value)), true
}

// newtypeConv reports whether the call `id(x)` is a strong-typedef conversion `X(x)`
// — the callee names the newtype and the result is that nominal type. Such a
// conversion legitimately resolves the callee to the type symbol, so the generic "a
// resolved ref is that symbol's call, not a conversion" guard must not skip it.
func newtypeConv(id *ast.Ident, dst sema.Type) bool {
	named, ok := dst.(*types.Named)
	return ok && named.Def != nil && named.Def.Name == id.Name
}

// convExpr renders the conversion itself: a zero test into bool, a plain cast when
// the source range provably fits, and otherwise a checked zrt_conv_* call carrying
// the target's bounds.
func (e *emitter) convExpr(dst, src sema.Scalar, ctype, x string) string {
	if dst.Class == sema.ScalarBool {
		if src.Class == sema.ScalarBool {
			return fmt.Sprintf("(%s)", x)
		}
		// `bool(8)` is a zero test — the re-construction of a truth value from a number.
		return fmt.Sprintf("((%s) != 0)", x)
	}
	if sema.Lossless(src, dst) {
		return fmt.Sprintf("(%s)(%s)", ctype, x)
	}
	// A RUNE's values are not a range, so no (lo, hi) pair states them: U+D800..U+DFFF
	// are surrogates, which exist only inside UTF-16 and are not characters. It is the
	// one target with a predicate of its own, and zrt_conv_rune is that predicate — the
	// same zrt_is_rune the UTF-8 validator and the encoder ask, so "a single valid
	// Unicode code point" is one sentence rather than three. A widening source never
	// reaches here (byte -> rune is Lossless above, and no byte is a surrogate).
	if dst.Rune {
		return fmt.Sprintf("(%s)zrt_conv_rune(%s)", ctype, toInt64(src, x))
	}
	if src.Class == sema.ScalarFloat {
		if dst.Class == sema.ScalarSigned {
			return fmt.Sprintf("(%s)zrt_conv_i_from_f((double)(%s), %s, %s)",
				ctype, x, floatLo(dst), floatHi(dst))
		}
		return fmt.Sprintf("(%s)zrt_conv_u_from_f((double)(%s), %s)", ctype, x, floatHi(dst))
	}
	switch {
	case dst.Class == sema.ScalarSigned && src.Class == sema.ScalarSigned:
		return fmt.Sprintf("(%s)zrt_conv_i_from_i((int64_t)(%s), %s, %s)", ctype, x, signedLo(dst), signedHi(dst))
	case dst.Class == sema.ScalarSigned && src.Class == sema.ScalarUnsigned:
		return fmt.Sprintf("(%s)zrt_conv_i_from_u((uint64_t)(%s), %s)", ctype, x, signedHi(dst))
	case dst.Class == sema.ScalarUnsigned && src.Class == sema.ScalarSigned:
		return fmt.Sprintf("(%s)zrt_conv_u_from_i((int64_t)(%s), %s)", ctype, x, unsignedHi(dst))
	default:
		return fmt.Sprintf("(%s)zrt_conv_u_from_u((uint64_t)(%s), %s)", ctype, x, unsignedHi(dst))
	}
}

// toInt64 brings a scalar to the int64 a bound test reads it as. A float cannot be
// cast there directly — an out-of-range double is undefined in C — so it goes through
// the same truncating range check `int(x)` is, and NaN and the infinities abort on that
// path rather than on the one after it.
func toInt64(src sema.Scalar, x string) string {
	if src.Class == sema.ScalarFloat {
		return fmt.Sprintf("zrt_conv_i_from_f((double)(%s), -9223372036854775808.0, 9223372036854775807.0)", x)
	}
	return fmt.Sprintf("(int64_t)(%s)", x)
}

// The target bounds, rendered as C. The 64-bit ends use <stdint.h>'s macros: the
// decimal spelling of INT64_MIN is not a C integer constant (it parses as a negation
// of a value one past INT64_MAX), and UINT64_MAX needs a suffix to stay unsigned.

func signedLo(s sema.Scalar) string {
	if s.Bits >= 64 {
		return "INT64_MIN"
	}
	return strconv.FormatInt(-(int64(1) << (s.Bits - 1)), 10)
}

func signedHi(s sema.Scalar) string {
	if s.Bits >= 64 {
		return "INT64_MAX"
	}
	return strconv.FormatInt((int64(1)<<(s.Bits-1))-1, 10)
}

func unsignedHi(s sema.Scalar) string {
	if s.Bits >= 64 {
		return "UINT64_MAX"
	}
	return strconv.FormatUint((uint64(1)<<s.Bits)-1, 10)
}

// The same bounds as double literals, for the float source. At 64 bits the decimal
// text rounds to the nearest double (2^63 / 2^64), which is exactly what the helper's
// open-interval test wants — it keeps the C cast inside the target width.

func floatLo(s sema.Scalar) string {
	if s.Bits >= 64 {
		return "-9223372036854775808.0"
	}
	return strconv.FormatInt(-(int64(1)<<(s.Bits-1)), 10) + ".0"
}

func floatHi(s sema.Scalar) string {
	if s.Class == sema.ScalarUnsigned {
		if s.Bits >= 64 {
			return "18446744073709551615.0"
		}
		return strconv.FormatUint((uint64(1)<<s.Bits)-1, 10) + ".0"
	}
	if s.Bits >= 64 {
		return "9223372036854775807.0"
	}
	return strconv.FormatInt((int64(1)<<(s.Bits-1))-1, 10) + ".0"
}

// programUsesCheckedConv reports whether the program has a conversion that can fail,
// which is the one shape needing the runtime (the zrt_conv_* helpers and the
// zrt_abort they raise through). A program whose every conversion is lossless emits
// plain casts and stays byte-identical.
func (e *emitter) programUsesCheckedConv() bool {
	for node, dst := range e.info.ExprTypes {
		call, ok := node.(*ast.Call)
		if !ok || len(call.Args) != 1 {
			continue
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			continue
		}
		if !newtypeConv(id, dst) {
			if _, shadowed := e.info.Refs[id]; shadowed {
				continue
			}
		}
		dstS, ok := sema.ConversionTarget(id.Name, dst)
		if !ok || dstS.Class == sema.ScalarBool {
			continue
		}
		// `int(s)` / `uint(s)` / `float(s)` parse through the runtime, which can raise.
		// Naming only `int` here left the other two emitting a zrt_parse_* call into a
		// translation unit with no runtime in it, and cc reported the undeclared function
		// against generated C — for two forms the documentation promises.
		if e.info.ExprTypes[call.Args[0].Value] == sema.Str {
			switch id.Name {
			case "int", "uint", "float":
				return true
			}
		}
		srcS, ok := sema.ScalarOf(e.info.ExprTypes[call.Args[0].Value])
		if ok && !sema.Lossless(srcS, dstS) {
			return true
		}
	}
	return false
}

// --- checked integer arithmetic (docs/core/types.md) -----------------------------
//
// `+`, `-`, `*`, `/`, `%`, the shifts and unary `-` all have inputs for which the
// mathematical answer is not representable, and C's answer for each of those is
// either a silent wrap or undefined behaviour. Zerg's is an OverflowError (a
// DivideByZeroError for a zero divisor) — that is what makes `+%`, `-%` and `*%`
// worth having, and while both spellings emitted the same C the pair meant nothing.
//
// A narrower target computes in 64 bits and then narrows through the SAME checked
// conversion helper `byte(x)` uses, so `byte(200) + byte(100)` raises rather than
// giving 44 — one rule, stated once, for every width the language has.

// arithHelper names the runtime helper for an operator, or "" when the operator has
// no failure mode (the bitwise three, and the `%`-suffixed wrapping forms, which are
// the whole point of the suffix).
func arithHelper(k token.Kind, unsigned bool) string {
	var name string
	switch k {
	case token.Plus:
		name = "add"
	case token.Minus:
		name = "sub"
	case token.Star:
		name = "mul"
	case token.Slash:
		name = "div"
	case token.Percent:
		name = "mod"
	case token.Shl:
		name = "shl"
	case token.Shr:
		name = "shr"
	default:
		return ""
	}
	if unsigned {
		return "zrt_" + name + "_u64"
	}
	return "zrt_" + name + "_i64"
}

// floorDiv renders `a // b`. Its result is always an `int`, so the operand types decide
// only HOW: two integers are the language's one integer division, already Euclidean and
// already checked, and a float pair goes through the helper that divides as a double and
// lands in int64 through the same range check `int(x)` is.
func (e *emitter) floorDiv(operand sema.Type, l, r string) string {
	e.needsRuntime = true
	if s, ok := sema.ScalarOf(operand); ok && s.Class == sema.ScalarFloat {
		return fmt.Sprintf("(zrt_floordiv_f((double)(%s), (double)(%s)))", l, r)
	}
	return fmt.Sprintf("(zrt_div_i64((int64_t)(%s), (int64_t)(%s)))", l, r)
}

// checkedArith renders a checked integer operation, or reports false when this
// expression is not one — a float, a bool, a str, or an operator that cannot fail.
func (e *emitter) checkedArith(t sema.Type, k token.Kind, l, r string) (string, bool) {
	s, ok := sema.ScalarOf(t)
	if !ok || (s.Class != sema.ScalarSigned && s.Class != sema.ScalarUnsigned) {
		return "", false
	}
	// a sub-64-bit unsigned operand fits int64 exactly, so it computes there and
	// narrows back; only a full-width unsigned needs the u64 helpers.
	unsigned := s.Class == sema.ScalarUnsigned && s.Bits >= 64
	helper := arithHelper(k, unsigned)
	if helper == "" {
		return "", false
	}
	wide := "int64_t"
	if unsigned {
		wide = "uint64_t"
	}
	var call string
	if (k == token.Shl || k == token.Shr) && !unsigned {
		// the shift's limit is the OPERAND's width, not the width it computes in; the
		// unsigned helpers are only ever chosen at 64 bits, so they take no width
		call = fmt.Sprintf("%s((%s)(%s), (%s)(%s), %d)", helper, wide, l, wide, r, s.Bits)
	} else {
		call = fmt.Sprintf("%s((%s)(%s), (%s)(%s))", helper, wide, l, wide, r)
	}
	return e.narrowArith(t, call), true
}

// checkedNeg renders unary `-`. Its one overflow is the type's minimum, which has no
// positive counterpart; `-%` is the wrapping form and stays the bare operator.
func (e *emitter) checkedNeg(t sema.Type, operand ast.Expr, x string) (string, bool) {
	// `-1` is the SPELLING of a negative constant, not an operation on 1 — the literal
	// is already range-checked by sema, and routing it through the runtime would pull
	// zergrt.h into every program that writes a negative number.
	if lit, ok := operand.(*ast.IntLit); ok && lit.Value != math.MinInt64 {
		return "", false
	}
	s, ok := sema.ScalarOf(t)
	if !ok {
		return "", false
	}
	// An UNSIGNED target has no negative values at all, so every negation but `-0`
	// leaves its range — which the narrowing conversion is already the check for. Only
	// a full-width unsigned is left alone: it does not fit the int64 the helper takes,
	// and `uint` is the one width where that matters.
	if s.Class != sema.ScalarSigned && s.Class != sema.ScalarUnsigned {
		return "", false
	}
	// A full-width unsigned takes a helper of its own: its top half is not int64's, so
	// the one above would raise on values it holds. Left with C's wrap it gave `-u` the
	// answer `-%u` is the way to ask for.
	if s.Class == sema.ScalarUnsigned && s.Bits >= 64 {
		return e.narrowArith(t, fmt.Sprintf("zrt_neg_u64((uint64_t)(%s))", x)), true
	}
	return e.narrowArith(t, fmt.Sprintf("zrt_neg_i64((int64_t)(%s))", x)), true
}

// narrowArith brings a 64-bit result back to a narrower target through the checked
// conversion, which is what makes the overflow of a `byte` a byte's overflow.
func (e *emitter) narrowArith(t sema.Type, call string) string {
	s, ok := sema.ScalarOf(t)
	if !ok || s.Bits >= 64 {
		return "(" + call + ")"
	}
	return e.convExpr(s, sema.Scalar{Class: sema.ScalarSigned, Bits: 64}, e.ctype(t), call)
}

// wrapArith is narrowArith for an operator that WRAPS, and the difference is the whole
// point of the `%` spelling. Both operands travel as an i64, so the wrap happens at 64
// bits — and then nothing brought it back at all: `byte(255) +% byte(1)` answered 256,
// which is not a byte, and `~b` for a byte answered the 64-bit complement. The result
// was right only where it landed straight back in a `byte` slot, because the binding's
// own conversion narrowed it.
//
// A CHECKED narrow is wrong here — it is the raise the `%` forms exist to opt out of —
// so the way back is a truncating cast, which for an unsigned target is a defined wrap
// and IS the answer being asked for. Only a narrow unsigned wraps: a `rune` is int32_t,
// where the same cast is implementation-defined, and a rune's values are a predicate
// rather than a range, so it keeps the checked narrow that refuses a surrogate.
func (e *emitter) wrapArith(t sema.Type, call string) string {
	s, ok := sema.ScalarOf(t)
	if !ok || s.Bits >= 64 {
		return "(" + call + ")"
	}
	if s.Class != sema.ScalarUnsigned {
		return e.narrowArith(t, call)
	}
	return fmt.Sprintf("((%s)(%s))", e.ctype(t), call)
}

// wrapsToWidth reports whether an operator's result has to be brought back to a narrow
// unsigned target by truncation: the three `%`-suffixed forms, the wrapping negate, and
// the complement. Each of them either has no failure mode or has opted out of the one
// it would have, so none goes through a checked helper — which is exactly why none of
// them was narrowed at all until this was asked.
func wrapsToWidth(k token.Kind) bool {
	switch k {
	case token.PlusMod, token.MinusMod, token.StarMod, token.Tilde:
		return true
	}
	return false
}

// programUsesCheckedArith reports whether the program has an arithmetic operation
// that can raise. It mirrors programUsesCheckedConv: the "zergrt.h" include is
// decided before any expression is rendered, so the question has to be asked of the
// type overlay rather than answered while lowering. A program whose every operator
// is bitwise, wrapping, or floating-point stays byte-identical.
func (e *emitter) programUsesCheckedArith() bool {
	opCalls := e.opCallNodes()
	for node, t := range e.info.ExprTypes {
		switch n := node.(type) {
		case *ast.Binary:
			if opCalls[n] {
				continue // an operator an impl provides is a call, not arithmetic
			}
			if n.Op == token.SlashDiv {
				return true
			}
			if _, ok := e.checkedArith(t, n.Op, "0", "0"); ok {
				return true
			}
		case *ast.Unary:
			if n.Op != token.Minus {
				continue
			}
			if _, ok := e.checkedNeg(t, n.X, "0"); ok {
				return true
			}
		}
	}
	return false
}

// opCallNodes is every operator ANY instance lowers to a method an impl provides.
// Dispatch is recorded per instance and this question is asked before any instance is
// current, so all of them are consulted — once, rather than per node.
func (e *emitter) opCallNodes() map[*ast.Binary]bool {
	all := map[*ast.Binary]bool{}
	for _, in := range e.prog.Funcs {
		for n := range in.OpCalls {
			all[n] = true
		}
	}
	return all
}
