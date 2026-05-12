package build

import (
	"fmt"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// cgen_v13_asm.go lowers an AsmBlock into a GCC `__asm__ volatile`
// statement. The lowering uses the GCC operand-substitution form:
//
//	__asm__ volatile ( "<template>" : : "r"(op0), "r"(op1), … : <clobbers> )
//
// `<template>` is the body bytes with every `%` doubled to `%%` (so the
// raw arm64 mnemonic register percent-prefix on other targets does not
// trip the substitution) and every `${name}` interp replaced by `%N` where
// N is the operand index. Operands are emitted in chunk order, mirroring
// the AST split. v0.13 supports two operand types per PLAN pin 5:
//
//   - byte         → register operand bound to the binding's uint8_t value
//   - list[byte]   → register operand bound to the list's `.data` pointer
//
// Clobber list follows PLAN pin 7: the full arm64 caller-saved set plus
// "memory" and "cc". x29 (fp) is deliberately omitted — pin 8 makes it a
// user-preserve register and documenting that contract is U6's job. The
// constant clobberListV13ARM64 is exported (lower-cased package-internal)
// so the cgen unit test can assert every register name appears.

// asmClobbersV13ARM64 names the conservative caller-saved register set
// the cgen pins for every inline-asm block on macOS arm64. Order matches
// PLAN pin 7's bullet ordering so a diff against the spec is trivial.
// Memory and cc come first to mirror the GCC convention.
//
// Pin 7 explicit set:
//   - "memory", "cc"
//   - x0…x17, x30
//   - v0…v7, v16…v31
//
// x29 (fp) is NOT in the set — pin 8 makes it user-preserve.
var asmClobbersV13ARM64 = func() []string {
	out := []string{"memory", "cc"}
	for i := 0; i <= 17; i++ {
		out = append(out, fmt.Sprintf("x%d", i))
	}
	out = append(out, "x30")
	for i := 0; i <= 7; i++ {
		out = append(out, fmt.Sprintf("v%d", i))
	}
	for i := 16; i <= 31; i++ {
		out = append(out, fmt.Sprintf("v%d", i))
	}
	return out
}()

// emitAsmBlock lowers an AsmBlock into a GCC `__asm__ volatile` statement.
// Called from cgen.emitStmt. The lowering takes the parser-split chunks
// (text + interp) and rebuilds the body with `%N` operand placeholders;
// each interp expands into one input operand whose C expression depends
// on the binding's type.
//
// The block is wrapped in a `do { … } while (0)` so it composes with
// surrounding statement contexts (if-arms, for-bodies, defer-bodies) the
// same way every other compound emit does — without the wrap, `if (c)
// __asm__ …` would parse fine on first glance but the trailing semicolon
// rule for inline-asm-as-statement differs between compilers; the do/while
// pattern moots that.
func (g *cgen) emitAsmBlock(s *syntax.AsmBlock) error {
	var template strings.Builder
	var operands []string
	for _, chunk := range s.Chunks {
		switch chunk.Kind {
		case syntax.AsmChunkText:
			// `%` in a GCC inline-asm template is the operand-substitution
			// prefix. Double every `%` so the user's literal `%`-bearing
			// asm (e.g. `%w0` register specifiers on other arm64 dialects)
			// passes through to the assembler unchanged.
			for i := 0; i < len(chunk.Text); i++ {
				c := chunk.Text[i]
				if c == '%' {
					template.WriteString("%%")
					continue
				}
				template.WriteByte(c)
			}
		case syntax.AsmChunkInterp:
			opExpr, err := g.asmInterpOperand(chunk)
			if err != nil {
				return err
			}
			fmt.Fprintf(&template, "%%%d", len(operands))
			operands = append(operands, opExpr)
		}
	}

	g.writeIndent()
	g.b.WriteString("do {\n")
	g.indent++
	g.writeIndent()
	g.b.WriteString("__asm__ volatile (")
	g.b.WriteString(cQuote(template.String()))
	g.b.WriteString("\n")
	g.indent++
	g.writeIndent()
	g.b.WriteString(": /* outputs */\n")
	g.writeIndent()
	g.b.WriteString(": /* inputs */")
	for i, op := range operands {
		if i > 0 {
			g.b.WriteString(",")
		}
		fmt.Fprintf(&g.b, " \"r\"(%s)", op)
	}
	g.b.WriteString("\n")
	g.writeIndent()
	g.b.WriteString(": /* clobbers */")
	for i, c := range asmClobbersV13ARM64 {
		if i > 0 {
			g.b.WriteString(",")
		}
		fmt.Fprintf(&g.b, " %s", cQuote(c))
	}
	g.b.WriteString("\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString(");\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("} while (0);\n")
	_ = s.Pos
	return nil
}

// asmInterpOperand builds the C input-operand expression for one
// AsmChunkInterp. Typeck (U3) already validated the binding type; here we
// just dispatch on that type to pick the right C-side surface:
//
//   - byte       → `z_<name>` (the mangled local; cast to uint64_t so the
//                  GCC "r" constraint can choose any GPR width).
//   - list[byte] → `z_<name>.data` (cast to uintptr_t for the same reason).
//
// The runtime types are: byte → uint8_t, list[byte] → struct with
// `uint8_t* data` per the v0.2 list shape (cgen.go:1281). Widening to a
// register-sized type avoids a partial-write hazard if the user picks an
// `x` register and only writes the low bits; we'd rather the asm body
// reflect the actual register the operand lands in.
func (g *cgen) asmInterpOperand(chunk syntax.AsmChunk) (string, error) {
	t := chunk.BoundType
	if t == nil {
		// Defensive: U3 typeck stamps every AsmChunkInterp with its
		// resolved BoundType. If we land here, either typeck was skipped
		// (build with no typecheck pass — never happens in production) or
		// a future cgen reaches AsmBlock through a path that bypasses
		// typeck. Surface a precise error so the regression has somewhere
		// obvious to land.
		return "", fmt.Errorf("%s: asm interpolation '${%s}' has no BoundType (typeck/cgen drift?)",
			chunk.NamePos, chunk.Name)
	}
	switch {
	case t.Kind == syntax.TypeByte:
		return fmt.Sprintf("((uint64_t)%s)", mangle(chunk.Name)), nil
	case t.Kind == syntax.TypeList && t.Element != nil && t.Element.Kind == syntax.TypeByte:
		return fmt.Sprintf("((uintptr_t)%s.data)", mangle(chunk.Name)), nil
	}
	// Typeck pins the surface; reaching this branch means a future type
	// snuck through without a cgen mapping. Surface a focused error.
	return "", fmt.Errorf("%s: asm interpolation '${%s}' has type %s; cgen has no lowering",
		chunk.NamePos, chunk.Name, t)
}
