// U9 printing: the final surface — group 9 (the chan constructor and select),
// group 10 (import, single and the grouped one-spec-per-line form), and group 12
// (the module-level unsafe declaration group and inline assembly), in the same
// canonical layout as the rest. The spawn/send/defer/del statements and the
// unsafe block-expression print inline from the shared stmt/expr dispatch in
// fmt.go; the multi-line forms live here.
package fmt

import "github.com/cmj0121/zerg/src/bootstrap/internal/ast"

// --- group 9: chan constructor & select ---------------------------------------

// chanNew prints the channel constructor 'chan[T](cap?)'.
func (p *printer) chanNew(n *ast.ChanNew) {
	p.write("chan[")
	p.typ(n.Elem)
	p.write("](")
	if n.Cap != nil {
		p.expr(n.Cap, precLowest)
	}
	p.write(")")
}

// selectStmt prints 'select { arm+ }', one arm per line: a recv arm
// '(bind :=)? <-chan', a send arm 'chan <- value', or the 'done'/'_' heads,
// each with its '=> body'.
func (p *printer) selectStmt(n *ast.SelectStmt) {
	p.write("select {")
	p.nl()
	p.indent++
	for i := range n.Arms {
		arm := &n.Arms[i]
		p.leadTrivia(arm.Lead())
		p.ind()
		p.selectArm(arm)
		p.trailTrivia(arm.Trail())
		p.nl()
	}
	p.indent--
	p.ind()
	p.write("}")
}

// selectArm prints one select arm's head and '=> body'.
func (p *printer) selectArm(arm *ast.SelectArm) {
	switch arm.Kind {
	case ast.SelectRecv:
		if arm.HasBind {
			p.write(arm.Bind)
			p.write(" := ")
		}
		p.write("<-")
		p.expr(arm.Chan, precRecv)
	case ast.SelectSend:
		p.expr(arm.Chan, precLowest)
		p.write(" <- ")
		p.expr(arm.Value, precLowest)
	case ast.SelectDone:
		p.write("done")
	case ast.SelectDefault:
		p.write("_")
	}
	p.write(" => ")
	p.expr(arm.Body, precLowest)
}

// --- group 10: import ---------------------------------------------------------

// importStmt prints the single form 'import spec' or the grouped form
// 'import ( … )' with one spec per line.
func (p *printer) importStmt(n *ast.ImportStmt) {
	p.write("import")
	if n.Grouped {
		p.write(" (")
		p.nl()
		p.indent++
		for _, s := range n.Specs {
			p.leadTrivia(s.Lead())
			p.ind()
			p.importSpec(s)
			p.trailTrivia(s.Trail())
			p.nl()
		}
		p.leadTrivia(n.End)
		p.indent--
		p.ind()
		p.write(")")
		return
	}
	p.write(" ")
	p.importSpec(n.Specs[0])
}

// importSpec prints 'pub? "path" (as id)?'.
func (p *printer) importSpec(s *ast.ImportSpec) {
	if s.Pub {
		p.write("pub ")
	}
	p.write(quote(s.Path))
	if s.Alias != "" {
		p.write(" as ")
		p.write(s.Alias)
	}
}

// --- group 12: unsafe group & asm ---------------------------------------------

// unsafeGroup prints the module-level 'unsafe { … }' declaration group; each item
// is a declaration (with its own decorators) or a binding, one per line.
func (p *printer) unsafeGroup(n *ast.UnsafeGroup) {
	p.write("unsafe")
	p.openBody()
	for _, it := range n.Items {
		p.leadTrivia(it.Lead())
		if d, ok := it.(ast.Decl); ok {
			p.decorators(declDecorators(d))
			p.ind()
			p.declDispatch(d)
		} else {
			p.ind()
			p.stmt(it)
		}
		p.trailTrivia(it.Trail())
		p.nl()
	}
	p.endBrace(n.End)
}

// asmExpr prints inline assembly 'asm(template, operand, …)'.
func (p *printer) asmExpr(n *ast.AsmExpr) {
	p.write("asm(")
	p.write(quote(n.Template))
	for _, op := range n.Operands {
		p.write(", ")
		p.asmOperand(op)
	}
	p.write(")")
}

// asmOperand prints one asm operand.
func (p *printer) asmOperand(op *ast.AsmOperand) {
	switch op.Kind {
	case ast.AsmIn:
		p.asmConstraint("in", op)
	case ast.AsmOut:
		p.asmConstraint("out", op)
	case ast.AsmInout:
		p.asmConstraint("inout", op)
	case ast.AsmClobber:
		p.write("clobber(")
		for i, c := range op.Clobbers {
			if i > 0 {
				p.write(", ")
			}
			p.write(quote(c))
		}
		p.write(")")
	}
}

// asmConstraint prints a 'head("constraint") expr' operand (in/out/inout).
func (p *printer) asmConstraint(head string, op *ast.AsmOperand) {
	p.write(head)
	p.write("(")
	p.write(quote(op.Constraint))
	p.write(") ")
	p.expr(op.Value, precLowest)
}
