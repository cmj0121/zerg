package emit

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file lowers the binary operations over `str`. A Zerg `str` is a NUL-terminated
// `const char*`, so neither operator the language gives it maps to a C operator on
// that pointer:
//
//   - `a + b` concatenates into a NEW str (docs/collections.md: `str` implements Add),
//     which is the runtime's zrt_str_concat — it allocates a fresh Zerg-owned string.
//   - a comparison sorts lexicographically by code point (`str` implements Ord), which
//     is strcmp. A native `==` would compare POINTERS instead, so two equal strings
//     built by different routes would silently test unequal — the reason
//     registerPrimitiveBlessed used to withhold Eq/Ord from `str` altogether.
//
// Both are gated on the operand type being `str`, so every other operand keeps its
// native C operator and its emitted C stays byte-identical.

// strBinary lowers a binary operation over two `str` operands. It reports false for
// any other operand type, leaving the caller on the native C operator path.
func (e *emitter) strBinary(n *ast.Binary) (string, bool) {
	if !e.isStrExpr(n.L) || !e.isStrExpr(n.R) {
		return "", false
	}
	switch n.Op {
	case token.Plus:
		return e.strConcat(n.L, n.R), true
	case token.EqEq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return e.strCompare(n), true
	}
	return "", false
}

// strCompare lowers a str comparison to strcmp. Unmanaged it is byte-identical to the
// pre-S2 backend. Managed, an operand that is an owned producer (a concat/f-string/
// conversion temporary) must be released after strcmp reads it, or it leaks; a borrowed
// variable or immortal literal is left alone.
func (e *emitter) strCompare(n *ast.Binary) string {
	lc, rc := e.expr(n.L), e.expr(n.R)
	op := binaryOp(n.Op)
	lOwned, rOwned := e.strManaged && e.strOwned(n.L), e.strManaged && e.strOwned(n.R)
	if !lOwned && !rOwned {
		return fmt.Sprintf("(strcmp(%s, %s) %s 0)", lc, rc, op)
	}
	lv, rv, cv := e.freshName("cl"), e.freshName("cr"), e.freshName("cc")
	rel := ""
	if lOwned {
		rel += fmt.Sprintf(" zrt_str_release(%s);", lv)
	}
	if rOwned {
		rel += fmt.Sprintf(" zrt_str_release(%s);", rv)
	}
	return fmt.Sprintf("({ const char *%s = %s; const char *%s = %s; int %s = strcmp(%s, %s);%s (%s %s 0); })",
		lv, lc, rv, rc, cv, lv, rv, rel, cv, op)
}

// strConcat lowers `a + b` over strings to zrt_str_concat. In an unmanaged program the
// result is a leaked heap string and the operands are borrowed reads — byte-identical to
// the pre-S2 backend. In a MANAGED program every operand is a cell: concat READS both and
// returns a fresh rc=1 cell, so an operand that is itself an owned producer (a nested
// concat / f-string fragment / conversion, i.e. not a borrowed lvalue) must be RELEASED
// after the read or it leaks — an immortal literal or a borrowed variable is left alone
// (release no-ops on the former, and the latter is owned by its binding). The escaping
// result is left for its consumer (a binding drops it; a print releases it).
func (e *emitter) strConcat(l, r ast.Expr) string {
	lc, rc := e.expr(l), e.expr(r)
	if !e.strManaged {
		return fmt.Sprintf("zrt_str_concat(%s, %s)", lc, rc)
	}
	return e.strConcatC(lc, rc, e.strOwned(l), e.strOwned(r))
}

// strConcatC joins two already-rendered managed-str operands, releasing each one that is
// an owned producer after the concat consumes it. When neither operand is owned it is a
// bare call (the common `var + var`); otherwise a statement-expression captures both,
// concatenates, releases the owned ones, and yields the fresh result.
func (e *emitter) strConcatC(lc, rc string, lOwned, rOwned bool) string {
	if !lOwned && !rOwned {
		return fmt.Sprintf("zrt_str_concat(%s, %s)", lc, rc)
	}
	lv, rv, ov := e.freshName("sl"), e.freshName("sr"), e.freshName("so")
	rel := ""
	if lOwned {
		rel += fmt.Sprintf(" zrt_str_release(%s);", lv)
	}
	if rOwned {
		rel += fmt.Sprintf(" zrt_str_release(%s);", rv)
	}
	return fmt.Sprintf("({ const char *%s = %s; const char *%s = %s; const char *%s = zrt_str_concat(%s, %s);%s %s; })",
		lv, lc, rv, rc, ov, lv, rv, rel, ov)
}

// strOwned reports whether a managed-str expression yields an OWNED value the consumer
// must release: a fresh producer (a concat/f-string/conversion rvalue) or a literal
// (immortal — release no-ops) is owned; a borrowed lvalue (a variable/field/container
// index) is not, since its binding owns it. It mirrors namesStorage inverted.
func (e *emitter) strOwned(x ast.Expr) bool {
	return !e.namesStorage(x)
}

// strLiteral lowers a `str` literal value. Unmanaged, it is the C string literal,
// byte-identical to the pre-S2 backend. Managed, every str is a cell, so a literal is an
// IMMORTAL cell in static storage reached through zrt_ref_payload: a self-contained
// statement-expression with a file-lifetime `static` cell, so retain/release (no-ops on
// immortal) and strcmp all work uniformly with heap strings.
func (e *emitter) strLiteral(value string) string {
	if !e.strManaged {
		return cString(value)
	}
	s := e.freshName("slit")
	// b holds the decoded bytes plus a NUL; C counts decoded bytes, so the initializer
	// spelling (with escapes) sizes to len(value)+1 regardless of how it is escaped.
	n := len(value) + 1
	return fmt.Sprintf("({ static struct { zrt_ref_hdr h; char b[%d]; } %s = {{ZRT_RC_IMMORTAL, NULL}, %s}; (const char *)zrt_ref_payload(&%s); })",
		n, s, cString(value), s)
}

// programProducesHeapStr reports whether the program produces a runtime heap string —
// a str concatenation, an f-string that renders/joins, or a `str(bytes|runes)`
// conversion. It is the S2 gate: when true, str is managed (every str value is a cell);
// when false, str stays a bare const char* and the program is byte-identical.
func (e *emitter) programProducesHeapStr() bool {
	return e.programUsesStrConcat() || e.programUsesFormat() || e.programUsesStrConv() ||
		e.programUsesStrIntrinsic()
}

// programUsesStrIntrinsic reports whether the program lowers a str-PRODUCING runtime
// intrinsic — the os leaves `__zrt_getenv` / `__zrt_platform` / `__zrt_arch`, each of
// which returns a FRESH str cell (sys.c's sys_str_cell). Like the other heap-str
// producers this makes str managed, so a managed program retains/releases the cell
// instead of leaking it.
func (e *emitter) programUsesStrIntrinsic() bool {
	for node := range e.info.ExprTypes {
		call, ok := node.(*ast.Call)
		if !ok {
			continue
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			continue
		}
		if _, shadowed := e.info.Refs[id]; shadowed {
			continue
		}
		switch id.Name {
		case "__zrt_getenv", "__zrt_platform", "__zrt_arch":
			return true
		}
	}
	return false
}

// programUsesStrConv reports whether the program builds a str from a byte/rune list
// (`str(bytes)` / `str(runes)`), a heap producer (str.c). The list->str bridge is the
// only str-returning conversion; `list[byte](s)` goes the other way (it yields a list).
func (e *emitter) programUsesStrConv() bool {
	for node, t := range e.info.ExprTypes {
		call, ok := node.(*ast.Call)
		if !ok || t != sema.Str {
			continue
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok || id.Name != "str" || len(call.Args) != 1 {
			continue
		}
		if _, shadowed := e.info.Refs[id]; shadowed {
			continue
		}
		if lt, ok := e.info.ExprTypes[call.Args[0].Value].(*types.List); ok {
			if lt.Elem == types.Byte || lt.Elem == types.Rune {
				return true
			}
		}
	}
	return false
}

// isStrExpr reports whether an expression's type is `str` in the instance being
// emitted, reading through the instance's substitution overlay so a generic body
// monomorphized at T = str is recognized too.
func (e *emitter) isStrExpr(x ast.Expr) bool {
	if e.cur == nil {
		return e.info.ExprTypes[x] == sema.Str
	}
	return e.cur.ExprType(e.info, x) == sema.Str
}

// programUsesStrConcat reports whether the program concatenates two strings — the one
// str operation that needs the runtime, since zrt_str_concat rides in the
// always-linked fmt.c. A str COMPARISON needs only <string.h>, which every emitted
// program already includes. A program that concatenates nothing leaves the flag false
// and stays byte-identical.
func (e *emitter) programUsesStrConcat() bool {
	for node, t := range e.info.ExprTypes {
		if b, ok := node.(*ast.Binary); ok && b.Op == token.Plus && t == sema.Str {
			return true
		}
	}
	return false
}
