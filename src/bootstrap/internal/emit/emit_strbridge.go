package emit

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// The str <-> list bridge lowering (docs/collections.md). `list[byte](s)` / `list[rune](s)`
// decode a str to its octets / code points; `str(bytes)` / `str(runes)` build a str back,
// validating the str invariant and raising on violation. Each is one runtime call
// (str.c). The list argument passes by value — a header copy that shares the buffer — and
// the runtime only reads it, so the caller keeps ownership and drops the list as usual.

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
			return fmt.Sprintf("zrt_str_from_bytes(%s)", e.expr(arg)), true
		case types.Rune:
			return fmt.Sprintf("zrt_str_from_runes(%s)", e.expr(arg)), true
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
