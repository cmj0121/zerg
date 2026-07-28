package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Built-in map[K, V] methods (docs/code/collections.md, GRAMMAR group 4). A map is not a
// user struct, so its methods are compiler intrinsics dispatched on the receiver's
// static `*types.Map` type (like the list intrinsics), typed here and lowered in
// emit_map.go. Two are modelled this iteration:
//   - `.len() -> int`        the entry count
//   - `.get(k: K) -> V?`     the checked access (None on a missing key)
//
// The forcing access `m[k]` (KeyError abort on miss), the index-assign `m[k] = v`, the
// membership `k in m`, and `for k in m` are handled on their own paths (inferBracket /
// checkAssign / the `in` operator / forInElem).
func (c *checker) mapMethodCall(n *ast.Call, fld *ast.Field, mt *types.Map) Type {
	switch fld.Name {
	case "len":
		if len(n.Args) != 0 {
			c.errorf(n.Span(), "len() takes no arguments, got %d", len(n.Args))
			c.synthArgs(n)
		}
		return Int
	case "get":
		if len(n.Args) != 1 {
			c.errorf(n.Span(), "get(k) takes exactly one argument, got %d", len(n.Args))
			c.synthArgs(n)
			return &types.Opt{Elem: mt.Val}
		}
		// The key argument must be ASSIGNABLE to the key type, exactly like `m[k]` and
		// `m[k] = v` (both use checkElem). A bare `check()` only context-types a literal
		// and never reports a mismatch, so `m.get(true)` on an int map would silently
		// coerce bool -> int and `m.get("x")` would emit `int64_t = "x"` (bad C to cc);
		// checkElem enforces assignability and reports a clean "map key" diagnostic.
		c.checkElem(n.Args[0].Value, mt.Key, "map key")
		return &types.Opt{Elem: mt.Val}
	}
	c.errorf(n.Span(), "a map has no method %q", fld.Name)
	c.synthArgs(n)
	return Invalid
}
