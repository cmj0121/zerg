package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Built-in map[K, V] methods (docs/collections.md, GRAMMAR group 4). A map is not a
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
		c.check(n.Args[0].Value, mt.Key)
		return &types.Opt{Elem: mt.Val}
	}
	c.errorf(n.Span(), "a map has no method %q", fld.Name)
	c.synthArgs(n)
	return Invalid
}
