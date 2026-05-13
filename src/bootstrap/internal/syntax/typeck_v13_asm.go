package syntax

// typeck_v13_asm.go holds the v0.13 inline-asm typecheck pass. The parser
// (parser_v13_asm.go) splits the body into chunks at `${name}` markers; here
// we walk the AsmChunkInterp chunks and resolve each name against the
// enclosing scope, enforcing the surface contract from PLAN pin 5 plus the
// v0.14 extensions:
//
//   - `name` must resolve to an in-scope binding. Unknown names reject with
//     a focused diagnostic anchored on the interp position.
//   - Accepted types are `byte` (lowered to an immediate operand), `int`
//     (lowered to a register-width input or output operand — v0.14), or
//     `list[byte]` (lowered to a `.data` pointer to the first byte).
//     Every other type rejects.
//   - When the binding is declared `mut` AND the type is `int` or `byte`,
//     the chunk is marked as an output operand. Cgen emits these as GCC
//     `"+r"` inout operands so the asm body can write a value back into
//     the binding (e.g. a syscall return value). `mut list[byte]` is NOT
//     a write-back surface — the cgen contract lowers `.data`, and we do
//     not currently support pointer-rebinding through inline asm.
//
// AsmChunkText chunks pass through untouched — the body bytes are opaque to
// typeck. The binder pass runs even for empty bodies because the rest of
// the checker expects every node walked here to leave the scope chain
// unchanged regardless of body shape.

// checkAsmBlock validates the interpolation references inside the block. It
// is called from the main statement walk in typeck.go's checkStmt switch.
func (c *checker) checkAsmBlock(s *AsmBlock) error {
	for i := range s.Chunks {
		chunk := &s.Chunks[i]
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
		if !isAsmInterpAcceptedType(t) {
			return typeErr(chunk.NamePos,
				"asm interpolation '${%s}' must be byte, int, or list[byte], got %s",
				chunk.Name, t)
		}
		// Stamp the resolved type onto the chunk so cgen can dispatch on it
		// without re-running scope resolution. Pointer-into-slice write so
		// the value actually lands in the AST node.
		chunk.BoundType = t
		// Mut int / mut byte → output operand. The cgen will emit `"+r"`.
		// Immutable bindings (let / const) stay input-only regardless of
		// type. list[byte] is never an output even when the binding is
		// mut: the cgen lowers `.data` for it, and pointer-rebinding is
		// not a supported write-back surface.
		if b.kind == bindMut && isAsmInterpOutputCapableType(t) {
			chunk.IsOutput = true
		}
	}
	return nil
}

// isAsmInterpAcceptedType returns true iff t is one of the cgen-lowerable
// operand types: `byte`, `int`, or `list[byte]`. Any other type (including
// `list[int]`, `list[rune]`, `rune`, `str`, or a struct wrapping a byte)
// rejects so we don't pretend to support shapes the cgen has no contract
// for.
func isAsmInterpAcceptedType(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == TypeByte || t.Kind == TypeInt {
		return true
	}
	if t.Kind == TypeList && t.Element != nil && t.Element.Kind == TypeByte {
		return true
	}
	return false
}

// isAsmInterpOutputCapableType returns true iff a `mut` binding of this
// type may serve as an asm output operand. Register-sized scalars qualify
// (`int` lowers to int64_t, `byte` lowers to uint8_t — GCC's "+r" handles
// both). `list[byte]` is excluded; see checkAsmBlock for the rationale.
func isAsmInterpOutputCapableType(t *Type) bool {
	if t == nil {
		return false
	}
	return t.Kind == TypeInt || t.Kind == TypeByte
}
