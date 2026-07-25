package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// The str <-> list bridge (docs/collections.md). A `str` is not indexable and holds no
// NUL; to scan or edit text you convert it to a `list[byte]` (raw octets) or a
// `list[rune]` (code points), and build a str back from such a list with `str(...)`,
// which validates the str invariant and raises on violation. These are the only
// conversions that involve `str` — it is not a scalar, so the scalar re-construction
// (convert.go) never applies to it.

// strToList types `list[byte](s)` / `list[rune](s)` — a str converted to its bytes or
// its code points. The argument must be a str and the element byte or rune.
func (c *checker) strToList(n *ast.Call, elem Type) Type {
	result := &types.List{Elem: elem}
	if !isByteOrRune(elem) {
		// not a str bridge — leave it to the ordinary paths (a genuine `list[T]` used as
		// something else is not modelled as a call, so this is just a clean fallthrough).
		return nil
	}
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "converting a str to %s takes exactly one str, got %d", result, len(n.Args))
		c.synthArgs(n)
		return result
	}
	src := c.synth(n.Args[0].Value)
	if !bad(src) && src != Str {
		c.errorf(n.Args[0].Value.Span(), "cannot convert %s to %s; only a str converts to a byte or rune list", src, result)
	}
	return result
}

// strFromList types `str(bytes)` / `str(runes)` — a str built from a byte or rune list,
// validated (valid UTF-8, no NUL) and raising on violation. The argument must be a
// `list[byte]` or `list[rune]`.
func (c *checker) strFromList(n *ast.Call) Type {
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "str(x) builds a str from one list[byte]/list[rune], got %d arguments", len(n.Args))
		c.synthArgs(n)
		return Str
	}
	src := c.synth(n.Args[0].Value)
	if !bad(src) && !isByteOrRuneList(src) {
		c.errorf(n.Args[0].Value.Span(), "cannot build a str from %s; str(x) takes a list[byte] or list[rune]", src)
	}
	return Str
}

// isByteOrRune reports whether a type is byte or rune — the two elements a str bridges
// through.
func isByteOrRune(t Type) bool {
	return t == types.Byte || t == types.Rune
}

// isByteOrRuneList reports whether a type is list[byte] or list[rune].
func isByteOrRuneList(t Type) bool {
	l, ok := t.(*types.List)
	return ok && isByteOrRune(l.Elem)
}
