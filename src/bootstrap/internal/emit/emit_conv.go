package emit

import (
	"fmt"
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file lowers the primitive conversion `T(x)` (docs/types.md, "Type
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
	// `int(s)` parses a decimal integer from a str through the runtime (raises on a
	// malformed string), which is not a scalar re-construction.
	if id.Name == "int" && e.cur.ExprType(e.info, n.Args[0].Value) == sema.Str {
		return fmt.Sprintf("zrt_parse_int(%s)", e.expr(n.Args[0].Value)), true
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
		// `int(s)` parses through the runtime, which can raise.
		if id.Name == "int" && e.info.ExprTypes[call.Args[0].Value] == sema.Str {
			return true
		}
		srcS, ok := sema.ScalarOf(e.info.ExprTypes[call.Args[0].Value])
		if ok && !sema.Lossless(srcS, dstS) {
			return true
		}
	}
	return false
}
