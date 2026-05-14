// Package fmt implements the canonical Zerg source formatter (`zerg fmt`).
//
// v0.10 Unit 2. Format takes a syntax.Program (parsed via
// ParseWithComments / ParseWithOptionsAndComments) and returns canonical
// text suitable for write-back via `zerg fmt -w`.
//
// Canonical style (per PLAN.md):
//
//   - 4-space indent.
//   - K&R braces: `fn x() -> int {` on declaration line; closing `}` on its
//     own line.
//   - Trailing commas in multi-line lists / struct literals / enum variant
//     lists / match arms. Single-line lists carry no trailing comma.
//   - Exactly one blank line between top-level decls; zero inside blocks
//     (existing single blank lines preserved, multiples normalised to one).
//   - `T?` / `?.` / `??` sugar preserved (user-written form wins) — read
//     from AST as the user wrote.
//   - Parens around expressions kept when ambiguity is plausible; removed
//     for trivial single-ident / call cases. When in doubt, KEEP.
//   - Soft 100-col target; not hard-enforced at v0.10.
//
// Comment emission:
//
//   - Program.HeadComments emit verbatim at file top, one per line, each
//     prefixed `# `.
//   - For each decl/stmt with LeadingComments, each comment emits on its
//     own line (current indent + `# `) BEFORE the decl/stmt.
//   - Trailing inline comments are NOT emitted at v0.10 (documented limit).
//
// Round-trip property: Format(Parse(Format(Parse(s)))) == Format(Parse(s))
// for every parsed-clean input. Format never errors for a well-typed
// Program — the parser already rejected invalid shapes.
package fmt

import (
	stdfmt "fmt"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Format produces the canonical text for prog. Never errors for a Program
// produced by syntax.ParseWithComments on parsed-clean input.
func Format(prog *syntax.Program) []byte {
	if prog == nil {
		return nil
	}
	w := &writer{}
	// Head comments occupy lines 1..len(prog.HeadComments). If the first
	// non-comment statement starts on the next line, the source had no
	// blank between the head block and the first stmt; preserve that. If
	// the first stmt's line is more than one above the head's last line,
	// the source had a blank — emit one.
	headBlank := false
	if len(prog.HeadComments) > 0 && len(prog.Statements) > 0 {
		firstStmtLine := firstLineOfStmt(prog.Statements[0])
		if firstStmtLine > len(prog.HeadComments)+1 {
			headBlank = true
		}
	} else if len(prog.HeadComments) > 0 {
		// Comment-only file or trailing-comment file: no blank needed.
		headBlank = false
	}
	w.head(prog.HeadComments, headBlank)
	w.program(prog)
	return []byte(w.b.String())
}

// writer accumulates canonical text and the current indent level. Indent is
// the number of 4-space stops; `indentStr()` materialises it on demand.
type writer struct {
	b      strings.Builder
	indent int
}

const indentUnit = "    "

func (w *writer) write(s string) { w.b.WriteString(s) }

func (w *writer) writef(f string, args ...any) {
	w.b.WriteString(stdfmt.Sprintf(f, args...))
}

func (w *writer) newline() { w.b.WriteByte('\n') }

func (w *writer) writeIndent() {
	for i := 0; i < w.indent; i++ {
		w.b.WriteString(indentUnit)
	}
}

func (w *writer) head(comments []string, blank bool) {
	for _, c := range comments {
		w.b.WriteString(canonicalCommentLine(c))
		w.newline()
	}
	if len(comments) > 0 && blank {
		w.newline()
	}
}

// emitLeadingComments writes any leading comments for a stmt/decl at the
// current indent, each prefixed `# ` on its own line.
func (w *writer) emitLeadingComments(comments []string) {
	for _, c := range comments {
		w.writeIndent()
		w.b.WriteString(canonicalCommentLine(c))
		w.newline()
	}
}

// canonicalCommentLine renders a comment body (the Text field of a
// CommentToken, which is the source after the `#` with any leading space
// preserved) as the canonical `# body` form. We collapse the lexer-
// preserved leading whitespace so a re-parse produces the same Text and
// the formatter is idempotent. Empty comment bodies render as a bare `#`.
func canonicalCommentLine(body string) string {
	trimmed := strings.TrimLeft(body, " \t")
	if trimmed == "" {
		return "#"
	}
	return "# " + trimmed
}

// program emits the top-level statements. Blank-line policy is purely
// source-driven: preserve a single blank line between two adjacent stmts
// when the source had one or more, drop blanks otherwise. The first
// top-level stmt emits without a leading blank.
//
// Multi-line decls (fn / impl / spec / struct-multiline / enum-multiline
// bodies) close on a line strictly later than their open; the gap test
// (currStart > prevEnd + 1) covers that naturally without special-casing.
func (w *writer) program(prog *syntax.Program) {
	stmts := prog.Statements
	for i, s := range stmts {
		if i > 0 {
			prev := stmts[i-1]
			if firstLineOfStmt(s) > lastLineOfStmt(prev)+1 {
				w.newline()
			}
		}
		w.stmt(s)
	}
}

// firstLineOfStmt returns the first source line spanned by a stmt — its
// first leading comment (if any) or its own StmtPos line.
func firstLineOfStmt(s syntax.Stmt) int {
	leading := leadingCommentsOf(s)
	if len(leading) > 0 {
		// We don't track per-comment lines, but the leading-comment block
		// always sits IMMEDIATELY above the stmt with optional intervening
		// blank lines. Pick stmt-line - len(leading) as a lower bound; the
		// blank-detection logic only cares about >1 gap so this is safe.
		return s.StmtPos().Line - len(leading)
	}
	return s.StmtPos().Line
}

// lastLineOfStmt returns the source line of the stmt's last token — for a
// compound stmt this is the closing `}` line, for an atom it's the stmt
// position. We don't have closing-brace positions in the AST, so we
// approximate as `last_inner + 1` for stmts that own a closing brace.
func lastLineOfStmt(s syntax.Stmt) int {
	switch x := s.(type) {
	case *syntax.FnDecl:
		if x.Body != nil {
			return blockLastLine(x.Body, x.Pos.Line) + 1
		}
		return x.Pos.Line
	case *syntax.StructDecl:
		if len(x.Fields) > 0 {
			lastFieldLine := x.Fields[len(x.Fields)-1].Pos.Line
			if lastFieldLine > x.Pos.Line {
				// Multi-line struct decl — closing `}` is one line below.
				return lastFieldLine + 1
			}
		}
		return x.Pos.Line
	case *syntax.EnumDecl:
		if len(x.Variants) > 0 {
			lastVarLine := x.Variants[len(x.Variants)-1].Pos.Line
			if lastVarLine > x.Pos.Line {
				return lastVarLine + 1
			}
		}
		return x.Pos.Line
	case *syntax.SpecDecl:
		max := x.Pos.Line
		hasMulti := false
		for _, m := range x.Methods {
			if m == nil {
				continue
			}
			ml := m.Pos.Line
			if m.Body != nil {
				ml = blockLastLine(m.Body, ml) + 1
			}
			if ml > max {
				max = ml
				hasMulti = true
			}
			if m.Pos.Line > x.Pos.Line {
				hasMulti = true
			}
		}
		if hasMulti {
			return max + 1
		}
		return max
	case *syntax.ImplDecl:
		max := x.Pos.Line
		hasMulti := false
		for _, m := range x.Methods {
			if m == nil {
				continue
			}
			ml := m.Pos.Line
			if m.Body != nil {
				ml = blockLastLine(m.Body, ml) + 1
			}
			if ml > max {
				max = ml
				hasMulti = true
			}
			if m.Pos.Line > x.Pos.Line {
				hasMulti = true
			}
		}
		if hasMulti {
			return max + 1
		}
		return max
	case *syntax.IfStmt:
		max := x.Pos.Line
		if x.Then != nil {
			max = blockLastLine(x.Then, max)
		}
		for _, eli := range x.Elifs {
			if eli.Body != nil {
				max = blockLastLine(eli.Body, max)
			}
		}
		if x.Else != nil {
			max = blockLastLine(x.Else, max)
		}
		return max + 1
	case *syntax.ForStmt:
		if x.Body != nil {
			return blockLastLine(x.Body, x.Pos.Line) + 1
		}
		return x.Pos.Line
	case *syntax.MatchStmt:
		max := x.Pos.Line
		for _, arm := range x.Arms {
			if arm.Body != nil {
				max = blockLastLine(arm.Body, max)
			}
		}
		return max + 1
	case *syntax.SelectStmt:
		max := x.Pos.Line
		for _, arm := range x.Arms {
			if arm.Body != nil {
				max = blockLastLine(arm.Body, max)
			}
		}
		return max + 1
	case *syntax.AsmBlock:
		// The body's last line is BodyRaw lines past the `{` line.
		newlines := 0
		for i := 0; i < len(x.BodyRaw); i++ {
			if x.BodyRaw[i] == '\n' {
				newlines++
			}
		}
		return x.OpenBracePos.Line + newlines + 1
	case *syntax.DeferStmt:
		if x.Body != nil {
			// Single-stmt synthetic body: no closing brace.
			if isSyntheticArmBody(x.Body) {
				return x.Body.Statements[0].StmtPos().Line
			}
			return blockLastLine(x.Body, x.Pos.Line) + 1
		}
		return x.Pos.Line
	case *syntax.SpawnStmt:
		// `spawn fn() { ... }()` carries an anon-fn whose body extends
		// past the spawn keyword line. Probe the call's last line.
		return exprLastLine(x.Call, x.Pos.Line)
	case *syntax.SendStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.ExprStmt:
		return exprLastLine(x.Expr, x.Pos.Line)
	case *syntax.LetStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.MutStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.ConstStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.AssignStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.MultiAssignStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	case *syntax.PrintStmt:
		return exprLastLine(x.Expr, x.Pos.Line)
	case *syntax.ReturnStmt:
		return exprLastLine(x.Value, x.Pos.Line)
	}
	return s.StmtPos().Line
}

// exprLastLine returns the source line of the rightmost token in expr —
// for AnonFnExpr / IIFE this is the closing brace's line. fallback is
// returned when expr is nil or has no nested compound shape.
func exprLastLine(e syntax.Expr, fallback int) int {
	if e == nil {
		return fallback
	}
	switch x := e.(type) {
	case *syntax.AnonFnExpr:
		if x.Body != nil {
			return blockLastLine(x.Body, x.Pos.Line) + 1
		}
		return x.Pos.Line
	case *syntax.CallExpr:
		// IIFE shape: callee is an anon-fn whose body extends below the
		// call expression's own line. Probe the callee's last line and
		// the args.
		max := exprLastLine(x.Callee, x.Pos.Line)
		for _, a := range x.Args {
			if l := exprLastLine(a, x.Pos.Line); l > max {
				max = l
			}
		}
		return max
	case *syntax.MethodCallExpr:
		max := exprLastLine(x.Receiver, x.Pos.Line)
		for _, a := range x.Args {
			if l := exprLastLine(a, x.Pos.Line); l > max {
				max = l
			}
		}
		return max
	case *syntax.BinaryExpr:
		max := exprLastLine(x.Left, x.Pos.Line)
		if l := exprLastLine(x.Right, x.Pos.Line); l > max {
			max = l
		}
		return max
	case *syntax.UnaryExpr:
		return exprLastLine(x.Operand, x.Pos.Line)
	case *syntax.ParenExpr:
		return exprLastLine(x.Inner, x.Pos.Line)
	}
	if pe, ok := e.(interface{ ExprPos() syntax.Position }); ok {
		l := pe.ExprPos().Line
		if l > fallback {
			return l
		}
	}
	return fallback
}

// blockLastLine returns the line of the last inner stmt of b. Does NOT
// include the closing brace; callers add +1 when they own a brace.
func blockLastLine(b *syntax.Block, fallback int) int {
	if b == nil || len(b.Statements) == 0 {
		return fallback
	}
	max := fallback
	for _, s := range b.Statements {
		l := lastLineOfStmt(s)
		if l > max {
			max = l
		}
	}
	return max
}

// stmt dispatches on concrete Stmt type. It emits leading comments first,
// then the stmt body, ending with a single trailing newline.
func (w *writer) stmt(s syntax.Stmt) {
	leading := leadingCommentsOf(s)
	w.emitLeadingComments(leading)
	switch x := s.(type) {
	case *syntax.LetStmt:
		w.letLike("", x.Name, x.Tuple, x.Type, x.Value)
	case *syntax.MutStmt:
		w.letLike("mut", x.Name, x.Tuple, x.Type, x.Value)
	case *syntax.ConstStmt:
		w.letLike("const", x.Name, x.Tuple, x.Type, x.Value)
	case *syntax.AssignStmt:
		w.assignStmt(x)
	case *syntax.MultiAssignStmt:
		w.multiAssignStmt(x)
	case *syntax.PrintStmt:
		w.printStmt(x)
	case *syntax.ExprStmt:
		w.writeIndent()
		w.expr(x.Expr)
		w.newline()
	case *syntax.ReturnStmt:
		w.returnStmt(x)
	case *syntax.BreakStmt:
		w.guardKeyword("break", x.Guard)
	case *syntax.ContinueStmt:
		w.guardKeyword("continue", x.Guard)
	case *syntax.NopStmt:
		w.writeIndent()
		w.write("nop")
		w.newline()
	case *syntax.IfStmt:
		w.ifStmt(x)
	case *syntax.ForStmt:
		w.forStmt(x)
	case *syntax.MatchStmt:
		w.matchStmt(x)
	case *syntax.ImportDecl:
		w.importDecl(x)
	case *syntax.FnDecl:
		w.fnDecl(x, false /*method*/)
	case *syntax.StructDecl:
		w.structDecl(x)
	case *syntax.EnumDecl:
		w.enumDecl(x)
	case *syntax.SpecDecl:
		w.specDecl(x)
	case *syntax.ImplDecl:
		w.implDecl(x)
	case *syntax.SpawnStmt:
		w.spawnStmt(x)
	case *syntax.DeferStmt:
		w.deferStmt(x)
	case *syntax.SendStmt:
		w.sendStmt(x)
	case *syntax.SelectStmt:
		w.selectStmt(x)
	case *syntax.AsmBlock:
		w.asmBlock(x)
	default:
		// Unknown stmt shape — emit a placeholder rather than panicking so
		// fmt is robust against future AST additions.
		w.writeIndent()
		w.writef("/* unhandled stmt: %T */", s)
		w.newline()
	}
}

// leadingCommentsOf extracts the LeadingComments slice from any stmt that
// carries one.
func leadingCommentsOf(s syntax.Stmt) []string {
	switch x := s.(type) {
	case *syntax.LetStmt:
		return x.LeadingComments
	case *syntax.MutStmt:
		return x.LeadingComments
	case *syntax.ConstStmt:
		return x.LeadingComments
	case *syntax.AssignStmt:
		return x.LeadingComments
	case *syntax.MultiAssignStmt:
		return x.LeadingComments
	case *syntax.ExprStmt:
		return x.LeadingComments
	case *syntax.PrintStmt:
		return x.LeadingComments
	case *syntax.ReturnStmt:
		return x.LeadingComments
	case *syntax.BreakStmt:
		return x.LeadingComments
	case *syntax.ContinueStmt:
		return x.LeadingComments
	case *syntax.FnDecl:
		return x.LeadingComments
	case *syntax.IfStmt:
		return x.LeadingComments
	case *syntax.ForStmt:
		return x.LeadingComments
	case *syntax.NopStmt:
		return x.LeadingComments
	case *syntax.ImportDecl:
		return x.LeadingComments
	case *syntax.StructDecl:
		return x.LeadingComments
	case *syntax.EnumDecl:
		return x.LeadingComments
	case *syntax.MatchStmt:
		return x.LeadingComments
	case *syntax.SpecDecl:
		return x.LeadingComments
	case *syntax.ImplDecl:
		return x.LeadingComments
	case *syntax.SpawnStmt:
		return x.LeadingComments
	case *syntax.DeferStmt:
		return x.LeadingComments
	case *syntax.SendStmt:
		return x.LeadingComments
	case *syntax.SelectStmt:
		return x.LeadingComments
	}
	return nil
}

// letLike covers immutable bindings, mut, and const decls. v0.11 retired
// the `let` keyword; the immutable form (kw == "") emits with no leading
// keyword. mut / const keep their keyword-led shape.
func (w *writer) letLike(kw, name string, tup *syntax.TupleBinding, ty *syntax.TypeRef, val syntax.Expr) {
	w.writeIndent()
	if kw != "" {
		w.write(kw)
		w.write(" ")
	}
	if tup != nil {
		w.write("(")
		for i, n := range tup.Names {
			if i > 0 {
				w.write(", ")
			}
			w.write(n)
		}
		w.write(")")
	} else {
		w.write(name)
	}
	if ty != nil {
		w.write(": ")
		w.write(typeRefText(ty))
		w.write(" = ")
	} else {
		w.write(" := ")
	}
	w.expr(val)
	w.newline()
}

func (w *writer) assignStmt(s *syntax.AssignStmt) {
	w.writeIndent()
	w.expr(s.Target)
	w.write(" ")
	w.write(s.Op.String())
	w.write(" ")
	w.expr(s.Value)
	w.newline()
}

// multiAssignStmt formats `a, b, ... = e1, e2, ...` (v0.15 multi-LHS
// reassignment). A TupleLit RHS is always rendered as bare commas — this
// canonicalizes any user-written `a, b = (e1, e2)` into the bare form, so
// the formatter has one normal shape for the construct. A non-TupleLit RHS
// (a function call returning a tuple, etc.) prints unchanged.
func (w *writer) multiAssignStmt(s *syntax.MultiAssignStmt) {
	w.writeIndent()
	for i, t := range s.Targets {
		if i > 0 {
			w.write(", ")
		}
		w.expr(t)
	}
	w.write(" = ")
	if tup, ok := s.Value.(*syntax.TupleLit); ok {
		for i, e := range tup.Elements {
			if i > 0 {
				w.write(", ")
			}
			w.expr(e)
		}
	} else {
		w.expr(s.Value)
	}
	w.newline()
}

func (w *writer) printStmt(s *syntax.PrintStmt) {
	w.writeIndent()
	w.write("print ")
	w.expr(s.Expr)
	w.newline()
}

func (w *writer) returnStmt(s *syntax.ReturnStmt) {
	w.writeIndent()
	w.write("return")
	if s.Value != nil {
		w.write(" ")
		w.expr(s.Value)
	}
	if s.Guard != nil {
		w.write(" if ")
		w.expr(s.Guard)
	}
	w.newline()
}

func (w *writer) guardKeyword(kw string, guard syntax.Expr) {
	w.writeIndent()
	w.write(kw)
	if guard != nil {
		w.write(" if ")
		w.expr(guard)
	}
	w.newline()
}

func (w *writer) ifStmt(s *syntax.IfStmt) {
	w.writeIndent()
	w.write("if ")
	w.expr(s.Cond)
	w.write(" {")
	w.newline()
	w.block(s.Then)
	w.writeIndent()
	w.write("}")
	for _, eli := range s.Elifs {
		w.write(" elif ")
		w.expr(eli.Cond)
		w.write(" {")
		w.newline()
		w.block(eli.Body)
		w.writeIndent()
		w.write("}")
	}
	if s.Else != nil {
		w.write(" else {")
		w.newline()
		w.block(s.Else)
		w.writeIndent()
		w.write("}")
	}
	w.newline()
}

func (w *writer) forStmt(s *syntax.ForStmt) {
	w.writeIndent()
	switch s.Kind {
	case syntax.ForInfinite:
		w.write("for {")
	case syntax.ForCond:
		w.write("for ")
		w.expr(s.Cond)
		w.write(" {")
	case syntax.ForRange:
		w.write("for ")
		w.write(s.Var)
		w.write(" in ")
		w.expr(s.Range.Start)
		if s.Range.Inclusive {
			w.write("..=")
		} else {
			w.write("..")
		}
		w.expr(s.Range.End)
		w.write(" {")
	case syntax.ForIter:
		w.write("for ")
		w.write(s.Var)
		w.write(" in ")
		w.expr(s.Iter)
		w.write(" {")
	case syntax.ForChan:
		w.write("for ")
		w.write(s.Var)
		w.write(" in ")
		w.expr(s.Iter)
		w.write(" {")
	}
	w.newline()
	w.block(s.Body)
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) matchStmt(s *syntax.MatchStmt) {
	w.writeIndent()
	w.write("match ")
	w.expr(s.Subject)
	w.write(" {")
	w.newline()
	w.indent++
	for _, arm := range s.Arms {
		w.matchArm(arm)
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) matchArm(arm syntax.MatchArm) {
	w.writeIndent()
	w.pattern(arm.Pattern)
	if arm.Guard != nil {
		w.write(" if ")
		w.expr(arm.Guard)
	}
	w.write(" => ")
	// Single-statement body detection. parseMatchArm wraps a single
	// statement in a synthetic Block whose Pos == the inner stmt's
	// StmtPos(); a real `{ … }` block carries the position of the `{`,
	// so a mismatch identifies the bare-stmt form.
	if isSyntheticArmBody(arm.Body) {
		// Render just the inner stmt inline without indent or newline
		// duplication.
		w.armSingleStmt(arm.Body.Statements[0])
	} else {
		w.write("{")
		w.newline()
		w.block(arm.Body)
		w.writeIndent()
		w.write("}")
		w.newline()
	}
}

// isSyntheticArmBody reports whether body was a synthetic single-stmt
// wrapper produced by the parser — Body.Pos == Body.Statements[0].StmtPos().
func isSyntheticArmBody(b *syntax.Block) bool {
	if b == nil || len(b.Statements) != 1 {
		return false
	}
	return b.Pos == b.Statements[0].StmtPos()
}

// armSingleStmt emits a single-statement match-arm body inline. The stmt's
// LeadingComments are dropped — the arm is one-line and the parser does not
// attach comments to a single-stmt arm body in practice. Compound stmts
// (match / if / for) emit at the current indent on the line that opened
// the arm, so the resulting source matches the corpus convention of
// `pat => match x { ... }`.
func (w *writer) armSingleStmt(s syntax.Stmt) {
	switch x := s.(type) {
	case *syntax.PrintStmt:
		w.write("print ")
		w.expr(x.Expr)
		w.newline()
	case *syntax.ReturnStmt:
		w.write("return")
		if x.Value != nil {
			w.write(" ")
			w.expr(x.Value)
		}
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
		w.newline()
	case *syntax.BreakStmt:
		w.write("break")
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
		w.newline()
	case *syntax.ContinueStmt:
		w.write("continue")
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
		w.newline()
	case *syntax.NopStmt:
		w.write("nop")
		w.newline()
	case *syntax.ExprStmt:
		w.expr(x.Expr)
		w.newline()
	case *syntax.SendStmt:
		w.expr(x.Chan)
		w.write(" <- ")
		w.expr(x.Value)
		w.newline()
	case *syntax.AssignStmt:
		w.expr(x.Target)
		w.write(" ")
		w.write(x.Op.String())
		w.write(" ")
		w.expr(x.Value)
		w.newline()
	case *syntax.MultiAssignStmt:
		for i, t := range x.Targets {
			if i > 0 {
				w.write(", ")
			}
			w.expr(t)
		}
		w.write(" = ")
		if tup, ok := x.Value.(*syntax.TupleLit); ok {
			for i, e := range tup.Elements {
				if i > 0 {
					w.write(", ")
				}
				w.expr(e)
			}
		} else {
			w.expr(x.Value)
		}
		w.newline()
	case *syntax.LetStmt:
		// v0.11: keyword-led `let` was retired. The bare-binding shape is
		// the canonical immutable form; tuple destructure renders without a
		// leading keyword.
		if x.Tuple != nil {
			w.write("(")
			for i, n := range x.Tuple.Names {
				if i > 0 {
					w.write(", ")
				}
				w.write(n)
			}
			w.write(")")
		} else {
			w.write(x.Name)
		}
		if x.Type != nil {
			w.write(": ")
			w.write(typeRefText(x.Type))
			w.write(" = ")
		} else {
			w.write(" := ")
		}
		w.expr(x.Value)
		w.newline()
	case *syntax.MutStmt:
		w.write("mut ")
		if x.Tuple != nil {
			w.write("(")
			for i, n := range x.Tuple.Names {
				if i > 0 {
					w.write(", ")
				}
				w.write(n)
			}
			w.write(")")
		} else {
			w.write(x.Name)
		}
		if x.Type != nil {
			w.write(": ")
			w.write(typeRefText(x.Type))
			w.write(" = ")
		} else {
			w.write(" := ")
		}
		w.expr(x.Value)
		w.newline()
	case *syntax.MatchStmt:
		// Compound: render the match-stmt at the column of `=>`'s right
		// edge — but writeIndent already happened for the arm header.
		// We're mid-line at the `=> ` position, so emit the match body
		// without re-indenting and with the inner match-arm body at
		// indent+1. Easiest: emit `match expr {`, NL, body at indent+1,
		// `}`, NL.
		w.write("match ")
		w.expr(x.Subject)
		w.write(" {")
		w.newline()
		w.indent++
		for _, arm := range x.Arms {
			w.matchArm(arm)
		}
		w.indent--
		w.writeIndent()
		w.write("}")
		w.newline()
	case *syntax.IfStmt:
		w.write("if ")
		w.expr(x.Cond)
		w.write(" {")
		w.newline()
		w.block(x.Then)
		w.writeIndent()
		w.write("}")
		for _, eli := range x.Elifs {
			w.write(" elif ")
			w.expr(eli.Cond)
			w.write(" {")
			w.newline()
			w.block(eli.Body)
			w.writeIndent()
			w.write("}")
		}
		if x.Else != nil {
			w.write(" else {")
			w.newline()
			w.block(x.Else)
			w.writeIndent()
			w.write("}")
		}
		w.newline()
	case *syntax.ForStmt:
		w.forStmtInline(x)
	default:
		// Fallback for unknown stmt shapes: open a brace block. Loses
		// idempotence in extreme cases but keeps the output parseable.
		w.write("{")
		w.newline()
		w.indent++
		w.stmt(s)
		w.indent--
		w.writeIndent()
		w.write("}")
		w.newline()
	}
}

// forStmtInline emits a for-stmt without a leading writeIndent (the caller
// already positioned the cursor). Used when a for-stmt is the single
// inline body of a match arm.
func (w *writer) forStmtInline(s *syntax.ForStmt) {
	switch s.Kind {
	case syntax.ForInfinite:
		w.write("for {")
	case syntax.ForCond:
		w.write("for ")
		w.expr(s.Cond)
		w.write(" {")
	case syntax.ForRange:
		w.write("for ")
		w.write(s.Var)
		w.write(" in ")
		w.expr(s.Range.Start)
		if s.Range.Inclusive {
			w.write("..=")
		} else {
			w.write("..")
		}
		w.expr(s.Range.End)
		w.write(" {")
	case syntax.ForIter, syntax.ForChan:
		w.write("for ")
		w.write(s.Var)
		w.write(" in ")
		w.expr(s.Iter)
		w.write(" {")
	}
	w.newline()
	w.block(s.Body)
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) importDecl(s *syntax.ImportDecl) {
	w.writeIndent()
	w.write("import ")
	w.write(quoteString(s.Path))
	if s.Alias != "" {
		w.write(" as ")
		w.write(s.Alias)
	}
	w.newline()
}

func (w *writer) fnDecl(s *syntax.FnDecl, isMethod bool) {
	_ = isMethod
	w.writeIndent()
	if s.Pub {
		w.write("pub ")
	}
	w.write("fn ")
	w.write(s.Name)
	w.typeParams(s.TypeParams)
	w.fnParams(s.Params)
	if s.Return != nil {
		w.write(" -> ")
		w.write(typeRefText(s.Return))
	}
	if s.BuiltinName != "" {
		w.write(" __builtin ")
		w.write(s.BuiltinName)
		w.newline()
		return
	}
	if s.Body == nil {
		// Signature-only (spec method without default body); the caller
		// (specDecl) handles that path, but if we get here just terminate.
		w.newline()
		return
	}
	w.write(" {")
	w.newline()
	w.block(s.Body)
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) typeParams(params []syntax.TypeParam) {
	if len(params) == 0 {
		return
	}
	w.write("[")
	for i, tp := range params {
		if i > 0 {
			w.write(", ")
		}
		w.write(tp.Name)
		for j, b := range tp.Bounds {
			if j == 0 {
				w.write(": ")
			} else {
				w.write(" + ")
			}
			w.write(typeRefText(b))
		}
	}
	w.write("]")
}

func (w *writer) fnParams(params []syntax.FnParam) {
	w.write("(")
	for i, p := range params {
		if i > 0 {
			w.write(", ")
		}
		w.write(p.Name)
		w.write(": ")
		w.write(typeRefText(p.Type))
	}
	w.write(")")
}

func (w *writer) structDecl(s *syntax.StructDecl) {
	w.writeIndent()
	if s.Pub {
		w.write("pub ")
	}
	w.write("struct ")
	w.write(s.Name)
	w.typeParams(s.TypeParams)
	w.write(" {")
	if len(s.Fields) == 0 {
		w.write(" }")
		w.newline()
		return
	}
	// Single-line form when the user wrote a one-liner. Detection: every
	// field is on the same line as the declaration's open-brace (i.e.
	// s.Pos.Line == every field.Pos.Line). The corpus uses the one-line
	// form for all small struct decls.
	if structFieldsOneLine(s) {
		w.write(" ")
		for i, f := range s.Fields {
			if i > 0 {
				w.write(", ")
			}
			w.write(f.Name)
			w.write(": ")
			w.write(typeRefText(f.Type))
		}
		w.write(" }")
		w.newline()
		return
	}
	w.newline()
	w.indent++
	for i, f := range s.Fields {
		w.writeIndent()
		w.write(f.Name)
		w.write(": ")
		w.write(typeRefText(f.Type))
		// Comma between fields, none on the final entry — matches the
		// existing v0.0–v0.9 corpus convention (see internal/fmt/STYLE.md).
		if i < len(s.Fields)-1 {
			w.write(",")
		}
		w.newline()
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func structFieldsOneLine(s *syntax.StructDecl) bool {
	for _, f := range s.Fields {
		if f.Pos.Line != s.Pos.Line {
			return false
		}
	}
	return true
}

func (w *writer) enumDecl(s *syntax.EnumDecl) {
	w.writeIndent()
	if s.Pub {
		w.write("pub ")
	}
	w.write("enum ")
	w.write(s.Name)
	w.typeParams(s.TypeParams)
	w.write(" {")
	if len(s.Variants) == 0 {
		w.write(" }")
		w.newline()
		return
	}
	if enumVariantsOneLine(s) {
		w.write(" ")
		for i, v := range s.Variants {
			if i > 0 {
				w.write(", ")
			}
			w.variantDecl(v)
		}
		w.write(" }")
		w.newline()
		return
	}
	w.newline()
	w.indent++
	for i, v := range s.Variants {
		w.writeIndent()
		w.variantDecl(v)
		if i < len(s.Variants)-1 {
			w.write(",")
		}
		w.newline()
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func enumVariantsOneLine(s *syntax.EnumDecl) bool {
	for _, v := range s.Variants {
		if v.Pos.Line != s.Pos.Line {
			return false
		}
	}
	return true
}

func (w *writer) variantDecl(v syntax.VariantDecl) {
	w.write(v.Name)
	if len(v.Payload) > 0 {
		w.write("(")
		for i, t := range v.Payload {
			if i > 0 {
				w.write(", ")
			}
			w.write(typeRefText(t))
		}
		w.write(")")
	}
}

func (w *writer) specDecl(s *syntax.SpecDecl) {
	w.writeIndent()
	if s.Pub {
		w.write("pub ")
	}
	w.write("spec ")
	w.write(s.Name)
	w.typeParams(s.TypeParams)
	w.write(" {")
	if len(s.Methods) == 0 {
		w.write(" }")
		w.newline()
		return
	}
	w.newline()
	w.indent++
	for i, m := range s.Methods {
		if i > 0 {
			w.newline()
		}
		w.specMethod(m)
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) specMethod(m *syntax.SpecMethod) {
	w.writeIndent()
	if m.Pub {
		w.write("pub ")
	}
	w.write("fn ")
	w.write(m.Name)
	w.typeParams(m.TypeParams)
	w.fnParams(m.Params)
	if m.Return != nil {
		w.write(" -> ")
		w.write(typeRefText(m.Return))
	}
	if m.Body == nil {
		// Signature-only method.
		w.newline()
		return
	}
	w.write(" {")
	w.newline()
	w.block(m.Body)
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) implDecl(s *syntax.ImplDecl) {
	w.writeIndent()
	w.write("impl")
	w.typeParams(s.TypeParams)
	w.write(" ")
	if s.TypeModule != "" {
		w.write(s.TypeModule)
		w.write(".")
	}
	w.write(s.Type)
	if len(s.TypeArgs) > 0 {
		w.write("[")
		for i, a := range s.TypeArgs {
			if i > 0 {
				w.write(", ")
			}
			w.write(typeRefText(a))
		}
		w.write("]")
	}
	if s.Spec != "" {
		w.write(" for ")
		if s.SpecModule != "" {
			w.write(s.SpecModule)
			w.write(".")
		}
		w.write(s.Spec)
	}
	w.write(" {")
	if len(s.Methods) == 0 {
		w.write("}")
		w.newline()
		return
	}
	w.newline()
	w.indent++
	for i, m := range s.Methods {
		if i > 0 {
			w.newline()
		}
		w.fnDecl(m, true)
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) spawnStmt(s *syntax.SpawnStmt) {
	w.writeIndent()
	w.write("spawn ")
	w.expr(s.Call)
	w.newline()
}

func (w *writer) deferStmt(s *syntax.DeferStmt) {
	w.writeIndent()
	w.write("defer ")
	// The parser wraps single-stmt defers in a one-element Block. Detect
	// via Body.Pos == Body.Statements[0].StmtPos() and emit the bare
	// statement form so `defer print 1` round-trips.
	if isSyntheticArmBody(s.Body) {
		w.armSingleStmt(s.Body.Statements[0])
		return
	}
	w.write("{")
	w.newline()
	w.block(s.Body)
	w.writeIndent()
	w.write("}")
	w.newline()
}

// asmBlock emits `asm { body }` with the body byte-preserved. The parser
// captures BodyRaw verbatim — including newlines, leading/trailing
// whitespace, ASCII comments, and `${name}` markers — so the formatter
// produces a faithful round-trip without re-serialising from chunks. The
// rule that fmt round-trips asm bodies byte-for-byte ships as one of U2's
// regression tests.
func (w *writer) asmBlock(s *syntax.AsmBlock) {
	w.writeIndent()
	w.write("asm {")
	w.write(s.BodyRaw)
	w.write("}")
	w.newline()
}

func (w *writer) sendStmt(s *syntax.SendStmt) {
	w.writeIndent()
	w.expr(s.Chan)
	w.write(" <- ")
	w.expr(s.Value)
	w.newline()
}

func (w *writer) selectStmt(s *syntax.SelectStmt) {
	w.writeIndent()
	w.write("select {")
	w.newline()
	w.indent++
	for _, arm := range s.Arms {
		w.selectArm(arm)
	}
	w.indent--
	w.writeIndent()
	w.write("}")
	w.newline()
}

func (w *writer) selectArm(arm syntax.SelectArm) {
	w.writeIndent()
	switch arm.Op {
	case syntax.SelectRecvBind:
		w.write(arm.BindName)
		w.write(" := <- ")
		w.expr(arm.Chan)
	case syntax.SelectRecvDiscard:
		w.write("<- ")
		w.expr(arm.Chan)
	case syntax.SelectSend:
		w.expr(arm.Chan)
		w.write(" <- ")
		w.expr(arm.Value)
	case syntax.SelectDefault:
		w.write("_")
	}
	// Select arm body is always brace-form per the grammar (no bare-stmt
	// shortcut). Emit `op -> { ... }`.
	w.write(" -> {")
	if arm.Body != nil && len(arm.Body.Statements) > 0 {
		// Inline single-stmt body when the source had it on one line. The
		// select-arm parser does NOT use the synthetic-block path (the
		// `{ ... }` is always real braces), so detect "fits on one line"
		// via the body's pos line == its single stmt's pos line.
		if len(arm.Body.Statements) == 1 {
			s0 := arm.Body.Statements[0]
			if isInlineableStmt(s0) && arm.Body.Pos.Line == s0.StmtPos().Line {
				w.write(" ")
				inlineStmt(w, s0)
				w.write(" }")
				w.newline()
				return
			}
		}
		w.newline()
		w.block(arm.Body)
		w.writeIndent()
		w.write("}")
		w.newline()
		return
	}
	w.write(" }")
	w.newline()
}

func isInlineableStmt(s syntax.Stmt) bool {
	switch s.(type) {
	case *syntax.PrintStmt, *syntax.ReturnStmt, *syntax.BreakStmt, *syntax.ContinueStmt,
		*syntax.NopStmt, *syntax.ExprStmt, *syntax.SendStmt, *syntax.AssignStmt,
		*syntax.LetStmt, *syntax.MutStmt, *syntax.ConstStmt:
		return true
	}
	return false
}

// inlineStmt emits a stmt without indent or trailing newline. Used inside
// brace-bodied select arms that fit on one line.
func inlineStmt(w *writer, s syntax.Stmt) {
	switch x := s.(type) {
	case *syntax.PrintStmt:
		w.write("print ")
		w.expr(x.Expr)
	case *syntax.ReturnStmt:
		w.write("return")
		if x.Value != nil {
			w.write(" ")
			w.expr(x.Value)
		}
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
	case *syntax.BreakStmt:
		w.write("break")
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
	case *syntax.ContinueStmt:
		w.write("continue")
		if x.Guard != nil {
			w.write(" if ")
			w.expr(x.Guard)
		}
	case *syntax.NopStmt:
		w.write("nop")
	case *syntax.ExprStmt:
		w.expr(x.Expr)
	case *syntax.SendStmt:
		w.expr(x.Chan)
		w.write(" <- ")
		w.expr(x.Value)
	case *syntax.AssignStmt:
		w.expr(x.Target)
		w.write(" ")
		w.write(x.Op.String())
		w.write(" ")
		w.expr(x.Value)
	case *syntax.MultiAssignStmt:
		for i, t := range x.Targets {
			if i > 0 {
				w.write(", ")
			}
			w.expr(t)
		}
		w.write(" = ")
		if tup, ok := x.Value.(*syntax.TupleLit); ok {
			for i, e := range tup.Elements {
				if i > 0 {
					w.write(", ")
				}
				w.expr(e)
			}
		} else {
			w.expr(x.Value)
		}
	}
}

// block emits the statements inside a block at indent+1. Each stmt carries
// its own newline. Blank lines between statements in the source are
// preserved up to one (multi-blank → single blank).
func (w *writer) block(b *syntax.Block) {
	if b == nil {
		return
	}
	w.indent++
	for i, s := range b.Statements {
		if i > 0 {
			prev := b.Statements[i-1]
			if firstLineOfStmt(s) > lastLineOfStmt(prev)+1 {
				w.newline()
			}
		}
		w.stmt(s)
	}
	w.indent--
}

// ---------------------------------------------------------------------------
// Patterns.
// ---------------------------------------------------------------------------

func (w *writer) pattern(p syntax.Pattern) {
	switch x := p.(type) {
	case *syntax.LitPat:
		w.expr(x.Lit)
	case *syntax.WildcardPat:
		w.write("_")
	case *syntax.BindPat:
		w.write(x.Name)
	case *syntax.TuplePat:
		w.write("(")
		for i, e := range x.Elements {
			if i > 0 {
				w.write(", ")
			}
			w.pattern(e)
		}
		w.write(")")
	case *syntax.StructPat:
		w.write(x.TypeName)
		w.write(" { ")
		for i, f := range x.Fields {
			if i > 0 {
				w.write(", ")
			}
			// Shorthand: when the sub-pattern is a BindPat with the same
			// name, drop the `: pat` suffix.
			if bp, ok := f.Pattern.(*syntax.BindPat); ok && bp.Name == f.Name {
				w.write(f.Name)
			} else {
				w.write(f.Name)
				w.write(": ")
				w.pattern(f.Pattern)
			}
		}
		if x.Rest {
			if len(x.Fields) > 0 {
				w.write(", ")
			}
			w.write("..")
		}
		w.write(" }")
	case *syntax.EnumPat:
		w.write(x.TypeName)
		w.write(".")
		w.write(x.VariantName)
		if len(x.Payload) > 0 {
			w.write("(")
			for i, sp := range x.Payload {
				if i > 0 {
					w.write(", ")
				}
				w.pattern(sp)
			}
			w.write(")")
		}
	default:
		w.writef("/* unhandled pattern: %T */", p)
	}
}

// ---------------------------------------------------------------------------
// Expressions.
// ---------------------------------------------------------------------------

// expr is the master expression printer. It calls into specialised helpers
// for compound shapes; atoms emit inline.
func (w *writer) expr(e syntax.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *syntax.IntLit:
		w.write(x.Text)
	case *syntax.FloatLit:
		w.write(x.Text)
	case *syntax.StringLit:
		w.write(quoteString(x.Value))
	case *syntax.BoolLit:
		if x.Value {
			w.write("true")
		} else {
			w.write("false")
		}
	case *syntax.RuneLit:
		w.write(quoteRune(x.Value))
	case *syntax.NilLit:
		w.write("nil")
	case *syntax.IdentExpr:
		w.write(x.Name)
	case *syntax.ThisExpr:
		w.write("this")
	case *syntax.ParenExpr:
		w.write("(")
		w.expr(x.Inner)
		w.write(")")
	case *syntax.UnaryExpr:
		w.unaryExpr(x)
	case *syntax.BinaryExpr:
		w.binaryExpr(x)
	case *syntax.CallExpr:
		w.callExpr(x)
	case *syntax.MethodCallExpr:
		w.methodCallExpr(x)
	case *syntax.FieldAccessExpr:
		w.fieldAccessExpr(x)
	case *syntax.IndexExpr:
		w.expr(x.Receiver)
		w.write("[")
		w.expr(x.Index)
		w.write("]")
	case *syntax.SliceExpr:
		w.sliceExpr(x)
	case *syntax.RangeExpr:
		w.expr(x.Start)
		if x.Inclusive {
			w.write("..=")
		} else {
			w.write("..")
		}
		w.expr(x.End)
	case *syntax.ListLit:
		w.listLit(x)
	case *syntax.TupleLit:
		w.tupleLit(x)
	case *syntax.StructLit:
		w.structLit(x)
	case *syntax.EnumLit:
		w.enumLit(x)
	case *syntax.PropagateExpr:
		w.expr(x.Inner)
		w.write("?")
	case *syntax.CoalesceExpr:
		w.expr(x.Left)
		w.write(" ?? ")
		w.expr(x.Right)
	case *syntax.AnonFnExpr:
		w.anonFn(x)
	case *syntax.ChanConstructorExpr:
		w.write("chan[")
		w.write(typeRefText(x.Element))
		w.write("]")
		w.write("(")
		if x.Capacity != nil {
			w.expr(x.Capacity)
		}
		w.write(")")
	case *syntax.RecvExpr:
		w.write("<- ")
		w.expr(x.Chan)
	default:
		w.writef("/* unhandled expr: %T */", e)
	}
}

func (w *writer) unaryExpr(e *syntax.UnaryExpr) {
	switch e.Op {
	case syntax.UnaryNeg:
		w.write("-")
		// `--x` would lex as decrement; the parser doesn't admit one but we
		// guard anyway.
		w.expr(e.Operand)
	case syntax.UnaryNot:
		w.write("not ")
		w.expr(e.Operand)
	case syntax.UnaryBitNot:
		w.write("~")
		w.expr(e.Operand)
	}
}

func (w *writer) binaryExpr(e *syntax.BinaryExpr) {
	w.expr(e.Left)
	w.write(" ")
	w.write(e.Op.String())
	w.write(" ")
	w.expr(e.Right)
}

func (w *writer) callExpr(e *syntax.CallExpr) {
	w.expr(e.Callee)
	w.write("(")
	for i, a := range e.Args {
		if i > 0 {
			w.write(", ")
		}
		w.expr(a)
	}
	w.write(")")
}

func (w *writer) methodCallExpr(e *syntax.MethodCallExpr) {
	w.expr(e.Receiver)
	w.write(".")
	w.write(e.Method)
	w.write("(")
	for i, a := range e.Args {
		if i > 0 {
			w.write(", ")
		}
		w.expr(a)
	}
	w.write(")")
}

func (w *writer) fieldAccessExpr(e *syntax.FieldAccessExpr) {
	w.expr(e.Receiver)
	if e.Safe {
		w.write("?.")
	} else {
		w.write(".")
	}
	w.write(e.FieldName)
}

func (w *writer) sliceExpr(e *syntax.SliceExpr) {
	w.expr(e.Receiver)
	w.write("[")
	if e.Low != nil {
		w.expr(e.Low)
	}
	if e.Inclusive {
		w.write("..=")
	} else {
		w.write("..")
	}
	if e.High != nil {
		w.expr(e.High)
	}
	w.write("]")
}

func (w *writer) listLit(e *syntax.ListLit) {
	w.write("[")
	for i, el := range e.Elements {
		if i > 0 {
			w.write(", ")
		}
		w.expr(el)
	}
	w.write("]")
}

func (w *writer) tupleLit(e *syntax.TupleLit) {
	w.write("(")
	for i, el := range e.Elements {
		if i > 0 {
			w.write(", ")
		}
		w.expr(el)
	}
	w.write(")")
}

func (w *writer) structLit(e *syntax.StructLit) {
	if e.Module != "" {
		w.write(e.Module)
		w.write(".")
	}
	w.write(e.TypeName)
	w.write(" { ")
	for i, f := range e.Fields {
		if i > 0 {
			w.write(", ")
		}
		w.write(f.Name)
		w.write(": ")
		w.expr(f.Value)
	}
	w.write(" }")
}

func (w *writer) enumLit(e *syntax.EnumLit) {
	if e.Module != "" {
		w.write(e.Module)
		w.write(".")
	}
	w.write(e.EnumName)
	w.write(".")
	w.write(e.Variant)
	if len(e.Payload) > 0 {
		w.write("(")
		for i, p := range e.Payload {
			if i > 0 {
				w.write(", ")
			}
			w.expr(p)
		}
		w.write(")")
	}
}

func (w *writer) anonFn(e *syntax.AnonFnExpr) {
	w.write("fn")
	w.fnParams(e.Params)
	if e.Return != nil {
		w.write(" -> ")
		w.write(typeRefText(e.Return))
	}
	w.write(" {")
	if e.Body != nil && anonBodyOneLine(e.Body) {
		w.write(" ")
		// Render single-stmt body without indent.
		s0 := e.Body.Statements[0]
		if isInlineableStmt(s0) {
			inlineStmt(w, s0)
			w.write(" }")
			return
		}
	}
	w.newline()
	w.block(e.Body)
	w.writeIndent()
	w.write("}")
}

func anonBodyOneLine(b *syntax.Block) bool {
	if b == nil || len(b.Statements) != 1 {
		return false
	}
	if !isInlineableStmt(b.Statements[0]) {
		return false
	}
	// All statements on the same line as the open brace.
	return b.Pos.Line == b.Statements[0].StmtPos().Line
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// typeRefText returns the canonical source-text rendering of a TypeRef.
// Mirrors TypeRef.String() but is centralised here so the formatter can
// future-proof against String() drift.
func typeRefText(r *syntax.TypeRef) string {
	if r == nil {
		return ""
	}
	var s string
	switch r.Kind {
	case syntax.TypeRefNamed:
		s = ""
		if r.Module != "" {
			s = r.Module + "." + r.Name
		} else {
			s = r.Name
		}
		if len(r.TypeArgs) > 0 {
			s += "["
			for i, a := range r.TypeArgs {
				if i > 0 {
					s += ", "
				}
				s += typeRefText(a)
			}
			s += "]"
		}
	case syntax.TypeRefList:
		s = "list[" + typeRefText(r.Element) + "]"
	case syntax.TypeRefTuple:
		s = "tuple["
		for i, e := range r.Elements {
			if i > 0 {
				s += ", "
			}
			s += typeRefText(e)
		}
		s += "]"
	}
	if r.Nullable {
		s += "?"
	}
	return s
}

// quoteString emits s as a double-quoted Zerg string literal with the
// escapes the lexer recognises: `\\`, `\"`, `\n`, `\t`, `\r`. Other ASCII
// control bytes are passed through verbatim — every byte the lexer accepted
// inside the source must round-trip out, even if the canonical form is
// arguably odd.
func quoteString(s string) string {
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
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// quoteRune emits a rune literal in the lexer-accepted form `'X'`. The
// lexer's escapes are: \\ \" \' \n \t \r \0 (no \u{…}). For NUL we emit
// `'\0'`; for printable ASCII the bare char form; for everything else
// (incl. non-ASCII Unicode) we emit the literal UTF-8 bytes between
// quotes — the lexer's UTF-8-decode path accepts any valid rune.
func quoteRune(r int64) string {
	switch r {
	case 0:
		return `'\0'`
	case '\n':
		return `'\n'`
	case '\t':
		return `'\t'`
	case '\r':
		return `'\r'`
	case '\\':
		return `'\\'`
	case '\'':
		return `'\''`
	}
	if r >= 0x20 && r < 0x7f {
		return stdfmt.Sprintf("'%c'", rune(r))
	}
	// Non-ASCII Unicode rune: emit the UTF-8 bytes inside single quotes.
	// The lexer's DecodeRuneInString path round-trips any valid rune.
	return "'" + string(rune(r)) + "'"
}
