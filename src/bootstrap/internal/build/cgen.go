// Package build emits a C source file from a parsed Zerg program and shells
// out to the system C compiler to produce a native binary.
//
// At v0.1 the codegen lowers the full primitive-typed surface: variables,
// expressions, control flow, top-level functions, and `print`. The runtime
// helpers live inline in runtime.go and are emitted once at the top of the
// generated .c file. Stdout produced by the compiled binary must equal stdout
// produced by the interpreter for every program in the parity corpus —
// mirror run.go's semantics, do not freelance.
package build

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Emit writes the C source for prog to w. The output is a complete,
// self-contained .c file with `int main(void)` as the entry point. Emit
// assumes prog has already been type-checked: every Expr's Type() must be
// non-nil. Callers that go through Build / EmitSource get this for free
// because both call syntax.Check before reaching here.
func Emit(prog *syntax.Program, w io.Writer) error {
	g := &cgen{indent: 1}
	g.b.WriteString(runtimeC)
	g.b.WriteString("\n")

	// Emit top-level fn forward declarations first, then their bodies. That
	// means a fn can call any other top-level fn regardless of textual order
	// — same parity as the interpreter, which two-passes via collectTopLevel.
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*syntax.FnDecl)
		if !ok {
			continue
		}
		writeFnSig(&g.b, fn)
		g.b.WriteString(";\n")
	}
	if hasFn(prog) {
		g.b.WriteString("\n")
	}

	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*syntax.FnDecl)
		if !ok {
			continue
		}
		if err := g.emitFn(fn); err != nil {
			return err
		}
		g.b.WriteString("\n")
	}

	g.b.WriteString("int main(void) {\n")
	for _, stmt := range prog.Statements {
		if _, ok := stmt.(*syntax.FnDecl); ok {
			continue
		}
		if err := g.emitStmt(stmt); err != nil {
			return err
		}
	}
	g.b.WriteString("    return 0;\n")
	g.b.WriteString("}\n")

	_, err := io.WriteString(w, g.b.String())
	return err
}

// hasFn reports whether prog declares any top-level function. We use it to
// decide whether to emit the trailing newline between the forward-decl block
// and the body block — purely cosmetic, but keeps generated source readable.
func hasFn(prog *syntax.Program) bool {
	for _, s := range prog.Statements {
		if _, ok := s.(*syntax.FnDecl); ok {
			return true
		}
	}
	return false
}

// cgen is the per-Emit codegen state. It owns the output builder and the
// current indentation level (in tabs of 4 spaces). We do not track scope:
// every Zerg name maps deterministically to a `z_<name>` C name, so collisions
// with C keywords / our runtime are impossible by construction. See the
// "Identifier mangling" note in PLAN.md.
type cgen struct {
	b      strings.Builder
	indent int
}

// writeIndent writes the current indent prefix.
func (g *cgen) writeIndent() {
	for i := 0; i < g.indent; i++ {
		g.b.WriteString("    ")
	}
}

// ---------------------------------------------------------------------------
// Statement emission.
// ---------------------------------------------------------------------------

func (g *cgen) emitStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		g.writeIndent()
		// `(void)0;` is clearer than an empty statement and survives
		// `-Wpedantic` without complaint.
		g.b.WriteString("(void)0;\n")
		return nil
	case *syntax.PrintStmt:
		return g.emitPrint(s)
	case *syntax.LetStmt:
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.MutStmt:
		return g.emitDecl(s.Name, s.Type, s.Value, false)
	case *syntax.ConstStmt:
		// C `const` adds qualifier; semantics in v0.1 are otherwise identical
		// to let. typeck has validated the rhs is a constant expression.
		return g.emitDecl(s.Name, s.Type, s.Value, true)
	case *syntax.AssignStmt:
		return g.emitAssign(s)
	case *syntax.ExprStmt:
		expr, err := g.exprStr(s.Expr)
		if err != nil {
			return err
		}
		g.writeIndent()
		// Cast the result to (void) so that a non-void function call used as a
		// statement does not raise an unused-result warning under `-Wall`.
		fmt.Fprintf(&g.b, "(void)(%s);\n", expr)
		return nil
	case *syntax.IfStmt:
		return g.emitIf(s)
	case *syntax.ForStmt:
		return g.emitFor(s)
	case *syntax.ReturnStmt:
		return g.emitReturn(s)
	case *syntax.BreakStmt:
		return g.emitFlow(s.Guard, "break")
	case *syntax.ContinueStmt:
		return g.emitFlow(s.Guard, "continue")
	case *syntax.FnDecl:
		// Top-level fn decls are emitted by Emit before main; encountering one
		// here means typeck let a nested fn through, which it does not.
		return fmt.Errorf("internal: nested function %q at %s", s.Name, s.Pos)
	}
	return fmt.Errorf("codegen: unhandled statement %T at %s", stmt, stmt.StmtPos())
}

// emitPrint dispatches on the static type of the argument expression. typeck
// has already validated the type is one of the four printable primitives.
func (g *cgen) emitPrint(s *syntax.PrintStmt) error {
	expr, err := g.exprStr(s.Expr)
	if err != nil {
		return err
	}
	helper := ""
	switch s.Expr.Type() {
	case syntax.TInt():
		helper = "zerg_print_int"
	case syntax.TFloat():
		helper = "zerg_print_float"
	case syntax.TBool():
		helper = "zerg_print_bool"
	case syntax.TStr():
		helper = "zerg_print_str"
	default:
		return fmt.Errorf("codegen: cannot print value of type %s at %s", s.Expr.Type(), s.Pos)
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "%s(%s);\n", helper, expr)
	return nil
}

// emitDecl lowers let/mut/const into a C local declaration. The annotated
// type ref (when present) and the inferred type from the rhs are equal by
// typeck — but we prefer the rhs's type because LetStmt/MutStmt may have a
// nil Type ref (the := walrus form).
func (g *cgen) emitDecl(name string, ref *syntax.TypeRef, value syntax.Expr, isConst bool) error {
	t := value.Type()
	if t == nil && ref != nil {
		t = ref.Resolved
	}
	if t == nil {
		return fmt.Errorf("codegen: missing type for %q", name)
	}
	exprS, err := g.exprStr(value)
	if err != nil {
		return err
	}
	g.writeIndent()
	if isConst {
		g.b.WriteString("const ")
	}
	fmt.Fprintf(&g.b, "%s %s = %s;\n", cType(t), mangle(name), exprS)
	return nil
}

// emitAssign lowers any assign-op to the C equivalent. Compound `+=` on str
// expands to `lhs = zerg_str_concat(lhs, rhs)` — there is no in-place form.
func (g *cgen) emitAssign(s *syntax.AssignStmt) error {
	rhs, err := g.exprStr(s.Value)
	if err != nil {
		return err
	}
	target := mangle(s.Target.Name)
	g.writeIndent()
	switch s.Op {
	case syntax.AssignSet:
		fmt.Fprintf(&g.b, "%s = %s;\n", target, rhs)
	case syntax.AssignAdd:
		if s.Target.Type() == syntax.TStr() {
			fmt.Fprintf(&g.b, "%s = zerg_str_concat(%s, %s);\n", target, target, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s += %s;\n", target, rhs)
	case syntax.AssignSub:
		fmt.Fprintf(&g.b, "%s -= %s;\n", target, rhs)
	case syntax.AssignMul:
		fmt.Fprintf(&g.b, "%s *= %s;\n", target, rhs)
	case syntax.AssignDiv:
		fmt.Fprintf(&g.b, "%s /= %s;\n", target, rhs)
	case syntax.AssignMod:
		// Float % is `fmod(a, b)`; there is no `%=` form for double in C, so we
		// expand to a self-assign.
		if s.Target.Type() == syntax.TFloat() {
			fmt.Fprintf(&g.b, "%s = fmod(%s, %s);\n", target, target, rhs)
			return nil
		}
		fmt.Fprintf(&g.b, "%s %%= %s;\n", target, rhs)
	case syntax.AssignAnd:
		fmt.Fprintf(&g.b, "%s &= %s;\n", target, rhs)
	case syntax.AssignOr:
		fmt.Fprintf(&g.b, "%s |= %s;\n", target, rhs)
	case syntax.AssignXor:
		fmt.Fprintf(&g.b, "%s ^= %s;\n", target, rhs)
	case syntax.AssignShl:
		fmt.Fprintf(&g.b, "%s <<= %s;\n", target, rhs)
	case syntax.AssignShr:
		fmt.Fprintf(&g.b, "%s >>= %s;\n", target, rhs)
	default:
		return fmt.Errorf("codegen: unknown assign op %s at %s", s.Op, s.Pos)
	}
	return nil
}

// emitIf walks the if-elif-else chain and emits a matching C chain.
func (g *cgen) emitIf(s *syntax.IfStmt) error {
	cond, err := g.exprStr(s.Cond)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) {\n", cond)
	if err := g.emitBlockBody(s.Then); err != nil {
		return err
	}
	for i := range s.Elifs {
		ec := &s.Elifs[i]
		c, err := g.exprStr(ec.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "} else if (%s) {\n", c)
		if err := g.emitBlockBody(ec.Body); err != nil {
			return err
		}
	}
	if s.Else != nil {
		g.writeIndent()
		g.b.WriteString("} else {\n")
		if err := g.emitBlockBody(s.Else); err != nil {
			return err
		}
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitBlockBody emits the statements between `{` and `}` with one extra level
// of indent. The braces themselves are emitted by the caller.
func (g *cgen) emitBlockBody(b *syntax.Block) error {
	g.indent++
	defer func() { g.indent-- }()
	for _, st := range b.Statements {
		if err := g.emitStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// emitFor lowers all three for shapes. Range iteration uses a scoped C `for`
// so the loop variable's lifetime ends with the loop; the loop variable is
// always int64_t per typeck.
func (g *cgen) emitFor(s *syntax.ForStmt) error {
	switch s.Kind {
	case syntax.ForInfinite:
		g.writeIndent()
		g.b.WriteString("while (1) {\n")
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForCond:
		cond, err := g.exprStr(s.Cond)
		if err != nil {
			return err
		}
		g.writeIndent()
		fmt.Fprintf(&g.b, "while (%s) {\n", cond)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	case syntax.ForRange:
		start, err := g.exprStr(s.Range.Start)
		if err != nil {
			return err
		}
		end, err := g.exprStr(s.Range.End)
		if err != nil {
			return err
		}
		cmp := "<"
		if s.Range.Inclusive {
			// `..=` with end == INT64_MAX would loop forever after wrap. PLAN
			// accepts this footgun at v0.1 — same hazard as the interpreter,
			// which uses Go `for i <= end` with int64.
			cmp = "<="
		}
		v := mangle(s.Var)
		g.writeIndent()
		fmt.Fprintf(&g.b, "for (int64_t %s = %s; %s %s %s; ++%s) {\n", v, start, v, cmp, end, v)
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	}
	return fmt.Errorf("codegen: unknown for kind at %s", s.Pos)
}

// emitReturn handles bare return, return-with-value, and the guard form. The
// guard wraps the whole statement in `if (cond) { return ...; }` so the rest
// of the function still executes when the guard is false — same semantics as
// run.go's execReturn.
func (g *cgen) emitReturn(s *syntax.ReturnStmt) error {
	body := "return;"
	if s.Value != nil {
		v, err := g.exprStr(s.Value)
		if err != nil {
			return err
		}
		body = fmt.Sprintf("return %s;", v)
	}
	if s.Guard == nil {
		g.writeIndent()
		g.b.WriteString(body)
		g.b.WriteString("\n")
		return nil
	}
	guard, err := g.exprStr(s.Guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) { %s }\n", guard, body)
	return nil
}

// emitFlow handles break/continue with optional guard, identical pattern.
func (g *cgen) emitFlow(guard syntax.Expr, kw string) error {
	if guard == nil {
		g.writeIndent()
		fmt.Fprintf(&g.b, "%s;\n", kw)
		return nil
	}
	c, err := g.exprStr(guard)
	if err != nil {
		return err
	}
	g.writeIndent()
	fmt.Fprintf(&g.b, "if (%s) %s;\n", c, kw)
	return nil
}

// emitFn writes a complete static function definition. The signature mirrors
// the Zerg fn's resolved param/return types; the body is a brace block.
func (g *cgen) emitFn(fn *syntax.FnDecl) error {
	writeFnSig(&g.b, fn)
	g.b.WriteString(" {\n")
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	g.b.WriteString("}\n")
	return nil
}

// writeFnSig renders the C signature `static <ret> z_<name>(<params>)` (no
// trailing punctuation). Callers append `;` for forward decls or ` {` for
// definitions.
func writeFnSig(b *strings.Builder, fn *syntax.FnDecl) {
	ret := "void"
	if fn.Return != nil && fn.Return.Resolved != nil && fn.Return.Resolved != syntax.TVoid() {
		ret = cType(fn.Return.Resolved)
	}
	b.WriteString("static ")
	b.WriteString(ret)
	b.WriteByte(' ')
	b.WriteString(mangle(fn.Name))
	b.WriteByte('(')
	if len(fn.Params) == 0 {
		b.WriteString("void")
	} else {
		for i, p := range fn.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(cType(p.Type.Resolved))
			b.WriteByte(' ')
			b.WriteString(mangle(p.Name))
		}
	}
	b.WriteByte(')')
}

// ---------------------------------------------------------------------------
// Expression rendering. Returns the C source text for the expression as a
// string the caller can splice into a statement. Every BinaryExpr / UnaryExpr
// renders surrounding parens to preserve precedence — let the C compiler's
// optimiser collapse redundant parens.
// ---------------------------------------------------------------------------

func (g *cgen) exprStr(expr syntax.Expr) (string, error) {
	switch e := expr.(type) {
	case *syntax.IntLit:
		// INT64_C wraps the literal in the right suffix for the platform's
		// int64_t. Picked over plain `LL` because INT64_MIN as `-9223372036854775808LL`
		// can be parsed by some compilers as `-(9223372036854775808LL)` and warned
		// on; INT64_C is the portable form documented in PLAN.md.
		return fmt.Sprintf("INT64_C(%d)", e.Int), nil
	case *syntax.FloatLit:
		// %.17g round-trips any IEEE 754 double, so the C parser sees the same
		// bit pattern Go parsed. This protects parity even for inputs that the
		// canonical %g compresses (e.g. 0.1 prints as "0.1" but its exact value
		// has more digits).
		return strconv.FormatFloat(e.Float, 'g', 17, 64), nil
	case *syntax.StringLit:
		return fmt.Sprintf("zerg_str_lit(%s, %d)", cQuote(e.Value), len(e.Value)), nil
	case *syntax.BoolLit:
		if e.Value {
			return "(_Bool)1", nil
		}
		return "(_Bool)0", nil
	case *syntax.IdentExpr:
		return mangle(e.Name), nil
	case *syntax.ParenExpr:
		inner, err := g.exprStr(e.Inner)
		if err != nil {
			return "", err
		}
		return "(" + inner + ")", nil
	case *syntax.UnaryExpr:
		return g.unaryStr(e)
	case *syntax.BinaryExpr:
		return g.binaryStr(e)
	case *syntax.CallExpr:
		ident, ok := e.Callee.(*syntax.IdentExpr)
		if !ok {
			return "", fmt.Errorf("codegen: non-ident callee at %s", e.Pos)
		}
		var sb strings.Builder
		sb.WriteString(mangle(ident.Name))
		sb.WriteByte('(')
		for i, a := range e.Args {
			if i > 0 {
				sb.WriteString(", ")
			}
			s, err := g.exprStr(a)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
		sb.WriteByte(')')
		return sb.String(), nil
	}
	return "", fmt.Errorf("codegen: unhandled expression %T at %s", expr, expr.ExprPos())
}

// unaryStr lowers -, ~, not. The mapping is identical to C except for `not`
// which uses C's `!`.
func (g *cgen) unaryStr(e *syntax.UnaryExpr) (string, error) {
	inner, err := g.exprStr(e.Operand)
	if err != nil {
		return "", err
	}
	switch e.Op {
	case syntax.UnaryNeg:
		return fmt.Sprintf("(-%s)", inner), nil
	case syntax.UnaryBitNot:
		return fmt.Sprintf("(~%s)", inner), nil
	case syntax.UnaryNot:
		return fmt.Sprintf("(!%s)", inner), nil
	}
	return "", fmt.Errorf("codegen: unknown unary op %s at %s", e.Op, e.Pos)
}

// binaryStr lowers each operator per the run.go semantics, type-dispatching
// where the operator differs across types (str equality / ordering / concat,
// float floor-div / mod). C `&&` / `||` already short-circuit, so logical and/or
// translate directly without a helper.
func (g *cgen) binaryStr(e *syntax.BinaryExpr) (string, error) {
	left, err := g.exprStr(e.Left)
	if err != nil {
		return "", err
	}
	right, err := g.exprStr(e.Right)
	if err != nil {
		return "", err
	}
	lt := e.Left.Type()
	switch e.Op {
	case syntax.BinAdd:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_concat(%s, %s)", left, right), nil
		}
		return infix(left, "+", right), nil
	case syntax.BinSub:
		return infix(left, "-", right), nil
	case syntax.BinMul:
		return infix(left, "*", right), nil
	case syntax.BinDiv:
		return infix(left, "/", right), nil
	case syntax.BinFloorDiv:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("floor((%s) / (%s))", left, right), nil
		}
		// Int floor-div is identical to truncating division at v0.1 — same as
		// Go's int64 `/` and C's int64_t `/` under -fwrapv. PLAN.md "// on int:
		// identical to / on int".
		return infix(left, "/", right), nil
	case syntax.BinMod:
		if lt == syntax.TFloat() {
			return fmt.Sprintf("fmod((%s), (%s))", left, right), nil
		}
		return infix(left, "%", right), nil
	case syntax.BinBitAnd:
		return infix(left, "&", right), nil
	case syntax.BinBitOr:
		return infix(left, "|", right), nil
	case syntax.BinBitXor:
		return infix(left, "^", right), nil
	case syntax.BinShl:
		return infix(left, "<<", right), nil
	case syntax.BinShr:
		return infix(left, ">>", right), nil
	case syntax.BinEq:
		if lt == syntax.TStr() {
			return fmt.Sprintf("zerg_str_eq(%s, %s)", left, right), nil
		}
		return infix(left, "==", right), nil
	case syntax.BinNE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(!zerg_str_eq(%s, %s))", left, right), nil
		}
		return infix(left, "!=", right), nil
	case syntax.BinLT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) < 0)", left, right), nil
		}
		return infix(left, "<", right), nil
	case syntax.BinGT:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) > 0)", left, right), nil
		}
		return infix(left, ">", right), nil
	case syntax.BinLE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) <= 0)", left, right), nil
		}
		return infix(left, "<=", right), nil
	case syntax.BinGE:
		if lt == syntax.TStr() {
			return fmt.Sprintf("(zerg_str_cmp(%s, %s) >= 0)", left, right), nil
		}
		return infix(left, ">=", right), nil
	case syntax.BinAnd:
		return infix(left, "&&", right), nil
	case syntax.BinOr:
		return infix(left, "||", right), nil
	case syntax.BinXor:
		// Bool-only per typeck. We cast both sides to _Bool so the `^` runs on
		// 0/1 values — bitwise xor on _Bool yields the boolean xor.
		return fmt.Sprintf("((_Bool)(%s) ^ (_Bool)(%s))", left, right), nil
	}
	return "", fmt.Errorf("codegen: unknown binary op %s at %s", e.Op, e.Pos)
}

// infix renders `(left OP right)`. The outer parens preserve precedence; the
// C compiler removes them in any reasonable optimiser pass.
func infix(left, op, right string) string {
	return "(" + left + " " + op + " " + right + ")"
}

// cType maps a Zerg primitive type to its C representation.
func cType(t *syntax.Type) string {
	switch t {
	case syntax.TInt():
		return "int64_t"
	case syntax.TFloat():
		return "double"
	case syntax.TBool():
		return "_Bool"
	case syntax.TStr():
		return "zerg_str"
	case syntax.TVoid():
		return "void"
	}
	return "void"
}

// mangle prefixes every Zerg name with `z_` so the C symbol can never clash
// with a C keyword (`int`, `for`), a C runtime symbol (`printf`, `main`), or a
// runtime helper (`zerg_print_int`). The lone exception is `main`: Emit hard-
// codes the entry point and never funnels the literal name "main" through this
// helper.
func mangle(name string) string {
	return "z_" + name
}

// cQuote returns a C string literal, complete with surrounding double quotes,
// whose runtime value equals s. Non-printable bytes are emitted as octal
// escapes so the output is portable across C compilers.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			// Octal must be zero-padded to three digits so a literal digit
			// in the source can't be folded into the escape. High-bit bytes
			// pass through verbatim so a UTF-8 source string round-trips
			// byte-identically through the C compiler.
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
