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

// asmClobberGroupsV13ARM64 names the conservative caller-saved register
// set the cgen pins for every inline-asm block on macOS arm64, grouped
// for readability in the emit. PLAN pin 7's bullet ordering:
//
//   - "memory", "cc"
//   - x0…x17, x30
//   - v0…v7, v16…v31
//
// x29 (fp) is NOT in the set — pin 8 makes it user-preserve.
var asmClobberGroupsV13ARM64 = func() [][]string {
	gpr := make([]string, 0, 19)
	for i := 0; i <= 17; i++ {
		gpr = append(gpr, fmt.Sprintf("x%d", i))
	}
	gpr = append(gpr, "x30")
	fpLow := make([]string, 0, 8)
	for i := 0; i <= 7; i++ {
		fpLow = append(fpLow, fmt.Sprintf("v%d", i))
	}
	fpHigh := make([]string, 0, 16)
	for i := 16; i <= 31; i++ {
		fpHigh = append(fpHigh, fmt.Sprintf("v%d", i))
	}
	return [][]string{
		{"memory", "cc"},
		gpr,
		fpLow,
		fpHigh,
	}
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
	g.b.WriteString("__asm__ volatile (\n")
	g.indent++
	for _, line := range splitAsmTemplateLines(template.String()) {
		g.writeIndent()
		g.b.WriteString(cQuote(line))
		g.b.WriteString("\n")
	}
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
	g.b.WriteString(": /* clobbers */\n")
	for gi, group := range asmClobberGroupsV13ARM64 {
		g.writeIndent()
		g.b.WriteString("    ")
		for ri, c := range group {
			if ri > 0 {
				g.b.WriteString(", ")
			}
			g.b.WriteString(cQuote(c))
		}
		if gi < len(asmClobberGroupsV13ARM64)-1 {
			g.b.WriteString(",")
		}
		g.b.WriteString("\n")
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString(");\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("} while (0);\n")
	_ = s.Pos
	return nil
}

// splitAsmTemplateLines breaks the assembled template into one C string
// literal per source asm line. Each non-final line keeps its trailing `\n`
// so the assembler still sees identical bytes; a final empty fragment
// (template ending with `\n`) is dropped so we don't emit a stray `""`.
// One literal per line lets the emitted C source read like hand-written
// inline asm and lines up debugger source columns with the user's body.
func splitAsmTemplateLines(tmpl string) []string {
	if tmpl == "" {
		return []string{""}
	}
	parts := strings.Split(tmpl, "\n")
	for i := 0; i < len(parts)-1; i++ {
		parts[i] += "\n"
	}
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
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
