package build

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// cgen_v13_asm.go lowers an AsmBlock into a GCC `__asm__ volatile`
// statement. The lowering uses the GCC operand-substitution form:
//
//	__asm__ volatile ( "<template>" : <outputs> : <inputs> : <clobbers> )
//
// `<template>` is the body bytes with every `%` doubled to `%%` (so the
// raw arm64 mnemonic register percent-prefix on other targets does not
// trip the substitution) and every `${name}` interp replaced by `%N`
// where N is the operand index. GCC numbers operands across both the
// output and input lists in declaration order, starting at 0 — outputs
// first, then inputs. v0.13 + v0.14 operand types:
//
//   - byte         (input)   → `"r"(((uint64_t)z_<name>))`
//   - int          (input)   → `"r"(((int64_t)z_<name>))`        — v0.14
//   - list[byte]   (input)   → `"r"(((uintptr_t)z_<name>.data))`
//   - mut byte     (output)  → `"+r"(z_<name>)`                  — v0.14
//   - mut int      (output)  → `"+r"(z_<name>)`                  — v0.14
//
// Output operands use the `"+r"` inout constraint so the asm body may
// read the binding's initial value (the user typically writes
// `mut x: int = 0` to make the read deterministic). Pure write-only
// (`"=r"`) is not used because there is no Zerg-level surface for
// "uninitialised output" and `"+r"` is strictly more permissive.
//
// Clobber list follows v0.13 PLAN pin 7: the full arm64 caller-saved
// set plus "memory" and "cc". x29 (fp) is deliberately omitted — pin 8
// makes it a user-preserve register.

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

// asmClobberGroupsV14AMD64 names the conservative caller-saved register
// set for inline asm on x86_64 hosts (System V AMD64 ABI, which is the
// macOS x86_64 calling convention). Grouped for readability:
//
//   - "memory", "cc"
//   - rax (return / scratch), rcx, rdx, rsi, rdi (arg regs), r8, r9
//     (arg regs), r10, r11 (caller-saved temporaries)
//   - xmm0..xmm15 (all caller-saved under SysV AMD64)
//
// rbx / rbp / r12..r15 are callee-saved and NOT clobbered. The base
// pointer (rbp) is the x86_64 analogue of arm64's x29 — user-preserve.
// A v0.14 inline-asm body that needs rbx / r12..r15 MUST save and
// restore them itself, same hard rule as arm64's x29 / x30 contract.
var asmClobberGroupsV14AMD64 = [][]string{
	{"memory", "cc"},
	{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11"},
	{"xmm0", "xmm1", "xmm2", "xmm3", "xmm4", "xmm5", "xmm6", "xmm7"},
	{"xmm8", "xmm9", "xmm10", "xmm11", "xmm12", "xmm13", "xmm14", "xmm15"},
}

// asmTargetArchOverride is the test seam for asmTargetArch(). Empty
// in production; tests use SetAsmTargetArchForTest to force a specific
// arch and assert the corresponding clobber emit even on a build host
// of a different arch.
var asmTargetArchOverride string

// asmTargetArch returns the arch tag that drives clobber selection.
// Today the cgen targets the host (zerg build invokes the host cc),
// so runtime.GOARCH is the source of truth; the override hook lets
// tests assert both arches without rebuilding the binary against a
// different GOARCH. A future cross-compilation path threads a real
// target tag through cgen and obsoletes the override.
func asmTargetArch() string {
	if asmTargetArchOverride != "" {
		return asmTargetArchOverride
	}
	return runtime.GOARCH
}

// SetAsmTargetArchForTest overrides asmTargetArch() for the duration
// of the returned restore func. Mirrors the loader's host overrides;
// always defer the restore so a panic doesn't leak the override.
func SetAsmTargetArchForTest(arch string) func() {
	prev := asmTargetArchOverride
	asmTargetArchOverride = arch
	return func() { asmTargetArchOverride = prev }
}

// asmClobberGroups returns the clobber set appropriate for the build
// target. Unknown arches fall through to the arm64 set — programs that
// use asm on such hosts already rejected at the v0.13 PLAN pin 1 gate
// (the inline-asm surface is documented macOS-arm64-or-amd64-only as
// of v0.14), so this fall-through is purely defensive and never
// reached in normal operation.
func asmClobberGroups() [][]string {
	if asmTargetArch() == "amd64" {
		return asmClobberGroupsV14AMD64
	}
	return asmClobberGroupsV13ARM64
}

// emitAsmBlock lowers an AsmBlock into a GCC `__asm__ volatile` statement.
// Called from cgen.emitStmt. The lowering takes the parser-split chunks
// (text + interp), classifies each interp as output or input (per the
// typeck-stamped IsOutput flag), and rebuilds the body with `%N` operand
// placeholders. GCC numbers operands as outputs-first, inputs-second, so
// the index assignment runs in a separate pre-pass before the template
// emit walks the chunks in source order.
//
// The block is wrapped in a `do { … } while (0)` so it composes with
// surrounding statement contexts (if-arms, for-bodies, defer-bodies) the
// same way every other compound emit does.
func (g *cgen) emitAsmBlock(s *syntax.AsmBlock) error {
	// Pre-pass: assign GCC operand indices. Outputs get the low indices
	// (0..M-1) per the GCC ABI; inputs follow (M..M+N-1). Each chunk gets
	// its own index even if the same `${name}` appears twice — GCC
	// permits dup "+r" operands tied to the same C lvalue (the last
	// write wins, semantics the user is responsible for inside the body).
	interpIndex := make([]int, len(s.Chunks))
	var outputs, inputs []string
	for i, chunk := range s.Chunks {
		if chunk.Kind != syntax.AsmChunkInterp {
			continue
		}
		opExpr, err := asmInterpOperand(chunk)
		if err != nil {
			return err
		}
		if chunk.IsOutput {
			interpIndex[i] = len(outputs)
			outputs = append(outputs, opExpr)
		}
	}
	outputCount := len(outputs)
	for i, chunk := range s.Chunks {
		if chunk.Kind != syntax.AsmChunkInterp || chunk.IsOutput {
			continue
		}
		opExpr, err := asmInterpOperand(chunk)
		if err != nil {
			return err
		}
		interpIndex[i] = outputCount + len(inputs)
		inputs = append(inputs, opExpr)
	}

	// Template build: emit text chunks verbatim (with `%` doubled) and
	// replace each interp with its assigned `%N` placeholder.
	var template strings.Builder
	for i, chunk := range s.Chunks {
		switch chunk.Kind {
		case syntax.AsmChunkText:
			for j := 0; j < len(chunk.Text); j++ {
				c := chunk.Text[j]
				if c == '%' {
					template.WriteString("%%")
					continue
				}
				template.WriteByte(c)
			}
		case syntax.AsmChunkInterp:
			fmt.Fprintf(&template, "%%%d", interpIndex[i])
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
	g.b.WriteString(": /* outputs */")
	for i, op := range outputs {
		if i > 0 {
			g.b.WriteString(",")
		}
		fmt.Fprintf(&g.b, " \"+r\"(%s)", op)
	}
	g.b.WriteString("\n")
	g.writeIndent()
	g.b.WriteString(": /* inputs */")
	for i, op := range inputs {
		if i > 0 {
			g.b.WriteString(",")
		}
		fmt.Fprintf(&g.b, " \"r\"(%s)", op)
	}
	g.b.WriteString("\n")
	g.writeIndent()
	g.b.WriteString(": /* clobbers */\n")
	clobbers := asmClobberGroups()
	for gi, group := range clobbers {
		g.writeIndent()
		g.b.WriteString("    ")
		for ri, c := range group {
			if ri > 0 {
				g.b.WriteString(", ")
			}
			g.b.WriteString(cQuote(c))
		}
		if gi < len(clobbers)-1 {
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

// asmInterpOperand builds the C operand expression for one AsmChunkInterp.
// Direction (input vs output) is encoded by the caller via chunk.IsOutput;
// the operand expression itself depends only on the binding's type and
// whether the operand must be a writable lvalue.
//
// Input forms widen scalars to register-sized types so the GCC "r"
// constraint can pick any GPR width without partial-write surprises:
//
//   - byte       (input)  → `((uint64_t)z_<name>)`
//   - int        (input)  → `((int64_t)z_<name>)`        v0.14
//   - list[byte] (input)  → `((uintptr_t)z_<name>.data)`
//
// Output forms hand GCC the raw lvalue so it can attach a "+r" constraint
// and emit load/store around the body without a cast wrapper:
//
//   - mut byte   (output) → `z_<name>`                   v0.14
//   - mut int    (output) → `z_<name>`                   v0.14
func asmInterpOperand(chunk syntax.AsmChunk) (string, error) {
	t := chunk.BoundType
	if t == nil {
		// Defensive: typeck stamps every AsmChunkInterp with its resolved
		// BoundType. If we land here, either typeck was skipped (never
		// happens in production) or a future cgen reaches AsmBlock through
		// a path that bypasses typeck. Surface a precise error so the
		// regression has somewhere obvious to land.
		return "", fmt.Errorf("%s: asm interpolation '${%s}' has no BoundType (typeck/cgen drift?)",
			chunk.NamePos, chunk.Name)
	}
	name := mangle(chunk.Name)
	switch {
	case t.Kind == syntax.TypeByte:
		if chunk.IsOutput {
			return name, nil
		}
		return fmt.Sprintf("((uint64_t)%s)", name), nil
	case t.Kind == syntax.TypeInt:
		if chunk.IsOutput {
			return name, nil
		}
		return fmt.Sprintf("((int64_t)%s)", name), nil
	case t.Kind == syntax.TypeList && t.Element != nil && t.Element.Kind == syntax.TypeByte:
		// list[byte] is input-only (typeck never sets IsOutput on it).
		return fmt.Sprintf("((uintptr_t)%s.data)", name), nil
	}
	return "", fmt.Errorf("%s: asm interpolation '${%s}' has type %s; cgen has no lowering",
		chunk.NamePos, chunk.Name, t)
}
