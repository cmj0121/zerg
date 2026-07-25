package emit

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// The str <-> list bridge lowering (docs/collections.md). `list[byte](s)` / `list[rune](s)`
// decode a str to its octets / code points; `str(bytes)` / `str(runes)` build a str back,
// validating the str invariant and raising on violation. Each is one runtime call
// (str.c). `str(list)` CONSUMES its list argument (str.c drops it on both the success and
// the raise path), so strFromList hands the runtime an OWNED list: a fresh (rvalue) list
// goes straight over, while a named list passes a fresh copy so the owner keeps its own
// scope-exit drop. This is what makes `str(<temporary>)` leak-free even when it raises —
// an unwind can't skip a post-call drop that no longer exists.

// strBridgeEmit lowers a str<->list conversion, reporting false for any other call. It
// runs before the ordinary call paths so `str(...)` and `list[byte](...)` never fall
// through to a bogus `zg_str` / construction.
func (e *emitter) strBridgeEmit(n *ast.Call) (string, bool) {
	switch callee := n.Callee.(type) {
	case *ast.Ident:
		// str(bytes) / str(runes): a str built from a byte or rune list.
		if callee.Name != "str" || len(n.Args) != 1 {
			return "", false
		}
		if _, shadowed := e.info.Refs[callee]; shadowed {
			return "", false
		}
		arg := n.Args[0].Value
		lt, ok := e.cur.ExprType(e.info, arg).(*types.List)
		if !ok {
			return "", false
		}
		switch lt.Elem {
		case types.Byte:
			return e.strFromList("zrt_str_from_bytes", arg), true
		case types.Rune:
			return e.strFromList("zrt_str_from_runes", arg), true
		}
		return "", false
	case *ast.Bracket:
		// list[byte](s) / list[rune](s): a str's octets or code points.
		id, ok := callee.Base.(*ast.Ident)
		if !ok || id.Name != "list" || len(n.Args) != 1 {
			return "", false
		}
		lt, ok := e.cur.ExprType(e.info, n).(*types.List)
		if !ok {
			return "", false
		}
		switch lt.Elem {
		case types.Byte:
			return fmt.Sprintf("zrt_str_bytes(%s)", e.expr(n.Args[0].Value)), true
		case types.Rune:
			return fmt.Sprintf("zrt_str_runes(%s)", e.expr(n.Args[0].Value)), true
		}
	}
	return "", false
}

// strFromList lowers `str(list)` given the runtime consumer fn. Because the runtime
// CONSUMES (drops) the list, it must receive an OWNED one: a fresh rvalue goes straight
// over (already owned), while a named list (namesStorage) passes a deep copy so the
// runtime drops the copy and the named owner still drops its own at scope exit.
func (e *emitter) strFromList(fn string, arg ast.Expr) string {
	if !e.namesStorage(arg) {
		return fmt.Sprintf("%s(%s)", fn, e.expr(arg))
	}
	c := e.freshName("sfbcopy")
	return fmt.Sprintf("({ zrt_list %s; zrt_list_copy(&%s, &(%s)); %s(%s); })", c, c, e.expr(arg), fn, c)
}
