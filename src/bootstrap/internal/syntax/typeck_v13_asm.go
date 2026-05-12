package syntax

// typeck_v13_asm.go holds the v0.13 inline-asm typecheck pass. The parser
// (parser_v13_asm.go) splits the body into chunks at `${name}` markers; here
// we walk the AsmChunkInterp chunks and resolve each name against the
// enclosing scope, enforcing the surface contract from PLAN pin 5:
//
//   - `name` must resolve to an in-scope binding. Unknown names reject with
//     a focused diagnostic anchored on the interp position.
//   - The bound type must be `byte` (lowered as an immediate operand by cgen
//     U4) or `list[byte]` (lowered as a `.data` pointer to the first byte).
//     Every other type rejects.
//
// AsmChunkText chunks pass through untouched — the body bytes are opaque to
// typeck. The binder pass runs even for empty bodies because the rest of the
// checker expects every node walked here to leave the scope chain unchanged
// regardless of body shape.

// checkAsmBlock validates the interpolation references inside the block. It
// is called from the main statement walk in typeck.go's checkStmt switch.
func (c *checker) checkAsmBlock(s *AsmBlock) error {
	for _, chunk := range s.Chunks {
		if chunk.Kind != AsmChunkInterp {
			continue
		}
		b, ok := c.scope.lookup(chunk.Name)
		if !ok {
			return typeErr(chunk.NamePos,
				"asm interpolation '${%s}' references unknown name", chunk.Name)
		}
		t := b.typ
		if t == nil {
			// A binding with no type implies typeck hit the interp before
			// the declaration's RHS was checked. The statement walk is
			// strictly forward, so this is a "should not happen" branch —
			// emit a defensive diagnostic with the position so the
			// regression has somewhere obvious to land.
			return typeErr(chunk.NamePos,
				"asm interpolation '${%s}' has unresolved type", chunk.Name)
		}
		if isAsmInterpAcceptedType(t) {
			continue
		}
		return typeErr(chunk.NamePos,
			"asm interpolation '${%s}' must be byte or list[byte], got %s",
			chunk.Name, t)
	}
	return nil
}

// isAsmInterpAcceptedType returns true iff t is the byte primitive or a
// list[byte] composite — the two shapes cgen U4 knows how to lower into an
// inline-asm operand. Any other type (including list[int], list[rune], or
// a struct wrapping a byte) rejects so we don't pretend to support shapes
// the cgen has no contract for.
func isAsmInterpAcceptedType(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == TypeByte {
		return true
	}
	if t.Kind == TypeList && t.Element != nil && t.Element.Kind == TypeByte {
		return true
	}
	return false
}
